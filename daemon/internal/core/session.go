package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"agentc/internal/acp"
	"agentc/internal/agyproc"
)

// SessionMeta is the persisted, client-visible state of a session.
type SessionMeta struct {
	ID            string             `json:"id"`
	Agent         string             `json:"agent,omitempty"` // agy (claude, codex planned)
	Cwd           string             `json:"cwd"`
	AddDirs       []string           `json:"addDirs,omitempty"`
	SkipPerms     bool               `json:"skipPermissions"`
	Sandbox       bool               `json:"sandbox,omitempty"`
	AgentName     string             `json:"agentName,omitempty"`
	NoTelemetry   bool               `json:"disableTelemetry,omitempty"`
	ExtraArgs     []string           `json:"extraArgs,omitempty"`
	Title         string             `json:"title"`
	TitleLocked   bool               `json:"titleLocked,omitempty"`
	CreatedAt     int64              `json:"createdAt"`
	UpdatedAt     int64              `json:"updatedAt"`
	Models        *acp.Models        `json:"models,omitempty"`
	Modes         *acp.Modes         `json:"modes,omitempty"`
	ConfigOptions []acp.ConfigOption `json:"configOptions,omitempty"`
	Commands      []acp.Command      `json:"commands,omitempty"`
	WantModel     string             `json:"wantModel,omitempty"`
	WantMode      string             `json:"wantMode,omitempty"`
	WantThought   string             `json:"wantThought,omitempty"`
	ContextUsed   int64              `json:"contextUsed"`
	ContextSize   int64              `json:"contextSize"`
	Usage         acp.Usage          `json:"usage"`
	Turns         int                `json:"turns"`
	ForkedFrom    string             `json:"forkedFrom,omitempty"`
	Origin        string             `json:"origin,omitempty"`
	Attention     string             `json:"attention,omitempty"`
	LastText      string             `json:"lastText,omitempty"`
	LastError     string             `json:"lastError,omitempty"`
	Queue         []QueuedPrompt     `json:"queue,omitempty"`
	Archived      bool               `json:"archived,omitempty"`
	Pinned        bool               `json:"pinned,omitempty"`
	NeedsReload   bool               `json:"needsReload,omitempty"`
}

// QueuedPrompt is a message waiting for the current turn to end.
type QueuedPrompt struct {
	ItemID string             `json:"itemId"`
	Blocks []acp.ContentBlock `json:"blocks"`
}

// Summary is the list-row view of a session.
type Summary struct {
	ID          string    `json:"id"`
	Agent       string    `json:"agent"`
	Title       string    `json:"title"`
	Cwd         string    `json:"cwd"`
	State       string    `json:"state"`
	Live        bool      `json:"live"`
	Known       bool      `json:"known"`
	Attention   string    `json:"attention,omitempty"`
	Pending     int       `json:"pendingPermissions"`
	Queued      int       `json:"queued"`
	Model       string    `json:"model,omitempty"`
	ModelName   string    `json:"modelName,omitempty"`
	Mode        string    `json:"mode,omitempty"`
	Thought     string    `json:"thought,omitempty"`
	SkipPerms   bool      `json:"skipPermissions"`
	CreatedAt   int64     `json:"createdAt"`
	UpdatedAt   int64     `json:"updatedAt"`
	LastText    string    `json:"lastText,omitempty"`
	ContextUsed int64     `json:"contextUsed"`
	ContextSize int64     `json:"contextSize"`
	Usage       acp.Usage `json:"usage"`
	Turns       int       `json:"turns"`
	Archived    bool      `json:"archived"`
	Pinned      bool      `json:"pinned"`
	ForkedFrom  string    `json:"forkedFrom,omitempty"`
	Origin      string    `json:"origin,omitempty"`
	Error       string    `json:"error,omitempty"`
	Pid         int       `json:"pid,omitempty"`
	RSSKiB      int64     `json:"rssKiB,omitempty"`
}

// Detail is the full view sent when a client opens a session.
type Detail struct {
	Summary
	AddDirs       []string           `json:"addDirs,omitempty"`
	Sandbox       bool               `json:"sandbox,omitempty"`
	AgentName     string             `json:"agentName,omitempty"`
	ExtraArgs     []string           `json:"extraArgs,omitempty"`
	Models        *acp.Models        `json:"models,omitempty"`
	Modes         *acp.Modes         `json:"modes,omitempty"`
	ConfigOptions []acp.ConfigOption `json:"configOptions,omitempty"`
	Commands      []acp.Command      `json:"commands,omitempty"`
}

type pendingPerm struct {
	rpcID  json.RawMessage
	conn   *acp.Conn
	itemID string
	callID string
}

// Session is one agy conversation managed by the daemon.
type Session struct {
	m            *Manager
	mu           sync.Mutex
	meta         SessionMeta
	tr           *Transcript
	proc         *agyproc.Proc
	startWait    chan struct{}
	startErr     error
	closing      bool
	removed      bool
	inflight     int
	turnStart    time.Time
	perms        map[string]*pendingPerm
	permSeq      int
	pendingOps   []Op
	flushTimer   *time.Timer
	saveTimer    *time.Timer
	lastActivity time.Time
	sentInFlight []string
}

func newSession(m *Manager, meta SessionMeta) *Session {
	return &Session{m: m, meta: meta, tr: NewTranscript(), perms: map[string]*pendingPerm{}, lastActivity: time.Now()}
}

// ID returns the conversation id.
func (s *Session) ID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.meta.ID
}

func (s *Session) stateLocked() string {
	switch {
	case s.startWait != nil:
		return "starting"
	case s.proc == nil:
		if s.meta.LastError != "" && s.meta.Turns == 0 && len(s.tr.Items) == 0 {
			return "error"
		}
		return "sleeping"
	case len(s.perms) > 0:
		return "waiting"
	case s.inflight > 0:
		return "running"
	default:
		return "idle"
	}
}

