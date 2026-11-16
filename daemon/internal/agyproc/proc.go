// Package agyproc spawns and supervises `agy acp` processes.
package agyproc

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"agentc/internal/acp"
)

// Options configure a new agy acp process.
type Options struct {
	AgyPath         string
	Cwd             string
	SkipPermissions bool
	Sandbox         bool
	AgentName       string
	NoTelemetry     bool
	AddDirs         []string
	ExtraArgs       []string
	LogDir          string
	Env             []string
	Handler         acp.Handler
	Trace           func(dir string, line []byte)
}

// Proc is a running `agy acp` process with an initialized ACP connection.
type Proc struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	Conn      *acp.Conn
	LogFile   string
	Init      map[string]any
	exited    chan struct{}
	exitErr   error
	mu        sync.Mutex
	lsPort    int
	closing   bool
	StartedAt time.Time
}

var lsPortRe = regexp.MustCompile(`random port at (\d+) for HTTP\s*$`)

// Start launches `agy acp` and performs the ACP initialize handshake.
func Start(ctx context.Context, o Options) (*Proc, error) {
	if o.AgyPath == "" {
		o.AgyPath = "agy"
	}
	if err := os.MkdirAll(o.LogDir, 0o700); err != nil {
		return nil, err
	}
	logFile := filepath.Join(o.LogDir, fmt.Sprintf("acp-%s-%d.log", time.Now().Format("20060102-150405"), time.Now().UnixNano()%100000))
	args := []string{"acp", "--log-file", logFile}
	if o.SkipPermissions {
		args = append(args, "--dangerously-skip-permissions")
	}
	if o.Sandbox {
		args = append(args, "--sandbox")
	}
	if strings.TrimSpace(o.AgentName) != "" {
		args = append(args, "--agent", strings.TrimSpace(o.AgentName))
	}
	if o.NoTelemetry {
		args = append(args, "--disable-telemetry")
	}
	for _, d := range o.AddDirs {
		if strings.TrimSpace(d) != "" {
			args = append(args, "--add-dir", d)
		}
	}
	args = append(args, o.ExtraArgs...)
	cmd := exec.Command(o.AgyPath, args...)
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
	errFile, err := os.OpenFile(logFile+".stderr", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err == nil {
		cmd.Stderr = errFile
	}
	if err := cmd.Start(); err != nil {
		if errFile != nil {
			errFile.Close()
		}
		return nil, fmt.Errorf("start agy acp: %w", err)
	}
	p := &Proc{cmd: cmd, stdin: stdin, LogFile: logFile, exited: make(chan struct{}), StartedAt: time.Now()}
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

	ictx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	var init map[string]any
	err = p.Conn.Call(ictx, "initialize", map[string]any{
		"protocolVersion": 1,
		"clientCapabilities": map[string]any{
			"fs":       map[string]any{"readTextFile": false, "writeTextFile": false},
			"terminal": false,
		},
		"clientInfo": map[string]any{"name": "agentc", "title": "agy companion", "version": "1.0"},
	}, &init)
	if err != nil {
		p.Kill()
		return nil, fmt.Errorf("acp initialize: %w%s", err, p.stderrTail())
	}
	p.Init = init
	return p, nil
}

func (p *Proc) stderrTail() string {
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

// Pid returns the OS process id.
func (p *Proc) Pid() int {
	if p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// Exited is closed when the process ends.
func (p *Proc) Exited() <-chan struct{} { return p.exited }

// Alive reports whether the process is still running.
func (p *Proc) Alive() bool {
	select {
	case <-p.exited:
		return false
	default:
		return true
	}
}

// ExitErr returns the wait error (nil while running or on clean exit).
func (p *Proc) ExitErr() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exitErr
}

// LSPort returns the language server's HTTP (Connect) port, parsing the log file.
func (p *Proc) LSPort(ctx context.Context) (int, error) {
	p.mu.Lock()
	if p.lsPort > 0 {
		port := p.lsPort
		p.mu.Unlock()
		return port, nil
	}
	p.mu.Unlock()
	deadline := time.Now().Add(45 * time.Second)
	for {
		if port := scanPort(p.LogFile); port > 0 {
			p.mu.Lock()
			p.lsPort = port
			p.mu.Unlock()
			return port, nil
		}
		if !p.Alive() {
			return 0, errors.New("agy process exited")
		}
		if time.Now().After(deadline) {
			return 0, errors.New("timed out waiting for agy language server port")
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
}

func scanPort(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !strings.Contains(line, "random port at") {
			continue
		}
		if m := lsPortRe.FindStringSubmatch(line); m != nil {
			n, _ := strconv.Atoi(m[1])
			return n
		}
	}
	return 0
}

// Close shuts the process down gracefully: closing stdin lets agy flush
// conversation state; then SIGTERM, then SIGKILL to the whole process group.
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
	select {
	case <-p.exited:
		return
	case <-time.After(grace):
	}
	p.signal(syscall.SIGTERM)
	select {
	case <-p.exited:
		return
	case <-time.After(5 * time.Second):
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
	if p.cmd.Process == nil {
		return
	}
	pid := p.cmd.Process.Pid
	_ = syscall.Kill(-pid, sig)
	_ = p.cmd.Process.Signal(sig)
}

// RSSKiB returns the resident set size of the agy process (0 if unknown).
func (p *Proc) RSSKiB() int64 {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", p.Pid()))
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
