package claude

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"agentc/internal/acp"
)

// Options configure a Claude session (one `claude -p` process per session).
type Options struct {
	Bin             string
	Env             []string
	LogDir          string
	HomeDir         string
	SkipPermissions bool
	AddDirs         []string
	Model           string
	Effort          string
	Mode            string // initial permission mode
	AgentName       string // --agent
	ExtraArgs       []string
	ResumeAt        string // --resume-session-at (after a rewind)
	ForkFrom        string // parent session of a fork that has no transcript yet
	ReplayLimit     int    // max transcript entries replayed by session/load
	// Handler receives the agent's ACP traffic (the session engine).
	Handler acp.Handler
	Trace   func(dir string, line []byte)
}

// Extension notifications sent to the session engine (ACP "_" methods).
const (
	ExtNotice      = "_agentc/notice"      // {level, text}
	ExtLive        = "_agentc/live"        // {tasks: []Task}
	ExtUserMessage = "_agentc/userMessage" // {itemId, uuid}
	ExtRateLimit   = "_agentc/rateLimit"   // rate_limit_info
	ExtSuggestion  = "_agentc/suggestion"  // {text}
	ExtStatus      = "_agentc/status"      // {status}
	ExtPermGone    = "_agentc/permWithdrawn"
	ExtInfo        = "_agentc/info" // {account, outputStyle, agents, version}
	// ExtTurn reports turns Claude starts on its own (a background task or
	// subagent finished, a scheduled wake-up): {state: started|ended, result}.
	ExtTurn = "_agentc/turn"
)

// Task is a background task or subagent reported by Claude.
type Task struct {
	ID           string `json:"id"`
	Type         string `json:"type"` // local_bash|local_agent|remote_agent|…
	Description  string `json:"description"`
	Status       string `json:"status"` // running|completed|failed|stopped|killed
	ToolUseID    string `json:"toolUseId,omitempty"`
	SubagentType string `json:"subagentType,omitempty"`
	Background   bool   `json:"background,omitempty"`
	Start        int64  `json:"start"`
	End          int64  `json:"end,omitempty"`
	OutputFile   string `json:"outputFile,omitempty"`
	Summary      string `json:"summary,omitempty"`
	Progress     string `json:"progress,omitempty"`
	LastTool     string `json:"lastTool,omitempty"`
	Tokens       int64  `json:"tokens,omitempty"`
	ToolUses     int64  `json:"toolUses,omitempty"`
}

type toolState struct {
	name   string
	input  map[string]any
	hidden bool
	sub    bool // belongs to a subagent
	done   bool
}

type promptState struct {
	uuid      string
	itemID    string
	text      string
	delivered bool
	done      chan struct{}
	res       *acp.PromptResult
	err       error
}

type permState struct {
	toolUseID string
	sub       bool
}

// Agent is an in-process ACP agent backed by `claude -p`. The session engine
// talks to it through Conn exactly as it talks to `agy acp`.
type Agent struct {
	o     Options
	Conn  *acp.Conn // client side, used by the session engine
	agent *acp.Conn // agent side
	cw    *io.PipeWriter
	aw    *io.PipeWriter

	mu       sync.Mutex
	proc     *Proc
	sid      string
	cwd      string
	started  bool
	closed   bool
	exited   chan struct{}
	exitErr  error
	initResp map[string]json.RawMessage
	models   []modelInfo
	model    string // model value as chosen (alias or id); "" = default
	resolved string // concrete model id reported by the CLI
	mode     string
	effort   string
	ctxSize  int64
	cost     float64 // cumulative cost reported by this process
	streamed map[string]bool
	curMsg   string
	tools    map[string]*toolState
	prompts  []*promptState
	perms    map[string]*permState
	tasks    map[string]*Task
	order    []string
	cancel   bool
	lastCtx  int64
	autoTurn bool // a turn no prompt of ours started
}

type modelInfo struct {
	Value                 string   `json:"value"`
	ResolvedModel         string   `json:"resolvedModel"`
	DisplayName           string   `json:"displayName"`
	Description           string   `json:"description"`
	SupportsEffort        bool     `json:"supportsEffort"`
	SupportedEffortLevels []string `json:"supportedEffortLevels"`
	SupportsAutoMode      bool     `json:"supportsAutoMode"`
	SupportsFastMode      bool     `json:"supportsFastMode"`
}

// Start creates the adapter. The claude process starts on session/new,
// session/resume or session/load, which carry the cwd and session id.
func Start(o Options) *Agent {
	if o.ReplayLimit <= 0 {
		o.ReplayLimit = 4000
	}
	cr, cw := io.Pipe() // client → agent
	ar, aw := io.Pipe() // agent → client
	a := &Agent{o: o, cw: cw, aw: aw, exited: make(chan struct{}), streamed: map[string]bool{},
		tools: map[string]*toolState{}, perms: map[string]*permState{}, tasks: map[string]*Task{}}
	a.Conn = acp.NewConn(ar, cw, o.Handler)
	a.agent = acp.NewConn(cr, aw, agentHandler{a})
	return a
}

// ACP returns the client-side connection.
func (a *Agent) ACP() *acp.Conn { return a.Conn }

// Pid returns the claude process id (0 before it starts).
func (a *Agent) Pid() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.proc == nil {
		return 0
	}
	return a.proc.Pid()
}

// RSSKiB returns the claude process's resident memory.
func (a *Agent) RSSKiB() int64 {
	a.mu.Lock()
	p := a.proc
	a.mu.Unlock()
	if p == nil {
		return 0
	}
	return p.RSSKiB()
}

// Exited is closed when the session ends.
func (a *Agent) Exited() <-chan struct{} { return a.exited }

// Alive reports whether the session is usable.
func (a *Agent) Alive() bool {
	select {
	case <-a.exited:
		return false
	default:
		return true
	}
}

// ExitErr explains why the session ended.
func (a *Agent) ExitErr() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.exitErr
}

// SessionID returns the Claude session id once known.
func (a *Agent) SessionID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sid
}

// Close stops claude and tears the adapter down.
func (a *Agent) Close(grace time.Duration) {
	a.mu.Lock()
	p := a.proc
	a.closed = true
	a.mu.Unlock()
	if p != nil {
		p.Close(grace)
		<-a.exited
		return
	}
	a.finish(nil)
}

func (a *Agent) finish(err error) {
	a.mu.Lock()
	select {
	case <-a.exited:
		a.mu.Unlock()
		return
	default:
	}
	a.exitErr = err
	prompts := a.prompts
	a.prompts = nil
	a.mu.Unlock()
	for _, p := range prompts {
		p.err = ErrExited
		close(p.done)
	}
	_ = a.aw.Close()
	_ = a.cw.Close()
	close(a.exited)
}

// Control sends a raw control request to the running claude process.
func (a *Agent) Control(ctx context.Context, subtype string, params map[string]any) (json.RawMessage, error) {
	a.mu.Lock()
	p := a.proc
	a.mu.Unlock()
	if p == nil {
		return nil, errors.New("claude is not running")
	}
	return p.Control(ctx, subtype, params)
}

