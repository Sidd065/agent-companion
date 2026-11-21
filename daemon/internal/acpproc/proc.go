// Package acpproc spawns and supervises any generic ACP (Agent Client Protocol)
// subprocess speaking JSON-RPC 2.0 over newline-delimited stdio.
package acpproc

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"agentc/internal/acp"
)

// Options configure a generic ACP agent subprocess.
type Options struct {
	Command   string
	Args      []string
	Cwd       string
	LogDir    string
	LogPrefix string
	Env       []string
	Handler   acp.Handler
	Trace     func(dir string, line []byte)
}

// Proc is a running generic ACP subprocess with an initialized ACP connection.
type Proc struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	Conn      *acp.Conn
	LogFile   string
	Init      map[string]any
	exited    chan struct{}
	exitErr   error
	mu        sync.Mutex
	closing   bool
	StartedAt time.Time
}

// Start launches the configured ACP command and runs the ACP initialize handshake.
func Start(ctx context.Context, o Options) (*Proc, error) {
	if strings.TrimSpace(o.Command) == "" {
		return nil, fmt.Errorf("acpproc: empty command")
	}
	if o.LogDir != "" {
		if err := os.MkdirAll(o.LogDir, 0o700); err != nil {
			return nil, err
		}
	}
	prefix := o.LogPrefix
	if prefix == "" {
		prefix = "acp"
	}
	var logFile string
	var errFile *os.File
	if o.LogDir != "" {
		logFile = filepath.Join(o.LogDir, fmt.Sprintf("%s-%s-%d.log", prefix, time.Now().Format("20060102-150405"), time.Now().UnixNano()%100000))
		f, err := os.OpenFile(logFile+".stderr", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err == nil {
			errFile = f
		}
	}

	cmd := exec.Command(o.Command, o.Args...)
	cmd.Dir = o.Cwd
	if len(o.Env) > 0 {
		cmd.Env = o.Env
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		if errFile != nil {
			errFile.Close()
		}
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		if errFile != nil {
			errFile.Close()
		}
		return nil, err
	}
	if errFile != nil {
		cmd.Stderr = errFile
	}

	if err := cmd.Start(); err != nil {
		if errFile != nil {
			errFile.Close()
		}
		return nil, fmt.Errorf("start acp %q: %w", o.Command, err)
	}

	p := &Proc{
		cmd:       cmd,
		stdin:     stdin,
		LogFile:   logFile,
		exited:    make(chan struct{}),
		StartedAt: time.Now(),
	}
	p.Conn = acp.NewConn(stdout, stdin, o.Handler)
	p.Conn.Trace = o.Trace

	go func() {
		err := cmd.Wait()
		if errFile != nil {
			errFile.Close()
		}
		p.mu.Lock()
		p.exitErr = err
		p.mu.Unlock()
		close(p.exited)
	}()

	ictx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var initResp map[string]any
	err = p.Conn.Call(ictx, "initialize", map[string]any{
		"protocolVersion": 1,
		"clientCapabilities": map[string]any{
			"fs":       map[string]any{"readTextFile": false, "writeTextFile": false},
			"terminal": false,
		},
		"clientInfo": map[string]any{"name": "agentc", "title": "Agent Companion", "version": "2.0"},
	}, &initResp)
	if err != nil {
		p.Kill()
		return nil, fmt.Errorf("acp initialize (%s): %w%s", o.Command, err, p.stderrTail())
	}
	p.Init = initResp
	return p, nil
}

func (p *Proc) stderrTail() string {
	if p.LogFile == "" {
		return ""
	}
	b, err := os.ReadFile(p.LogFile + ".stderr")
	if err != nil || len(b) == 0 {
		return ""
	}
	s := strings.TrimSpace(string(b))
	if len(s) > 600 {
		s = s[len(s)-600:]
	}
	return " (stderr: " + s + ")"
}

// ACP returns the underlying ACP JSON-RPC connection.
func (p *Proc) ACP() *acp.Conn { return p.Conn }

// Pid returns the OS process id.
func (p *Proc) Pid() int {
	if p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// Alive reports whether the subprocess is still running.
func (p *Proc) Alive() bool {
	select {
	case <-p.exited:
		return false
	default:
		return true
	}
}

// Exited is closed once the subprocess exits.
func (p *Proc) Exited() <-chan struct{} { return p.exited }

// ExitErr returns the wait error after exit.
func (p *Proc) ExitErr() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exitErr
}

// Close shuts down the subprocess cleanly: close stdin -> SIGTERM -> SIGKILL.
func (p *Proc) Close(grace time.Duration) {
	p.mu.Lock()
	if p.closing {
		p.mu.Unlock()
		<-p.exited
		return
	}
	p.closing = true
	p.mu.Unlock()
	_ = p.stdin.Close()
	if grace <= 0 {
		grace = 3 * time.Second
	}
	select {
	case <-p.exited:
		return
	case <-time.After(grace):
	}
	p.signal(syscall.SIGTERM)
	select {
	case <-p.exited:
		return
	case <-time.After(3 * time.Second):
	}
	p.signal(syscall.SIGKILL)
	<-p.exited
}

// Kill terminates the process group immediately.
func (p *Proc) Kill() {
	p.signal(syscall.SIGKILL)
	_ = p.stdin.Close()
}

func (p *Proc) signal(sig syscall.Signal) {
	if p.cmd == nil || p.cmd.Process == nil {
		return
	}
	pid := p.cmd.Process.Pid
	_ = syscall.Kill(-pid, sig)
	_ = p.cmd.Process.Signal(sig)
}

// RSSKiB returns the resident set size in KiB from /proc.
func (p *Proc) RSSKiB() int64 {
	pid := p.Pid()
	if pid <= 0 {
		return 0
	}
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			f := strings.Fields(line)
			if len(f) >= 2 {
				n, _ := strconv.ParseInt(f[1], 10, 64)
				return n
			}
		}
	}
	return 0
}
