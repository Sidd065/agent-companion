// Package codex adapts OpenAI Codex CLI (`codex app-server`, >= 0.172.0)
// behind an in-process ACP connection with native OpenAI and OpenRouter
// `/v1/responses` bridge support.
package codex

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"agentc/internal/acp"
)

// AvailableModes returns the exact 5 Codex modes mandated by PROJECT.md § Interface Contracts 2.
func AvailableModes() []acp.Mode {
	return []acp.Mode{
		{ID: "default", Name: "Build (Supervised)"},
		{ID: "plan", Name: "Plan"},
		{ID: "acceptEdits", Name: "Auto-accept edits"},
		{ID: "auto", Name: "Auto (Workspace write)"},
		{ID: "full-access", Name: "Full access"},
	}
}

// ReasoningEfforts returns the exact 4 Codex reasoning efforts mandated by PROJECT.md.
func ReasoningEfforts() []string {
	return []string{"low", "medium", "high", "xhigh"}
}

// NormalizeModeID maps mode aliases to the canonical 5 mode IDs.
func NormalizeModeID(mode string, skipPermissions bool) string {
	switch strings.TrimSpace(mode) {
	case "plan":
		return "plan"
	case "acceptEdits", "accept-edits", "auto-edits":
		return "acceptEdits"
	case "auto":
		return "auto"
	case "full-access", "fullAccess", "bypassPermissions", "danger-full-access":
		return "full-access"
	case "default", "supervised", "build":
		return "default"
	case "":
		if skipPermissions {
			return "full-access"
		}
		return "default"
	default:
		return "default"
	}
}

// NormalizeEffort validates a reasoning effort string.
func NormalizeEffort(effort string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "low", "medium", "high", "xhigh", "minimal", "none", "max", "ultra":
		return strings.ToLower(strings.TrimSpace(effort))
	default:
		return ""
	}
}

// PoliciesForMode maps an agentc mode + explicit flag overrides onto Codex 0.172.0
// AskForApproval, SandboxMode (thread/*), SandboxPolicy (turn/start), and CollaborationMode.mode.
func PoliciesForMode(mode, explicitApproval, explicitSandbox string, skipPerms bool, cwd string, addDirs []string) (approvalPolicy, threadSandbox string, turnSandboxPolicy map[string]any, collabMode string) {
	roots := buildWritableRoots(cwd, addDirs)
	normMode := NormalizeModeID(mode, skipPerms)
	collabMode = "default"

	switch normMode {
	case "plan":
		collabMode = "plan"
		approvalPolicy = "on-request"
		if skipPerms {
			approvalPolicy = "never"
		}
		threadSandbox = "read-only"
		turnSandboxPolicy = map[string]any{
			"type":          "readOnly",
			"networkAccess": true,
		}
	case "acceptEdits":
		approvalPolicy = "on-request"
		threadSandbox = "workspace-write"
		turnSandboxPolicy = map[string]any{
			"type":          "workspaceWrite",
			"writableRoots": roots,
			"networkAccess": true,
		}
	case "auto":
		approvalPolicy = "never"
		threadSandbox = "workspace-write"
		turnSandboxPolicy = map[string]any{
			"type":          "workspaceWrite",
			"writableRoots": roots,
			"networkAccess": true,
		}
	case "full-access":
		approvalPolicy = "never"
		threadSandbox = "danger-full-access"
		turnSandboxPolicy = map[string]any{
			"type": "dangerFullAccess",
		}
	default: // "default" (Supervised)
		if skipPerms {
			approvalPolicy = "never"
			threadSandbox = "danger-full-access"
			turnSandboxPolicy = map[string]any{"type": "dangerFullAccess"}
		} else {
			approvalPolicy = "untrusted"
			threadSandbox = "workspace-write"
			turnSandboxPolicy = map[string]any{
				"type":          "workspaceWrite",
				"writableRoots": roots,
				"networkAccess": true,
			}
		}
	}

	switch strings.TrimSpace(explicitApproval) {
	case "untrusted", "on-request", "never":
		approvalPolicy = strings.TrimSpace(explicitApproval)
	}

	switch strings.TrimSpace(explicitSandbox) {
	case "read-only", "readOnly":
		threadSandbox = "read-only"
		turnSandboxPolicy = map[string]any{"type": "readOnly", "networkAccess": true}
	case "workspace-write", "workspaceWrite":
		threadSandbox = "workspace-write"
		turnSandboxPolicy = map[string]any{"type": "workspaceWrite", "writableRoots": roots, "networkAccess": true}
	case "danger-full-access", "dangerFullAccess":
		threadSandbox = "danger-full-access"
		turnSandboxPolicy = map[string]any{"type": "dangerFullAccess"}
	}

	return approvalPolicy, threadSandbox, turnSandboxPolicy, collabMode
}

func buildWritableRoots(cwd string, addDirs []string) []string {
	var roots []string
	seen := map[string]bool{}
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		if !seen[p] {
			seen[p] = true
			roots = append(roots, p)
		}
	}
	add(cwd)
	for _, d := range addDirs {
		add(d)
	}
	if len(roots) == 0 {
		return []string{}
	}
	return roots
}

// Options configure a persistent Codex ACP adapter instance.
type Options struct {
	Bin             string
	Env             []string
	Cwd             string
	AddDirs         []string
	CodexHome       string
	UserHome        string
	SkipPermissions bool
	ApprovalPolicy  string
	SandboxMode     string
	Provider        string // "" | "openrouter" | "openai"
	DefaultModel    string
	Model           string
	Effort          string
	Mode            string
	ExtraArgs       []string
	Ephemeral       bool
	ExcludeTurns    bool // when true, session/load uses excludeTurns: true to avoid duplicating pre-hydrated items

	Bridge        *Bridge
	Catalog       *Catalog
	CustomModels  []CustomModelInput
	HasOpenRouter bool
	HasNativeAuth bool

	Handler acp.Handler
	Trace   func(dir string, line []byte)
}

type codexUsageTotals struct {
	InputTokens       int64
	OutputTokens      int64
	CachedReadTokens  int64
	CachedWriteTokens int64
	ThoughtTokens     int64
	TotalTokens       int64
}

type turnCompletion struct {
	turnID string
	status string
	errMsg string
}

type pendingServerReq struct {
	reqKey string
	cancel context.CancelFunc
}

type subagentTaskInfo struct {
	ID           string `json:"id"`
	Type         string `json:"type"`
	Description  string `json:"description"`
	SubagentType string `json:"subagentType,omitempty"`
	Status       string `json:"status"`
	Progress     string `json:"progress,omitempty"`
	Start        int64  `json:"start"`
	End          int64  `json:"end,omitempty"`
}

// Agent implements core.agentProc over a persistent `codex app-server` process.
type Agent struct {
	o      Options
	Conn   *acp.Conn
	agent  *acp.Conn
	cw     *io.PipeWriter
	aw     *io.PipeWriter
	exited chan struct{}

	mu            sync.Mutex
	proc          *AppServerProc
	sid           string
	cwd           string
	useOpenRouter bool
	wireModel     string
	displayModel  string
	mode          string
	effort        string
	closed        bool
	exitErr       error

	activeTurnID    string
	turnWaiters     map[string]chan turnCompletion
	anyTurnWaiter   chan turnCompletion
	compactWaiters  []chan struct{}
	cumulativeUsage codexUsageTotals

	sawTextDelta map[string]bool
	cmdOutputs   map[string]*strings.Builder
	lastTurnDiff string
	serverReqs   map[string]*pendingServerReq
	subagents    map[string]subagentTaskInfo
}

// Start creates a new in-process Codex ACP adapter.
func Start(o Options) *Agent {
	if strings.TrimSpace(o.Bin) == "" {
		o.Bin = ResolveBinary("", o.UserHome)
	}
	hasOR := o.HasOpenRouter || (o.Bridge != nil && o.Bridge.HasAPIKey())
	hasNative := o.HasNativeAuth || HasNativeOpenAIAuth(o.CodexHome, o.UserHome, o.Env)
	useOR, wireModel, dispModel := ResolveProviderAndModel(o.Model, o.DefaultModel, o.Provider, hasOR, hasNative)

	cr, aw := io.Pipe()
	ar, cw := io.Pipe()
	a := &Agent{
		o:             o,
		cw:            cw,
		aw:            aw,
		exited:        make(chan struct{}),
		cwd:           o.Cwd,
		useOpenRouter: useOR,
		wireModel:     wireModel,
		displayModel:  dispModel,
		mode:          NormalizeModeID(o.Mode, o.SkipPermissions),
		effort:        firstNonEmpty(NormalizeEffort(o.Effort), "medium"),
		turnWaiters:   map[string]chan turnCompletion{},
		sawTextDelta:  map[string]bool{},
		cmdOutputs:    map[string]*strings.Builder{},
		serverReqs:    map[string]*pendingServerReq{},
		subagents:     map[string]subagentTaskInfo{},
	}
	a.o.HasOpenRouter = hasOR
	a.o.HasNativeAuth = hasNative

	a.Conn = acp.NewConn(cr, cw, o.Handler)
	a.Conn.Trace = o.Trace
	a.agent = acp.NewConn(ar, aw, acpAdapterHandler{a: a})
	return a
}

// ACP returns the client-facing ACP connection.
func (a *Agent) ACP() *acp.Conn { return a.Conn }

// Pid returns the running codex app-server PID (0 if not started).
func (a *Agent) Pid() int {
	a.mu.Lock()
	p := a.proc
	a.mu.Unlock()
	if p == nil {
		return 0
	}
	return p.Pid()
}