func (s *Session) summaryLocked() Summary {
	m := s.meta
	sum := Summary{
		ID: m.ID, Agent: firstNonEmpty(m.Agent, "agy"), Title: m.Title, Cwd: m.Cwd, State: s.stateLocked(), Live: s.proc != nil, Known: true,
		Attention: m.Attention, Pending: len(s.perms), Queued: len(m.Queue), SkipPerms: m.SkipPerms,
		CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt, LastText: m.LastText, ContextUsed: m.ContextUsed,
		ContextSize: m.ContextSize, Usage: m.Usage, Turns: m.Turns, Archived: m.Archived, Pinned: m.Pinned,
		ForkedFrom: m.ForkedFrom, Origin: m.Origin, Error: m.LastError,
	}
	if len(s.perms) > 0 {
		sum.Attention = "permission"
	}
	if m.Models != nil {
		sum.Model = m.Models.CurrentModelID
		for _, md := range m.Models.AvailableModels {
			if md.ModelID == m.Models.CurrentModelID {
				sum.ModelName = md.Name
			}
		}
	}
	if sum.Model == "" && m.WantModel != "" {
		sum.Model = m.WantModel
	}
	if m.Modes != nil {
		sum.Mode = m.Modes.CurrentModeID
	}
	for _, o := range m.ConfigOptions {
		if o.ID == "thought_level" {
			sum.Thought = o.CurrentValue
		}
	}
	if s.proc != nil {
		sum.Pid = s.proc.Pid()
	}
	return sum
}

// Summary returns the list view.
func (s *Session) Summary() Summary {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.summaryLocked()
}

func (s *Session) detailLocked() Detail {
	return Detail{Summary: s.summaryLocked(), AddDirs: s.meta.AddDirs, Sandbox: s.meta.Sandbox, AgentName: s.meta.AgentName, ExtraArgs: s.meta.ExtraArgs,
		Models: s.meta.Models, Modes: s.meta.Modes,
		ConfigOptions: s.meta.ConfigOptions, Commands: s.meta.Commands}
}

// ---- op streaming ----

func (s *Session) queueOpsLocked(ops ...Op) {
	for _, op := range ops {
		if op.Op == "" {
			continue
		}
		s.pendingOps = append(s.pendingOps, op)
	}
	if len(s.pendingOps) > 0 && s.flushTimer == nil {
		s.flushTimer = time.AfterFunc(60*time.Millisecond, s.flush)
	}
	s.scheduleSaveLocked()
}

func (s *Session) flush() {
	s.mu.Lock()
	ops := s.pendingOps
	s.pendingOps = nil
	s.flushTimer = nil
	id := s.meta.ID
	s.mu.Unlock()
	if len(ops) == 0 {
		return
	}
	s.m.hub.ToSubscribers(id, "session.ops", map[string]any{"id": id, "ops": TransportOps(coalesce(ops))})
}

func coalesce(ops []Op) []Op {
	out := make([]Op, 0, len(ops))
	for _, op := range ops {
		if n := len(out); n > 0 && op.Op == "append" && out[n-1].Op == "append" && out[n-1].ID == op.ID {
			out[n-1].Text += op.Text
			out[n-1].Seq = op.Seq
			continue
		}
		out = append(out, op)
	}
	return out
}

// Subscribe attaches a client and sends it a snapshot or a delta since seq.
func (s *Session) Subscribe(c *Client, since int64) {
	s.mu.Lock()
	// Deliver anything already pending to existing subscribers first.
	pending := s.pendingOps
	s.pendingOps = nil
	if s.flushTimer != nil {
		s.flushTimer.Stop()
		s.flushTimer = nil
	}
	id := s.meta.ID
	if len(pending) > 0 {
		s.m.hub.ToSubscribers(id, "session.ops", map[string]any{"id": id, "ops": TransportOps(coalesce(pending))})
	}
	c.Subscribe(id, true)
	detail := s.detailLocked()
	if since > 0 {
		if ops, ok := s.tr.OpsSince(since); ok {
			c.Event("session.delta", map[string]any{"id": id, "session": detail, "seq": s.tr.Seq, "ops": TransportOps(coalesce(ops))})
			s.mu.Unlock()
			return
		}
	}
	c.Event("session.snapshot", map[string]any{"id": id, "session": detail, "seq": s.tr.Seq, "items": s.tr.TransportItems()})
	s.mu.Unlock()
}

// FullItem returns an untruncated item.
func (s *Session) FullItem(id string) *Item {
	s.mu.Lock()
	defer s.mu.Unlock()
	if it := s.tr.Get(id); it != nil {
		return cloneItem(it)
	}
	return nil
}

func (s *Session) scheduleSaveLocked() {
	if s.saveTimer != nil || s.removed {
		return
	}
	s.saveTimer = time.AfterFunc(1500*time.Millisecond, func() {
		s.mu.Lock()
		s.saveTimer = nil
		s.mu.Unlock()
		s.m.saveSession(s)
	})
}

// emit flushes pending transcript ops, then broadcasts the summary, so clients
// always see the transcript before the state change it explains.
func (s *Session) emit() {
	s.flush()
	s.m.emitSummary(s)
}

// ---- ACP handler ----

type procHandler struct{ s *Session }

func (h procHandler) HandleNotification(method string, params json.RawMessage) {
	if method != "session/update" {
		return
	}
	var n acp.SessionNotification
	if err := json.Unmarshal(params, &n); err != nil {
		return
	}
	h.s.onUpdate(n.Update)
}

func (h procHandler) HandleRequest(c *acp.Conn, id json.RawMessage, method string, params json.RawMessage) {
	switch method {
	case "session/request_permission":
		h.s.onPermission(c, id, params)
	default:
		_ = c.ReplyError(id, -32601, "method not supported by agentc: "+method)
	}
}

