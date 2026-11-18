package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"agentc/internal/acp"
	"agentc/internal/agyproc"
	"agentc/internal/claude"
	"agentc/internal/lsapi"
)

// Config configures the session manager.
type Config struct {
	AgyPath       string
	ClaudePath    string // claude executable ("" = ~/.local/bin/claude or PATH)
	StateDir      string
	HomeDir       string
	AgyDataDir    string // ~/.gemini/antigravity-cli
	IdleHibernate time.Duration
	MaxLive       int
	DefaultSkip   bool
}

// Notification is pushed to every connected client.
type Notification struct {
	ID        string                 `json:"id"`
	Seq       int64                  `json:"seq"`
	SessionID string                 `json:"sessionId,omitempty"`
	TermID    string                 `json:"termId,omitempty"`
	Kind      string                 `json:"kind"`
	Title     string                 `json:"title"`
	Body      string                 `json:"body"`
	ReqID     string                 `json:"reqId,omitempty"`
	Options   []acp.PermissionOption `json:"options,omitempty"`
	Ts        int64                  `json:"ts"`
}

// HistEntry is an agy conversation known from agy's own storage.
type HistEntry struct {
	ID        string `json:"id"`
	Agent     string `json:"agent,omitempty"`
	Title     string `json:"title"`
	Cwd       string `json:"cwd"`
	UpdatedAt int64  `json:"updatedAt"`
	Archived  bool   `json:"archived"`
	Pinned    bool   `json:"pinned"`
	Steps     int    `json:"steps"`
}

// Manager owns all sessions.
type Manager struct {
	cfg Config
	hub *Hub
	// live-state watchers of running, subscribed sessions (see live.go)
	liveWatches map[string]*liveWatch
	mu          sync.Mutex
	sessions    map[string]*Session
	utilMu      sync.Mutex
	util        *agyproc.Proc
	utilUse     time.Time
	histMu      sync.Mutex
	hist        []HistEntry
	histAt      time.Time
	notMu       sync.Mutex
	notifs      []Notification
	notSeq      int64
	stopCh      chan struct{}
	// Sink receives every notification (e.g. Firebase push delivery).
	Sink func(Notification)
}

// NewManager loads persisted sessions.
func NewManager(cfg Config, hub *Hub) *Manager {
	if cfg.IdleHibernate == 0 {
		cfg.IdleHibernate = 30 * time.Minute
	}
	if cfg.MaxLive == 0 {
		cfg.MaxLive = 8
	}
	m := &Manager{cfg: cfg, hub: hub, sessions: map[string]*Session{}, stopCh: make(chan struct{}), liveWatches: map[string]*liveWatch{}}
	_ = os.MkdirAll(filepath.Join(cfg.StateDir, "sessions"), 0o700)
	_ = os.MkdirAll(filepath.Join(cfg.StateDir, "attachments"), 0o700)
	m.loadSessions()
	go m.janitor()
	go m.liveLoop()
	go m.claudeFollowLoop()
	return m
}

// Hub returns the client hub.
func (m *Manager) Hub() *Hub { return m.hub }

// Cfg returns the configuration.
func (m *Manager) Cfg() Config { return m.cfg }

// ---- persistence ----

type savedSession struct {
	Meta  SessionMeta `json:"meta"`
	Seq   int64       `json:"seq"`
	Items []*Item     `json:"items"`
}

func (m *Manager) sessionFile(id string) string {
	return filepath.Join(m.cfg.StateDir, "sessions", id+".json")
}

