// Package claude drives Claude Code (`claude -p` in stream-json mode) and
// presents it to the session engine as an ACP agent, so Claude sessions share
// the transcript, queueing, permission and notification machinery used for agy.
// It also reads Claude's own session store (~/.claude/projects) for history,
// forks, rewinds, subagent transcripts and live terminal sessions.
package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// ErrExited is returned for calls on a process that has exited.
var ErrExited = errors.New("claude process exited")

// Head is the discriminator of a stream-json message.
type Head struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	RequestID string `json:"request_id"`
}

// ProcOptions configure a `claude -p` process.
type ProcOptions struct {
	Bin     string
	Cwd     string
	Args    []string // everything after the stream-json protocol flags
	Env     []string
	LogFile string
	// OnMessage receives every stdout message except control traffic, in order.
	OnMessage func(h Head, raw json.RawMessage)
	// OnControl receives CLI-initiated control requests (can_use_tool …).
	OnControl func(id string, req json.RawMessage)
	// OnControlCancel is called when the CLI withdraws a control request.
	OnControlCancel func(id string)
	// Trace, when set, receives every raw line ("<<" from claude, ">>" to it).
	Trace func(dir string, line []byte)
}

type ctlResult struct {
	ok   bool
	body json.RawMessage
	err  string
}

// Proc is a running `claude -p --input-format stream-json` process.
type Proc struct {
	o         ProcOptions
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	wmu       sync.Mutex
	mu        sync.Mutex
	pending   map[string]chan ctlResult
	nextID    atomic.Int64
	exited    chan struct{}
	exitErr   error
	stderr    *os.File
	StartedAt time.Time
}

var (
	ownMu sync.Mutex
	own   = map[int]bool{}
)

// OwnPids returns the claude processes this program started that are still
// running (including ones shutting down).
func OwnPids() map[int]bool {
	ownMu.Lock()
	defer ownMu.Unlock()
	out := make(map[int]bool, len(own))
	for p := range own {
		out[p] = true
	}
	return out
}

// BaseArgs are the protocol flags every agentc-driven Claude process gets.
func BaseArgs() []string {
	return []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
		"--include-partial-messages", "--replay-user-messages", "--permission-prompt-tool", "stdio",
		"--thinking-display", "summarized"}
}

// StartProc launches claude.
func StartProc(o ProcOptions) (*Proc, error) {
	if o.Bin == "" {
		o.Bin = "claude"
	}
	args := append(BaseArgs(), o.Args...)
	cmd := exec.Command(o.Bin, args...)
	cmd.Dir = o.Cwd
	cmd.Env = o.Env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	p := &Proc{o: o, cmd: cmd, stdin: stdin, pending: map[string]chan ctlResult{}, exited: make(chan struct{}), StartedAt: time.Now()}
	if o.LogFile != "" {
		_ = os.MkdirAll(filepath.Dir(o.LogFile), 0o700)
		if f, err := os.OpenFile(o.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
			fmt.Fprintf(f, "%s claude %s\n", time.Now().Format(time.RFC3339), strings.Join(redactArgs(args), " "))
			p.stderr = f
			cmd.Stderr = f
		}
	}
	if err := cmd.Start(); err != nil {
		if p.stderr != nil {
			p.stderr.Close()
		}
		return nil, fmt.Errorf("start claude: %w", err)
	}
	ownMu.Lock()
	own[cmd.Process.Pid] = true
	ownMu.Unlock()
	go p.readLoop(stdout)
	return p, nil
}

// redactArgs hides inline JSON/system prompts from the log header.
func redactArgs(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		if len(a) > 200 {
			a = a[:200] + "…"
		}
		out[i] = a
	}
	return out
}

func (p *Proc) readLoop(r io.Reader) {
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			p.dispatch(line)
		}
		if err != nil {
			break
		}
	}
	err := p.cmd.Wait()
	ownMu.Lock()
	delete(own, p.cmd.Process.Pid)
	ownMu.Unlock()
	p.mu.Lock()
	p.exitErr = err
	pend := p.pending
	p.pending = map[string]chan ctlResult{}
	p.mu.Unlock()
	for _, ch := range pend {
		close(ch)
	}
	if p.stderr != nil {
		fmt.Fprintf(p.stderr, "%s exited: %v\n", time.Now().Format(time.RFC3339), err)
		p.stderr.Close()
	}
	close(p.exited)
}