func textOf(raw json.RawMessage) (acp.ContentBlock, bool) {
	var u struct {
		Content acp.ContentBlock `json:"content"`
	}
	if err := json.Unmarshal(raw, &u); err != nil {
		return acp.ContentBlock{}, false
	}
	return u.Content, true
}

func (s *Session) onUpdate(raw json.RawMessage) {
	var h acp.UpdateHeader
	if err := json.Unmarshal(raw, &h); err != nil {
		return
	}
	emit := false
	s.mu.Lock()
	content := true
	switch h.SessionUpdate {
	case "user_message_chunk":
		if b, ok := textOf(raw); ok {
			s.queueOpsLocked(s.tr.UserChunk(b, s.blockForLocked)...)
		}
	case "agent_message_chunk", "agent_thought_chunk":
		kind := "agent"
		if h.SessionUpdate == "agent_thought_chunk" {
			kind = "thought"
		}
		if b, ok := textOf(raw); ok {
			if b.Type == "text" || b.Type == "" {
				s.queueOpsLocked(s.tr.TextChunk(kind, b.Text)...)
			} else if blk := s.blockForLocked(b); blk != nil {
				s.tr.closeText()
				it := &Item{ID: s.tr.newID("a"), Kind: kind, Ts: now(), Blocks: []Block{*blk}}
				s.queueOpsLocked(s.tr.add(it))
			}
		}
	case "tool_call", "tool_call_update":
		ops, _ := s.tr.ToolCall(raw, h.SessionUpdate == "tool_call")
		s.queueOpsLocked(ops...)
	case "plan":
		var p struct {
			Entries json.RawMessage `json:"entries"`
		}
		if json.Unmarshal(raw, &p) == nil {
			s.queueOpsLocked(s.tr.PlanUpdate(p.Entries)...)
		}
	case "available_commands_update":
		var u struct {
			AvailableCommands []acp.Command `json:"availableCommands"`
		}
		if json.Unmarshal(raw, &u) == nil {
			s.meta.Commands = u.AvailableCommands
			s.configChangedLocked()
		}
		content = false
	case "current_mode_update":
		var u struct {
			CurrentModeID string `json:"currentModeId"`
		}
		if json.Unmarshal(raw, &u) == nil {
			if s.meta.Modes == nil {
				s.meta.Modes = &acp.Modes{}
			}
			s.meta.Modes.CurrentModeID = u.CurrentModeID
			s.configChangedLocked()
			emit = true
		}
		content = false
	case "config_option_update":
		var u struct {
			ConfigOptions []acp.ConfigOption `json:"configOptions"`
		}
		if json.Unmarshal(raw, &u) == nil {
			s.meta.ConfigOptions = u.ConfigOptions
			s.syncFromConfigLocked()
			s.configChangedLocked()
			emit = true
		}
		content = false
	case "usage_update":
		var u struct {
			Used int64 `json:"used"`
			Size int64 `json:"size"`
		}
		if json.Unmarshal(raw, &u) == nil {
			s.meta.ContextUsed, s.meta.ContextSize = u.Used, u.Size
			s.m.hub.ToSubscribers(s.meta.ID, "session.usage", map[string]any{"id": s.meta.ID, "used": u.Used, "size": u.Size})
		}
		content = false
	case "session_info_update":
		var u struct {
			Title *string `json:"title"`
		}
		if json.Unmarshal(raw, &u) == nil && u.Title != nil && *u.Title != "" && !s.meta.TitleLocked {
			s.meta.Title = *u.Title
			emit = true
		}
		content = false
	default:
		content = false
	}
	if content {
		s.meta.UpdatedAt = now()
		s.lastActivity = time.Now()
	}
	s.mu.Unlock()
	if emit {
		s.emit()
	}
}

// syncFromConfigLocked keeps Models.CurrentModelID aligned with config options.
func (s *Session) syncFromConfigLocked() {
	var base, thought string
	for _, o := range s.meta.ConfigOptions {
		switch o.ID {
		case "model":
			base = o.CurrentValue
		case "thought_level":
			thought = o.CurrentValue
		}
	}
	if s.meta.Models == nil || base == "" {
		return
	}
	cand := base
	if thought != "" {
		cand = base + "-" + thought
	}
	for _, md := range s.meta.Models.AvailableModels {
		if md.ModelID == cand {
			s.meta.Models.CurrentModelID = cand
			return
		}
	}
	for _, md := range s.meta.Models.AvailableModels {
		if md.ModelID == base {
			s.meta.Models.CurrentModelID = base
			return
		}
	}
}

func (s *Session) configChangedLocked() {
	id := s.meta.ID
	d := s.detailLocked()
	s.m.hub.ToSubscribers(id, "session.config", map[string]any{"id": id, "session": d})
	s.scheduleSaveLocked()
}

// blockForLocked converts an ACP content block to a transcript block, storing
// inline media as attachments.
func (s *Session) blockForLocked(b acp.ContentBlock) *Block {
	switch b.Type {
	case "image", "audio":
		blk := &Block{Type: b.Type, Mime: b.MimeType, URI: b.URI}
		if b.Data != "" {
			if name, err := s.m.saveAttachment(s.meta.ID, b.MimeType, b.Data); err == nil {
				blk.URI = "att:" + s.meta.ID + "/" + name
				blk.Name = name
			}
		}
		return blk
	case "resource_link":
		name := b.Name
		if name == "" {
			name = filepath.Base(strings.TrimPrefix(b.URI, "file://"))
		}
		return &Block{Type: "link", URI: b.URI, Name: name, Mime: b.MimeType}
	case "resource":
		var r struct {
			URI      string `json:"uri"`
			Text     string `json:"text"`
			MimeType string `json:"mimeType"`
		}
		_ = json.Unmarshal(b.Resource, &r)
		txt := r.Text
		if len(txt) > 4000 {
			txt = txt[:4000] + "…"
		}
		return &Block{Type: "resource", URI: r.URI, Text: txt, Mime: r.MimeType, Name: filepath.Base(strings.TrimPrefix(r.URI, "file://"))}
	}
	return nil
}