func (m *Manager) loadSessions() {
	files, _ := filepath.Glob(filepath.Join(m.cfg.StateDir, "sessions", "*.json"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var ss savedSession
		if err := json.Unmarshal(b, &ss); err != nil || ss.Meta.ID == "" {
			log.Printf("skip corrupt session file %s: %v", f, err)
			continue
		}
		for _, it := range ss.Items {
			if it.Kind == "user" && it.State == "sent" {
				it.State = ""
			}
			if it.Kind == "perm" && it.State == "pending" {
				it.State = "cancelled"
			}
		}
		ss.Meta.LastError = ""
		for i := range ss.Meta.Asides {
			if ss.Meta.Asides[i].State == "pending" {
				ss.Meta.Asides[i].State, ss.Meta.Asides[i].Error = "failed", "Interrupted: agentc restarted before Claude answered."
			}
		}
		s := newSession(m, ss.Meta)
		s.tr.LoadItems(ss.Items, ss.Seq)
		s.lastActivity = time.UnixMilli(ss.Meta.UpdatedAt)
		m.sessions[ss.Meta.ID] = s
	}
	log.Printf("loaded %d sessions from %s", len(m.sessions), m.cfg.StateDir)
}

func (m *Manager) saveSession(s *Session) {
	// One save at a time per session, so an older snapshot can't land on top
	// of a newer one.
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	s.mu.Lock()
	if s.removed || s.meta.ID == "" {
		s.mu.Unlock()
		return
	}
	s.tr.Dirty = false
	b, err := json.Marshal(savedSession{Meta: s.meta, Seq: s.tr.Seq, Items: s.tr.Items})
	id := s.meta.ID
	s.mu.Unlock()
	if err != nil {
		log.Printf("save %s: %v", id, err)
		return
	}
	if err := writeFileAtomic(m.sessionFile(id), b); err != nil {
		log.Printf("save %s: %v", id, err)
	}
}

// writeFileAtomic replaces path with b. Each write gets its own temporary
// file, so concurrent writers can't interleave or lose the file mid-rename.
func writeFileAtomic(path string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err = f.Write(b); err == nil {
		err = f.Close()
	} else {
		_ = f.Close()
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}

// SaveAll persists every session (used at shutdown).
func (m *Manager) SaveAll() {
	for _, s := range m.all() {
		m.saveSession(s)
	}
}

// Shutdown stops all agy processes gracefully.
func (m *Manager) Shutdown() {
	close(m.stopCh)
	var wg sync.WaitGroup
	for _, s := range m.all() {
		wg.Add(1)
		go func(s *Session) {
			defer wg.Done()
			s.Hibernate()
		}(s)
	}
	wg.Wait()
	m.utilMu.Lock()
	if m.util != nil {
		m.util.Close(5 * time.Second)
		m.util = nil
	}
	m.utilMu.Unlock()
	m.SaveAll()
}

func (m *Manager) all() []*Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s)
	}
	return out
}

// Get returns a registered session.
func (m *Manager) Get(id string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[id]
}

func (m *Manager) emitSummary(s *Session) {
	sum := s.Summary()
	if sum.ID == "" {
		return
	}
	s.mu.Lock()
	removed := s.removed
	s.mu.Unlock()
	if removed {
		return
	}
	m.hub.Broadcast("session.changed", sum)
}

// ---- notifications ----

func (m *Manager) notify(n Notification) {
	m.notMu.Lock()
	m.notSeq++
	n.Seq = m.notSeq
	n.ID = fmt.Sprintf("n%d-%d", time.Now().Unix(), m.notSeq)
	n.Ts = now()
	m.notifs = append(m.notifs, n)
	if len(m.notifs) > 200 {
		m.notifs = m.notifs[len(m.notifs)-100:]
	}
	sink := m.Sink
	m.notMu.Unlock()
	m.hub.Broadcast("notify", n)
	if sink != nil {
		go sink(n)
	}
}

// Notify lets other subsystems (terminals) raise notifications.
func (m *Manager) Notify(n Notification) { m.notify(n) }

// NotificationsSince returns buffered notifications after seq.
func (m *Manager) NotificationsSince(seq int64) []Notification {
	m.notMu.Lock()
	defer m.notMu.Unlock()
	var out []Notification
	for _, n := range m.notifs {
		if n.Seq > seq {
			out = append(out, n)
		}
	}
	return out
}

// ---- attachments ----

func (m *Manager) attachmentDir(id string) string {
	return filepath.Join(m.cfg.StateDir, "attachments", id)
}