// ---- ACP agent side ----

type agentHandler struct{ a *Agent }

func (h agentHandler) HandleNotification(method string, params json.RawMessage) {
	if method == "session/cancel" {
		go h.a.interrupt()
	}
}

func (h agentHandler) HandleRequest(c *acp.Conn, id json.RawMessage, method string, params json.RawMessage) {
	go func() {
		res, err := h.a.handle(method, params)
		if err != nil {
			_ = c.ReplyError(id, -32000, err.Error())
			return
		}
		_ = c.Reply(id, res)
	}()
}

func (a *Agent) handle(method string, params json.RawMessage) (any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	switch method {
	case "initialize":
		return map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{"loadSession": true}}, nil
	case "session/new", "session/resume", "session/load":
		var p struct {
			SessionID string `json:"sessionId"`
			Cwd       string `json:"cwd"`
		}
		_ = json.Unmarshal(params, &p)
		return a.open(ctx, strings.TrimPrefix(method, "session/"), p.SessionID, p.Cwd)
	case "session/prompt":
		var p struct {
			Prompt []acp.ContentBlock `json:"prompt"`
			Meta   struct {
				Agentc struct {
					ItemID string `json:"itemId"`
					UUID   string `json:"uuid"`
				} `json:"agentc"`
			} `json:"_meta"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		return a.prompt(p.Prompt, p.Meta.Agentc.ItemID, p.Meta.Agentc.UUID)
	case "session/set_model":
		var p struct {
			ModelID string `json:"modelId"`
		}
		_ = json.Unmarshal(params, &p)
		return map[string]any{}, a.setModel(ctx, p.ModelID)
	case "session/set_mode":
		var p struct {
			ModeID string `json:"modeId"`
		}
		_ = json.Unmarshal(params, &p)
		return map[string]any{}, a.setMode(ctx, p.ModeID)
	case "session/set_config_option":
		var p struct {
			ConfigID string `json:"configId"`
			Value    string `json:"value"`
		}
		_ = json.Unmarshal(params, &p)
		var err error
		switch p.ConfigID {
		case "thought_level", "effort":
			err = a.setEffort(ctx, p.Value)
		case "model":
			err = a.setModel(ctx, p.Value)
		case "mode":
			err = a.setMode(ctx, p.Value)
		default:
			err = fmt.Errorf("unknown option %q", p.ConfigID)
		}
		if err != nil {
			return nil, err
		}
		a.mu.Lock()
		opts := a.configOptionsLocked()
		a.mu.Unlock()
		return map[string]any{"configOptions": opts}, nil
	case "_agentc/control":
		var p struct {
			Subtype string         `json:"subtype"`
			Params  map[string]any `json:"params"`
		}
		_ = json.Unmarshal(params, &p)
		return a.Control(ctx, p.Subtype, p.Params)
	}
	return nil, fmt.Errorf("method %s is not supported for Claude Code", method)
}

// NewUUID returns a random RFC 4122 v4 UUID.
func NewUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func (a *Agent) buildArgs(mode, sid string) []string {
	o := a.o
	var args []string
	if o.Model != "" && o.Model != "default" {
		args = append(args, "--model", o.Model)
	}
	if o.Effort != "" {
		args = append(args, "--effort", o.Effort)
	}
	if o.Mode != "" && o.Mode != "default" {
		args = append(args, "--permission-mode", o.Mode)
	} else if o.SkipPermissions {
		args = append(args, "--dangerously-skip-permissions")
	}
	args = append(args, "--allow-dangerously-skip-permissions")
	for _, d := range o.AddDirs {
		if strings.TrimSpace(d) != "" {
			args = append(args, "--add-dir", d)
		}
	}
	if strings.TrimSpace(o.AgentName) != "" {
		args = append(args, "--agent", strings.TrimSpace(o.AgentName))
	}
	switch {
	case mode == "new":
		args = append(args, "--session-id", sid)
	case o.ForkFrom != "":
		args = append(args, "--resume", o.ForkFrom, "--fork-session", "--session-id", sid)
		if o.ResumeAt != "" {
			args = append(args, "--resume-session-at", o.ResumeAt)
		}
	default:
		args = append(args, "--resume", sid)
		if o.ResumeAt != "" {
			args = append(args, "--resume-session-at", o.ResumeAt)
		}
	}
	args = append(args, "--prompt-suggestions")
	return append(args, o.ExtraArgs...)
}

func (a *Agent) open(ctx context.Context, mode, sid, cwd string) (any, error) {
	a.mu.Lock()
	if a.started {
		a.mu.Unlock()
		return nil, errors.New("this adapter already hosts a session")
	}
	a.started = true
	if mode == "new" || sid == "" {
		sid = NewUUID()
		mode = "new"
	}
	a.sid, a.cwd = sid, cwd
	a.model, a.effort = a.o.Model, a.o.Effort
	a.mode = a.o.Mode
	if a.mode == "" {
		a.mode = "default"
		if a.o.SkipPermissions {
			a.mode = "bypassPermissions"
		}
	}
	a.mu.Unlock()
	if mode == "load" {
		// Replay the stored conversation before the process starts writing to it.
		if err := a.replay(sid, cwd); err != nil {
			log.Printf("claude %s: replay: %v", sid, err)
			a.notify(ExtNotice, map[string]any{"level": "warn", "text": "Could not read the stored conversation: " + err.Error()})
		}
	}
	logFile := ""
	if a.o.LogDir != "" {
		logFile = filepath.Join(a.o.LogDir, fmt.Sprintf("claude-%s-%s.log", time.Now().Format("20060102-150405"), sid[:8]))
	}
	p, err := StartProc(ProcOptions{Bin: a.o.Bin, Cwd: cwd, Args: a.buildArgs(mode, sid), Env: a.o.Env, LogFile: logFile,
		OnMessage: a.onMessage, OnControl: a.onControl, OnControlCancel: a.onControlCancel, Trace: a.o.Trace})
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.proc = p
	a.mu.Unlock()
	go func() {
		<-p.Exited()
		err := p.ExitErr()
		a.mu.Lock()
		closed := a.closed
		a.mu.Unlock()
		if closed {
			err = nil
		} else if err == nil {
			err = errors.New("claude exited")
		}
		a.finish(err)
	}()
	initRes, err := p.Control(ctx, "initialize", map[string]any{})
	if err != nil {
		tail := ""
		if logFile != "" {
			tail = logTail(logFile)
		}
		p.Close(3 * time.Second)
		if tail != "" {
			return nil, fmt.Errorf("claude did not start: %v: %s", err, tail)
		}
		return nil, fmt.Errorf("claude did not start: %w", err)
	}
	var ir map[string]json.RawMessage
	_ = json.Unmarshal(initRes, &ir)
	var models []modelInfo
	_ = json.Unmarshal(ir["models"], &models)
	var cpm string
	_ = json.Unmarshal(ir["current_permission_mode"], &cpm)
	// The effective effort comes from settings when not chosen explicitly.
	effort := a.o.Effort
	if effort == "" {
		if raw, err := p.Control(ctx, "get_settings", nil); err == nil {
			var gs struct {
				Applied struct {
					Effort string `json:"effort"`
					Model  string `json:"model"`
				} `json:"applied"`
			}
			if json.Unmarshal(raw, &gs) == nil {
				effort = gs.Applied.Effort
				a.mu.Lock()
				a.resolved = gs.Applied.Model
				a.mu.Unlock()
			}
		}
	}
	a.mu.Lock()
	a.initResp, a.models = ir, models
	if cpm != "" {
		a.mode = cpm
	}
	a.effort = effort
	setup := a.setupLocked()
	cmds := a.commandsLocked()
	a.mu.Unlock()
	a.update(map[string]any{"sessionUpdate": "available_commands_update", "availableCommands": cmds})
	info := map[string]any{}
	for _, k := range []string{"output_style", "available_output_styles", "agents", "claude_code_version", "fast_mode_state"} {
		if v, ok := ir[k]; ok {
			info[k] = v
		}
	}
	if acc, ok := ir["account"]; ok {
		var am map[string]any
		if json.Unmarshal(acc, &am) == nil {
			delete(am, "email") // the app doesn't need it
			info["account"] = am
		}
	}
	a.notify(ExtInfo, info)
	return setup, nil
}

func logTail(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	var keep []string
	for i := len(lines) - 1; i >= 1 && len(keep) < 3; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" && !strings.Contains(l, " exited: ") {
			keep = append([]string{l}, keep...)
		}
	}
	return strings.Join(keep, " | ")
}

var modeNames = []acp.Mode{
	{ID: "default", Name: "Ask before edits", Description: "Claude asks before editing files or running commands"},
	{ID: "acceptEdits", Name: "Accept edits", Description: "File edits are applied without asking"},
	{ID: "plan", Name: "Plan mode", Description: "Claude researches and proposes a plan without changing anything"},
	{ID: "auto", Name: "Auto", Description: "A classifier approves safe actions and asks about risky ones"},
	{ID: "bypassPermissions", Name: "Bypass permissions", Description: "Everything runs without asking"},
	{ID: "dontAsk", Name: "Don't ask", Description: "Anything not pre-approved is denied"},
}

func (a *Agent) currentModelLocked() *modelInfo {
	want := a.model
	if want == "" {
		want = "default"
	}
	for i := range a.models {
		if a.models[i].Value == want {
			return &a.models[i]
		}
	}
	for i := range a.models {
		if a.resolved != "" && a.models[i].Value == a.resolved {
			return &a.models[i]
		}
	}
	return nil
}

func (a *Agent) modesLocked() *acp.Modes {
	m := &acp.Modes{CurrentModeID: a.mode}
	cur := a.currentModelLocked()
	for _, md := range modeNames {
		if md.ID == "auto" && cur != nil && !cur.SupportsAutoMode && a.mode != "auto" {
			continue
		}
		m.AvailableModes = append(m.AvailableModes, md)
	}
	return m
}

func (a *Agent) modelsLocked() *acp.Models {
	ms := &acp.Models{CurrentModelID: firstNonEmpty(a.model, "default")}
	for _, m := range a.models {
		ms.AvailableModels = append(ms.AvailableModels, acp.Model{ModelID: m.Value, Name: m.DisplayName, Description: m.Description})
	}
	return ms
}

func (a *Agent) configOptionsLocked() []acp.ConfigOption {
	cur := a.currentModelLocked()
	levels := []string{"low", "medium", "high", "xhigh", "max"}
	if cur != nil {
		if !cur.SupportsEffort {
			return nil
		}
		if len(cur.SupportedEffortLevels) > 0 {
			levels = cur.SupportedEffortLevels
		}
	}
	names := map[string]string{"low": "Low", "medium": "Medium", "high": "High", "xhigh": "Extra high", "max": "Max"}
	o := acp.ConfigOption{ID: "thought_level", Name: "Effort", Category: "thought_level", Type: "select", CurrentValue: a.effort}
	for _, l := range levels {
		o.Options = append(o.Options, acp.ConfigOptionValue{Value: l, Name: firstNonEmpty(names[l], l)})
	}
	return []acp.ConfigOption{o}
}

func (a *Agent) setupLocked() acp.SessionSetup {
	return acp.SessionSetup{SessionID: a.sid, Models: a.modelsLocked(), Modes: a.modesLocked(), ConfigOptions: a.configOptionsLocked()}
}

// builtinCommands work in Claude's headless mode but aren't always listed.
var builtinCommands = []acp.Command{
	{Name: "compact", Description: "Summarize the conversation to free up context"},
	{Name: "context", Description: "Show what is using the context window"},
	{Name: "cost", Description: "Show usage and cost"},
	{Name: "clear", Description: "Start a fresh conversation"},
}

func (a *Agent) commandsLocked() []acp.Command {
	var cmds []struct {
		Name         string `json:"name"`
		Description  string `json:"description"`
		ArgumentHint string `json:"argumentHint"`
	}
	_ = json.Unmarshal(a.initResp["commands"], &cmds)
	seen := map[string]bool{}
	var out []acp.Command
	for _, c := range cmds {
		cmd := acp.Command{Name: c.Name, Description: c.Description}
		if c.ArgumentHint != "" {
			cmd.Input, _ = json.Marshal(map[string]string{"hint": c.ArgumentHint})
		}
		seen[c.Name] = true
		out = append(out, cmd)
	}
	for _, c := range builtinCommands {
		if !seen[c.Name] {
			out = append(out, c)
		}
	}
	return out
}

// ---- outgoing ACP helpers ----

func (a *Agent) update(u map[string]any) {
	a.mu.Lock()
	sid := a.sid
	a.mu.Unlock()
	_ = a.agent.Notify("session/update", map[string]any{"sessionId": sid, "update": u})
}

func (a *Agent) notify(method string, params any) {
	_ = a.agent.Notify(method, params)
}

// ---- prompting ----

func toClaudeContent(blocks []acp.ContentBlock) ([]map[string]any, string) {
	var out []map[string]any
	var text []string
	for _, b := range blocks {
		switch b.Type {
		case "text", "":
			out = append(out, map[string]any{"type": "text", "text": b.Text})
			text = append(text, b.Text)
		case "image":
			if b.Data != "" {
				out = append(out, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": firstNonEmpty(b.MimeType, "image/png"), "data": b.Data}})
			}
		case "resource_link":
			p := strings.TrimPrefix(b.URI, "file://")
			out = append(out, map[string]any{"type": "text", "text": "Attached file: " + p})
		case "resource":
			var r struct {
				URI  string `json:"uri"`
				Text string `json:"text"`
			}
			_ = json.Unmarshal(b.Resource, &r)
			out = append(out, map[string]any{"type": "text", "text": fmt.Sprintf("<file path=%q>\n%s\n</file>", strings.TrimPrefix(r.URI, "file://"), r.Text)})
		}
	}
	return out, strings.Join(text, "\n")
}

func (a *Agent) prompt(blocks []acp.ContentBlock, itemID, uuid string) (any, error) {
	content, text := toClaudeContent(blocks)
	if len(content) == 0 {
		return nil, errors.New("empty message")
	}
	if uuid == "" {
		uuid = NewUUID()
	}
	ps := &promptState{uuid: uuid, itemID: itemID, text: text, done: make(chan struct{})}
	a.mu.Lock()
	p := a.proc
	if p == nil {
		a.mu.Unlock()
		return nil, errors.New("claude is not running")
	}
	sid := a.sid
	a.prompts = append(a.prompts, ps)
	a.mu.Unlock()
	msg := map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": content},
		"parent_tool_use_id": nil, "session_id": sid, "uuid": uuid}
	if err := p.Send(msg); err != nil {
		a.dropPrompt(ps)
		return nil, err
	}
	<-ps.done
	if ps.err != nil {
		return nil, ps.err
	}
	return ps.res, nil
}

func (a *Agent) dropPrompt(ps *promptState) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i, x := range a.prompts {
		if x == ps {
			a.prompts = append(a.prompts[:i], a.prompts[i+1:]...)
			return
		}
	}
}

func (a *Agent) interrupt() {
	a.mu.Lock()
	p := a.proc
	busy := len(a.prompts) > 0 || a.autoTurn
	if busy {
		a.cancel = true
	}
	a.mu.Unlock()
	if p == nil || !busy {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := p.Control(ctx, "interrupt", nil); err != nil {
		log.Printf("claude: interrupt: %v", err)
	}
}

func (a *Agent) setModel(ctx context.Context, model string) error {
	if _, err := a.Control(ctx, "set_model", map[string]any{"model": model}); err != nil {
		return err
	}
	a.mu.Lock()
	a.model = model
	if model == "default" {
		a.model = ""
	}
	opts := a.configOptionsLocked()
	modes := a.modesLocked()
	a.mu.Unlock()
	a.update(map[string]any{"sessionUpdate": "config_option_update", "configOptions": opts})
	_ = modes
	return nil
}

func (a *Agent) setMode(ctx context.Context, mode string) error {
	if _, err := a.Control(ctx, "set_permission_mode", map[string]any{"mode": mode}); err != nil {
		return err
	}
	a.mu.Lock()
	a.mode = mode
	a.mu.Unlock()
	a.update(map[string]any{"sessionUpdate": "current_mode_update", "currentModeId": mode})
	return nil
}

func (a *Agent) setEffort(ctx context.Context, effort string) error {
	var v any = effort
	if effort == "" || effort == "default" {
		v = nil
	}
	if _, err := a.Control(ctx, "apply_flag_settings", map[string]any{"settings": map[string]any{"effortLevel": v}}); err != nil {
		return err
	}
	a.mu.Lock()
	a.effort = effort
	a.mu.Unlock()
	return nil
}

// ---- incoming claude messages ----

type assistantMsg struct {
	UUID            string  `json:"uuid"`
	ParentToolUseID *string `json:"parent_tool_use_id"`
	Message         struct {
		ID      string            `json:"id"`
		Model   string            `json:"model"`
		Content []json.RawMessage `json:"content"`
		Usage   *usage            `json:"usage"`
	} `json:"message"`
	Error string `json:"error"`
}

type usage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	OutputTokensDetails      struct {
		ThinkingTokens int64 `json:"thinking_tokens"`
	} `json:"output_tokens_details"`
}

type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

func (a *Agent) onMessage(h Head, raw json.RawMessage) {
	switch h.Type {
	case "stream_event":
		a.onStream(raw)
	case "assistant":
		a.onAssistant(raw)
	case "user":
		a.onUser(raw)
	case "result":
		a.onResult(raw)
	case "system":
		a.onSystem(h.Subtype, raw)
	case "command_lifecycle":
		var c struct {
			UUID  string `json:"command_uuid"`
			State string `json:"state"`
		}
		if json.Unmarshal(raw, &c) == nil && c.State == "started" {
			a.markDelivered(c.UUID)
		}
	case "rate_limit_event":
		var r struct {
			Info json.RawMessage `json:"rate_limit_info"`
		}
		if json.Unmarshal(raw, &r) == nil && len(r.Info) > 0 {
			a.notify(ExtRateLimit, r.Info)
		}
	case "prompt_suggestion":
		var r struct {
			Suggestion string `json:"suggestion"`
		}
		if json.Unmarshal(raw, &r) == nil && strings.TrimSpace(r.Suggestion) != "" {
			a.notify(ExtSuggestion, map[string]any{"text": r.Suggestion})
		}
	}
}

func (a *Agent) onStream(raw json.RawMessage) {
	var e struct {
		ParentToolUseID *string `json:"parent_tool_use_id"`
		Event           struct {
			Type    string `json:"type"`
			Index   int    `json:"index"`
			Message struct {
				ID    string `json:"id"`
				Usage *usage `json:"usage"`
			} `json:"message"`
			ContentBlock block `json:"content_block"`
			Delta        struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				Thinking string `json:"thinking"`
			} `json:"delta"`
		} `json:"event"`
	}
	if json.Unmarshal(raw, &e) != nil || e.ParentToolUseID != nil {
		return
	}
	switch e.Event.Type {
	case "message_start":
		a.mu.Lock()
		a.curMsg = e.Event.Message.ID
		a.streamed[e.Event.Message.ID] = true
		a.mu.Unlock()
		a.noteActivity()
	case "content_block_start":
		if b := e.Event.ContentBlock; b.Type == "tool_use" && b.ID != "" {
			a.mu.Lock()
			_, known := a.tools[b.ID]
			if !known {
				a.tools[b.ID] = &toolState{name: b.Name, hidden: b.Name == "TodoWrite"}
			}
			cwd := a.cwd
			a.mu.Unlock()
			if !known && b.Name != "TodoWrite" {
				ti := DescribeTool(b.Name, nil, cwd)
				a.update(map[string]any{"sessionUpdate": "tool_call", "toolCallId": b.ID, "name": b.Name, "title": ti.Title,
					"kind": ti.Kind, "status": "pending", "rawInput": map[string]any{}})
			}
		}
	case "content_block_delta":
		switch e.Event.Delta.Type {
		case "text_delta":
			if e.Event.Delta.Text != "" {
				a.update(map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": e.Event.Delta.Text}})
			}
		case "thinking_delta":
			if e.Event.Delta.Thinking != "" {
				a.update(map[string]any{"sessionUpdate": "agent_thought_chunk", "content": map[string]any{"type": "text", "text": e.Event.Delta.Thinking}})
			}
		}
	}
}

func (a *Agent) onAssistant(raw json.RawMessage) {
	var m assistantMsg
	if json.Unmarshal(raw, &m) != nil {
		return
	}
	if m.ParentToolUseID == nil {
		a.noteActivity()
	}
	if m.ParentToolUseID != nil {
		// A subagent's step: mark its tool uses so their results stay out of the
		// main transcript, and surface progress in the live state.
		for _, rb := range m.Message.Content {
			var b block
			if json.Unmarshal(rb, &b) == nil && b.Type == "tool_use" {
				a.mu.Lock()
				a.tools[b.ID] = &toolState{name: b.Name, sub: true}
				a.mu.Unlock()
			}
		}
		return
	}
	a.mu.Lock()
	streamed := a.streamed[m.Message.ID]
	cwd := a.cwd
	if m.Message.Model != "" && m.Message.Model != "<synthetic>" {
		a.resolved = m.Message.Model
	}
	a.mu.Unlock()
	for _, rb := range m.Message.Content {
		var b block
		if json.Unmarshal(rb, &b) != nil {
			continue
		}
		switch b.Type {
		case "text":
			if !streamed && strings.TrimSpace(b.Text) != "" {
				a.update(map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": b.Text}})
			}
		case "thinking":
			if !streamed && strings.TrimSpace(b.Thinking) != "" {
				a.update(map[string]any{"sessionUpdate": "agent_thought_chunk", "content": map[string]any{"type": "text", "text": b.Thinking}})
			}
		case "tool_use":
			var input map[string]any
			_ = json.Unmarshal(b.Input, &input)
			a.mu.Lock()
			ts := a.tools[b.ID]
			if ts == nil {
				ts = &toolState{name: b.Name}
				a.tools[b.ID] = ts
			}
			ts.input = input
			ts.hidden = b.Name == "TodoWrite"
			a.mu.Unlock()
			if b.Name == "TodoWrite" {
				a.update(map[string]any{"sessionUpdate": "plan", "entries": TodosToPlan(input)})
				continue
			}
			if b.Name == "EnterPlanMode" {
				a.mu.Lock()
				a.mode = "plan"
				a.mu.Unlock()
			}
			ti := DescribeTool(b.Name, input, cwd)
			u := map[string]any{"sessionUpdate": "tool_call", "toolCallId": b.ID, "name": b.Name, "title": ti.Title,
				"kind": ti.Kind, "status": "in_progress", "rawInput": input}
			if len(ti.Locations) > 0 {
				u["locations"] = ti.Locations
			}
			if len(ti.Content) > 0 {
				u["content"] = ti.Content
			}
			a.update(u)
		}
	}
	if m.Error != "" {
		a.notify(ExtNotice, map[string]any{"level": "error", "text": apiErrorText(m.Error, m.Message.Content)})
	}
	if u := m.Message.Usage; u != nil {
		used := u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens + u.OutputTokens
		a.mu.Lock()
		size := a.ctxSize
		changed := used > 0 && used != a.lastCtx
		if changed {
			a.lastCtx = used
		}
		a.mu.Unlock()
		if changed {
			if size == 0 {
				size = 200000
			}
			a.update(map[string]any{"sessionUpdate": "usage_update", "used": used, "size": size})
		}
	}
}

func apiErrorText(kind string, content []json.RawMessage) string {
	for _, rb := range content {
		var b block
		if json.Unmarshal(rb, &b) == nil && b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			return b.Text
		}
	}
	return "Claude API error: " + kind
}

type userMsg struct {
	UUID            string          `json:"uuid"`
	ParentToolUseID *string         `json:"parent_tool_use_id"`
	IsReplay        bool            `json:"isReplay"`
	IsSynthetic     bool            `json:"isSynthetic"`
	ToolUseResult   json.RawMessage `json:"tool_use_result"`
	Message         struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

func (a *Agent) onUser(raw json.RawMessage) {
	var m userMsg
	if json.Unmarshal(raw, &m) != nil {
		return
	}
	if m.IsReplay {
		a.markDelivered(m.UUID)
		return
	}
	var blocks []block
	if json.Unmarshal(m.Message.Content, &blocks) != nil {
		return // plain text from the CLI itself (interruption markers etc.)
	}
	for _, b := range blocks {
		if b.Type != "tool_result" {
			continue
		}
		a.mu.Lock()
		ts := a.tools[b.ToolUseID]
		if ts != nil {
			ts.done = true
		}
		a.mu.Unlock()
		if ts == nil || ts.hidden || ts.sub || m.ParentToolUseID != nil {
			continue
		}
		var tur json.RawMessage
		if len(blocks) == 1 {
			tur = m.ToolUseResult
		}
		status, rawOut, content := ToolResult(ts.name, ts.input, b.Content, b.IsError, tur)
		u := map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": b.ToolUseID, "status": status}
		if rawOut != nil {
			u["rawOutput"] = rawOut
		}
		if content != nil {
			u["content"] = content
		}
		a.update(u)
		if ts.name == "ExitPlanMode" && !b.IsError {
			a.mu.Lock()
			if a.mode == "plan" {
				a.mode = "default"
			}
			mode := a.mode
			a.mu.Unlock()
			a.update(map[string]any{"sessionUpdate": "current_mode_update", "currentModeId": mode})
		}
	}
}

func (a *Agent) markDelivered(uuid string) {
	if uuid == "" {
		return
	}
	a.mu.Lock()
	var ps *promptState
	for _, p := range a.prompts {
		if p.uuid == uuid && !p.delivered {
			p.delivered = true
			ps = p
		}
	}
	a.mu.Unlock()
	if ps != nil {
		a.notify(ExtUserMessage, map[string]any{"itemId": ps.itemID, "uuid": ps.uuid})
	}
}

type resultMsg struct {
	Subtype      string                     `json:"subtype"`
	IsError      bool                       `json:"is_error"`
	StopReason   *string                    `json:"stop_reason"`
	Result       string                     `json:"result"`
	TotalCostUSD float64                    `json:"total_cost_usd"`
	DurationMs   int64                      `json:"duration_ms"`
	NumTurns     int                        `json:"num_turns"`
	Usage        *usage                     `json:"usage"`
	ModelUsage   map[string]json.RawMessage `json:"modelUsage"`
	Errors       []string                   `json:"errors"`
}

func (a *Agent) onResult(raw json.RawMessage) {
	var r resultMsg
	if json.Unmarshal(raw, &r) != nil {
		return
	}
	a.mu.Lock()
	cancelled := a.cancel
	a.cancel = false
	costDelta := r.TotalCostUSD - a.cost
	if costDelta < 0 {
		costDelta = r.TotalCostUSD
	}
	a.cost = r.TotalCostUSD
	for _, mu := range r.ModelUsage {
		var x struct {
			ContextWindow int64 `json:"contextWindow"`
		}
		if json.Unmarshal(mu, &x) == nil && x.ContextWindow > a.ctxSize {
			a.ctxSize = x.ContextWindow
		}
	}
	// Every prompt the CLI has taken up is answered by this result; one it
	// never acknowledged (slash commands aren't echoed) is the oldest pending.
	var done []*promptState
	var keep []*promptState
	for _, p := range a.prompts {
		if p.delivered {
			done = append(done, p)
		} else {
			keep = append(keep, p)
		}
	}
	if len(done) == 0 && len(keep) > 0 {
		done, keep = keep[:1], keep[1:]
	}
	a.prompts = keep
	size, used := a.ctxSize, a.lastCtx
	a.mu.Unlock()
	if size > 0 && used > 0 {
		a.update(map[string]any{"sessionUpdate": "usage_update", "used": used, "size": size})
	}
	stop := "end_turn"
	var err error
	switch {
	case cancelled:
		stop = "cancelled"
	case r.Subtype == "success":
		if r.StopReason != nil && *r.StopReason != "" && *r.StopReason != "tool_use" {
			stop = *r.StopReason
		}
	case r.Subtype == "error_max_turns":
		stop = "max_turn_requests"
	case r.Subtype == "error_max_budget_usd":
		err = errors.New("stopped: the spending limit (--max-budget-usd) was reached")
	default:
		msg := strings.Join(r.Errors, "; ")
		if strings.HasPrefix(msg, "[ede_diagnostic]") || msg == "" {
			msg = "Claude stopped with an error (" + r.Subtype + ")"
		}
		if r.Result != "" && r.IsError {
			msg = r.Result
		}
		err = errors.New(msg)
	}
	var u *acp.Usage
	if r.Usage != nil {
		u = &acp.Usage{InputTokens: r.Usage.InputTokens + r.Usage.CacheCreationInputTokens + r.Usage.CacheReadInputTokens,
			OutputTokens: r.Usage.OutputTokens, CachedReadTokens: r.Usage.CacheReadInputTokens,
			CachedWriteTokens: r.Usage.CacheCreationInputTokens, ThoughtTokens: r.Usage.OutputTokensDetails.ThinkingTokens,
			CostUSD: costDelta}
		u.TotalTokens = u.InputTokens + u.OutputTokens
	}
	for i, p := range done {
		if err != nil && !(cancelled) {
			p.err = err
		} else {
			p.res = &acp.PromptResult{StopReason: stop}
			if i == len(done)-1 {
				p.res.Usage = u
			}
		}
		close(p.done)
	}
	a.mu.Lock()
	auto := a.autoTurn
	a.autoTurn = false
	a.mu.Unlock()
	if auto {
		ev := map[string]any{"state": "ended", "stopReason": stop}
		if len(done) == 0 && u != nil {
			ev["usage"] = u
		}
		if err != nil {
			ev["error"] = err.Error()
		}
		a.notify(ExtTurn, ev)
	}
}

// noteActivity starts an autonomous turn when Claude works without a prompt
// of ours pending (e.g. a background subagent finished and Claude picks up
// its report).
func (a *Agent) noteActivity() {
	a.mu.Lock()
	start := len(a.prompts) == 0 && !a.autoTurn && a.initResp != nil
	if start {
		a.autoTurn = true
	}
	a.mu.Unlock()
	if start {
		a.notify(ExtTurn, map[string]any{"state": "started"})
	}
}

func (a *Agent) onSystem(subtype string, raw json.RawMessage) {
	switch subtype {
	case "init":
		var m struct {
			Model          string `json:"model"`
			PermissionMode string `json:"permissionMode"`
		}
		if json.Unmarshal(raw, &m) != nil {
			return
		}
		a.noteActivity()
		a.mu.Lock()
		if m.Model != "" {
			a.resolved = m.Model
		}
		changed := m.PermissionMode != "" && m.PermissionMode != a.mode
		if changed {
			a.mode = m.PermissionMode
		}
		a.mu.Unlock()
		if changed {
			a.update(map[string]any{"sessionUpdate": "current_mode_update", "currentModeId": m.PermissionMode})
		}
	case "session_title_changed":
		var m struct {
			Title string `json:"title"`
		}
		if json.Unmarshal(raw, &m) == nil && m.Title != "" {
			a.update(map[string]any{"sessionUpdate": "session_info_update", "title": m.Title})
		}
	case "commands_changed":
		var m struct {
			Commands json.RawMessage `json:"commands"`
		}
		if json.Unmarshal(raw, &m) == nil && len(m.Commands) > 0 {
			a.mu.Lock()
			if a.initResp == nil {
				a.initResp = map[string]json.RawMessage{}
			}
			a.initResp["commands"] = m.Commands
			cmds := a.commandsLocked()
			a.mu.Unlock()
			a.update(map[string]any{"sessionUpdate": "available_commands_update", "availableCommands": cmds})
		}
	case "compact_boundary":
		var m struct {
			Meta struct {
				Trigger   string `json:"trigger"`
				PreTokens int64  `json:"pre_tokens"`
			} `json:"compact_metadata"`
		}
		_ = json.Unmarshal(raw, &m)
		text := "Conversation compacted"
		if m.Meta.Trigger == "auto" {
			text += " automatically"
		}
		a.notify(ExtNotice, map[string]any{"level": "info", "text": text})
	case "status":
		var m struct {
			Status *string `json:"status"`
		}
		if json.Unmarshal(raw, &m) == nil {
			st := ""
			if m.Status != nil {
				st = *m.Status
			}
			a.notify(ExtStatus, map[string]any{"status": st})
		}
	case "api_retry":
		var m struct {
			Attempt    int    `json:"attempt"`
			MaxRetries int    `json:"max_retries"`
			DelayMs    int64  `json:"retry_delay_ms"`
			Status     int    `json:"error_status"`
			Error      string `json:"error"`
		}
		_ = json.Unmarshal(raw, &m)
		text := "API error"
		if m.Status > 0 {
			text += fmt.Sprintf(" (%d)", m.Status)
		}
		if m.Attempt > 0 {
			text += fmt.Sprintf(", retrying (attempt %d", m.Attempt)
			if m.MaxRetries > 0 {
				text += fmt.Sprintf(" of %d", m.MaxRetries)
			}
			text += ")"
		}
		a.notify(ExtNotice, map[string]any{"level": "warn", "text": text})
	case "informational", "notification", "model_fallback", "model_refusal_fallback", "model_refusal_no_fallback", "permission_denied":
		var m map[string]any
		if json.Unmarshal(raw, &m) != nil {
			return
		}
		text := firstNonEmpty(str(m, "message"), str(m, "text"), str(m, "content"), str(m, "summary"))
		if text == "" {
			return
		}
		level := "info"
		if subtype != "informational" && subtype != "notification" {
			level = "warn"
		}
		a.notify(ExtNotice, map[string]any{"level": level, "text": text})
	case "task_started", "task_progress", "task_updated", "task_notification", "background_tasks_changed":
		a.onTask(subtype, raw)
	}
}

func (a *Agent) onTask(subtype string, raw json.RawMessage) {
	var m struct {
		TaskID       string `json:"task_id"`
		ToolUseID    string `json:"tool_use_id"`
		Description  string `json:"description"`
		TaskType     string `json:"task_type"`
		SubagentType string `json:"subagent_type"`
		Background   bool   `json:"is_backgrounded"`
		Status       string `json:"status"`
		OutputFile   string `json:"output_file"`
		Summary      string `json:"summary"`
		LastToolName string `json:"last_tool_name"`
		Usage        struct {
			TotalTokens int64 `json:"total_tokens"`
			ToolUses    int64 `json:"tool_uses"`
		} `json:"usage"`
		Patch struct {
			Status  string `json:"status"`
			EndTime int64  `json:"end_time"`
		} `json:"patch"`
		Tasks []struct {
			TaskID      string `json:"task_id"`
			TaskType    string `json:"task_type"`
			Description string `json:"description"`
		} `json:"tasks"`
	}
	if json.Unmarshal(raw, &m) != nil {
		return
	}
	now := time.Now().UnixMilli()
	a.mu.Lock()
	get := func(id string) *Task {
		t := a.tasks[id]
		if t == nil {
			t = &Task{ID: id, Status: "running", Start: now}
			a.tasks[id] = t
			a.order = append(a.order, id)
		}
		return t
	}
	switch subtype {
	case "task_started":
		t := get(m.TaskID)
		t.Type, t.Description, t.ToolUseID = m.TaskType, m.Description, m.ToolUseID
		t.SubagentType, t.Background = m.SubagentType, m.Background
		if t.Type == "local_bash" {
			t.OutputFile = a.taskOutputPathLocked(t.ID)
		}
	case "task_progress":
		t := get(m.TaskID)
		t.Progress = m.Description
		if m.LastToolName != "" {
			t.LastTool = m.LastToolName
		}
		t.Tokens, t.ToolUses = m.Usage.TotalTokens, m.Usage.ToolUses
	case "task_updated":
		t := get(m.TaskID)
		if m.Patch.Status != "" {
			t.Status = m.Patch.Status
		}
		if m.Patch.EndTime > 0 {
			t.End = m.Patch.EndTime
		}
	case "task_notification":
		t := get(m.TaskID)
		if m.Status != "" {
			t.Status = m.Status
		}
		if m.OutputFile != "" {
			t.OutputFile = m.OutputFile
		}
		if m.Summary != "" {
			t.Summary = m.Summary
		}
		if t.End == 0 {
			t.End = now
		}
	case "background_tasks_changed":
		running := map[string]bool{}
		for _, bt := range m.Tasks {
			running[bt.TaskID] = true
			t := get(bt.TaskID)
			t.Background = true
			if t.Type == "" {
				t.Type = bt.TaskType
			}
			if t.Description == "" {
				t.Description = bt.Description
			}
		}
		_ = running
	}
	tasks := a.tasksLocked()
	a.mu.Unlock()
	a.notify(ExtLive, map[string]any{"tasks": tasks})
}

func (a *Agent) tasksLocked() []Task {
	out := make([]Task, 0, len(a.order))
	for _, id := range a.order {
		if t := a.tasks[id]; t != nil {
			out = append(out, *t)
		}
	}
	// Keep the list bounded: drop the oldest finished entries.
	if len(out) > 60 {
		sort.SliceStable(out, func(i, j int) bool { return out[i].Status == "running" && out[j].Status != "running" })
		out = out[:60]
	}
	return out
}

// Tasks returns the current background tasks and subagents.
func (a *Agent) Tasks() []Task {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.tasksLocked()
}

// TaskOutputPath returns where Claude writes a background command's output.
func (a *Agent) TaskOutputPath(id string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if t := a.tasks[id]; t != nil && t.OutputFile != "" {
		return t.OutputFile
	}
	return a.taskOutputPathLocked(id)
}

func (a *Agent) taskOutputPathLocked(id string) string {
	tmp := os.Getenv("TMPDIR")
	for _, kv := range a.o.Env {
		if strings.HasPrefix(kv, "TMPDIR=") {
			tmp = strings.TrimPrefix(kv, "TMPDIR=")
		}
	}
	if tmp == "" {
		tmp = "/tmp"
	}
	return filepath.Join(tmp, fmt.Sprintf("claude-%d", os.Getuid()), SanitizePath(a.cwd), a.sid, "tasks", id+".output")
}

// ---- permissions ----

type canUseTool struct {
	ToolName                string           `json:"tool_name"`
	Input                   map[string]any   `json:"input"`
	PermissionSuggestions   []map[string]any `json:"permission_suggestions"`
	BlockedPath             string           `json:"blocked_path"`
	DecisionReason          string           `json:"decision_reason"`
	Title                   string           `json:"title"`
	DisplayName             string           `json:"display_name"`
	ToolUseID               string           `json:"tool_use_id"`
	AgentID                 string           `json:"agent_id"`
	Description             string           `json:"description"`
	RequiresUserInteraction bool             `json:"requires_user_interaction"`
	SuppressAlwaysAllow     bool             `json:"suppress_always_allow_rule"`
}

func (a *Agent) onControl(id string, raw json.RawMessage) {
	var h struct {
		Subtype string `json:"subtype"`
	}
	_ = json.Unmarshal(raw, &h)
	a.mu.Lock()
	p := a.proc
	a.mu.Unlock()
	if p == nil {
		return
	}
	switch h.Subtype {
	case "can_use_tool":
		var req canUseTool
		if err := json.Unmarshal(raw, &req); err != nil {
			_ = p.RespondError(id, "bad request")
			return
		}
		go a.askPermission(p, id, req)
	case "elicitation":
		_ = p.Respond(id, map[string]any{"action": "decline"})
	default:
		_ = p.RespondError(id, "agentc does not support "+h.Subtype)
	}
}

func (a *Agent) onControlCancel(id string) {
	a.mu.Lock()
	ps := a.perms[id]
	delete(a.perms, id)
	a.mu.Unlock()
	if ps != nil {
		a.notify(ExtPermGone, map[string]any{"key": id})
	}
}

func suggestionLabel(s []map[string]any) string {
	for _, x := range s {
		switch str(x, "type") {
		case "setMode":
			switch str(x, "mode") {
			case "acceptEdits":
				return "Allow all edits this session"
			case "bypassPermissions":
				return "Allow everything this session"
			}
		case "addRules", "replaceRules":
			rules, _ := x["rules"].([]any)
			var names []string
			for _, r := range rules {
				if rm, ok := r.(map[string]any); ok {
					n := str(rm, "toolName")
					if c := str(rm, "ruleContent"); c != "" {
						n += "(" + c + ")"
					}
					names = append(names, n)
				}
			}
			scope := ""
			switch str(x, "destination") {
			case "session":
				scope = " this session"
			case "localSettings", "projectSettings":
				scope = " in this project"
			case "userSettings":
				scope = " everywhere"
			}
			if len(names) > 0 {
				return "Always allow " + short(strings.Join(names, ", "), 60) + scope
			}
		case "addDirectories":
			dirs, _ := x["directories"].([]any)
			if len(dirs) > 0 {
				return fmt.Sprintf("Allow access to %v", dirs[0])
			}
		}
	}
	return "Always allow"
}

func (a *Agent) askPermission(p *Proc, id string, req canUseTool) {
	a.mu.Lock()
	cwd, sid := a.cwd, a.sid
	ts := a.tools[req.ToolUseID]
	sub := req.AgentID != "" || (ts != nil && ts.sub)
	bypassOK := true
	a.perms[id] = &permState{toolUseID: req.ToolUseID, sub: sub}
	a.mu.Unlock()
	ti := DescribeTool(req.ToolName, req.Input, cwd)
	callID := req.ToolUseID
	if callID == "" {
		callID = "perm-" + id
	}
	if sub {
		callID = "sub:" + callID
	}
	title := ti.Title
	if title == "" {
		title = firstNonEmpty(req.Description, req.DisplayName, req.ToolName)
	}
	toolCall := map[string]any{"toolCallId": callID, "name": req.ToolName, "title": title, "kind": ti.Kind, "status": "pending", "rawInput": req.Input}
	if len(ti.Content) > 0 {
		toolCall["content"] = ti.Content
	}
	if len(ti.Locations) > 0 {
		toolCall["locations"] = ti.Locations
	}
	var options []acp.PermissionOption
	kind := "tool"
	switch req.ToolName {
	case "AskUserQuestion":
		kind = "question"
		options = []acp.PermissionOption{{OptionID: "answer", Name: "Submit answers", Kind: "allow_once"}, {OptionID: "deny", Name: "Skip", Kind: "reject_once"}}
	case "ExitPlanMode":
		kind = "plan"
		options = []acp.PermissionOption{
			{OptionID: "plan_accept_edits", Name: "Yes, and auto-accept edits", Kind: "allow_always"},
			{OptionID: "plan_default", Name: "Yes, and manually approve edits", Kind: "allow_once"},
		}
		if bypassOK {
			options = append(options, acp.PermissionOption{OptionID: "plan_bypass", Name: "Yes, and bypass permissions", Kind: "allow_always"})
		}
		options = append(options, acp.PermissionOption{OptionID: "deny", Name: "No, keep planning", Kind: "reject_once"})
	default:
		options = []acp.PermissionOption{{OptionID: "allow", Name: "Allow", Kind: "allow_once"}}
		if len(req.PermissionSuggestions) > 0 && !req.SuppressAlwaysAllow {
			options = append(options, acp.PermissionOption{OptionID: "always", Name: suggestionLabel(req.PermissionSuggestions), Kind: "allow_always"})
		}
		options = append(options, acp.PermissionOption{OptionID: "deny", Name: "Deny", Kind: "reject_once"})
	}
	meta := map[string]any{"key": id, "kind": kind, "tool": req.ToolName}
	if req.DecisionReason != "" {
		meta["reason"] = req.DecisionReason
	}
	if req.BlockedPath != "" {
		meta["blockedPath"] = req.BlockedPath
	}
	if sub {
		meta["subagent"] = true
	}
	params := map[string]any{"sessionId": sid, "toolCall": toolCall, "options": options, "_meta": map[string]any{"agentc": meta}}
	var res struct {
		Outcome struct {
			Outcome  string `json:"outcome"`
			OptionID string `json:"optionId"`
			Meta     struct {
				Answers map[string]any `json:"answers"`
				Message string         `json:"message"`
			} `json:"_meta"`
		} `json:"outcome"`
	}
	err := a.agent.Call(context.Background(), "session/request_permission", params, &res)
	a.mu.Lock()
	_, still := a.perms[id]
	delete(a.perms, id)
	a.mu.Unlock()
	if !still {
		return // withdrawn by the CLI
	}
	allow := func(updated map[string]any, perms []map[string]any) map[string]any {
		r := map[string]any{"behavior": "allow", "updatedInput": updated}
		if len(perms) > 0 {
			r["updatedPermissions"] = perms
		}
		return r
	}
	setMode := func(mode string) []map[string]any {
		return []map[string]any{{"type": "setMode", "mode": mode, "destination": "session"}}
	}
	var resp map[string]any
	newMode := ""
	if err != nil || res.Outcome.Outcome != "selected" {
		resp = map[string]any{"behavior": "deny", "message": "The user cancelled this request.", "interrupt": true}
	} else {
		msg := strings.TrimSpace(res.Outcome.Meta.Message)
		switch res.Outcome.OptionID {
		case "allow":
			resp = allow(req.Input, nil)
		case "always":
			resp = allow(req.Input, req.PermissionSuggestions)
			for _, s := range req.PermissionSuggestions {
				if str(s, "type") == "setMode" {
					newMode = str(s, "mode")
				}
			}
		case "answer":
			in := map[string]any{}
			for k, v := range req.Input {
				in[k] = v
			}
			answers := res.Outcome.Meta.Answers
			if len(answers) == 0 && msg != "" {
				// A free-text reply (e.g. from a notification) answers every question.
				answers = map[string]any{}
				if qs, ok := req.Input["questions"].([]any); ok {
					for _, q := range qs {
						if qm, ok := q.(map[string]any); ok && str(qm, "question") != "" {
							answers[str(qm, "question")] = msg
						}
					}
				}
			}
			in["answers"] = answers
			resp = allow(in, nil)
		case "plan_accept_edits":
			resp, newMode = allow(req.Input, setMode("acceptEdits")), "acceptEdits"
		case "plan_default":
			resp, newMode = allow(req.Input, setMode("default")), "default"
		case "plan_bypass":
			resp, newMode = allow(req.Input, setMode("bypassPermissions")), "bypassPermissions"
		default:
			// Phrase it the way Claude Code's own terminal UI does, so the model
			// knows the text comes from the user rather than from the tool.
			const rejected = "The user doesn't want to proceed with this tool use. The tool use was rejected (eg. if it was a file edit, the new_string was NOT written to the file)."
			switch {
			case msg != "" && req.ToolName == "ExitPlanMode":
				msg = "The user doesn't want to proceed with this plan yet. To tell you how to proceed, the user said:\n" + msg
			case msg != "":
				msg = rejected + " To tell you how to proceed, the user said:\n" + msg
			case req.ToolName == "ExitPlanMode":
				msg = "The user doesn't want to proceed with this plan yet. Keep planning and ask what to change."
			case req.ToolName == "AskUserQuestion":
				msg = "The user skipped these questions."
			default:
				msg = rejected
			}
			resp = map[string]any{"behavior": "deny", "message": msg}
		}
	}
	if err := p.Respond(id, resp); err != nil {
		log.Printf("claude %s: permission reply: %v", sid, err)
	}
	if newMode != "" {
		a.mu.Lock()
		a.mode = newMode
		a.mu.Unlock()
		a.update(map[string]any{"sessionUpdate": "current_mode_update", "currentModeId": newMode})
	}
	if sub {
		// The subagent's own tool row lives outside the main transcript.
		st := "completed"
		if resp["behavior"] != "allow" {
			st = "failed"
		}
		a.update(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": callID, "status": st})
	}
}