// ---- permissions ----

func (s *Session) onPermission(c *acp.Conn, id json.RawMessage, params json.RawMessage) {
	var req acp.PermissionRequest
	if err := json.Unmarshal(params, &req); err != nil {
		_ = c.ReplyError(id, -32602, "bad params")
		return
	}
	var tc struct {
		ToolCallID string `json:"toolCallId"`
		Title      string `json:"title"`
		Kind       string `json:"kind"`
	}
	_ = json.Unmarshal(req.ToolCall, &tc)
	s.mu.Lock()
	if len(req.ToolCall) > 0 {
		ops, _ := s.tr.ToolCall(req.ToolCall, false)
		s.queueOpsLocked(ops...)
	}
	s.permSeq++
	reqID := fmt.Sprintf("r%d", s.permSeq)
	p := &Perm{ReqID: reqID, CallID: tc.ToolCallID, Title: tc.Title, ToolKind: tc.Kind, Options: req.Options}
	it, op := s.tr.AddPerm(p)
	s.queueOpsLocked(op)
	s.perms[reqID] = &pendingPerm{rpcID: id, conn: c, itemID: it.ID, callID: tc.ToolCallID}
	title := s.meta.Title
	sid := s.meta.ID
	s.mu.Unlock()
	s.emit()
	s.m.notify(Notification{SessionID: sid, Kind: "permission", Title: "agy needs your permission", Body: firstNonEmpty(tc.Title, "Tool call") + " — " + title,
		ReqID: reqID, Options: req.Options})
}

// RespondPermission answers a pending permission request.
func (s *Session) RespondPermission(reqID, optionID string) error {
	s.mu.Lock()
	p := s.perms[reqID]
	if p == nil {
		s.mu.Unlock()
		return errors.New("permission request is no longer pending")
	}
	delete(s.perms, reqID)
	state, chosen := "resolved", optionID
	var result any
	if optionID == "" || optionID == "__cancel" {
		state, chosen = "cancelled", ""
		result = map[string]any{"outcome": map[string]any{"outcome": "cancelled"}}
	} else {
		result = map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": optionID}}
	}
	if op, ok := s.tr.ResolvePerm(reqID, state, chosen); ok {
		s.queueOpsLocked(op)
	}
	s.mu.Unlock()
	err := p.conn.Reply(p.rpcID, result)
	s.emit()
	return err
}

func (s *Session) cancelPermsLocked() []*pendingPerm {
	var out []*pendingPerm
	for id, p := range s.perms {
		if op, ok := s.tr.ResolvePerm(id, "cancelled", ""); ok {
			s.queueOpsLocked(op)
		}
		out = append(out, p)
		delete(s.perms, id)
	}
	return out
}

func replyCancelled(ps []*pendingPerm) {
	for _, p := range ps {
		_ = p.conn.Reply(p.rpcID, map[string]any{"outcome": map[string]any{"outcome": "cancelled"}})
	}
}

// ---- process lifecycle ----

func (s *Session) ensureLive(ctx context.Context, mode string) error {
	s.mu.Lock()
	if s.proc != nil && s.proc.Alive() {
		s.mu.Unlock()
		return nil
	}
	if s.startWait != nil {
		ch := s.startWait
		s.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
		s.mu.Lock()
		err := s.startErr
		s.mu.Unlock()
		return err
	}
	if mode == "" {
		mode = "resume"
		if s.meta.NeedsReload || len(s.tr.Items) == 0 {
			mode = "load"
		}
	}
	s.startWait = make(chan struct{})
	s.closing = false
	s.mu.Unlock()
	s.emit()
	err := s.start(ctx, mode)
	s.mu.Lock()
	close(s.startWait)
	s.startWait = nil
	s.startErr = err
	if err != nil {
		s.meta.LastError = err.Error()
	} else {
		s.meta.LastError = ""
	}
	s.mu.Unlock()
	s.emit()
	s.m.saveSession(s)
	return err
}