// AttachmentPath resolves an attachment reference "session/name" safely.
func (m *Manager) AttachmentPath(ref string) (string, error) {
	parts := strings.SplitN(ref, "/", 2)
	if len(parts) != 2 || strings.Contains(ref, "..") || strings.ContainsAny(parts[1], "/\\") {
		return "", errors.New("bad attachment reference")
	}
	return filepath.Join(m.attachmentDir(parts[0]), parts[1]), nil
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (m *Manager) saveAttachment(id, mimeType, b64 string) (string, error) {
	data, err := decodeB64(b64)
	if err != nil {
		return "", err
	}
	ext := ".bin"
	if exts, _ := mime.ExtensionsByType(mimeType); len(exts) > 0 {
		ext = exts[0]
		if mimeType == "image/jpeg" {
			ext = ".jpg"
		}
	}
	dir := m.attachmentDir(firstNonEmpty(id, "pending"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	name := fmt.Sprintf("%d-%s%s", time.Now().UnixMilli(), randHex(3), ext)
	return name, os.WriteFile(filepath.Join(dir, name), data, 0o600)
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._ -]+`)

func (m *Manager) saveAttachmentNamed(id, name, b64 string) (string, error) {
	data, err := decodeB64(b64)
	if err != nil {
		return "", err
	}
	name = strings.TrimSpace(unsafeName.ReplaceAllString(filepath.Base(name), "_"))
	if name == "" || name == "." {
		name = "file"
	}
	dir := m.attachmentDir(firstNonEmpty(id, "pending"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	final := name
	for i := 1; ; i++ {
		if _, err := os.Stat(filepath.Join(dir, final)); os.IsNotExist(err) {
			break
		}
		ext := filepath.Ext(name)
		final = fmt.Sprintf("%s-%d%s", strings.TrimSuffix(name, ext), i, ext)
	}
	return final, os.WriteFile(filepath.Join(dir, final), data, 0o600)
}

// ---- agy environment & settings ----

func (m *Manager) agyEnv() []string {
	drop := map[string]bool{"TMUX": true, "TMUX_PANE": true}
	var env []string
	pathSet := false
	for _, kv := range os.Environ() {
		k := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			k = kv[:i]
		}
		if drop[k] || strings.HasPrefix(k, "CLAUDE") || strings.HasPrefix(k, "ANTIGRAVITY_") {
			continue
		}
		if k == "PATH" {
			pathSet = true
			local := filepath.Join(m.cfg.HomeDir, ".local", "bin")
			if !strings.Contains(kv, local) {
				kv = "PATH=" + local + ":" + kv[5:]
			}
		}
		env = append(env, kv)
	}
	if !pathSet {
		env = append(env, "PATH="+filepath.Join(m.cfg.HomeDir, ".local", "bin")+":/usr/local/bin:/usr/bin:/bin")
	}
	return env
}

// AgyEnv exposes the sanitized environment for other subsystems.
func (m *Manager) AgyEnv() []string { return m.agyEnv() }

// AgySettings reads agy's settings.json.
func (m *Manager) AgySettings() map[string]any {
	b, err := os.ReadFile(filepath.Join(m.cfg.AgyDataDir, "settings.json"))
	if err != nil {
		return map[string]any{}
	}
	b = []byte(strings.TrimPrefix(string(b), "\xef\xbb\xbf"))
	var v map[string]any
	if json.Unmarshal(b, &v) != nil {
		return map[string]any{}
	}
	return v
}

func (m *Manager) defaultDelivery(agent string) string {
	if agent == "claude" {
		return "steer" // Claude takes messages mid-turn, like its own terminal UI
	}
	if v, _ := m.AgySettings()["queuedMessages"].(string); strings.Contains(strings.ToLower(v), "immediate") {
		return "steer"
	}
	return "queue"
}

func (m *Manager) defaultModel(agent string) string {
	if agent != "agy" {
		return "" // the agent's own default
	}
	if v, _ := m.AgySettings()["model"].(string); v != "" {
		return v
	}
	return ""
}

// ---- utility process (history + language-server RPCs) ----

func (m *Manager) utility(ctx context.Context) (*agyproc.Proc, error) {
	m.utilMu.Lock()
	defer m.utilMu.Unlock()
	m.utilUse = time.Now()
	if m.util != nil && m.util.Alive() {
		return m.util, nil
	}
	p, err := agyproc.Start(ctx, agyproc.Options{AgyPath: m.cfg.AgyPath, Cwd: m.cfg.HomeDir,
		LogDir: filepath.Join(m.cfg.StateDir, "logs"), Env: m.agyEnv(), Handler: utilHandler{}})
	if err != nil {
		return nil, err
	}
	// agy boots its backend (and language server) lazily on the first
	// session request; session/list is the cheapest way to trigger it.
	bctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	_ = p.Conn.Call(bctx, "session/list", map[string]any{}, nil)
	cancel()
	m.util = p
	return p, nil
}

type utilHandler struct{}

func (utilHandler) HandleNotification(string, json.RawMessage) {}
func (utilHandler) HandleRequest(c *acp.Conn, id json.RawMessage, method string, _ json.RawMessage) {
	_ = c.ReplyError(id, -32601, "not supported")
}

// LS returns a language-server client for a conversation: its own process if
// live, otherwise the shared utility process.
func (m *Manager) LS(ctx context.Context, convID string) (*lsapi.Client, error) {
	if s := m.Get(convID); s != nil {
		s.mu.Lock()
		p, _ := s.proc.(*agyproc.Proc)
		s.mu.Unlock()
		if p != nil && p.Alive() {
			port, err := p.LSPort(ctx)
			if err == nil {
				return lsapi.New(port), nil
			}
		}
	}
	return m.UtilityLS(ctx)
}

// UtilityLS returns the utility process's language server.
func (m *Manager) UtilityLS(ctx context.Context) (*lsapi.Client, error) {
	p, err := m.utility(ctx)
	if err != nil {
		return nil, err
	}
	port, err := p.LSPort(ctx)
	if err != nil {
		return nil, err
	}
	return lsapi.New(port), nil
}

// ---- history ----

func (m *Manager) invalidateHistory() {
	m.histMu.Lock()
	m.histAt = time.Time{}
	m.histMu.Unlock()
}

// History returns agy's conversations (authoritative list when force is set
// or the utility process is already up; otherwise agy's metadata cache).
func (m *Manager) History(ctx context.Context, force bool) []HistEntry {
	m.histMu.Lock()
	if !force && time.Since(m.histAt) < 15*time.Second && m.hist != nil {
		h := m.hist
		m.histMu.Unlock()
		return h
	}
	m.histMu.Unlock()

	byID := map[string]*HistEntry{}
	for _, e := range m.readMetadataCache() {
		e := e
		byID[e.ID] = &e
	}
	m.utilMu.Lock()
	utilUp := m.util != nil && m.util.Alive()
	m.utilMu.Unlock()
	if force || utilUp {
		if list, err := m.acpList(ctx); err == nil {
			for _, si := range list {
				e := byID[si.SessionID]
				if e == nil {
					e = &HistEntry{ID: si.SessionID}
					byID[si.SessionID] = e
				}
				if si.Title != "" {
					e.Title = si.Title
				}
				if si.Cwd != "" {
					e.Cwd = si.Cwd
				}
				if t, err := time.Parse(time.RFC3339, si.UpdatedAt); err == nil && t.UnixMilli() > e.UpdatedAt {
					e.UpdatedAt = t.UnixMilli()
				}
			}
		} else {
			log.Printf("history: session/list failed: %v", err)
		}
	}
	out := make([]HistEntry, 0, len(byID))
	out = append(out, m.claudeHistory()...)
	for _, e := range byID {
		ann := m.readAnnotations(e.ID)
		if ann.Title != "" {
			e.Title = ann.Title
		}
		e.Archived, e.Pinned = ann.Archived, ann.Pinned
		if _, err := os.Stat(filepath.Join(m.cfg.AgyDataDir, "conversations", e.ID+".db")); err != nil {
			continue // deleted
		}
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	m.histMu.Lock()
	m.hist, m.histAt = out, time.Now()
	m.histMu.Unlock()
	return out
}

func (m *Manager) acpList(ctx context.Context) ([]acp.SessionInfo, error) {
	p, err := m.utility(ctx)
	if err != nil {
		return nil, err
	}
	var all []acp.SessionInfo
	cursor := ""
	for i := 0; i < 50; i++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var r struct {
			Sessions   []acp.SessionInfo `json:"sessions"`
			NextCursor string            `json:"nextCursor"`
		}
		cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		err := p.Conn.Call(cctx, "session/list", params, &r)
		cancel()
		if err != nil {
			return nil, err
		}
		all = append(all, r.Sessions...)
		if r.NextCursor == "" || r.NextCursor == cursor {
			break
		}
		cursor = r.NextCursor
	}
	return all, nil
}

func (m *Manager) readMetadataCache() []HistEntry {
	b, err := os.ReadFile(filepath.Join(m.cfg.AgyDataDir, "cache", "conversation_metadata.json"))
	if err != nil {
		return nil
	}
	var c struct {
		Conversations map[string]struct {
			IsInternal bool `json:"is_internal"`
			Summary    *struct {
				Title         string   `json:"Title"`
				NumSteps      int      `json:"NumSteps"`
				UpdatedAt     string   `json:"UpdatedAt"`
				WorkspaceURIs []string `json:"WorkspaceURIs"`
				Internal      bool     `json:"Internal"`
			} `json:"summary"`
			LastModified string `json:"last_modified_time"`
		} `json:"conversations"`
	}
	if json.Unmarshal(b, &c) != nil {
		return nil
	}
	var out []HistEntry
	for id, v := range c.Conversations {
		if v.IsInternal || v.Summary == nil || v.Summary.Internal {
			continue
		}
		if v.Summary.NumSteps == 0 && v.Summary.Title == "" {
			continue
		}
		e := HistEntry{ID: id, Title: v.Summary.Title, Steps: v.Summary.NumSteps}
		if len(v.Summary.WorkspaceURIs) > 0 {
			e.Cwd = strings.TrimPrefix(v.Summary.WorkspaceURIs[0], "file://")
		}
		if t, err := time.Parse(time.RFC3339Nano, v.Summary.UpdatedAt); err == nil && t.Year() > 2000 {
			e.UpdatedAt = t.UnixMilli()
		} else if t, err := time.Parse(time.RFC3339Nano, v.LastModified); err == nil {
			e.UpdatedAt = t.UnixMilli()
		}
		out = append(out, e)
	}
	return out
}

// Annotations are agy's per-conversation flags.
type Annotations struct {
	Title    string
	Archived bool
	Pinned   bool
	Starred  bool
}

var annTitleRe = regexp.MustCompile(`(?m)^title:"((?:[^"\\]|\\.)*)"`)