func (p *Proc) dispatch(line []byte) {
	for len(line) > 0 && (line[len(line)-1] == '\n' || line[len(line)-1] == '\r') {
		line = line[:len(line)-1]
	}
	if len(line) == 0 || line[0] != '{' {
		return
	}
	if p.o.Trace != nil {
		p.o.Trace("<<", line)
	}
	var h Head
	if json.Unmarshal(line, &h) != nil {
		return
	}
	raw := json.RawMessage(append([]byte(nil), line...))
	switch h.Type {
	case "control_response":
		var r struct {
			Response struct {
				Subtype   string          `json:"subtype"`
				RequestID string          `json:"request_id"`
				Response  json.RawMessage `json:"response"`
				Error     string          `json:"error"`
			} `json:"response"`
		}
		if json.Unmarshal(raw, &r) != nil {
			return
		}
		p.mu.Lock()
		ch := p.pending[r.Response.RequestID]
		delete(p.pending, r.Response.RequestID)
		p.mu.Unlock()
		if ch != nil {
			ch <- ctlResult{ok: r.Response.Subtype == "success", body: r.Response.Response, err: r.Response.Error}
		}
	case "control_request":
		var r struct {
			RequestID string          `json:"request_id"`
			Request   json.RawMessage `json:"request"`
		}
		if json.Unmarshal(raw, &r) == nil && p.o.OnControl != nil {
			p.o.OnControl(r.RequestID, r.Request)
		}
	case "control_cancel_request":
		if p.o.OnControlCancel != nil {
			p.o.OnControlCancel(h.RequestID)
		}
	default:
		if p.o.OnMessage != nil {
			p.o.OnMessage(h, raw)
		}
	}
}

// Send writes one stream-json message.
func (p *Proc) Send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	select {
	case <-p.exited:
		return ErrExited
	default:
	}
	if p.o.Trace != nil {
		p.o.Trace(">>", b)
	}
	p.wmu.Lock()
	defer p.wmu.Unlock()
	_, err = p.stdin.Write(append(b, '\n'))
	return err
}

// Control sends a control request and waits for its response body.
func (p *Proc) Control(ctx context.Context, subtype string, params map[string]any) (json.RawMessage, error) {
	id := "agentc-" + strconv.FormatInt(p.nextID.Add(1), 10)
	req := map[string]any{"subtype": subtype}
	for k, v := range params {
		req[k] = v
	}
	ch := make(chan ctlResult, 1)
	p.mu.Lock()
	p.pending[id] = ch
	p.mu.Unlock()
	if err := p.Send(map[string]any{"type": "control_request", "request_id": id, "request": req}); err != nil {
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		return nil, err
	}
	select {
	case r, ok := <-ch:
		if !ok {
			return nil, ErrExited
		}
		if !r.ok {
			if r.err == "" {
				r.err = subtype + " failed"
			}
			return nil, errors.New(r.err)
		}
		return r.body, nil
	case <-ctx.Done():
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		return nil, ctx.Err()
	}
}

// Respond answers a CLI-initiated control request.
func (p *Proc) Respond(id string, body any) error {
	return p.Send(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": id, "response": body}})
}

// RespondError fails a CLI-initiated control request.
func (p *Proc) RespondError(id, msg string) error {
	return p.Send(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "error", "request_id": id, "error": msg}})
}

// Pid returns the process id.
func (p *Proc) Pid() int {
	if p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// Exited is closed when the process has exited.
func (p *Proc) Exited() <-chan struct{} { return p.exited }

// Alive reports whether the process is running.
func (p *Proc) Alive() bool {
	select {
	case <-p.exited:
		return false
	default:
		return true
	}
}

// ExitErr is the wait error once exited.
func (p *Proc) ExitErr() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exitErr
}

// Close ends the session: closing stdin lets claude finish and exit; the
// process group is terminated if it lingers past grace.
func (p *Proc) Close(grace time.Duration) {
	p.wmu.Lock()
	_ = p.stdin.Close()
	p.wmu.Unlock()
	select {
	case <-p.exited:
		return
	case <-time.After(grace / 2):
	}
	p.signal(syscall.SIGTERM)
	select {
	case <-p.exited:
		return
	case <-time.After(grace / 2):
	}
	p.signal(syscall.SIGKILL)
	<-p.exited
}

func (p *Proc) signal(sig syscall.Signal) {
	if p.cmd.Process == nil {
		return
	}
	pid := p.cmd.Process.Pid
	if err := syscall.Kill(-pid, sig); err != nil {
		_ = p.cmd.Process.Signal(sig)
	}
}

// RSSKiB returns the resident set size of the process.
func (p *Proc) RSSKiB() int64 {
	pid := p.Pid()
	if pid == 0 {
		return 0
	}
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0
	}
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "VmRSS:") {
			f := strings.Fields(l)
			if len(f) >= 2 {
				n, _ := strconv.ParseInt(f[1], 10, 64)
				return n
			}
		}
	}
	return 0
}