func (s *Session) start(ctx context.Context, mode string) error {
	s.mu.Lock()
	cwd, addDirs, skip, id := s.meta.Cwd, s.meta.AddDirs, s.meta.SkipPerms, s.meta.ID
	sandbox, agentName, noTel, extra := s.meta.Sandbox, s.meta.AgentName, s.meta.NoTelemetry, s.meta.ExtraArgs
	s.mu.Unlock()
	if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
		return fmt.Errorf("working directory %q is not available", cwd)
	}
	p, err := agyproc.Start(ctx, agyproc.Options{
		AgyPath: s.m.cfg.AgyPath, Cwd: cwd, SkipPermissions: skip, AddDirs: addDirs,
		Sandbox: sandbox, AgentName: agentName, NoTelemetry: noTel, ExtraArgs: extra,
		LogDir: filepath.Join(s.m.cfg.StateDir, "logs"), Env: s.m.agyEnv(), Handler: procHandler{s: s},
	})
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var setup acp.SessionSetup
	switch mode {
	case "new":
		err = p.Conn.Call(cctx, "session/new", map[string]any{"cwd": cwd, "mcpServers": []any{}}, &setup)
	case "resume":
		err = p.Conn.Call(cctx, "session/resume", map[string]any{"sessionId": id, "cwd": cwd, "mcpServers": []any{}}, &setup)
		if err != nil {
			log.Printf("session %s: resume failed (%v); loading with replay", id, err)
			mode = "load"
		}
	}
	if mode == "load" {
		s.mu.Lock()
		var keep []*Item
		for _, it := range s.tr.Items {
			if it.Kind == "user" && (it.State == "queued" || it.State == "sent") {
				keep = append(keep, it)
			}
		}
		s.queueOpsLocked(s.tr.Reset())
		s.mu.Unlock()
		setup = acp.SessionSetup{}
		err = p.Conn.Call(cctx, "session/load", map[string]any{"sessionId": id, "cwd": cwd, "mcpServers": []any{}}, &setup)
		s.mu.Lock()
		for _, it := range keep {
			s.queueOpsLocked(s.tr.add(it))
		}
		s.meta.NeedsReload = false
		s.mu.Unlock()
	}
	if err != nil {
		p.Close(5 * time.Second)
		return fmt.Errorf("agy session/%s: %w", mode, err)
	}
	if setup.SessionID == "" {
		setup.SessionID = id
	}
	s.mu.Lock()
	if s.meta.ID == "" {
		s.meta.ID = setup.SessionID
	}
	if setup.Models != nil {
		s.meta.Models = setup.Models
	}
	if setup.Modes != nil {
		s.meta.Modes = setup.Modes
	}
	if len(setup.ConfigOptions) > 0 {
		s.meta.ConfigOptions = setup.ConfigOptions
	}
	s.proc = p
	s.lastActivity = time.Now()
	wantModel, wantMode, wantThought := s.meta.WantModel, s.meta.WantMode, s.meta.WantThought
	s.mu.Unlock()
	go s.watchProc(p)
	// Apply desired configuration (model, thinking level, mode).
	s.applyConfig(cctx, wantModel, wantThought, wantMode)
	return nil
}

func (s *Session) applyConfig(ctx context.Context, model, thought, mode string) {
	if model != "" {
		if err := s.setModel(ctx, model); err != nil {
			log.Printf("session %s: set model %q: %v", s.ID(), model, err)
		}
	}
	// The phone's saved flags use the CLI's vocabulary (--effort low…max,
	// --mode accept-edits|auto|plan); fit them to what this ACP session offers.
	s.mu.Lock()
	thought = fitThought(s.meta.ConfigOptions, thought)
	if mode != "" && s.meta.Modes != nil && len(s.meta.Modes.AvailableModes) > 0 {
		ok := false
		for _, m := range s.meta.Modes.AvailableModes {
			ok = ok || m.ID == mode
		}
		if !ok {
			log.Printf("session %s: mode %q not offered over ACP; keeping %q", s.meta.ID, mode, s.meta.Modes.CurrentModeID)
			if s.meta.WantMode == mode {
				s.meta.WantMode = ""
			}
			mode = ""
		}
	}
	s.mu.Unlock()
	if thought != "" {
		if err := s.setConfig(ctx, "thought_level", thought); err != nil {
			log.Printf("session %s: set thought %q: %v", s.ID(), thought, err)
		}
	}
	if mode != "" {
		if err := s.setMode(ctx, mode); err != nil {
			log.Printf("session %s: set mode %q: %v", s.ID(), mode, err)
		}
	}
}

var thoughtRank = map[string]int{"minimal": 0, "low": 1, "medium": 2, "high": 3, "xhigh": 4, "max": 5}

// fitThought maps a requested reasoning effort onto the thought levels the
// session's model offers: an exact match, else the closest lower level, else
// the lowest one. Unknown or unconstrained values pass through unchanged.
func fitThought(opts []acp.ConfigOption, want string) string {
	if want == "" {
		return ""
	}
	for _, o := range opts {
		if o.ID != "thought_level" || len(o.Options) == 0 {
			continue
		}
		wr, known := thoughtRank[want]
		best, bestRank, lowest, lowRank := "", -1, "", 99
		for _, v := range o.Options {
			if v.Value == want {
				return want
			}
			r, ok := thoughtRank[v.Value]
			if !ok {
				continue
			}
			if known && r <= wr && r > bestRank {
				best, bestRank = v.Value, r
			}
			if r < lowRank {
				lowest, lowRank = v.Value, r
			}
		}
		if !known {
			return want
		}
		if best != "" {
			return best
		}
		return lowest
	}
	return want
}

func (s *Session) watchProc(p *agyproc.Proc) {
	<-p.Exited()
	s.mu.Lock()
	if s.proc != p {
		s.mu.Unlock()
		return
	}
	s.proc = nil
	perms := s.cancelPermsLocked()
	if !s.closing {
		msg := "agy process exited"
		if err := p.ExitErr(); err != nil {
			msg += ": " + err.Error()
		}
		s.meta.LastError = msg
		s.queueOpsLocked(s.tr.AddNotice("error", msg+". Send a message to restart it."))
	}
	s.mu.Unlock()
	_ = perms // connection is gone; nothing to reply to
	s.emit()
	s.m.saveSession(s)
}

// Hibernate stops the agy process; the session resumes on the next prompt.
func (s *Session) Hibernate() {
	s.mu.Lock()
	p := s.proc
	if p == nil {
		s.mu.Unlock()
		return
	}
	s.closing = true
	s.proc = nil
	perms := s.cancelPermsLocked()
	s.mu.Unlock()
	replyCancelled(perms)
	_ = p.Conn.Notify("session/cancel", map[string]any{"sessionId": s.ID()})
	p.Close(20 * time.Second)
	s.emit()
	s.m.saveSession(s)
}

// ---- prompting ----

// PromptInput is what the app sends.
type PromptInput struct {
	Text        string       `json:"text"`
	Attachments []Attachment `json:"attachments,omitempty"`
	Delivery    string       `json:"delivery,omitempty"` // queue|steer|"" (agy setting)
}