// Alive reports whether the adapter is open and its underlying process (if started) has not crashed.
func (a *Agent) Alive() bool {
	select {
	case <-a.exited:
		return false
	default:
		return true
	}
}

// Exited closes when the adapter closes or its active codex app-server exits unexpectedly.
func (a *Agent) Exited() <-chan struct{} { return a.exited }

// ExitErr returns any unexpected exit error from codex app-server.
func (a *Agent) ExitErr() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.exitErr
}

// RSSKiB returns RSS memory usage of the child codex app-server in KiB.
func (a *Agent) RSSKiB() int64 {
	a.mu.Lock()
	p := a.proc
	a.mu.Unlock()
	if p == nil {
		return 0
	}
	return p.RSSKiB()
}

// Close shuts down the adapter and terminates the child codex app-server process group.
func (a *Agent) Close(grace time.Duration) {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return
	}
	a.closed = true
	p := a.proc
	a.proc = nil
	rejs := make([]*pendingServerReq, 0, len(a.serverReqs))
	for k, sr := range a.serverReqs {
		rejs = append(rejs, sr)
		delete(a.serverReqs, k)
	}
	a.mu.Unlock()

	for _, sr := range rejs {
		if sr.cancel != nil {
			sr.cancel()
		}
	}
	if p != nil {
		p.Close(grace)
	}
	_ = a.cw.Close()
	_ = a.aw.Close()
	select {
	case <-a.exited:
	default:
		close(a.exited)
	}
}

func (a *Agent) ensureProc(ctx context.Context, useOR bool, ensureSlug string) (*AppServerProc, error) {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil, errors.New("codex adapter is closed")
	}
	if a.proc != nil && a.proc.Alive() {
		p := a.proc
		a.mu.Unlock()
		return p, nil
	}
	procCwd := a.cwd
	a.mu.Unlock()

	var catalogPath string
	var bridgeURL, bridgeTok string
	if useOR {
		if a.o.Bridge == nil || !a.o.Bridge.HasAPIKey() {
			return nil, errors.New("OpenRouter API key is not configured (pass -openrouter-api-key, set OPENROUTER_API_KEY, or configure Codex in Settings)")
		}
		bridgeURL = a.o.Bridge.BaseURL()
		bridgeTok = a.o.Bridge.BridgeToken()
		if a.o.Catalog != nil {
			if len(a.o.CustomModels) > 0 {
				a.o.Catalog.SetCustomModels(a.o.CustomModels)
			}
			_, path, err := a.o.Catalog.EnsureModel(ensureSlug)
			if err == nil {
				catalogPath = path
			} else {
				catalogPath = a.o.Catalog.CatalogPath()
			}
		}
	}

	p, err := StartAppServer(ctx, ProcConfig{
		Bin:           a.o.Bin,
		Cwd:           procCwd,
		CodexHome:     a.o.CodexHome,
		UserHome:      a.o.UserHome,
		Env:           a.o.Env,
		UseOpenRouter: useOR,
		BridgeBaseURL: bridgeURL,
		BridgeToken:   bridgeTok,
		CatalogPath:   catalogPath,
		ExtraArgs:     a.o.ExtraArgs,
		Handler:       codexServerHandler{a: a},
	})
	if err != nil {
		return nil, err
	}

	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		p.Close(time.Second)
		return nil, errors.New("codex adapter closed during startup")
	}
	a.proc = p
	a.mu.Unlock()

	go a.watchAppServer(p)
	return p, nil
}

func (a *Agent) watchAppServer(p *AppServerProc) {
	<-p.Done()
	a.mu.Lock()
	if a.proc != p || a.closed {
		a.mu.Unlock()
		return
	}
	a.proc = nil
	a.exitErr = p.ExitErr()
	for id, ch := range a.turnWaiters {
		select {
		case ch <- turnCompletion{turnID: id, status: "failed", errMsg: fmt.Sprint(a.exitErr)}:
		default:
		}
		delete(a.turnWaiters, id)
	}
	if a.anyTurnWaiter != nil {
		select {
		case a.anyTurnWaiter <- turnCompletion{status: "failed", errMsg: fmt.Sprint(a.exitErr)}:
		default:
		}
		a.anyTurnWaiter = nil
	}
	a.activeTurnID = ""
	a.mu.Unlock()

	_ = a.cw.Close()
	_ = a.aw.Close()
	select {
	case <-a.exited:
	default:
		close(a.exited)
	}
}

// ---- ACP Server Side (core.Session -> codex.Agent) ----

type acpAdapterHandler struct{ a *Agent }

func (h acpAdapterHandler) HandleNotification(method string, params json.RawMessage) {
	if method == "session/cancel" {
		h.a.cancelActiveTurn()
	}
}

func (h acpAdapterHandler) HandleRequest(c *acp.Conn, id json.RawMessage, method string, params json.RawMessage) {
	go func() {
		res, err := h.a.handleACPRequest(method, params)
		if err != nil {
			_ = c.ReplyError(id, -32000, err.Error())
			return
		}
		_ = c.Reply(id, res)
	}()
}