func (m *Manager) readAnnotations(id string) Annotations {
	b, err := os.ReadFile(filepath.Join(m.cfg.AgyDataDir, "annotations", id+".pbtxt"))
	if err != nil {
		return Annotations{}
	}
	s := string(b)
	var a Annotations
	if mm := annTitleRe.FindStringSubmatch(s); mm != nil {
		a.Title = unescapeProtoText(mm[1])
	}
	a.Archived = strings.Contains(s, "archived:true")
	a.Pinned = strings.Contains(s, "pinned:true")
	a.Starred = strings.Contains(s, "starred:true")
	return a
}

// unescapeProtoText decodes protobuf text-format string escapes (incl. octal UTF-8 bytes).
func unescapeProtoText(s string) string {
	var out []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' || i+1 >= len(s) {
			out = append(out, c)
			continue
		}
		i++
		switch d := s[i]; {
		case d == 'n':
			out = append(out, '\n')
		case d == 't':
			out = append(out, '\t')
		case d == 'r':
			out = append(out, '\r')
		case d >= '0' && d <= '7':
			v := 0
			j := 0
			for j < 3 && i+j < len(s) && s[i+j] >= '0' && s[i+j] <= '7' {
				v = v*8 + int(s[i+j]-'0')
				j++
			}
			out = append(out, byte(v))
			i += j - 1
		case d == 'x' && i+2 < len(s):
			var v int
			fmt.Sscanf(s[i+1:i+3], "%02x", &v)
			out = append(out, byte(v))
			i += 2
		default:
			out = append(out, d)
		}
	}
	return string(out)
}