// Attachment is an image/file attached from the phone or the host.
type Attachment struct {
	Type string `json:"type"` // image|file|path
	Name string `json:"name,omitempty"`
	Mime string `json:"mime,omitempty"`
	Data string `json:"data,omitempty"` // base64 for image/file
	Path string `json:"path,omitempty"` // host path for "path"
}

func (s *Session) buildBlocks(in PromptInput) ([]acp.ContentBlock, []Block, error) {
	var blocks []acp.ContentBlock
	var shown []Block
	if strings.TrimSpace(in.Text) != "" {
		blocks = append(blocks, acp.ContentBlock{Type: "text", Text: in.Text})
	}
	id := s.ID()
	for _, a := range in.Attachments {
		switch a.Type {
		case "image":
			mt := a.Mime
			if mt == "" {
				mt = "image/png"
			}
			blocks = append(blocks, acp.ContentBlock{Type: "image", MimeType: mt, Data: a.Data})
			if name, err := s.m.saveAttachment(id, mt, a.Data); err == nil {
				shown = append(shown, Block{Type: "image", Mime: mt, URI: "att:" + id + "/" + name, Name: firstNonEmpty(a.Name, name)})
			}
		case "file":
			name, err := s.m.saveAttachmentNamed(id, a.Name, a.Data)
			if err != nil {
				return nil, nil, err
			}
			path := filepath.Join(s.m.attachmentDir(id), name)
			mt := a.Mime
			if mt == "" {
				mt = mime.TypeByExtension(filepath.Ext(name))
			}
			blocks = append(blocks, acp.ContentBlock{Type: "resource_link", URI: "file://" + path, Name: firstNonEmpty(a.Name, name), MimeType: mt})
			shown = append(shown, Block{Type: "link", URI: "file://" + path, Name: firstNonEmpty(a.Name, name), Mime: mt})
		case "path":
			p := a.Path
			if !filepath.IsAbs(p) {
				p = filepath.Join(s.meta.Cwd, p)
			}
			name := firstNonEmpty(a.Name, filepath.Base(p))
			blocks = append(blocks, acp.ContentBlock{Type: "resource_link", URI: "file://" + p, Name: name})
			shown = append(shown, Block{Type: "link", URI: "file://" + p, Name: name})
		}
	}
	if len(blocks) == 0 {
		return nil, nil, errors.New("empty message")
	}
	return blocks, shown, nil
}

// Prompt sends (or queues) a user message.
func (s *Session) Prompt(in PromptInput) (map[string]any, error) {
	blocks, shown, err := s.buildBlocks(in)
	if err != nil {
		return nil, err
	}
	delivery := in.Delivery
	if delivery == "" {
		delivery = s.m.defaultDelivery()
	}
	s.mu.Lock()
	busy := s.inflight > 0 || s.startWait != nil
	if busy && delivery == "queue" {
		it, op := s.tr.AddUser(in.Text, shown, "queued")
		s.queueOpsLocked(op)
		s.meta.Queue = append(s.meta.Queue, QueuedPrompt{ItemID: it.ID, Blocks: blocks})
		s.mu.Unlock()
		s.emit()
		return map[string]any{"queued": true, "itemId": it.ID}, nil
	}
	it, op := s.tr.AddUser(in.Text, shown, "sent")
	s.queueOpsLocked(op)
	s.beginTurnLocked()
	s.mu.Unlock()
	s.emit()
	go s.runPrompt(it.ID, blocks)
	return map[string]any{"queued": false, "itemId": it.ID}, nil
}

func (s *Session) beginTurnLocked() {
	if s.inflight == 0 {
		s.turnStart = time.Now()
	}
	s.inflight++
	s.meta.Attention = ""
	s.meta.LastError = ""
	s.lastActivity = time.Now()
}

func (s *Session) runPrompt(itemID string, blocks []acp.ContentBlock) {
	ctx := context.Background()
	err := s.ensureLive(ctx, "")
	var res acp.PromptResult
	if err == nil {
		s.mu.Lock()
		p := s.proc
		id := s.meta.ID
		s.mu.Unlock()
		if p == nil {
			err = errors.New("agy process is not running")
		} else {
			err = p.Conn.Call(ctx, "session/prompt", map[string]any{"sessionId": id, "prompt": blocks}, &res)
		}
	}
	s.finishPrompt(itemID, &res, err)
}

