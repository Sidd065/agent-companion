// Package term manages persistent terminals (tmux sessions on a private
// socket) that the phone can attach to and the laptop can share via `agyr`.
package term

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
)

// Manager wraps a private tmux server.
type Manager struct {
	Sock      string // tmux socket
	Conf      string // tmux config file
	SelfExe   string // agentc binary (for pipe-pane osc-watch)
	LocalSock string // daemon's local unix socket (osc-watch reports here)
	AgyPath   string
	// ClaudePath is the claude executable for Claude Code terminals.
	ClaudePath string
	// CodexPath is the codex executable for Codex terminals.
	CodexPath string
	// CodexLaunch builds the command and extra tmux env vars for `codex` terminals
	// (injecting CODEX_HOME and loopback OpenRouter bridge flags without exposing raw keys).
	CodexLaunch func(convID string, skip bool, extraArgs []string) (bin string, args []string, extraEnv map[string]string)
	// DefaultSkip is used for agy terminals when the client doesn't say
	// (the app always sends its saved flag).
	DefaultSkip bool
	Env         func() []string
	mu          sync.Mutex
}

// Term is one terminal.
type Term struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Kind         string `json:"kind"` // shell|agy|claude|codex|cmd
	Cwd          string `json:"cwd"`
	Command      string `json:"command"`
	Created      int64  `json:"created"`
	Attached     int    `json:"attached"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	State        string `json:"state,omitempty"`
	Msg          string `json:"msg,omitempty"`
	Origin       string `json:"origin,omitempty"`
	ConvID       string `json:"conversationId,omitempty"`
	Current      string `json:"currentCommand,omitempty"`
	Dead         bool   `json:"dead"`
	SessionID    string `json:"sessionId,omitempty"`
	ScriptID     string `json:"scriptId,omitempty"`
	RemainOnExit bool   `json:"remainOnExit,omitempty"`
	DeadStatus   int    `json:"deadStatus,omitempty"`
}

const tmuxConf = `# agy companion private tmux server (generated)
set -g default-terminal "tmux-256color"
set -as terminal-features ',xterm-256color:RGB,xterm*:hyperlinks'
set -g history-limit 50000
set -g mouse on
set -g status off
set -g window-size latest
set -g aggressive-resize on
set -g escape-time 10
set -g focus-events on
set -g set-clipboard on
set -g allow-passthrough on
set -g remain-on-exit off
set -g set-titles on
set -g set-titles-string "#{@agentc_name}"
`

// Init writes the tmux config.
func (m *Manager) Init() error {
	if err := os.MkdirAll(filepath.Dir(m.Sock), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(m.Conf), 0o700); err != nil {
		return err
	}
	cur, _ := os.ReadFile(m.Conf)
	if string(cur) != tmuxConf {
		if err := os.WriteFile(m.Conf, []byte(tmuxConf), 0o600); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) tmux(ctx context.Context, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	full := append([]string{"-S", m.Sock, "-f", m.Conf}, args...)
	cmd := exec.CommandContext(cctx, "tmux", full...)
	cmd.Env = m.env()
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return out.String(), errors.New(msg)
	}
	return out.String(), nil
}

func (m *Manager) env() []string {
	var base []string
	if m.Env != nil {
		base = m.Env()
	} else {
		base = os.Environ()
	}
	out := base[:0:0]
	for _, kv := range base {
		if strings.HasPrefix(kv, "TMUX=") || strings.HasPrefix(kv, "TMUX_PANE=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func noServer(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "no server running") || strings.Contains(s, "error connecting") || strings.Contains(s, "No such file")
}

const listFmt = "#{session_name}\t#{session_created}\t#{session_attached}\t#{window_width}\t#{window_height}\t#{pane_current_path}\t#{pane_current_command}\t#{pane_dead}\t#{@agentc_name}\t#{@agentc_kind}\t#{@agentc_state}\t#{@agentc_msg}\t#{@agentc_origin}\t#{@agentc_conv}\t#{@agentc_cmd}\t#{@agentc_sess}\t#{@agentc_script}\t#{pane_dead_status}"

// List returns all terminals.
func (m *Manager) List(ctx context.Context) ([]Term, error) {
	out, err := m.tmux(ctx, "list-sessions", "-F", listFmt)
	if err != nil {
		if noServer(err) {
			return []Term{}, nil
		}
		return nil, err
	}
	terms := []Term{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 15 {
			continue
		}
		created, _ := strconv.ParseInt(f[1], 10, 64)
		att, _ := strconv.Atoi(f[2])
		w, _ := strconv.Atoi(f[3])
		h, _ := strconv.Atoi(f[4])
		deadStatus := 0
		if len(f) >= 18 {
			deadStatus, _ = strconv.Atoi(f[17])
		}
		t := Term{ID: f[0], Created: created * 1000, Attached: att, Width: w, Height: h, Cwd: f[5], Current: f[6], Dead: f[7] == "1",
			Name: f[8], Kind: f[9], State: f[10], Msg: f[11], Origin: f[12], ConvID: f[13], Command: f[14], DeadStatus: deadStatus}
		if len(f) >= 17 {
			t.SessionID = f[15]
			t.ScriptID = f[16]
		}
		if t.Name == "" {
			t.Name = t.ID
		}
		if t.Kind == "" {
			t.Kind = "shell"
		}
		terms = append(terms, t)
	}
	sort.Slice(terms, func(i, j int) bool { return terms[i].Created > terms[j].Created })
	return terms, nil
}

// Get returns one terminal.
func (m *Manager) Get(ctx context.Context, id string) (*Term, error) {
	ts, err := m.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, t := range ts {
		if t.ID == id {
			t := t
			return &t, nil
		}
	}
	return nil, fmt.Errorf("terminal %s not found", id)
}

// CreateOpts describe a new terminal.
type CreateOpts struct {
	Kind         string   `json:"kind"` // shell|agy|cmd
	Cwd          string   `json:"cwd"`
	Name         string   `json:"name"`
	Command      string   `json:"command"` // for cmd
	Args         []string `json:"args"`    // extra agy args
	Cols         int      `json:"cols"`
	Rows         int      `json:"rows"`
	Origin       string   `json:"origin"`
	ConvID       string   `json:"conversationId"`
	SessionID    string   `json:"sessionId,omitempty"`
	ScriptID     string   `json:"scriptId,omitempty"`
	RemainOnExit bool     `json:"remainOnExit,omitempty"`
	Skip         *bool    `json:"skipPermissions"`
}

var idRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func newID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return "t" + hex.EncodeToString(b)
}

func shq(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Create starts a terminal in the background.
func (m *Manager) Create(ctx context.Context, o CreateOpts) (*Term, error) {
	if err := m.Init(); err != nil {
		return nil, err
	}
	home, _ := os.UserHomeDir()
	if o.Cwd == "" {
		o.Cwd = home
	}
	if st, err := os.Stat(o.Cwd); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("directory %q does not exist", o.Cwd)
	}
	if o.Cols <= 0 {
		o.Cols = 120
	}
	if o.Rows <= 0 {
		o.Rows = 40
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}
	id := newID()
	var command string
	var extraEnv map[string]string
	switch o.Kind {
	case "claude":
		bin := m.ClaudePath
		if bin == "" {
			bin = "claude"
		}
		parts := []string{shq(bin)}
		skip := m.DefaultSkip
		if o.Skip != nil {
			skip = *o.Skip
		}
		hasMode := false
		for _, a := range o.Args {
			if a == "--dangerously-skip-permissions" || a == "--permission-mode" || strings.HasPrefix(a, "--permission-mode=") {
				hasMode = true
			}
		}
		if skip && !hasMode {
			parts = append(parts, "--dangerously-skip-permissions")
		}
		if o.ConvID != "" {
			parts = append(parts, "--resume", shq(o.ConvID))
		}
		if m.SelfExe != "" {
			parts = append(parts, "--settings", shq(ClaudeHookSettings(m.SelfExe, id, m.Sock, m.Conf, m.LocalSock)))
		}
		for _, a := range o.Args {
			parts = append(parts, shq(a))
		}
		command = strings.Join(parts, " ")
		if o.Name == "" {
			o.Name = "claude · " + filepath.Base(o.Cwd)
		}
	case "agy":
		agy := m.AgyPath
		if agy == "" {
			agy = "agy"
		}
		parts := []string{shq(agy)}
		skip := m.DefaultSkip
		if o.Skip != nil {
			skip = *o.Skip
		}
		hasSkip := false
		for _, a := range o.Args {
			if a == "--dangerously-skip-permissions" {
				hasSkip = true
			}
		}
		if skip && !hasSkip {
			parts = append(parts, "--dangerously-skip-permissions")
		}
		if o.ConvID != "" {
			parts = append(parts, "--conversation", shq(o.ConvID))
		}
		for _, a := range o.Args {
			parts = append(parts, shq(a))
		}
		command = strings.Join(parts, " ")
		if o.Name == "" {
			o.Name = "agy · " + filepath.Base(o.Cwd)
		}
	case "codex":
		skip := m.DefaultSkip
		if o.Skip != nil {
			skip = *o.Skip
		}
		if m.CodexLaunch != nil {
			bin, launchArgs, envMap := m.CodexLaunch(o.ConvID, skip, o.Args)
			extraEnv = envMap
			if bin == "" {
				bin = "codex"
			}
			parts := []string{shq(bin)}
			for _, a := range launchArgs {
				parts = append(parts, shq(a))
			}
			command = strings.Join(parts, " ")
		} else {
			bin := m.CodexPath
			if bin == "" {
				bin = "codex"
			}
			parts := []string{shq(bin)}
			hasBypass := false
			for _, a := range o.Args {
				if a == "--dangerously-bypass-approvals-and-sandbox" || a == "--full-auto" {
					hasBypass = true
				}
			}
			if skip && !hasBypass {
				parts = append(parts, "--dangerously-bypass-approvals-and-sandbox")
			}
			if o.ConvID != "" {
				parts = append(parts, "resume", shq(o.ConvID))
			}
			for _, a := range o.Args {
				parts = append(parts, shq(a))
			}
			command = strings.Join(parts, " ")
		}
		if o.Name == "" {
			o.Name = "codex · " + filepath.Base(o.Cwd)
		}
	case "cmd":
		if strings.TrimSpace(o.Command) == "" {
			return nil, errors.New("command is empty")
		}
		command = shq(shell) + " -lc " + shq(o.Command)
		if o.Name == "" {
			o.Name = o.Command
			if len(o.Name) > 40 {
				o.Name = o.Name[:40]
			}
		}
	default:
		o.Kind = "shell"
		command = shq(shell) + " -l"
		if o.Name == "" {
			o.Name = filepath.Base(shell) + " · " + filepath.Base(o.Cwd)
		}
	}
	args := []string{"new-session", "-d", "-s", id, "-x", strconv.Itoa(o.Cols), "-y", strconv.Itoa(o.Rows), "-c", o.Cwd,
		"-e", "AGY_CLI_OSC7501=on", "-e", "AGENTC_TERM_ID=" + id, "-e", "COLORTERM=truecolor"}
	spawnCmd := command
	if len(extraEnv) > 0 {
		keys := make([]string, 0, len(extraEnv))
		for k := range extraEnv {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			args = append(args, "-e", k+"="+extraEnv[k])
		}
		if p := extraEnv["PATH"]; p != "" && o.Kind != "shell" && o.Kind != "cmd" {
			spawnCmd = "export PATH=" + shq(p) + "; exec " + command
		}
	}
	args = append(args, spawnCmd)
	if _, err := m.tmux(ctx, args...); err != nil {
		return nil, fmt.Errorf("tmux new-session: %w", err)
	}
	origin := o.Origin
	if origin == "" {
		origin = "app"
	}
	set := []string{"set-option", "-t", id, "@agentc_name", o.Name, ";",
		"set-option", "-t", id, "@agentc_kind", o.Kind, ";",
		"set-option", "-t", id, "@agentc_origin", origin, ";",
		"set-option", "-t", id, "@agentc_cmd", command}
	if o.ConvID != "" {
		set = append(set, ";", "set-option", "-t", id, "@agentc_conv", o.ConvID)
	}
	if o.SessionID != "" {
		set = append(set, ";", "set-option", "-t", id, "@agentc_sess", o.SessionID)
	}
	if o.ScriptID != "" {
		set = append(set, ";", "set-option", "-t", id, "@agentc_script", o.ScriptID)
	}
	if o.RemainOnExit {
		set = append(set, ";", "set-option", "-t", id, "remain-on-exit", "on")
	}
	if _, err := m.tmux(ctx, set...); err != nil {
		return nil, err
	}
	if o.Kind == "agy" && m.SelfExe != "" {
		watch := fmt.Sprintf("exec %s osc-watch --sock %s --conf %s --id %s --report %s", shq(m.SelfExe), shq(m.Sock), shq(m.Conf), shq(id), shq(m.LocalSock))
		if _, err := m.tmux(ctx, "pipe-pane", "-O", "-t", id, watch); err != nil {
			// Older tmux: -o only.
			_, _ = m.tmux(ctx, "pipe-pane", "-o", "-t", id, watch)
		}
	}
	return m.Get(ctx, id)
}

// Kill ends a terminal.
func (m *Manager) Kill(ctx context.Context, id string) error {
	if !idRe.MatchString(id) {
		return errors.New("bad id")
	}
	_, err := m.tmux(ctx, "kill-session", "-t", id)
	return err
}

// Rename sets the display name.
func (m *Manager) Rename(ctx context.Context, id, name string) error {
	if !idRe.MatchString(id) {
		return errors.New("bad id")
	}
	_, err := m.tmux(ctx, "set-option", "-t", id, "@agentc_name", name)
	return err
}

// SendKeys types literal text (and optionally Enter) into a terminal.
func (m *Manager) SendKeys(ctx context.Context, id, text string, enter bool) error {
	if !idRe.MatchString(id) {
		return errors.New("bad id")
	}
	if text != "" {
		if _, err := m.tmux(ctx, "send-keys", "-t", id, "-l", text); err != nil {
			return err
		}
	}
	if enter {
		_, err := m.tmux(ctx, "send-keys", "-t", id, "Enter")
		return err
	}
	return nil
}

// Capture returns the visible screen (plain text) of a terminal.
func (m *Manager) Capture(ctx context.Context, id string, lines int) (string, error) {
	if !idRe.MatchString(id) {
		return "", errors.New("bad id")
	}
	args := []string{"capture-pane", "-p", "-t", id}
	if lines > 0 {
		args = append(args, "-S", strconv.Itoa(-lines))
	}
	return m.tmux(ctx, args...)
}

// Attachment is a tmux client running in a PTY for one viewer.
type Attachment struct {
	PTY *os.File
	Cmd *exec.Cmd
}

// Attach starts a tmux client for id in a new PTY.
func (m *Manager) Attach(id string, cols, rows int) (*Attachment, error) {
	if !idRe.MatchString(id) {
		return nil, errors.New("bad id")
	}
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}
	cmd := exec.Command("tmux", "-S", m.Sock, "-f", m.Conf, "attach-session", "-t", id)
	env := m.env()
	env = append(env, "TERM=xterm-256color", "COLORTERM=truecolor", "LANG=C.UTF-8")
	cmd.Env = env
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, err
	}
	return &Attachment{PTY: f, Cmd: cmd}, nil
}

// Resize changes the attachment's window size.
func (a *Attachment) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	return pty.Setsize(a.PTY, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

// Close detaches the tmux client (the terminal keeps running).
func (a *Attachment) Close() {
	if a.Cmd.Process != nil {
		_ = a.Cmd.Process.Signal(os.Interrupt)
		_ = a.Cmd.Process.Kill()
	}
	_ = a.PTY.Close()
	_ = a.Cmd.Wait()
}

// ---- OSC 7501 watcher (runs as `agentc osc-watch` under tmux pipe-pane) ----

// OSCState is a parsed OSC 7501 status report from the agy TUI.
type OSCState struct {
	State string
	Kind  string
	Msg   string
}

// ParseOSC7501 parses "state=…:kind=…:app=…:msg=<base64>".
func ParseOSC7501(payload string) OSCState {
	var s OSCState
	for _, part := range strings.Split(payload, ":") {
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		switch k {
		case "state":
			s.State = v
		case "kind":
			s.Kind = v
		case "msg":
			if b, err := base64.StdEncoding.DecodeString(v); err == nil {
				s.Msg = string(b)
			} else if b, err := base64.RawStdEncoding.DecodeString(v); err == nil {
				s.Msg = string(b)
			} else {
				s.Msg = v
			}
		}
	}
	return s
}

// WatchOSC reads pane output, tracking OSC 7501 states. It records them as
// tmux session options and reports to the daemon's local socket.
func WatchOSC(r io.Reader, sock, conf, id, report string) error {
	br := bufio.NewReaderSize(r, 64*1024)
	var last OSCState
	var buf []byte
	const marker = "\x1b]7501;"
	tail := make([]byte, 0, 16)
	for {
		chunk := make([]byte, 32*1024)
		n, err := br.Read(chunk)
		if n > 0 {
			data := append(tail, chunk[:n]...)
			tail = tail[:0]
			for len(data) > 0 {
				if buf != nil {
					// inside an OSC sequence: look for BEL or ST
					end := bytes.IndexAny(data, "\x07\x1b")
					if end < 0 {
						buf = append(buf, data...)
						if len(buf) > 8192 {
							buf = nil
						}
						data = nil
						break
					}
					buf = append(buf, data[:end]...)
					if data[end] == 0x1b && end+1 >= len(data) {
						// possible ST split across reads
						tail = append(tail, data[end:]...)
						data = nil
						break
					}
					skip := 1
					if data[end] == 0x1b {
						skip = 2
					}
					st := ParseOSC7501(string(buf))
					buf = nil
					data = data[end+skip:]
					if st.State != "" && (st != last) {
						last = st
						publish(sock, conf, id, report, st)
					}
					continue
				}
				i := bytes.Index(data, []byte(marker))
				if i < 0 {
					// keep a possible partial marker at the end
					k := len(data) - len(marker) + 1
					if k < 0 {
						k = 0
					}
					for j := k; j < len(data); j++ {
						if bytes.HasPrefix([]byte(marker), data[j:]) {
							tail = append(tail, data[j:]...)
							break
						}
					}
					data = nil
					break
				}
				buf = []byte{}
				data = data[i+len(marker):]
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

// Publish records a terminal state (tmux options) and reports it to the daemon.
func Publish(sock, conf, id, report, state, msg string) {
	publish(sock, conf, id, report, OSCState{State: state, Msg: msg})
}

// SetConversation links a terminal to the agent conversation it runs.
func SetConversation(sock, conf, id, conv string) {
	_ = exec.Command("tmux", "-S", sock, "-f", conf, "set-option", "-t", id, "@agentc_conv", conv).Run()
}

// ClaudeHookSettings returns a --settings value whose hooks report a Claude
// Code terminal's state (working, waiting for you, done) to the daemon.
func ClaudeHookSettings(self, id, sock, conf, report string) string {
	cmd := fmt.Sprintf("%s claude-hook --id %s --sock %s --conf %s --report %s", shq(self), shq(id), shq(sock), shq(conf), shq(report))
	h := []map[string]any{{"hooks": []map[string]any{{"type": "command", "command": cmd, "timeout": 10}}}}
	hm := []map[string]any{{"matcher": "*", "hooks": []map[string]any{{"type": "command", "command": cmd, "timeout": 10}}}}
	b, _ := json.Marshal(map[string]any{"hooks": map[string]any{
		"SessionStart": h, "UserPromptSubmit": h, "Notification": h, "Stop": h, "SessionEnd": h, "PostToolUse": hm,
	}})
	return string(b)
}

func publish(sock, conf, id, report string, st OSCState) {
	msg := st.Msg
	if len(msg) > 300 {
		msg = msg[:300]
	}
	state := st.State
	if st.Kind != "" && state == "blocked" {
		state = "blocked:" + st.Kind
	}
	_ = exec.Command("tmux", "-S", sock, "-f", conf, "set-option", "-t", id, "@agentc_state", state, ";",
		"set-option", "-t", id, "@agentc_msg", msg).Run()
	if report == "" {
		return
	}
	c := http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", report)
	}}}
	body, _ := json.Marshal(map[string]string{"id": id, "state": state, "msg": strings.ToValidUTF8(msg, "?")})
	resp, err := c.Post("http://agentc/local/term-state", "application/json", bytes.NewReader(body))
	if err == nil {
		resp.Body.Close()
	}
}