func (a *Agent) handleACPRequest(method string, params json.RawMessage) (any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	switch method {
	case "initialize":
		return map[string]any{
			"protocolVersion":   1,
			"agentCapabilities": map[string]any{"loadSession": true},
		}, nil

	case "session/new", "session/resume", "session/load":
		var p struct {
			SessionID    string `json:"sessionId"`
			Cwd          string `json:"cwd"`
			ExcludeTurns *bool  `json:"excludeTurns"`
		}
		_ = json.Unmarshal(params, &p)
		return a.handleSessionOpen(ctx, strings.TrimPrefix(method, "session/"), p.SessionID, p.Cwd, p.ExcludeTurns)

	case "session/prompt":
		var p struct {
			SessionID string             `json:"sessionId"`
			Prompt    []acp.ContentBlock `json:"prompt"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		return a.handlePrompt(ctx, p.Prompt)

	case "session/cancel":
		a.cancelActiveTurn()
		return map[string]any{"ok": true}, nil

	case "session/set_model":
		var p struct {
			ModelID string `json:"modelId"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		return a.handleSetModel(ctx, p.ModelID)

	case "session/set_mode":
		var p struct {
			ModeID string `json:"modeId"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		return a.handleSetMode(ctx, p.ModeID)

	case "session/set_config_option":
		var p struct {
			ConfigID string `json:"configId"`
			Value    string `json:"value"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		return a.handleSetConfigOption(ctx, p.ConfigID, p.Value)

	case "_agentc/compact":
		return a.handleCompact(ctx)

	case "_agentc/fork":
		var p struct {
			BeforeTurnID string `json:"beforeTurnId"`
			Cwd          string `json:"cwd"`
		}
		_ = json.Unmarshal(params, &p)
		return a.handleLiveFork(ctx, p.BeforeTurnID, p.Cwd)

	case "_agentc/revert":
		var p struct {
			BeforeTurnID string `json:"beforeTurnId"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		return a.handleLiveRevert(ctx, p.BeforeTurnID)

	case "_agentc/setTitle":
		var p struct {
			Title string `json:"title"`
		}
		_ = json.Unmarshal(params, &p)
		return a.handleLiveSetTitle(ctx, p.Title)

	default:
		return nil, fmt.Errorf("unsupported method %s", method)
	}
}

func (a *Agent) handleSessionOpen(ctx context.Context, kind, sessionID, cwd string, excludeTurnsParam *bool) (*acp.SessionSetup, error) {
	a.mu.Lock()
	if cwd != "" {
		a.cwd = cwd
	}
	useOR := a.useOpenRouter
	wireModel := a.wireModel
	dispModel := a.displayModel
	mode := a.mode
	curCwd := a.cwd
	a.mu.Unlock()

	p, err := a.ensureProc(ctx, useOR, wireModel)
	if err != nil {
		return nil, err
	}

	approval, sandboxMode, _, _ := PoliciesForMode(mode, a.o.ApprovalPolicy, a.o.SandboxMode, a.o.SkipPermissions, curCwd, a.o.AddDirs)
	roots := buildWritableRoots(curCwd, a.o.AddDirs)

	var thread Thread
	var respModel string

	startFresh := func() error {
		req := map[string]any{
			"cwd":                   curCwd,
			"approvalPolicy":        approval,
			"sandbox":               sandboxMode,
			"runtimeWorkspaceRoots": roots,
			"ephemeral":             a.o.Ephemeral,
		}
		if wireModel != "" {
			req["model"] = wireModel
		}
		if useOR {
			req["modelProvider"] = "openrouter"
		}
		var r struct {
			Thread Thread `json:"thread"`
			Model  string `json:"model"`
		}
		if err := p.Conn().Call(ctx, "thread/start", req, &r); err != nil {
			return err
		}
		thread = r.Thread
		respModel = r.Model
		return nil
	}

	if kind == "new" || strings.TrimSpace(sessionID) == "" {
		if err := startFresh(); err != nil {
			return nil, err
		}
	} else {
		excludeTurns := kind == "resume" || a.o.ExcludeTurns
		if excludeTurnsParam != nil {
			excludeTurns = *excludeTurnsParam
		}
		req := map[string]any{
			"threadId":              sessionID,
			"cwd":                   curCwd,
			"approvalPolicy":        approval,
			"sandbox":               sandboxMode,
			"runtimeWorkspaceRoots": roots,
			"excludeTurns":          excludeTurns,
		}
		if wireModel != "" {
			req["model"] = wireModel
		}
		if useOR {
			req["modelProvider"] = "openrouter"
		}
		var r struct {
			Thread Thread `json:"thread"`
			Model  string `json:"model"`
		}
		if err := p.Conn().Call(ctx, "thread/resume", req, &r); err != nil {
			// If thread was never persisted yet, start fresh.
			if err2 := startFresh(); err2 != nil {
				return nil, err
			}
		} else {
			thread = r.Thread
			respModel = r.Model
		}
	}

	if dispModel == "" && respModel != "" {
		dispModel = respModel
	}
	if wireModel == "" && respModel != "" {
		wireModel = respModel
	}

	a.mu.Lock()
	a.sid = thread.ID
	if dispModel != "" {
		a.displayModel = dispModel
	}
	if wireModel != "" {
		a.wireModel = wireModel
	}
	curDisp := a.displayModel
	curMode := a.mode
	curEffort := a.effort
	a.mu.Unlock()

	models := a.buildModelsState(ctx, p, curDisp)
	cfgOpts := a.buildConfigOptions(curMode, curEffort)

	// Replay historical turns ONLY during session/load when turns were included.
	if kind == "load" && len(thread.Turns) > 0 {
		ReplayThreadUpdates(&thread, curCwd, a.emitSessionUpdate)
	}

	// Advertise slash commands.
	a.emitSessionUpdate(map[string]any{
		"sessionUpdate": "available_commands_update",
		"availableCommands": []acp.Command{
			{Name: "compact", Description: "Summarize conversation context to free up tokens"},
			{Name: "plan", Description: "Switch session to Plan collaboration mode"},
		},
	})

	return &acp.SessionSetup{
		SessionID: thread.ID,
		Models:    models,
		Modes: &acp.Modes{
			AvailableModes: AvailableModes(),
			CurrentModeID:  curMode,
		},
		ConfigOptions: cfgOpts,
	}, nil
}

func (a *Agent) buildModelsState(ctx context.Context, p *AppServerProc, currentModelID string) *acp.Models {
	var avail []acp.Model
	seen := map[string]bool{}
	add := func(id, name, desc string) {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		avail = append(avail, acp.Model{
			ModelID:     id,
			Name:        firstNonEmpty(name, id),
			Description: desc,
		})
	}

	if currentModelID != "" {
		add(currentModelID, currentModelID, "")
	}
	for _, cm := range a.o.CustomModels {
		add(cm.Slug, firstNonEmpty(cm.Name, cm.Slug), "Custom model")
	}
	if a.o.Catalog != nil {
		var apiKey string
		if a.o.Bridge != nil {
			apiKey, _, _ = a.o.Bridge.snapshotConfig()
		}
		if list, err := a.o.Catalog.FetchModels(ctx, apiKey, false, a.o.CustomModels, nil, a.o.HasNativeAuth); err == nil {
			for _, m := range list {
				add(m.ModelID, m.Name, m.Description)
			}
		}
	}
	if len(avail) == 0 && p != nil {
		var ml struct {
			Data []struct {
				ID          string `json:"id"`
				Model       string `json:"model"`
				DisplayName string `json:"displayName"`
				Description string `json:"description"`
			} `json:"data"`
		}
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		if p.Conn().Call(cctx, "model/list", map[string]any{"limit": 200}, &ml) == nil {
			for _, m := range ml.Data {
				add(firstNonEmpty(m.Model, m.ID), m.DisplayName, m.Description)
			}
		}
		cancel()
	}
	if len(avail) == 0 {
		for _, m := range CuratedOpenRouterFallback() {
			add(m.ModelID, m.Name, m.Description)
		}
	}
	if currentModelID == "" && len(avail) > 0 {
		currentModelID = avail[0].ModelID
	}
	return &acp.Models{
		AvailableModels: avail,
		CurrentModelID:  currentModelID,
	}
}

func (a *Agent) buildConfigOptions(mode, effort string) []acp.ConfigOption {
	modeOpts := make([]acp.ConfigOptionValue, 0, len(AvailableModes()))
	for _, m := range AvailableModes() {
		modeOpts = append(modeOpts, acp.ConfigOptionValue{Value: m.ID, Name: m.Name, Description: m.Description})
	}
	return []acp.ConfigOption{
		{
			ID:           "thought_level",
			Name:         "Reasoning Effort",
			Category:     "thought_level",
			Type:         "select",
			CurrentValue: firstNonEmpty(effort, "medium"),
			Options: []acp.ConfigOptionValue{
				{Value: "low", Name: "Low", Description: "Fast responses with lighter reasoning"},
				{Value: "medium", Name: "Medium", Description: "Balanced speed and reasoning depth"},
				{Value: "high", Name: "High", Description: "Deeper reasoning for complex problems"},
				{Value: "xhigh", Name: "Extra High", Description: "Maximum reasoning depth"},
			},
		},
		{
			ID:           "mode",
			Name:         "Mode",
			Category:     "mode",
			Type:         "select",
			CurrentValue: mode,
			Options:      modeOpts,
		},
	}
}

func (a *Agent) handleSetModel(ctx context.Context, rawWant string) (any, error) {
	want := strings.TrimSpace(rawWant)
	if want == "" {
		return map[string]any{"ok": true}, nil
	}

	a.mu.Lock()
	oldUseOR := a.useOpenRouter
	hasOR := a.o.HasOpenRouter || (a.o.Bridge != nil && a.o.Bridge.HasAPIKey())
	hasNative := a.o.HasNativeAuth
	newUseOR, wireModel, dispModel := ResolveProviderAndModel(want, a.o.DefaultModel, a.o.Provider, hasOR, hasNative)
	a.useOpenRouter = newUseOR
	a.wireModel = wireModel
	a.displayModel = dispModel
	proc := a.proc
	sid := a.sid
	idle := a.activeTurnID == ""
	cwd := a.cwd
	mode := a.mode
	a.mu.Unlock()

	wasAdded := false
	if newUseOR && a.o.Catalog != nil {
		added, _, _ := a.o.Catalog.EnsureModel(want)
		wasAdded = added
		if wireModel != "" && wireModel != want {
			addedWire, _, _ := a.o.Catalog.EnsureModel(wireModel)
			wasAdded = wasAdded || addedWire
		}
	}

	// Codex loads model_catalog_json and model_provider at process startup.
	// If idle and switching to a newly synthesized slug or changing provider, transparently
	// restart codex app-server and resume the thread with excludeTurns: true.
	if proc != nil && idle && (oldUseOR != newUseOR || wasAdded) {
		doRestart := false
		a.mu.Lock()
		if a.proc == proc && a.activeTurnID == "" && !a.closed {
			a.proc = nil
			doRestart = true
		}
		a.mu.Unlock()
		if doRestart {
			proc.Close(2 * time.Second)

			if sid != "" {
				if newProc, err := a.ensureProc(ctx, newUseOR, wireModel); err == nil {
					approval, sandboxMode, _, _ := PoliciesForMode(mode, a.o.ApprovalPolicy, a.o.SandboxMode, a.o.SkipPermissions, cwd, a.o.AddDirs)
					roots := buildWritableRoots(cwd, a.o.AddDirs)
					resumeReq := map[string]any{
						"threadId":              sid,
						"cwd":                   cwd,
						"model":                 wireModel,
						"approvalPolicy":        approval,
						"sandbox":               sandboxMode,
						"runtimeWorkspaceRoots": roots,
						"excludeTurns":          true,
					}
					if newUseOR {
						resumeReq["modelProvider"] = "openrouter"
					}
					if err := newProc.Conn().Call(ctx, "thread/resume", resumeReq, nil); err != nil {
						startReq := map[string]any{
							"cwd":                   cwd,
							"approvalPolicy":        approval,
							"sandbox":               sandboxMode,
							"runtimeWorkspaceRoots": roots,
							"ephemeral":             a.o.Ephemeral,
						}
						if wireModel != "" {
							startReq["model"] = wireModel
						}
						if newUseOR {
							startReq["modelProvider"] = "openrouter"
						}
						var sr struct {
							Thread Thread `json:"thread"`
						}
						if err2 := newProc.Conn().Call(ctx, "thread/start", startReq, &sr); err2 == nil && sr.Thread.ID != "" {
							sid = sr.Thread.ID
						} else {
							sid = ""
						}
						a.mu.Lock()
						a.sid = sid
						a.mu.Unlock()
					}
				}
			}
			return map[string]any{"ok": true, "sessionId": sid}, nil
		}
	}

	if proc != nil && proc.Alive() && sid != "" {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_ = proc.Conn().Call(cctx, "thread/settings/update", map[string]any{
			"threadId": sid,
			"model":    wireModel,
		}, nil)
		cancel()
	}
	return map[string]any{"ok": true, "sessionId": sid}, nil
}

func (a *Agent) handleSetMode(ctx context.Context, rawMode string) (any, error) {
	norm := NormalizeModeID(rawMode, a.o.SkipPermissions)
	a.mu.Lock()
	a.mode = norm
	proc := a.proc
	sid := a.sid
	wireModel := a.wireModel
	effort := a.effort
	cwd := a.cwd
	cfgOpts := a.buildConfigOptions(a.mode, a.effort)
	a.mu.Unlock()

	if proc != nil && proc.Alive() && sid != "" && wireModel != "" {
		approval, _, sandboxPol, collab := PoliciesForMode(norm, a.o.ApprovalPolicy, a.o.SandboxMode, a.o.SkipPermissions, cwd, a.o.AddDirs)
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_ = proc.Conn().Call(cctx, "thread/settings/update", map[string]any{
			"threadId":          sid,
			"approvalPolicy":    approval,
			"sandboxPolicy":     sandboxPol,
			"collaborationMode": buildCollaborationMode(collab, wireModel, effort),
		}, nil)
		cancel()
	}

	a.emitSessionUpdate(map[string]any{
		"sessionUpdate": "current_mode_update",
		"currentModeId": norm,
	})
	a.emitSessionUpdate(map[string]any{
		"sessionUpdate": "config_option_update",
		"configOptions": cfgOpts,
	})
	return map[string]any{"ok": true}, nil
}

func (a *Agent) handleSetConfigOption(ctx context.Context, configID, value string) (any, error) {
	switch configID {
	case "thought_level", "thought", "effort":
		eff := NormalizeEffort(value)
		a.mu.Lock()
		a.effort = eff
		proc := a.proc
		sid := a.sid
		cfgOpts := a.buildConfigOptions(a.mode, a.effort)
		a.mu.Unlock()

		if proc != nil && proc.Alive() && sid != "" && eff != "" {
			cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			_ = proc.Conn().Call(cctx, "thread/settings/update", map[string]any{
				"threadId": sid,
				"effort":   eff,
			}, nil)
			cancel()
		}
		return map[string]any{"configOptions": cfgOpts}, nil

	case "mode":
		_, err := a.handleSetMode(ctx, value)
		a.mu.Lock()
		cfgOpts := a.buildConfigOptions(a.mode, a.effort)
		a.mu.Unlock()
		return map[string]any{"configOptions": cfgOpts}, err

	case "model":
		_, err := a.handleSetModel(ctx, value)
		a.mu.Lock()
		cfgOpts := a.buildConfigOptions(a.mode, a.effort)
		a.mu.Unlock()
		return map[string]any{"configOptions": cfgOpts}, err

	default:
		a.mu.Lock()
		cfgOpts := a.buildConfigOptions(a.mode, a.effort)
		a.mu.Unlock()
		return map[string]any{"configOptions": cfgOpts}, nil
	}
}

func buildCollaborationMode(collabMode, model, effort string) map[string]any {
	settings := map[string]any{
		"model":                  model,
		"developer_instructions": nil,
	}
	if effort != "" {
		settings["reasoning_effort"] = effort
	} else {
		settings["reasoning_effort"] = nil
	}
	return map[string]any{
		"mode":     collabMode,
		"settings": settings,
	}
}

func (a *Agent) handlePrompt(ctx context.Context, blocks []acp.ContentBlock) (*acp.PromptResult, error) {
	if isSlashCompact(blocks) {
		return a.handleCompact(ctx)
	}
	if isSlashPlan(blocks) {
		_, _ = a.handleSetMode(ctx, "plan")
		rest := stripLeadingSlash(blocks, "/plan")
		if len(rest) == 0 {
			a.emitSessionUpdate(map[string]any{
				"sessionUpdate": "agent_message_chunk",
				"content":       map[string]any{"type": "text", "text": "Switched to Plan mode."},
			})
			return &acp.PromptResult{StopReason: "end_turn"}, nil
		}
		blocks = rest
	}

	a.mu.Lock()
	useOR := a.useOpenRouter
	wireModel := a.wireModel
	promptCwd := a.cwd
	a.mu.Unlock()

	inputs := convertPromptBlocks(blocks, promptCwd)
	if len(inputs) == 0 {
		return nil, errors.New("empty prompt")
	}

	p, err := a.ensureProc(ctx, useOR, wireModel)
	if err != nil {
		return nil, err
	}

	// Check if a turn is already active -> live mid-turn steering (`turn/steer`).
	a.mu.Lock()
	activeID := a.activeTurnID
	sid := a.sid
	a.mu.Unlock()

	if activeID != "" && sid != "" {
		steerCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := p.Conn().Call(steerCtx, "turn/steer", map[string]any{
			"threadId":       sid,
			"expectedTurnId": activeID,
			"input":          inputs,
		}, nil)
		cancel()
		if err == nil {
			return &acp.PromptResult{StopReason: "steered"}, nil
		}
		// Active turn finished just before turn/steer arrived; fall through to turn/start.
	}

	a.mu.Lock()
	sid = a.sid
	wireModel = a.wireModel
	effort := a.effort
	mode := a.mode
	cwd := a.cwd
	startTotals := a.cumulativeUsage
	a.lastTurnDiff = ""
	waitCh := make(chan turnCompletion, 1)
	a.anyTurnWaiter = waitCh
	a.mu.Unlock()

	if sid == "" {
		if _, err := a.handleSessionOpen(ctx, "new", "", cwd, nil); err != nil {
			return nil, err
		}
		a.mu.Lock()
		sid = a.sid
		a.mu.Unlock()
	}

	approval, _, sandboxPol, collab := PoliciesForMode(mode, a.o.ApprovalPolicy, a.o.SandboxMode, a.o.SkipPermissions, cwd, a.o.AddDirs)
	turnParams := map[string]any{
		"threadId":       sid,
		"input":          inputs,
		"approvalPolicy": approval,
		"sandboxPolicy":  sandboxPol,
	}
	if wireModel != "" {
		turnParams["model"] = wireModel
		turnParams["collaborationMode"] = buildCollaborationMode(collab, wireModel, effort)
	}
	if effort != "" {
		turnParams["effort"] = effort
	}

	var startRes struct {
		Turn struct {
			ID     string     `json:"id"`
			Status string     `json:"status"`
			Error  *TurnError `json:"error"`
		} `json:"turn"`
	}
	if err := p.Conn().Call(context.Background(), "turn/start", turnParams, &startRes); err != nil {
		a.mu.Lock()
		if a.anyTurnWaiter == waitCh {
			a.anyTurnWaiter = nil
		}
		a.mu.Unlock()
		return nil, err
	}

	turnID := startRes.Turn.ID
	a.mu.Lock()
	if turnID != "" {
		// If turn/completed has not already fired on anyTurnWaiter, register keyed waiter.
		select {
		case comp := <-waitCh:
			waitCh <- comp
		default:
			a.activeTurnID = turnID
			a.turnWaiters[turnID] = waitCh
			if a.anyTurnWaiter == waitCh {
				a.anyTurnWaiter = nil
			}
		}
	}
	a.mu.Unlock()

	var comp turnCompletion
	select {
	case comp = <-waitCh:
	case <-p.Done():
		err := p.ExitErr()
		if err == nil {
			err = errors.New("codex app-server exited during turn")
		}
		return nil, err
	}

	a.mu.Lock()
	if turnID != "" {
		delete(a.turnWaiters, turnID)
		if a.activeTurnID == turnID {
			a.activeTurnID = ""
		}
	}
	endTotals := a.cumulativeUsage
	a.mu.Unlock()

	delta := computeUsageDelta(startTotals, endTotals)
	switch comp.status {
	case "failed":
		msg := strings.TrimSpace(comp.errMsg)
		if msg == "" {
			msg = "Codex turn failed"
		}
		return nil, errors.New(msg)
	case "interrupted":
		return &acp.PromptResult{StopReason: "cancelled", Usage: delta}, nil
	default:
		return &acp.PromptResult{StopReason: "end_turn", Usage: delta}, nil
	}
}

func computeUsageDelta(start, end codexUsageTotals) *acp.Usage {
	u := &acp.Usage{
		InputTokens:       max64(0, end.InputTokens-start.InputTokens),
		OutputTokens:      max64(0, end.OutputTokens-start.OutputTokens),
		CachedReadTokens:  max64(0, end.CachedReadTokens-start.CachedReadTokens),
		CachedWriteTokens: max64(0, end.CachedWriteTokens-start.CachedWriteTokens),
		ThoughtTokens:     max64(0, end.ThoughtTokens-start.ThoughtTokens),
		TotalTokens:       max64(0, end.TotalTokens-start.TotalTokens),
	}
	if u.TotalTokens == 0 && (u.InputTokens > 0 || u.OutputTokens > 0) {
		u.TotalTokens = u.InputTokens + u.OutputTokens
	}
	return u
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func (a *Agent) cancelActiveTurn() {
	a.mu.Lock()
	p := a.proc
	sid := a.sid
	turnID := a.activeTurnID
	a.mu.Unlock()

	if p == nil || !p.Alive() || sid == "" || turnID == "" {
		return
	}
	cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = p.Conn().Call(cctx, "turn/interrupt", map[string]any{
		"threadId": sid,
		"turnId":   turnID,
	}, nil)
}

func (a *Agent) handleCompact(ctx context.Context) (*acp.PromptResult, error) {
	a.mu.Lock()
	useOR := a.useOpenRouter
	wireModel := a.wireModel
	sid := a.sid
	a.mu.Unlock()

	p, err := a.ensureProc(ctx, useOR, wireModel)
	if err != nil {
		return nil, err
	}
	if sid == "" {
		return nil, errors.New("session thread not started")
	}

	waitCh := make(chan struct{}, 1)
	a.mu.Lock()
	a.compactWaiters = append(a.compactWaiters, waitCh)
	a.mu.Unlock()

	_ = a.agent.Notify("_agentc/status", map[string]any{"status": "compacting"})
	defer func() {
		_ = a.agent.Notify("_agentc/status", map[string]any{"status": ""})
	}()

	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := p.Conn().Call(cctx, "thread/compact/start", map[string]any{"threadId": sid}, nil); err != nil {
		return nil, err
	}
	select {
	case <-waitCh:
	case <-cctx.Done():
	}
	a.emitSessionUpdate(map[string]any{
		"sessionUpdate": "agent_message_chunk",
		"content":       map[string]any{"type": "text", "text": "Conversation context compacted."},
	})
	return &acp.PromptResult{StopReason: "end_turn"}, nil
}

func (a *Agent) handleLiveFork(ctx context.Context, beforeTurnID, cwd string) (*Thread, error) {
	a.mu.Lock()
	p := a.proc
	sid := a.sid
	useOR := a.useOpenRouter
	wireModel := a.wireModel
	if cwd == "" {
		cwd = a.cwd
	}
	a.mu.Unlock()

	if p == nil || !p.Alive() {
		var err error
		p, err = a.ensureProc(ctx, useOR, wireModel)
		if err != nil {
			return nil, err
		}
	}
	req := map[string]any{
		"threadId": sid,
		"cwd":      cwd,
	}
	if beforeTurnID != "" {
		req["beforeTurnId"] = beforeTurnID
	}
	if wireModel != "" {
		req["model"] = wireModel
	}
	if useOR {
		req["modelProvider"] = "openrouter"
	}
	var res struct {
		Thread Thread `json:"thread"`
	}
	if err := p.Conn().Call(ctx, "thread/fork", req, &res); err != nil {
		return nil, err
	}
	return &res.Thread, nil
}

func (a *Agent) handleLiveRevert(ctx context.Context, beforeTurnID string) (*Thread, error) {
	a.mu.Lock()
	p := a.proc
	sid := a.sid
	useOR := a.useOpenRouter
	wireModel := a.wireModel
	a.mu.Unlock()

	if p == nil || !p.Alive() {
		var err error
		p, err = a.ensureProc(ctx, useOR, wireModel)
		if err != nil {
			return nil, err
		}
	}
	var res struct {
		Thread Thread `json:"thread"`
	}
	if err := p.Conn().Call(ctx, "thread/revert", map[string]any{
		"threadId":     sid,
		"beforeTurnId": beforeTurnID,
	}, &res); err != nil {
		return nil, err
	}
	return &res.Thread, nil
}

func (a *Agent) handleLiveSetTitle(ctx context.Context, title string) (any, error) {
	a.mu.Lock()
	p := a.proc
	sid := a.sid
	a.mu.Unlock()
	if p != nil && p.Alive() && sid != "" {
		_ = p.Conn().Call(ctx, "thread/name/set", map[string]any{
			"threadId": sid,
			"name":     title,
		}, nil)
	}
	return map[string]any{"ok": true}, nil
}

func isSlashCompact(blocks []acp.ContentBlock) bool {
	if len(blocks) != 1 || blocks[0].Type != "text" {
		return false
	}
	t := strings.TrimSpace(blocks[0].Text)
	return t == "/compact"
}

func isSlashPlan(blocks []acp.ContentBlock) bool {
	if len(blocks) == 0 || blocks[0].Type != "text" {
		return false
	}
	t := strings.TrimSpace(blocks[0].Text)
	return t == "/plan" || strings.HasPrefix(t, "/plan ")
}

func stripLeadingSlash(blocks []acp.ContentBlock, cmd string) []acp.ContentBlock {
	out := append([]acp.ContentBlock(nil), blocks...)
	t := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(out[0].Text), cmd))
	if t == "" {
		return out[1:]
	}
	out[0].Text = t
	return out
}

func convertPromptBlocks(blocks []acp.ContentBlock, cwd string) []map[string]any {
	var out []map[string]any
	for _, b := range blocks {
		switch b.Type {
		case "text", "":
			if strings.TrimSpace(b.Text) != "" {
				out = append(out, map[string]any{"type": "text", "text": b.Text})
			}
		case "image":
			mt := firstNonEmpty(b.MimeType, "image/png")
			if b.Data != "" {
				out = append(out, map[string]any{
					"type": "image",
					"url":  "data:" + mt + ";base64," + b.Data,
				})
			} else if strings.HasPrefix(b.URI, "file://") {
				out = append(out, map[string]any{
					"type": "localImage",
					"path": strings.TrimPrefix(b.URI, "file://"),
				})
			}
		case "resource_link":
			path := strings.TrimPrefix(b.URI, "file://")
			if path != "" && !filepath.IsAbs(path) && cwd != "" {
				path = filepath.Join(cwd, path)
			}
			if strings.HasPrefix(strings.ToLower(b.MimeType), "image/") {
				out = append(out, map[string]any{"type": "localImage", "path": path})
			} else if path != "" {
				out = append(out, map[string]any{
					"type": "mention",
					"name": firstNonEmpty(b.Name, filepath.Base(path)),
					"path": path,
				})
			}
		case "resource":
			var r struct {
				URI  string `json:"uri"`
				Text string `json:"text"`
			}
			if json.Unmarshal(b.Resource, &r) == nil && r.Text != "" {
				out = append(out, map[string]any{"type": "text", "text": r.Text})
			}
		}
	}
	return out
}

// ---- Codex App-Server Notifications & Interactive Server Requests ----

type codexServerHandler struct{ a *Agent }

func (h codexServerHandler) HandleNotification(method string, params json.RawMessage) {
	h.a.onCodexNotification(method, params)
}

func (h codexServerHandler) HandleRequest(c *acp.Conn, id json.RawMessage, method string, params json.RawMessage) {
	go h.a.onCodexServerRequest(c, id, method, params)
}

func (a *Agent) emitSessionUpdate(update map[string]any) {
	a.mu.Lock()
	sid := a.sid
	a.mu.Unlock()
	raw, err := json.Marshal(update)
	if err != nil {
		return
	}
	_ = a.agent.Notify("session/update", acp.SessionNotification{
		SessionID: sid,
		Update:    raw,
	})
}

func (a *Agent) onCodexNotification(method string, params json.RawMessage) {
	switch method {
	case "turn/started":
		var ev struct {
			ThreadID string `json:"threadId"`
			Turn     struct {
				ID string `json:"id"`
			} `json:"turn"`
		}
		if json.Unmarshal(params, &ev) == nil && ev.Turn.ID != "" {
			a.mu.Lock()
			if a.sid == "" && ev.ThreadID != "" {
				a.sid = ev.ThreadID
			}
			a.activeTurnID = ev.Turn.ID
			if a.anyTurnWaiter != nil {
				a.turnWaiters[ev.Turn.ID] = a.anyTurnWaiter
				a.anyTurnWaiter = nil
			}
			a.mu.Unlock()
		}

	case "turn/completed":
		var ev struct {
			Turn struct {
				ID     string     `json:"id"`
				Status string     `json:"status"`
				Error  *TurnError `json:"error"`
			} `json:"turn"`
		}
		if json.Unmarshal(params, &ev) == nil {
			errMsg := ""
			if ev.Turn.Error != nil {
				errMsg = ev.Turn.Error.Message
				if ev.Turn.Error.AdditionalDetails != nil && *ev.Turn.Error.AdditionalDetails != "" {
					errMsg += ": " + *ev.Turn.Error.AdditionalDetails
				}
			}
			comp := turnCompletion{
				turnID: ev.Turn.ID,
				status: firstNonEmpty(ev.Turn.Status, "completed"),
				errMsg: errMsg,
			}
			a.mu.Lock()
			if a.activeTurnID == ev.Turn.ID {
				a.activeTurnID = ""
			}
			ch := a.turnWaiters[ev.Turn.ID]
			if ch == nil {
				ch = a.anyTurnWaiter
				a.anyTurnWaiter = nil
			}
			a.mu.Unlock()
			if ch != nil {
				select {
				case ch <- comp:
				default:
				}
			}
		}

	case "item/agentMessage/delta":
		var ev struct {
			ItemID string `json:"itemId"`
			Delta  string `json:"delta"`
		}
		if json.Unmarshal(params, &ev) == nil && ev.Delta != "" {
			a.mu.Lock()
			if ev.ItemID != "" {
				a.sawTextDelta[ev.ItemID] = true
			}
			a.mu.Unlock()
			a.emitSessionUpdate(map[string]any{
				"sessionUpdate": "agent_message_chunk",
				"content":       map[string]any{"type": "text", "text": ev.Delta},
			})
		}

	case "item/reasoning/summaryTextDelta", "item/reasoning/textDelta":
		var ev struct {
			ItemID string `json:"itemId"`
			Delta  string `json:"delta"`
		}
		if json.Unmarshal(params, &ev) == nil && ev.Delta != "" {
			a.mu.Lock()
			if ev.ItemID != "" {
				a.sawTextDelta["r:"+ev.ItemID] = true
			}
			a.mu.Unlock()
			a.emitSessionUpdate(map[string]any{
				"sessionUpdate": "agent_thought_chunk",
				"content":       map[string]any{"type": "text", "text": ev.Delta},
			})
		}

	case "item/reasoning/summaryPartAdded":
		var ev struct {
			SummaryIndex int64 `json:"summaryIndex"`
		}
		if json.Unmarshal(params, &ev) == nil && ev.SummaryIndex > 0 {
			a.emitSessionUpdate(map[string]any{
				"sessionUpdate": "agent_thought_chunk",
				"content":       map[string]any{"type": "text", "text": "\n"},
			})
		}

	case "item/plan/delta":
		var ev struct {
			ItemID string `json:"itemId"`
			Delta  string `json:"delta"`
		}
		if json.Unmarshal(params, &ev) == nil && ev.Delta != "" {
			a.mu.Lock()
			mode := a.mode
			if ev.ItemID != "" {
				a.sawTextDelta["p:"+ev.ItemID] = true
			}
			a.mu.Unlock()
			kind := "agent_thought_chunk"
			if mode == "plan" {
				kind = "agent_message_chunk"
			}
			a.emitSessionUpdate(map[string]any{
				"sessionUpdate": kind,
				"content":       map[string]any{"type": "text", "text": ev.Delta},
			})
		}

	case "turn/plan/updated":
		var ev struct {
			Plan []struct {
				Step   string `json:"step"`
				Status string `json:"status"` // pending | inProgress | completed
			} `json:"plan"`
		}
		if json.Unmarshal(params, &ev) == nil {
			entries := make([]map[string]any, 0, len(ev.Plan))
			for _, st := range ev.Plan {
				status := "pending"
				switch st.Status {
				case "inProgress", "in_progress":
					status = "in_progress"
				case "completed":
					status = "completed"
				}
				entries = append(entries, map[string]any{
					"content":  st.Step,
					"status":   status,
					"priority": "medium",
				})
			}
			a.emitSessionUpdate(map[string]any{
				"sessionUpdate": "plan",
				"entries":       entries,
			})
		}

	case "item/commandExecution/outputDelta":
		var ev struct {
			ItemID string `json:"itemId"`
			Delta  string `json:"delta"`
		}
		if json.Unmarshal(params, &ev) == nil && ev.ItemID != "" && ev.Delta != "" {
			a.mu.Lock()
			sb := a.cmdOutputs[ev.ItemID]
			if sb == nil {
				sb = &strings.Builder{}
				a.cmdOutputs[ev.ItemID] = sb
			}
			if sb.Len() < 256*1024 {
				sb.WriteString(ev.Delta)
			}
			acc := sb.String()
			a.mu.Unlock()

			a.emitSessionUpdate(map[string]any{
				"sessionUpdate": "tool_call_update",
				"toolCallId":    ev.ItemID,
				"status":        "in_progress",
				"content": []map[string]any{
					{
						"type":    "content",
						"content": map[string]any{"type": "text", "text": acc},
					},
				},
			})
		}

	case "item/fileChange/patchUpdated":
		var ev struct {
			ItemID  string             `json:"itemId"`
			Changes []FileUpdateChange `json:"changes"`
		}
		if json.Unmarshal(params, &ev) == nil && ev.ItemID != "" && len(ev.Changes) > 0 {
			a.mu.Lock()
			cwd := a.cwd
			a.mu.Unlock()
			toolName, title, locs, content, rawIn := BuildFileChangePayloads(cwd, ev.Changes)
			a.emitSessionUpdate(map[string]any{
				"sessionUpdate": "tool_call_update",
				"toolCallId":    ev.ItemID,
				"name":          toolName,
				"kind":          "edit",
				"title":         title,
				"status":        "in_progress",
				"locations":     locs,
				"content":       content,
				"rawInput":      rawIn,
			})
		}

	case "turn/diff/updated":
		var ev struct {
			TurnID string `json:"turnId"`
			Diff   string `json:"diff"`
		}
		if json.Unmarshal(params, &ev) == nil && ev.Diff != "" {
			a.mu.Lock()
			a.lastTurnDiff = ev.Diff
			a.mu.Unlock()
		}

	case "item/started":
		var ev struct {
			Item json.RawMessage `json:"item"`
		}
		if json.Unmarshal(params, &ev) == nil {
			a.onItemEvent(ev.Item, false)
		}

	case "item/completed":
		var ev struct {
			Item json.RawMessage `json:"item"`
		}
		if json.Unmarshal(params, &ev) == nil {
			a.onItemEvent(ev.Item, true)
		}

	case "thread/tokenUsage/updated":
		var ev struct {
			TokenUsage struct {
				Last struct {
					TotalTokens int64 `json:"totalTokens"`
				} `json:"last"`
				Total struct {
					InputTokens           int64 `json:"inputTokens"`
					CachedInputTokens     int64 `json:"cachedInputTokens"`
					CacheWriteInputTokens int64 `json:"cacheWriteInputTokens"`
					OutputTokens          int64 `json:"outputTokens"`
					ReasoningOutputTokens int64 `json:"reasoningOutputTokens"`
					TotalTokens           int64 `json:"totalTokens"`
				} `json:"total"`
				ModelContextWindow int64 `json:"modelContextWindow"`
			} `json:"tokenUsage"`
		}
		if json.Unmarshal(params, &ev) == nil {
			a.mu.Lock()
			a.cumulativeUsage = codexUsageTotals{
				InputTokens:       ev.TokenUsage.Total.InputTokens,
				OutputTokens:      ev.TokenUsage.Total.OutputTokens,
				CachedReadTokens:  ev.TokenUsage.Total.CachedInputTokens,
				CachedWriteTokens: ev.TokenUsage.Total.CacheWriteInputTokens,
				ThoughtTokens:     ev.TokenUsage.Total.ReasoningOutputTokens,
				TotalTokens:       ev.TokenUsage.Total.TotalTokens,
			}
			a.mu.Unlock()
			used := ev.TokenUsage.Last.TotalTokens
			if used <= 0 {
				used = ev.TokenUsage.Total.TotalTokens
			}
			if ev.TokenUsage.ModelContextWindow > 0 || used > 0 {
				a.emitSessionUpdate(map[string]any{
					"sessionUpdate": "usage_update",
					"used":          used,
					"size":          ev.TokenUsage.ModelContextWindow,
				})
			}
		}

	case "thread/compacted":
		a.mu.Lock()
		waiters := a.compactWaiters
		a.compactWaiters = nil
		a.mu.Unlock()
		for _, ch := range waiters {
			select {
			case ch <- struct{}{}:
			default:
			}
		}

	case "thread/name/updated":
		var ev struct {
			ThreadName *string `json:"threadName"`
		}
		if json.Unmarshal(params, &ev) == nil && ev.ThreadName != nil && *ev.ThreadName != "" {
			a.emitSessionUpdate(map[string]any{
				"sessionUpdate": "session_info_update",
				"title":         *ev.ThreadName,
			})
		}

	case "serverRequest/resolved":
		var ev struct {
			RequestID json.RawMessage `json:"requestId"`
		}
		if json.Unmarshal(params, &ev) == nil && len(ev.RequestID) > 0 {
			key := serverReqIDString(ev.RequestID)
			a.mu.Lock()
			sr := a.serverReqs[key]
			delete(a.serverReqs, key)
			a.mu.Unlock()
			if sr != nil {
				if sr.cancel != nil {
					sr.cancel()
				}
				_ = a.agent.Notify("_agentc/permWithdrawn", map[string]any{"key": sr.reqKey})
			}
		}

	case "error":
		var ev struct {
			WillRetry bool       `json:"willRetry"`
			Error     *TurnError `json:"error"`
		}
		if json.Unmarshal(params, &ev) == nil && ev.Error != nil && ev.WillRetry {
			_ = a.agent.Notify("_agentc/notice", map[string]any{
				"level": "info",
				"text":  "Retrying: " + ev.Error.Message,
			})
		}
	}
}

func (a *Agent) onItemEvent(raw json.RawMessage, completed bool) {
	var hdr struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &hdr) != nil || hdr.Type == "" {
		return
	}

	su := "tool_call"
	if completed {
		su = "tool_call_update"
	}

	switch hdr.Type {
	case "agentMessage":
		if completed {
			var am struct {
				ID   string `json:"id"`
				Text string `json:"text"`
			}
			if json.Unmarshal(raw, &am) == nil && am.Text != "" {
				a.mu.Lock()
				saw := a.sawTextDelta[am.ID]
				delete(a.sawTextDelta, am.ID)
				a.mu.Unlock()
				if !saw {
					a.emitSessionUpdate(map[string]any{
						"sessionUpdate": "agent_message_chunk",
						"content":       map[string]any{"type": "text", "text": am.Text},
					})
				}
			}
		}

	case "reasoning":
		if completed {
			var r struct {
				ID      string   `json:"id"`
				Summary []string `json:"summary"`
				Content []string `json:"content"`
			}
			if json.Unmarshal(raw, &r) == nil {
				a.mu.Lock()
				saw := a.sawTextDelta["r:"+r.ID]
				delete(a.sawTextDelta, "r:"+r.ID)
				a.mu.Unlock()
				if !saw {
					txt := strings.Join(r.Summary, "\n")
					if txt == "" {
						txt = strings.Join(r.Content, "\n")
					}
					if txt != "" {
						a.emitSessionUpdate(map[string]any{
							"sessionUpdate": "agent_thought_chunk",
							"content":       map[string]any{"type": "text", "text": txt},
						})
					}
				}
			}
		}

	case "plan":
		if completed {
			var pl struct {
				ID   string `json:"id"`
				Text string `json:"text"`
			}
			if json.Unmarshal(raw, &pl) == nil && pl.Text != "" {
				a.mu.Lock()
				saw := a.sawTextDelta["p:"+pl.ID]
				delete(a.sawTextDelta, "p:"+pl.ID)
				mode := a.mode
				a.mu.Unlock()
				if !saw {
					kind := "agent_thought_chunk"
					if mode == "plan" {
						kind = "agent_message_chunk"
					}
					a.emitSessionUpdate(map[string]any{
						"sessionUpdate": kind,
						"content":       map[string]any{"type": "text", "text": pl.Text},
					})
				}
			}
		}

	case "commandExecution":
		var ce struct {
			ID               string  `json:"id"`
			Command          string  `json:"command"`
			Cwd              string  `json:"cwd"`
			Status           string  `json:"status"`
			AggregatedOutput *string `json:"aggregatedOutput"`
			ExitCode         *int    `json:"exitCode"`
			DurationMs       *int64  `json:"durationMs"`
		}
		if json.Unmarshal(raw, &ce) != nil {
			return
		}
		a.mu.Lock()
		cwd := a.cwd
		var streamed string
		if sb := a.cmdOutputs[ce.ID]; sb != nil {
			streamed = sb.String()
		}
		if completed {
			delete(a.cmdOutputs, ce.ID)
		}
		a.mu.Unlock()

		status := mapCodexToolStatus(ce.Status)
		if completed && ce.ExitCode != nil && *ce.ExitCode != 0 {
			status = "failed"
		}
		outTxt := streamed
		if ce.AggregatedOutput != nil && *ce.AggregatedOutput != "" {
			outTxt = *ce.AggregatedOutput
		}
		payload := map[string]any{
			"sessionUpdate": su,
			"toolCallId":    firstNonEmpty(ce.ID, "cmd"),
			"name":          "Bash",
			"kind":          "execute",
			"title":         "$ " + ce.Command,
			"status":        status,
			"rawInput":      map[string]any{"command": ce.Command, "cwd": firstNonEmpty(ce.Cwd, cwd)},
		}
		if completed || outTxt != "" {
			payload["rawOutput"] = map[string]any{
				"output":     outTxt,
				"exitCode":   ce.ExitCode,
				"durationMs": ce.DurationMs,
			}
			if outTxt != "" {
				payload["content"] = []map[string]any{
					{
						"type":    "content",
						"content": map[string]any{"type": "text", "text": outTxt},
					},
				}
			}
		}
		a.emitSessionUpdate(payload)

	case "fileChange":
		var fc struct {
			ID      string             `json:"id"`
			Status  string             `json:"status"`
			Changes []FileUpdateChange `json:"changes"`
		}
		if json.Unmarshal(raw, &fc) != nil {
			return
		}
		a.mu.Lock()
		cwd := a.cwd
		turnDiff := a.lastTurnDiff
		a.mu.Unlock()

		changes := fc.Changes
		if len(changes) == 0 && turnDiff != "" {
			for _, pd := range ParseUnifiedGitDiff(cwd, turnDiff) {
				var ch FileUpdateChange
				ch.Path = pd.Path
				ch.Kind.Type = pd.Kind
				if pd.Kind == "add" {
					ch.Diff = pd.NewText
				} else if pd.Kind == "delete" {
					ch.Diff = pd.OldText
				} else {
					ch.Diff = turnDiff
				}
				changes = append(changes, ch)
			}
		}

		toolName, title, locs, content, rawIn := BuildFileChangePayloads(cwd, changes)
		a.emitSessionUpdate(map[string]any{
			"sessionUpdate": su,
			"toolCallId":    firstNonEmpty(fc.ID, "edit"),
			"name":          toolName,
			"kind":          "edit",
			"title":         title,
			"status":        mapCodexToolStatus(fc.Status),
			"locations":     locs,
			"content":       content,
			"rawInput":      rawIn,
		})

	case "collabAgentToolCall":
		var ca struct {
			ID                string   `json:"id"`
			Tool              string   `json:"tool"`
			Status            string   `json:"status"`
			ReceiverThreadIDs []string `json:"receiverThreadIds"`
			Prompt            *string  `json:"prompt"`
			Model             *string  `json:"model"`
			AgentsStates      map[string]struct {
				Status  string  `json:"status"`
				Message *string `json:"message"`
			} `json:"agentsStates"`
		}
		if json.Unmarshal(raw, &ca) != nil {
			return
		}
		promptStr := ""
		if ca.Prompt != nil {
			promptStr = *ca.Prompt
		}
		a.emitSessionUpdate(map[string]any{
			"sessionUpdate": su,
			"toolCallId":    firstNonEmpty(ca.ID, "agent"),
			"name":          "Agent",
			"kind":          "other",
			"title":         "Subagent: " + ca.Tool,
			"status":        mapCodexToolStatus(ca.Status),
			"rawInput": map[string]any{
				"tool":              ca.Tool,
				"prompt":            promptStr,
				"receiverThreadIds": ca.ReceiverThreadIDs,
				"model":             ca.Model,
			},
			"rawOutput": ca.AgentsStates,
		})
		a.updateSubagentsLive(ca.ReceiverThreadIDs, ca.Tool, promptStr, ca.AgentsStates, completed)

	case "mcpToolCall":
		var mc struct {
			ID        string `json:"id"`
			Server    string `json:"server"`
			Tool      string `json:"tool"`
			Arguments any    `json:"arguments"`
			Status    string `json:"status"`
			Result    any    `json:"result"`
			Error     any    `json:"error"`
		}
		if json.Unmarshal(raw, &mc) == nil {
			a.emitSessionUpdate(map[string]any{
				"sessionUpdate": su,
				"toolCallId":    firstNonEmpty(mc.ID, "mcp"),
				"name":          mc.Server + ":" + mc.Tool,
				"kind":          "other",
				"title":         fmt.Sprintf("MCP %s/%s", mc.Server, mc.Tool),
				"status":        mapCodexToolStatus(mc.Status),
				"rawInput":      mc.Arguments,
				"rawOutput":     map[string]any{"result": mc.Result, "error": mc.Error},
			})
		}

	case "dynamicToolCall":
		var dt struct {
			ID           string `json:"id"`
			Tool         string `json:"tool"`
			Arguments    any    `json:"arguments"`
			Status       string `json:"status"`
			ContentItems any    `json:"contentItems"`
		}
		if json.Unmarshal(raw, &dt) == nil {
			a.emitSessionUpdate(map[string]any{
				"sessionUpdate": su,
				"toolCallId":    firstNonEmpty(dt.ID, "tool"),
				"name":          dt.Tool,
				"kind":          "other",
				"title":         dt.Tool,
				"status":        mapCodexToolStatus(dt.Status),
				"rawInput":      dt.Arguments,
				"rawOutput":     dt.ContentItems,
			})
		}

	case "webSearch":
		var ws struct {
			ID    string `json:"id"`
			Query string `json:"query"`
		}
		if json.Unmarshal(raw, &ws) == nil {
			st := "in_progress"
			if completed {
				st = "completed"
			}
			a.emitSessionUpdate(map[string]any{
				"sessionUpdate": su,
				"toolCallId":    firstNonEmpty(ws.ID, "search"),
				"name":          "WebSearch",
				"kind":          "search",
				"title":         "Search: " + ws.Query,
				"status":        st,
				"rawInput":      map[string]any{"query": ws.Query},
			})
		}

	case "imageView":
		var iv struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		}
		if json.Unmarshal(raw, &iv) == nil {
			st := "in_progress"
			if completed {
				st = "completed"
			}
			a.emitSessionUpdate(map[string]any{
				"sessionUpdate": su,
				"toolCallId":    firstNonEmpty(iv.ID, "img"),
				"name":          "ReadImage",
				"kind":          "read",
				"title":         "View " + filepath.Base(iv.Path),
				"status":        st,
				"locations":     []map[string]any{{"path": iv.Path}},
				"rawInput":      map[string]any{"path": iv.Path},
			})
		}

	case "contextCompaction":
		statusVal := "compacting"
		if completed {
			statusVal = ""
		}
		_ = a.agent.Notify("_agentc/status", map[string]any{"status": statusVal})
	}
}

func (a *Agent) updateSubagentsLive(ids []string, tool, prompt string, states map[string]struct {
	Status  string  `json:"status"`
	Message *string `json:"message"`
}, completed bool) {
	nowMs := time.Now().UnixMilli()
	a.mu.Lock()
	for _, id := range ids {
		if id == "" {
			continue
		}
		cur, exists := a.subagents[id]
		if !exists {
			cur = subagentTaskInfo{
				ID:           id,
				Type:         "local_agent",
				SubagentType: "codex",
				Description:  clipTitle(firstNonEmpty(prompt, tool)),
				Status:       "running",
				Start:        nowMs,
			}
		}
		if st, ok := states[id]; ok {
			if st.Status != "" {
				cur.Status = st.Status
			}
			if st.Message != nil && *st.Message != "" {
				cur.Progress = clipTitle(*st.Message)
			}
		} else if completed && cur.Status == "running" {
			cur.Status = "completed"
			cur.End = nowMs
		}
		a.subagents[id] = cur
	}
	tasks := make([]subagentTaskInfo, 0, len(a.subagents))
	for _, t := range a.subagents {
		tasks = append(tasks, t)
	}
	a.mu.Unlock()
	_ = a.agent.Notify("_agentc/live", map[string]any{"tasks": tasks})
}

func serverReqIDString(id json.RawMessage) string {
	var s string
	if json.Unmarshal(id, &s) == nil {
		return "s:" + s
	}
	return strings.TrimSpace(string(id))
}

func newReqKey() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "cxperm-" + hex.EncodeToString(b)
}

func (a *Agent) onCodexServerRequest(c *acp.Conn, id json.RawMessage, method string, params json.RawMessage) {
	reqKey := newReqKey()
	ctx, cancel := context.WithCancel(context.Background())
	idStr := serverReqIDString(id)

	a.mu.Lock()
	sid := a.sid
	cwd := a.cwd
	a.serverReqs[idStr] = &pendingServerReq{reqKey: reqKey, cancel: cancel}
	a.mu.Unlock()

	defer func() {
		cancel()
		a.mu.Lock()
		delete(a.serverReqs, idStr)
		a.mu.Unlock()
	}()

	switch method {
	case "item/commandExecution/requestApproval":
		var req struct {
			ItemID  string  `json:"itemId"`
			Command string  `json:"command"`
			Cwd     string  `json:"cwd"`
			Reason  *string `json:"reason"`
		}
		_ = json.Unmarshal(params, &req)
		reason := ""
		if req.Reason != nil {
			reason = *req.Reason
		}
		toolCall := map[string]any{
			"toolCallId": firstNonEmpty(req.ItemID, reqKey),
			"name":       "Bash",
			"kind":       "execute",
			"title":      "$ " + req.Command,
			"status":     "pending",
			"rawInput":   map[string]any{"command": req.Command, "cwd": firstNonEmpty(req.Cwd, cwd)},
		}
		decision := a.askToolApproval(ctx, sid, reqKey, "Bash", reason, "", toolCall)
		_ = c.Reply(id, map[string]any{"decision": decision})

	case "item/fileChange/requestApproval":
		var req struct {
			ItemID    string  `json:"itemId"`
			Reason    *string `json:"reason"`
			GrantRoot *string `json:"grantRoot"`
		}
		_ = json.Unmarshal(params, &req)
		reason, grantRoot := "", ""
		if req.Reason != nil {
			reason = *req.Reason
		}
		if req.GrantRoot != nil {
			grantRoot = *req.GrantRoot
		}
		title := "Apply file changes"
		if grantRoot != "" {
			title = "Edit files in " + grantRoot
		}
		toolCall := map[string]any{
			"toolCallId": firstNonEmpty(req.ItemID, reqKey),
			"name":       "Edit",
			"kind":       "edit",
			"title":      title,
			"status":     "pending",
			"rawInput":   map[string]any{"path": grantRoot, "reason": reason},
		}
		decision := a.askToolApproval(ctx, sid, reqKey, "Edit", reason, grantRoot, toolCall)
		_ = c.Reply(id, map[string]any{"decision": decision})

	case "item/permissions/requestApproval":
		var req struct {
			ItemID      string         `json:"itemId"`
			Reason      *string        `json:"reason"`
			Permissions map[string]any `json:"permissions"`
		}
		_ = json.Unmarshal(params, &req)
		reason := ""
		if req.Reason != nil {
			reason = *req.Reason
		}
		toolCall := map[string]any{
			"toolCallId": firstNonEmpty(req.ItemID, reqKey),
			"name":       "Permissions",
			"kind":       "other",
			"title":      firstNonEmpty(reason, "Grant additional sandbox permissions"),
			"status":     "pending",
			"rawInput":   req.Permissions,
		}
		decision := a.askToolApproval(ctx, sid, reqKey, "Permissions", reason, "", toolCall)
		switch decision {
		case "accept":
			_ = c.Reply(id, map[string]any{"permissions": req.Permissions, "scope": "turn"})
		case "acceptForSession":
			_ = c.Reply(id, map[string]any{"permissions": req.Permissions, "scope": "session"})
		default:
			_ = c.Reply(id, map[string]any{"permissions": map[string]any{}, "scope": "turn"})
		}

	case "item/tool/requestUserInput":
		a.handleRequestUserInput(ctx, c, id, sid, reqKey, params)

	default:
		_ = c.ReplyError(id, -32601, "unsupported server request: "+method)
	}
}

func (a *Agent) askToolApproval(ctx context.Context, sid, reqKey, toolName, reason, blockedPath string, toolCall map[string]any) string {
	options := []acp.PermissionOption{
		{OptionID: "accept", Name: "Allow once", Kind: "allow_once"},
		{OptionID: "acceptForSession", Name: "Allow for session", Kind: "allow_always"},
		{OptionID: "decline", Name: "Deny", Kind: "reject_once"},
	}
	meta := map[string]any{
		"key":    reqKey,
		"kind":   "tool",
		"tool":   toolName,
		"reason": reason,
	}
	if blockedPath != "" {
		meta["blockedPath"] = blockedPath
	}
	var res struct {
		Outcome struct {
			Outcome  string `json:"outcome"`
			OptionID string `json:"optionId"`
		} `json:"outcome"`
	}
	err := a.agent.Call(ctx, "session/request_permission", map[string]any{
		"sessionId": sid,
		"toolCall":  toolCall,
		"options":   options,
		"_meta":     map[string]any{"agentc": meta},
	}, &res)
	if err != nil || res.Outcome.Outcome == "cancelled" {
		return "cancel"
	}
	switch res.Outcome.OptionID {
	case "accept":
		return "accept"
	case "acceptForSession":
		return "acceptForSession"
	default:
		return "decline"
	}
}

func (a *Agent) handleRequestUserInput(ctx context.Context, c *acp.Conn, id json.RawMessage, sid, reqKey string, params json.RawMessage) {
	var req struct {
		ItemID    string `json:"itemId"`
		Questions []struct {
			ID       string `json:"id"`
			Header   string `json:"header"`
			Question string `json:"question"`
			Options  []struct {
				Label       string `json:"label"`
				Description string `json:"description"`
			} `json:"options"`
		} `json:"questions"`
	}
	if err := json.Unmarshal(params, &req); err != nil || len(req.Questions) == 0 {
		_ = c.Reply(id, map[string]any{"answers": map[string]any{}})
		return
	}

	callID := firstNonEmpty(req.ItemID, reqKey)
	headerUsed := map[string]int{}
	uniqueHeaders := make([]string, len(req.Questions))
	normalized := make([]map[string]any, 0, len(req.Questions))

	for i, q := range req.Questions {
		hdr := strings.TrimSpace(q.Header)
		if hdr != "" {
			headerUsed[hdr]++
			if headerUsed[hdr] > 1 {
				hdr = fmt.Sprintf("%s (%d)", hdr, headerUsed[hdr])
			}
		}
		uniqueHeaders[i] = hdr
		opts := make([]map[string]any, 0, len(q.Options))
		for _, o := range q.Options {
			opts = append(opts, map[string]any{
				"label":       o.Label,
				"description": o.Description,
			})
		}
		normalized = append(normalized, map[string]any{
			"id":          q.ID,
			"header":      hdr,
			"question":    q.Question,
			"multiSelect": false,
			"options":     opts,
		})
	}

	rawInput := map[string]any{"questions": normalized}
	toolCall := map[string]any{
		"sessionUpdate": "tool_call",
		"toolCallId":    callID,
		"name":          "AskUserQuestion",
		"kind":          "other",
		"title":         "Question",
		"status":        "pending",
		"rawInput":      rawInput,
	}
	a.emitSessionUpdate(toolCall)

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
	err := a.agent.Call(ctx, "session/request_permission", map[string]any{
		"sessionId": sid,
		"toolCall": map[string]any{
			"toolCallId": callID,
			"name":       "AskUserQuestion",
			"kind":       "other",
			"title":      "Question",
			"rawInput":   rawInput,
		},
		"options": []acp.PermissionOption{
			{OptionID: "answer", Name: "Submit answers", Kind: "allow_once"},
			{OptionID: "deny", Name: "Dismiss", Kind: "reject_once"},
		},
		"_meta": map[string]any{
			"agentc": map[string]any{
				"key":  reqKey,
				"kind": "question",
				"tool": "AskUserQuestion",
			},
		},
	}, &res)

	if err != nil || res.Outcome.Outcome == "cancelled" || res.Outcome.OptionID == "deny" {
		a.emitSessionUpdate(map[string]any{
			"sessionUpdate": "tool_call_update",
			"toolCallId":    callID,
			"status":        "failed",
		})
		_ = c.Reply(id, map[string]any{"answers": map[string]any{}})
		return
	}

	codexAnswers := map[string]any{}
	uiAnswers := map[string]string{}
	ansMap := res.Outcome.Meta.Answers

	for i, q := range req.Questions {
		var vals []string
		for _, k := range []string{q.ID, q.Question, uniqueHeaders[i], q.Header, fmt.Sprintf("q%d", i)} {
			if k == "" || ansMap == nil {
				continue
			}
			if rawVal, ok := ansMap[k]; ok {
				vals = toStringSlice(rawVal)
				if len(vals) > 0 {
					break
				}
			}
		}
		if len(vals) == 0 && len(req.Questions) == 1 && strings.TrimSpace(res.Outcome.Meta.Message) != "" {
			vals = []string{strings.TrimSpace(res.Outcome.Meta.Message)}
		}
		if vals == nil {
			vals = []string{}
		}
		codexAnswers[q.ID] = map[string]any{"answers": vals}
		if len(vals) > 0 {
			uiAnswers[q.Question] = strings.Join(vals, ", ")
		}
	}

	a.emitSessionUpdate(map[string]any{
		"sessionUpdate": "tool_call_update",
		"toolCallId":    callID,
		"status":        "completed",
		"rawOutput":     map[string]any{"answers": uiAnswers},
	})
	_ = c.Reply(id, map[string]any{"answers": codexAnswers})
}

func toStringSlice(v any) []string {
	switch x := v.(type) {
	case string:
		if strings.TrimSpace(x) != "" {
			return []string{x}
		}
	case []string:
		return x
	case []any:
		var out []string
		for _, item := range x {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