func (s *Session) finishPrompt(itemID string, res *acp.PromptResult, err error) {
	s.mu.Lock()
	s.inflight--
	if s.inflight < 0 {
		s.inflight = 0
	}
	if it := s.tr.Get(itemID); it != nil && it.State == "sent" {
		st := ""
		if err != nil {
			st = "failed"
		}
		if op, ok := s.tr.SetUserState(itemID, st); ok {
			s.queueOpsLocked(op)
		}
	}
	dur := time.Since(s.turnStart).Milliseconds()
	notifyKind, notifyBody := "", ""
	if err != nil {
		msg := err.Error()
		if errors.Is(err, acp.ErrClosed) {
			msg = "agy process exited during the turn"
		}
		s.queueOpsLocked(s.tr.AddNotice("error", msg))
		s.meta.LastError = msg
		notifyKind, notifyBody = "error", msg
	} else {
		s.meta.Turns++
		if res.Usage != nil {
			s.meta.Usage.InputTokens += res.Usage.InputTokens
			s.meta.Usage.OutputTokens += res.Usage.OutputTokens
			s.meta.Usage.CachedReadTokens += res.Usage.CachedReadTokens
			s.meta.Usage.CachedWriteTokens += res.Usage.CachedWriteTokens
			s.meta.Usage.ThoughtTokens += res.Usage.ThoughtTokens
			s.meta.Usage.TotalTokens += res.Usage.TotalTokens
		}
		if s.inflight == 0 {
			s.queueOpsLocked(s.tr.AddTurn(&Turn{StopReason: res.StopReason, Usage: res.Usage, DurationMs: dur}))
		}
	}
	var next *QueuedPrompt
	if s.inflight == 0 {
		perms := s.cancelPermsLocked()
		go replyCancelled(perms)
		s.meta.Attention = s.computeAttentionLocked()
		s.meta.LastText = s.lastAgentTextLocked()
		s.lastActivity = time.Now()
		cancelled := err == nil && res.StopReason == "cancelled"
		if err == nil && !cancelled {
			switch s.meta.Attention {
			case "question":
				notifyKind, notifyBody = "question", "agy has a question for you"
			case "plan":
				notifyKind, notifyBody = "plan", "Plan ready for review"
			default:
				notifyKind, notifyBody = "done", firstNonEmpty(s.meta.LastText, "Turn finished")
			}
		}
		if len(s.meta.Queue) > 0 && !cancelled && s.proc != nil {
			q := s.meta.Queue[0]
			s.meta.Queue = s.meta.Queue[1:]
			next = &q
			if op, ok := s.tr.SetUserState(q.ItemID, "sent"); ok {
				s.queueOpsLocked(op)
			}
			s.beginTurnLocked()
			notifyKind = "" // the agent continues with the queued message
		}
	}
	sid, title := s.meta.ID, s.meta.Title
	s.mu.Unlock()
	s.emit()
	s.m.saveSession(s)
	if notifyKind != "" {
		s.m.notify(Notification{SessionID: sid, Kind: notifyKind, Title: firstNonEmpty(title, "agy session"), Body: notifyBody})
	}
	if next != nil {
		go s.runPrompt(next.ItemID, next.Blocks)
	}
}

func (s *Session) lastAgentTextLocked() string {
	for i := len(s.tr.Items) - 1; i >= 0; i-- {
		it := s.tr.Items[i]
		if it.Kind == "agent" && strings.TrimSpace(it.Text) != "" {
			t := strings.TrimSpace(it.Text)
			if len(t) > 400 {
				t = t[:400]
			}
			return t
		}
		if it.Kind == "user" {
			break
		}
	}
	return s.meta.LastText
}

func (s *Session) computeAttentionLocked() string {
	for i := len(s.tr.Items) - 1; i >= 0; i-- {
		it := s.tr.Items[i]
		if it.Kind == "user" {
			break
		}
		if it.Kind != "tool" || it.Tool == nil {
			continue
		}
		if it.Tool.Name == "ask_question" && (it.Tool.Status == "pending" || it.Tool.Status == "in_progress") {
			return "question"
		}
		if IsPlanArtifact(it.Tool) {
			return "plan"
		}
	}
	return ""
}

// IsPlanArtifact reports whether a tool call wrote a review-requesting artifact.
func IsPlanArtifact(t *Tool) bool {
	if t == nil || len(t.Input) == 0 || !strings.Contains(string(t.Input), "RequestFeedback") {
		return false
	}
	var in struct {
		ArtifactMetadata struct {
			RequestFeedback bool `json:"RequestFeedback"`
		} `json:"ArtifactMetadata"`
	}
	_ = json.Unmarshal(t.Input, &in)
	return in.ArtifactMetadata.RequestFeedback
}

// Cancel interrupts the running turn.
func (s *Session) Cancel() error {
	s.mu.Lock()
	p := s.proc
	id := s.meta.ID
	perms := s.cancelPermsLocked()
	s.mu.Unlock()
	replyCancelled(perms)
	if p == nil {
		return nil
	}
	s.emit()
	return p.Conn.Notify("session/cancel", map[string]any{"sessionId": id})
}

// QueueAction manipulates a queued message: send (steer now), remove, edit.
func (s *Session) QueueAction(itemID, action, text string) error {
	s.mu.Lock()
	idx := -1
	for i, q := range s.meta.Queue {
		if q.ItemID == itemID {
			idx = i
		}
	}
	if idx < 0 {
		s.mu.Unlock()
		return errors.New("message is no longer queued")
	}
	q := s.meta.Queue[idx]
	switch action {
	case "remove":
		s.meta.Queue = append(s.meta.Queue[:idx], s.meta.Queue[idx+1:]...)
		if op, ok := s.tr.Remove(itemID); ok {
			s.queueOpsLocked(op)
		}
		s.mu.Unlock()
		s.emit()
		return nil
	case "edit":
		for i := range q.Blocks {
			if q.Blocks[i].Type == "text" {
				q.Blocks[i].Text = text
			}
		}
		s.meta.Queue[idx] = q
		if op, ok := s.tr.UpdateUserText(itemID, text); ok {
			s.queueOpsLocked(op)
		}
		s.mu.Unlock()
		return nil
	case "send":
		s.meta.Queue = append(s.meta.Queue[:idx], s.meta.Queue[idx+1:]...)
		if op, ok := s.tr.SetUserState(itemID, "sent"); ok {
			s.queueOpsLocked(op)
		}
		s.beginTurnLocked()
		s.mu.Unlock()
		s.emit()
		go s.runPrompt(q.ItemID, q.Blocks)
		return nil
	}
	s.mu.Unlock()
	return fmt.Errorf("unknown queue action %q", action)
}

// ---- configuration ----

func (s *Session) liveConn() (*acp.Conn, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.proc == nil {
		return nil, s.meta.ID
	}
	return s.proc.Conn, s.meta.ID
}

// resolveModel maps a model id, base model or display name to a concrete modelId.
func resolveModel(models *acp.Models, want string) (string, bool) {
	if models == nil || want == "" {
		return "", false
	}
	lw := strings.ToLower(strings.TrimSpace(want))
	for _, m := range models.AvailableModels {
		if strings.ToLower(m.ModelID) == lw {
			return m.ModelID, true
		}
	}
	for _, m := range models.AvailableModels {
		if strings.ToLower(m.Name) == lw {
			return m.ModelID, true
		}
	}
	return "", false
}