// List merges daemon sessions with agy's conversation history.
func (m *Manager) List(ctx context.Context, force bool) []Summary {
	hist := m.History(ctx, force)
	histByID := map[string]HistEntry{}
	for _, h := range hist {
		histByID[h.ID] = h
	}
	var liveClaude map[string]claude.LiveSession
	if _, err := os.Stat(m.claudeDir()); err == nil {
		liveClaude = claude.LiveSessions(m.claudeDir())
		own := m.ownPids()
		for id, ls := range liveClaude {
			if own[ls.Pid] {
				delete(liveClaude, id)
			}
		}
	}
	var out []Summary
	seen := map[string]bool{}
	for _, s := range m.all() {
		sum := s.Summary()
		if sum.ID == "" {
			continue
		}
		if ls, ok := liveClaude[sum.ID]; ok {
			sum.LiveElse = firstNonEmpty(ls.Status, "running")
		}
		if h, ok := histByID[sum.ID]; ok {
			sum.Archived, sum.Pinned = h.Archived, h.Pinned
			if h.UpdatedAt > sum.UpdatedAt+5000 && !sum.Live {
				sum.UpdatedAt = h.UpdatedAt
				s.mu.Lock()
				s.meta.NeedsReload = true
				s.mu.Unlock()
			}
		}
		seen[sum.ID] = true
		out = append(out, sum)
	}
	for _, h := range hist {
		if seen[h.ID] {
			continue
		}
		sum := Summary{ID: h.ID, Agent: firstNonEmpty(h.Agent, "agy"), Title: firstNonEmpty(h.Title, "Untitled conversation"), Cwd: h.Cwd, State: "history",
			UpdatedAt: h.UpdatedAt, CreatedAt: h.UpdatedAt, Archived: h.Archived, Pinned: h.Pinned, Turns: h.Steps}
		if ls, ok := liveClaude[h.ID]; ok {
			sum.LiveElse = firstNonEmpty(ls.Status, "running")
		}
		out = append(out, sum)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out
}

// ---- create / open ----

// Flags are the agy launch options the app chose (saved on the phone).
type Flags struct {
	SkipPermissions  *bool    `json:"skipPermissions,omitempty"`
	Sandbox          bool     `json:"sandbox,omitempty"`
	AgentName        string   `json:"agentName,omitempty"` // --agent (custom agy agent)
	DisableTelemetry bool     `json:"disableTelemetry,omitempty"`
	ExtraArgs        []string `json:"extraArgs,omitempty"` // appended to `agy acp`
	AddDirs          []string `json:"addDirs,omitempty"`
	Model            string   `json:"model,omitempty"`
	Mode             string   `json:"mode,omitempty"`
	Thought          string   `json:"thought,omitempty"`
}

// CreateParams describe a new session.
type CreateParams struct {
	Flags
	Agent  string       `json:"agent,omitempty"` // agy (claude, codex planned)
	Cwd    string       `json:"cwd"`
	Prompt *PromptInput `json:"prompt,omitempty"`
}

func (m *Manager) applyFlags(meta *SessionMeta, f *Flags) {
	if f == nil {
		return
	}
	if f.SkipPermissions != nil {
		meta.SkipPerms = *f.SkipPermissions
	}
	meta.Sandbox, meta.AgentName, meta.NoTelemetry = f.Sandbox, strings.TrimSpace(f.AgentName), f.DisableTelemetry
	meta.ExtraArgs = nil
	for _, a := range f.ExtraArgs {
		if strings.TrimSpace(a) != "" {
			meta.ExtraArgs = append(meta.ExtraArgs, a)
		}
	}
	for _, d := range f.AddDirs {
		d = expandHome(d, m.cfg.HomeDir)
		if d != "" && d != meta.Cwd {
			dup := false
			for _, e := range meta.AddDirs {
				dup = dup || e == d
			}
			if !dup {
				meta.AddDirs = append(meta.AddDirs, d)
			}
		}
	}
	if f.Model != "" {
		meta.WantModel = f.Model
	}
	if f.Mode != "" {
		meta.WantMode = f.Mode
	}
	if f.Thought != "" {
		meta.WantThought = f.Thought
	}
}

// Create starts a new agy session.
func (m *Manager) Create(ctx context.Context, p CreateParams) (*Session, error) {
	agent := firstNonEmpty(p.Agent, "agy")
	if prof := m.Agent(agent); prof == nil || !prof.Available {
		reason := "unknown agent"
		if prof != nil {
			reason = prof.Reason
		}
		return nil, fmt.Errorf("agent %q is not available: %s", agent, reason)
	}
	cwd := expandHome(p.Cwd, m.cfg.HomeDir)
	if cwd == "" {
		cwd = m.cfg.HomeDir
	}
	st, err := os.Stat(cwd)
	if err != nil || !st.IsDir() {
		return nil, fmt.Errorf("directory %q does not exist", cwd)
	}
	ts := now()
	meta := SessionMeta{Agent: agent, Cwd: cwd, SkipPerms: m.cfg.DefaultSkip, WantModel: m.defaultModel(agent),
		CreatedAt: ts, UpdatedAt: ts, Origin: "app", Title: "New session"}
	m.applyFlags(&meta, &p.Flags)
	s := newSession(m, meta)
	if err := s.ensureLive(ctx, "new"); err != nil {
		return nil, err
	}
	s.mu.Lock()
	id := s.meta.ID
	s.mu.Unlock()
	m.mu.Lock()
	m.sessions[id] = s
	m.mu.Unlock()
	m.saveSession(s)
	m.invalidateHistory()
	m.emitSummary(s)
	if p.Prompt != nil && (strings.TrimSpace(p.Prompt.Text) != "" || len(p.Prompt.Attachments) > 0) {
		if _, err := s.Prompt(*p.Prompt); err != nil {
			return s, err
		}
	}
	m.enforceMaxLive()
	return s, nil
}

// Open returns a session, adopting an agy conversation from history if needed.
func (m *Manager) Open(ctx context.Context, id string) (*Session, error) {
	return m.OpenWith(ctx, id, nil)
}

// OpenWith is Open with the launch flags to use when adopting a conversation.
func (m *Manager) OpenWith(ctx context.Context, id string, flags *Flags) (*Session, error) {
	if s := m.Get(id); s != nil {
		return s, nil
	}
	var h *HistEntry
	for _, e := range m.History(ctx, false) {
		if e.ID == id {
			e := e
			h = &e
		}
	}
	if h == nil {
		for _, e := range m.History(ctx, true) {
			if e.ID == id {
				e := e
				h = &e
			}
		}
	}
	if h == nil {
		if p, err := m.claudeTranscript(id, ""); err == nil {
			if si, ok := claude.ReadSessionInfo(p); ok {
				h = &HistEntry{ID: id, Agent: "claude", Title: si.Title, Cwd: si.Cwd, UpdatedAt: si.Updated, Archived: si.Archived}
			}
		}
	}
	if h == nil {
		if _, err := os.Stat(filepath.Join(m.cfg.AgyDataDir, "conversations", id+".db")); err != nil {
			return nil, fmt.Errorf("conversation %s not found", id)
		}
		h = &HistEntry{ID: id}
	}
	if h.Agent == "claude" {
		return m.openClaude(h, flags)
	}
	cwd := h.Cwd
	if st, err := os.Stat(cwd); cwd == "" || err != nil || !st.IsDir() {
		cwd = m.cfg.HomeDir
	}
	meta := SessionMeta{ID: id, Agent: "agy", Cwd: cwd, SkipPerms: m.cfg.DefaultSkip, Title: firstNonEmpty(h.Title, "Conversation"),
		CreatedAt: h.UpdatedAt, UpdatedAt: h.UpdatedAt, Origin: "history", Archived: h.Archived, Pinned: h.Pinned, NeedsReload: true}
	if flags != nil {
		// An existing conversation keeps its own model, mode, effort and agent;
		// only process-level options (permissions, sandbox, dirs…) apply.
		f := *flags
		f.Model, f.Mode, f.Thought, f.AgentName = "", "", "", ""
		m.applyFlags(&meta, &f)
	}
	s := newSession(m, meta)
	m.mu.Lock()
	if existing := m.sessions[id]; existing != nil {
		m.mu.Unlock()
		return existing, nil
	}
	m.sessions[id] = s
	m.mu.Unlock()
	m.emitSummary(s)
	go func() {
		if err := s.ensureLive(context.Background(), "load"); err != nil {
			log.Printf("open %s: %v", id, err)
			s.mu.Lock()
			s.queueOpsLocked(s.tr.AddNotice("error", "Could not load conversation: "+err.Error()))
			s.mu.Unlock()
		}
		m.enforceMaxLive()
	}()
	return s, nil
}

// Wake starts the agy process for a sleeping session (resume).
func (m *Manager) Wake(ctx context.Context, id string) error {
	s := m.Get(id)
	if s == nil {
		return fmt.Errorf("unknown session %s", id)
	}
	go func() {
		_ = s.ensureLive(context.Background(), "")
		m.enforceMaxLive()
	}()
	return nil
}

// Reload replays the conversation from agy (fixes stale transcripts).
func (m *Manager) Reload(ctx context.Context, id string) error {
	s := m.Get(id)
	if s == nil {
		return fmt.Errorf("unknown session %s", id)
	}
	s.mu.Lock()
	busy := s.inflight > 0
	s.mu.Unlock()
	if busy {
		return errors.New("wait for the current turn to finish")
	}
	s.Hibernate()
	s.mu.Lock()
	s.meta.NeedsReload = true
	s.mu.Unlock()
	return s.ensureLive(ctx, "load")
}

// Forget drops a session from the daemon (agy keeps the conversation).
func (m *Manager) Forget(id string) {
	s := m.Get(id)
	if s == nil {
		return
	}
	s.Hibernate()
	s.mu.Lock()
	s.removed = true
	s.mu.Unlock()
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
	_ = os.Remove(m.sessionFile(id))
	m.hub.Broadcast("session.removed", map[string]any{"id": id})
}

func expandHome(p, home string) string {
	p = strings.TrimSpace(p)
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	if p != "" && !filepath.IsAbs(p) {
		return filepath.Join(home, p)
	}
	return filepath.Clean(p)
}

// ---- janitor ----

func (m *Manager) janitor() {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	lastPrune := time.Time{}
	for {
		select {
		case <-m.stopCh:
			return
		case <-t.C:
		}
		m.utilMu.Lock()
		if m.util != nil && time.Since(m.utilUse) > 5*time.Minute {
			u := m.util
			m.util = nil
			go u.Close(5 * time.Second)
		}
		m.utilMu.Unlock()
		for _, s := range m.all() {
			if m.idleFor(s) > m.cfg.IdleHibernate {
				log.Printf("hibernating idle session %s", s.ID())
				go s.Hibernate()
			}
		}
		m.enforceMaxLive()
		if time.Since(lastPrune) > 6*time.Hour {
			lastPrune = time.Now()
			m.pruneLogs()
		}
	}
}

// idleFor returns how long a live session has been idle and hibernatable (0 if not).
func (m *Manager) idleFor(s *Session) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.proc == nil || s.inflight > 0 || len(s.perms) > 0 || s.startWait != nil {
		return 0
	}
	if m.hub.HasSubscribers(s.meta.ID) {
		return 0
	}
	if time.Since(s.lastActivity) < time.Minute {
		return 0 // just created or used: the app is probably about to open it
	}
	for _, t := range s.tasks {
		if t.Status == "running" {
			return 0 // a background command or subagent is still working
		}
	}
	if s.agyBusyLocked() {
		return 0 // agy's background tasks or subagents are still working
	}
	n := len(s.tr.Items)
	for i := n - 1; i >= 0 && i >= n-80; i-- {
		it := s.tr.Items[i]
		if it.Kind == "tool" && it.Tool != nil && (it.Tool.Status == "in_progress" || it.Tool.Status == "pending") && it.Tool.Name != "ask_question" {
			return 0 // background command still running
		}
	}
	return time.Since(s.lastActivity)
}