func (s *Session) setModel(ctx context.Context, want string) error {
	conn, id := s.liveConn()
	s.mu.Lock()
	s.meta.WantModel = want
	models := s.meta.Models
	if conn == nil {
		// Not running: remember it (applied on wake) and show it now.
		if mid, ok := resolveModel(models, want); ok {
			s.meta.Models.CurrentModelID = mid
			for i, o := range s.meta.ConfigOptions {
				if o.ID == "thought_level" {
					for _, v := range o.Options {
						if strings.HasSuffix(mid, "-"+v.Value) {
							s.meta.ConfigOptions[i].CurrentValue = v.Value
							s.meta.WantThought = v.Value
						}
					}
				}
			}
		}
		s.configChangedLocked()
		s.mu.Unlock()
		s.emit()
		return nil
	}
	s.mu.Unlock()
	if mid, ok := resolveModel(models, want); ok {
		if err := conn.Call(ctx, "session/set_model", map[string]any{"sessionId": id, "modelId": mid}, nil); err != nil {
			return err
		}
		s.mu.Lock()
		if s.meta.Models != nil {
			s.meta.Models.CurrentModelID = mid
		}
		// agy tracks the thinking level as a separate config option: keep it
		// in step with the chosen variant (e.g. gemini-3.8-flash-medium).
		level := ""
		for _, o := range s.meta.ConfigOptions {
			if o.ID != "thought_level" {
				continue
			}
			for _, v := range o.Options {
				if strings.HasSuffix(mid, "-"+v.Value) {
					level = v.Value
				}
			}
			if o.CurrentValue == level {
				level = ""
			}
		}
		s.configChangedLocked()
		s.mu.Unlock()
		if level != "" {
			if err := s.setConfig(ctx, "thought_level", level); err != nil {
				log.Printf("session %s: sync thought level %s: %v", id, level, err)
			}
		}
		s.mu.Lock()
		if s.meta.Models != nil {
			s.meta.Models.CurrentModelID = mid
		}
		s.meta.WantModel = mid
		s.configChangedLocked()
		s.mu.Unlock()
		s.emit()
		return nil
	}
	// Maybe a base model value of the "model" config option.
	return s.setConfig(ctx, "model", want)
}

func (s *Session) setConfig(ctx context.Context, configID, value string) error {
	conn, id := s.liveConn()
	s.mu.Lock()
	if configID == "thought_level" {
		s.meta.WantThought = value
	}
	if conn == nil {
		for i, o := range s.meta.ConfigOptions {
			if o.ID == configID {
				s.meta.ConfigOptions[i].CurrentValue = value
			}
		}
		switch configID {
		case "mode":
			s.meta.WantMode = value
			if s.meta.Modes != nil {
				s.meta.Modes.CurrentModeID = value
			}
		case "model":
			s.syncFromConfigLocked()
			if s.meta.Models != nil {
				s.meta.WantModel = s.meta.Models.CurrentModelID
			}
		case "thought_level":
			s.syncFromConfigLocked()
			if s.meta.Models != nil {
				s.meta.WantModel = s.meta.Models.CurrentModelID
			}
		}
		s.configChangedLocked()
		s.mu.Unlock()
		s.emit()
		return nil
	}
	s.mu.Unlock()
	var res struct {
		ConfigOptions []acp.ConfigOption `json:"configOptions"`
	}
	if err := conn.Call(ctx, "session/set_config_option", map[string]any{"sessionId": id, "configId": configID, "value": value}, &res); err != nil {
		return err
	}
	s.mu.Lock()
	if len(res.ConfigOptions) > 0 {
		s.meta.ConfigOptions = res.ConfigOptions
		s.syncFromConfigLocked()
	}
	if configID == "model" {
		s.meta.WantModel = ""
		if s.meta.Models != nil {
			s.meta.WantModel = s.meta.Models.CurrentModelID
		}
	}
	if configID == "mode" && s.meta.Modes != nil {
		s.meta.Modes.CurrentModeID = value
	}
	s.configChangedLocked()
	s.mu.Unlock()
	s.emit()
	return nil
}

func (s *Session) setMode(ctx context.Context, mode string) error {
	conn, id := s.liveConn()
	s.mu.Lock()
	s.meta.WantMode = mode
	if conn == nil {
		if s.meta.Modes == nil {
			s.meta.Modes = &acp.Modes{}
		}
		s.meta.Modes.CurrentModeID = mode
		s.configChangedLocked()
		s.mu.Unlock()
		s.emit()
		return nil
	}
	s.mu.Unlock()
	if err := conn.Call(ctx, "session/set_mode", map[string]any{"sessionId": id, "modeId": mode}, nil); err != nil {
		return err
	}
	s.mu.Lock()
	if s.meta.Modes == nil {
		s.meta.Modes = &acp.Modes{}
	}
	s.meta.Modes.CurrentModeID = mode
	s.configChangedLocked()
	s.mu.Unlock()
	s.emit()
	return nil
}

// SetModel, SetMode and SetConfig are the public setters.
func (s *Session) SetModel(ctx context.Context, model string) error { return s.setModel(ctx, model) }

// SetMode sets the agent mode.
func (s *Session) SetMode(ctx context.Context, mode string) error { return s.setMode(ctx, mode) }

// SetConfig sets a config option.
func (s *Session) SetConfig(ctx context.Context, id, value string) error {
	return s.setConfig(ctx, id, value)
}

// ---- helpers ----

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func decodeB64(data string) ([]byte, error) {
	if i := strings.Index(data, ","); i >= 0 && strings.HasPrefix(data, "data:") {
		data = data[i+1:]
	}
	return base64.StdEncoding.DecodeString(data)
}