func (m *Manager) enforceMaxLive() {
	type cand struct {
		s    *Session
		idle time.Duration
	}
	var live []cand
	count := 0
	for _, s := range m.all() {
		s.mu.Lock()
		alive := s.proc != nil
		s.mu.Unlock()
		if alive {
			count++
			if d := m.idleFor(s); d > 0 {
				live = append(live, cand{s, d})
			}
		}
	}
	if count <= m.cfg.MaxLive {
		return
	}
	sort.Slice(live, func(i, j int) bool { return live[i].idle > live[j].idle })
	for i := 0; i < len(live) && count > m.cfg.MaxLive; i++ {
		log.Printf("too many live sessions (%d > %d): hibernating %s", count, m.cfg.MaxLive, live[i].s.ID())
		go live[i].s.Hibernate()
		count--
	}
}

func (m *Manager) pruneLogs() {
	files, _ := filepath.Glob(filepath.Join(m.cfg.StateDir, "logs", "*"))
	cutoff := time.Now().Add(-72 * time.Hour)
	for _, f := range files {
		if st, err := os.Stat(f); err == nil && st.ModTime().Before(cutoff) {
			_ = os.Remove(f)
		}
	}
}

// Stats summarizes the daemon's agy processes.
func (m *Manager) Stats() map[string]any {
	live, rss := 0, int64(0)
	for _, s := range m.all() {
		s.mu.Lock()
		if s.proc != nil {
			live++
			rss += s.proc.RSSKiB()
		}
		s.mu.Unlock()
	}
	m.utilMu.Lock()
	util := m.util != nil
	if m.util != nil {
		rss += m.util.RSSKiB()
	}
	m.utilMu.Unlock()
	return map[string]any{"sessions": len(m.all()), "live": live, "utility": util, "rssKiB": rss,
		"idleHibernateMin": int(m.cfg.IdleHibernate.Minutes()), "maxLive": m.cfg.MaxLive}
}
