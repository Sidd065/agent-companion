package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"agentc/internal/claude"
)

// ---- environment ----

func (m *Manager) claudeBin() string {
	if m.cfg.ClaudePath != "" {
		return m.cfg.ClaudePath
	}
	local := filepath.Join(m.cfg.HomeDir, ".local", "bin", "claude")
	if st, err := os.Stat(local); err == nil && !st.IsDir() {
		return local
	}
	if p, err := exec.LookPath("claude"); err == nil {
		return p
	}
	return "claude"
}

// ClaudeBin is the claude executable agentc uses.
func (m *Manager) ClaudeBin() string { return m.claudeBin() }

func (m *Manager) claudeDir() string { return claude.ConfigDir(m.cfg.HomeDir) }

// claudeEnv is the environment for Claude Code processes. Variables leaking
// from a Claude Code session that started the daemon are dropped; sessions
// are tagged so Claude lists them with the user's own (not as SDK runs).
func (m *Manager) claudeEnv() []string {
	env := m.agyEnv()
	if v := os.Getenv("CLAUDE_CONFIG_DIR"); v != "" {
		env = append(env, "CLAUDE_CONFIG_DIR="+v)
	}
	return append(env, "CLAUDE_CODE_ENTRYPOINT=agentc", "CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING=1")
}

// ClaudeEnv exposes the environment for other subsystems (terminals).
func (m *Manager) ClaudeEnv() []string { return m.claudeEnv() }

func (m *Manager) claudeTranscript(id, cwd string) (string, error) {
	return claude.FindSession(m.claudeDir(), id, cwd)
}

func (m *Manager) claudeTranscriptExists(id, cwd string) bool {
	_, err := m.claudeTranscript(id, cwd)
	return err == nil
}

// ownPids are the claude processes this daemon runs (or is stopping). It
// takes no session locks, so it is safe to call while holding one.
func (m *Manager) ownPids() map[int]bool {
	return claude.OwnPids()
}

// claudeLiveElsewhere reports a Claude Code process outside agentc that has
// the conversation open (a terminal on the computer).
func (m *Manager) claudeLiveElsewhere(id string) *claude.LiveSession {
	ls, ok := claude.LiveSessions(m.claudeDir())[id]
	if !ok {
		return nil
	}
	if m.ownPids()[ls.Pid] {
		return nil
	}
	return &ls
}

// ---- rate limits ----

var (
	rlMu sync.Mutex
	rl   json.RawMessage
	rlAt time.Time
)

func (m *Manager) setClaudeRateLimit(info json.RawMessage) {
	rlMu.Lock()
	changed := string(rl) != string(info)
	rl, rlAt = append(json.RawMessage(nil), info...), time.Now()
	rlMu.Unlock()
	if changed {
		m.hub.Broadcast("claude.rateLimit", map[string]any{"rateLimit": json.RawMessage(info), "at": time.Now().UnixMilli()})
	}
}

// ClaudeRateLimit returns the last rate-limit info Claude reported.
func (m *Manager) ClaudeRateLimit() (json.RawMessage, int64) {
	rlMu.Lock()
	defer rlMu.Unlock()
	if rl == nil {
		return nil, 0
	}
	return rl, rlAt.UnixMilli()
}

// ---- extension notifications from the Claude adapter ----

func (s *Session) onExtension(method string, params json.RawMessage) {
	switch method {
	case claude.ExtNotice:
		var n struct {
			Level string `json:"level"`
			Text  string `json:"text"`
		}
		if json.Unmarshal(params, &n) != nil || strings.TrimSpace(n.Text) == "" {
			return
		}
		s.mu.Lock()
		s.queueOpsLocked(s.tr.AddNotice(firstNonEmpty(n.Level, "info"), n.Text))
		s.mu.Unlock()
	case claude.ExtLive:
		var n struct {
			Tasks []claude.Task `json:"tasks"`
		}
		if json.Unmarshal(params, &n) != nil {
			return
		}
		s.mu.Lock()
		s.tasks = n.Tasks
		id := s.meta.ID
		s.mu.Unlock()
		s.m.hub.ToSubscribers(id, "session.live", map[string]any{"id": id, "live": s.m.claudeLive(s)})
	case claude.ExtUserMessage:
		var n struct {
			ItemID string `json:"itemId"`
			UUID   string `json:"uuid"`
		}
		if json.Unmarshal(params, &n) != nil {
			return
		}
		s.mu.Lock()
		if it := s.tr.Get(n.ItemID); it != nil {
			it.Ref = n.UUID
			if it.State == "sent" {
				it.State = ""
			}
			s.queueOpsLocked(s.tr.set(it))
		}
		s.mu.Unlock()
	case claude.ExtRateLimit:
		s.m.setClaudeRateLimit(params)
	case claude.ExtSuggestion:
		var n struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(params, &n) != nil {
			return
		}
		s.mu.Lock()
		s.suggestion = n.Text
		s.mu.Unlock()
		s.emit()
	case claude.ExtStatus:
		var n struct {
			Status string `json:"status"`
		}
		if json.Unmarshal(params, &n) != nil {
			return
		}
		if n.Status == "requesting" {
			n.Status = "" // the normal "working" state
		}
		s.mu.Lock()
		changed := s.status != n.Status
		s.status = n.Status
		s.mu.Unlock()
		if changed {
			s.emit()
		}
	case claude.ExtPermGone:
		var n struct {
			Key string `json:"key"`
		}
		if json.Unmarshal(params, &n) != nil {
			return
		}
		s.mu.Lock()
		var gone []*pendingPerm
		for reqID, p := range s.perms {
			if p.key == n.Key {
				if op, ok := s.tr.ResolvePerm(reqID, "cancelled", ""); ok {
					s.queueOpsLocked(op)
				}
				gone = append(gone, p)
				delete(s.perms, reqID)
			}
		}
		s.mu.Unlock()
		replyCancelled(gone)
		s.emit()
	case claude.ExtInfo:
		var info map[string]any
		if json.Unmarshal(params, &info) != nil {
			return
		}
		s.mu.Lock()
		s.meta.Info = info
		s.configChangedLocked()
		s.mu.Unlock()
	}
}

// ---- history replay (straight from Claude's transcript on disk) ----

func (s *Session) replayClaude() error {
	s.mu.Lock()
	id, cwd, resumeAt, forkOf := s.meta.ID, s.meta.Cwd, s.meta.ResumeAt, s.meta.ForkOf
	s.mu.Unlock()
	path, err := s.m.claudeTranscript(id, cwd)
	if err != nil && forkOf != "" {
		path, err = s.m.claudeTranscript(forkOf, cwd)
	}
	if err != nil {
		s.mu.Lock()
		s.meta.NeedsReload = false
		s.mu.Unlock()
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	chain, truncated, err := claude.ReadChain(path, 4000, 96<<20)
	if err != nil {
		return err
	}
	if resumeAt != "" {
		for i, e := range chain {
			if e.UUID == resumeAt {
				chain = chain[:i+1]
				break
			}
		}
	}
	s.mu.Lock()
	var keep []*Item
	for _, it := range s.tr.Items {
		if it.Kind == "user" && (it.State == "queued" || it.State == "sent") {
			keep = append(keep, it)
		}
	}
	s.queueOpsLocked(s.tr.Reset())
	if truncated {
		s.queueOpsLocked(s.tr.AddNotice("info", "Earlier messages of this long conversation aren't shown."))
	}
	s.replaying = true
	s.mu.Unlock()
	claude.ReplayUpdates(chain, cwd, func(u map[string]any) {
		raw, err := json.Marshal(u)
		if err == nil {
			s.onUpdate(raw)
		}
	}, func(level, text string) {
		s.mu.Lock()
		s.queueOpsLocked(s.tr.AddNotice(level, text))
		s.mu.Unlock()
	})
	s.mu.Lock()
	s.replaying = false
	for _, it := range keep {
		s.queueOpsLocked(s.tr.add(it))
	}
	s.meta.NeedsReload = false
	if st, err := os.Stat(path); err == nil && st.ModTime().UnixMilli() > s.meta.UpdatedAt {
		s.meta.UpdatedAt = st.ModTime().UnixMilli()
	}
	s.mu.Unlock()
	s.emit()
	return nil
}

// ---- history ----

type claudeAnn struct {
	Pinned bool `json:"pinned,omitempty"`
}

var claudeAnnMu sync.Mutex

func (m *Manager) claudeAnnFile() string { return filepath.Join(m.cfg.StateDir, "claude-annotations.json") }

func (m *Manager) claudeAnnotations() map[string]claudeAnn {
	claudeAnnMu.Lock()
	defer claudeAnnMu.Unlock()
	out := map[string]claudeAnn{}
	if b, err := os.ReadFile(m.claudeAnnFile()); err == nil {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

func (m *Manager) setClaudePinned(id string, on bool) error {
	claudeAnnMu.Lock()
	defer claudeAnnMu.Unlock()
	all := map[string]claudeAnn{}
	if b, err := os.ReadFile(m.claudeAnnFile()); err == nil {
		_ = json.Unmarshal(b, &all)
	}
	a := all[id]
	a.Pinned = on
	if a == (claudeAnn{}) {
		delete(all, id)
	} else {
		all[id] = a
	}
	b, _ := json.Marshal(all)
	return writeFileAtomic(m.claudeAnnFile(), b)
}

func (m *Manager) claudeHistory() []HistEntry {
	if _, err := os.Stat(filepath.Join(m.claudeDir(), "projects")); err != nil {
		return nil
	}
	ann := m.claudeAnnotations()
	var out []HistEntry
	for _, si := range claude.ListSessions(m.claudeDir()) {
		out = append(out, HistEntry{ID: si.ID, Agent: "claude", Title: si.Title, Cwd: si.Cwd, UpdatedAt: si.Updated,
			Archived: si.Archived, Pinned: ann[si.ID].Pinned})
	}
	return out
}

// ---- live state ----

func (m *Manager) claudeLive(s *Session) *Live {
	s.mu.Lock()
	tasks := append([]claude.Task(nil), s.tasks...)
	running := s.proc != nil
	s.mu.Unlock()
	live := &Live{Running: running, Tasks: []Task{}, Artifacts: []ArtifactInfo{}, Subagents: []Subagent{}, Messages: []AgentMessage{}}
	for _, t := range tasks {
		upd := t.Start
		if t.End > upd {
			upd = t.End
		}
		switch t.Type {
		case "local_agent", "remote_agent", "in_process_teammate", "local_workflow":
			role := firstNonEmpty(t.Description, t.SubagentType, "Subagent")
			if t.Progress != "" && t.Status == "running" {
				role += " · " + t.Progress
			}
			live.Subagents = append(live.Subagents, Subagent{ID: t.ID, Role: role, Type: t.SubagentType, State: strings.ToUpper(t.Status), Updated: upd})
		default:
			if t.Status == "running" && t.Background {
				live.Tasks = append(live.Tasks, Task{ID: t.ID, Tool: "Bash", Summary: t.Description, Description: t.Progress, Start: t.Start, Log: t.OutputFile})
			} else if t.Status != "running" && t.Background {
				live.Messages = append(live.Messages, AgentMessage{ID: t.ID, Sender: "Background task", Title: t.Description,
					Content: firstNonEmpty(t.Summary, t.Description+" "+t.Status), Ts: upd, Read: true})
			}
		}
	}
	for _, a := range m.claudeArtifacts(s) {
		live.Artifacts = append(live.Artifacts, a)
	}
	return live
}

// claudeArtifacts lists the plans a Claude session proposed.
func (m *Manager) claudeArtifacts(s *Session) []ArtifactInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	pending := map[string]bool{}
	for _, p := range s.perms {
		if p.kind == "plan" {
			if it := s.tr.Get(p.itemID); it != nil && it.Perm != nil {
				var in struct {
					PlanFilePath string `json:"planFilePath"`
				}
				_ = json.Unmarshal(it.Perm.Input, &in)
				pending[in.PlanFilePath] = true
			}
		}
	}
	seen := map[string]bool{}
	var out []ArtifactInfo
	for i := len(s.tr.Items) - 1; i >= 0 && len(out) < 20; i-- {
		it := s.tr.Items[i]
		if it.Kind != "tool" || it.Tool == nil || it.Tool.Name != "ExitPlanMode" {
			continue
		}
		var in struct {
			PlanFilePath string `json:"planFilePath"`
		}
		_ = json.Unmarshal(it.Tool.Input, &in)
		p := in.PlanFilePath
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		review := ""
		if it.Tool.Status == "completed" {
			review = "APPROVED"
		}
		out = append(out, ArtifactInfo{Name: filepath.Base(p), Path: p, URI: "file://" + p, Kind: "markdown", Summary: "Plan",
			Size: st.Size(), Updated: st.ModTime().UnixMilli(), RequestFeedback: pending[p], UserFacing: true, Review: review})
	}
	return out
}

func (m *Manager) claudeTaskOutput(s *Session, taskID string, offset int64) (map[string]any, error) {
	if strings.ContainsAny(taskID, "/\\") || strings.Contains(taskID, "..") {
		return nil, errors.New("bad task id")
	}
	s.mu.Lock()
	var path string
	running := false
	for _, t := range s.tasks {
		if t.ID == taskID {
			path = t.OutputFile
			running = t.Status == "running"
		}
	}
	a, _ := s.proc.(*claude.Agent)
	s.mu.Unlock()
	if path == "" && a != nil {
		path = a.TaskOutputPath(taskID)
	}
	if path == "" {
		return nil, errors.New("no output recorded for this task")
	}
	f, err := os.Open(path)
	if err != nil {
		if running {
			return map[string]any{"data": "", "offset": 0, "next": 0, "size": 0, "running": true}, nil
		}
		return nil, err
	}
	defer f.Close()
	st, _ := f.Stat()
	if offset < 0 || offset > st.Size() {
		offset = 0
	}
	if st.Size()-offset > 256<<10 && offset == 0 {
		offset = st.Size() - 256<<10
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	b, _ := io.ReadAll(io.LimitReader(f, 256<<10))
	return map[string]any{"data": string(b), "offset": offset, "next": offset + int64(len(b)), "size": st.Size(), "running": running}, nil
}

func (m *Manager) claudeSubagent(s *Session, subID string, max int) ([]SubStep, error) {
	s.mu.Lock()
	id, cwd := s.meta.ID, s.meta.Cwd
	s.mu.Unlock()
	if strings.ContainsAny(subID, "/\\") || strings.Contains(subID, "..") {
		return nil, errors.New("bad subagent id")
	}
	path, err := m.claudeTranscript(id, cwd)
	if err != nil {
		return nil, err
	}
	steps, err := claude.SubagentSteps(claude.SubagentPath(path, subID), max)
	if err != nil {
		return nil, err
	}
	out := make([]SubStep, 0, len(steps))
	for i, st := range steps {
		ss := SubStep{Index: i, Type: st.Type, Status: "done", Ts: st.Ts, Text: st.Text, Thinking: st.Thinking, Model: st.Model}
		for _, t := range st.Tools {
			ss.Tools = append(ss.Tools, SubTool{Name: t.Name, Args: t.Args})
		}
		out = append(out, ss)
	}
	return out, nil
}

// ---- control requests ----

var claudeControls = map[string]bool{
	"get_context_usage": true, "get_usage": true, "get_session_cost": true, "mcp_status": true, "mcp_toggle": true,
	"mcp_reconnect": true, "side_question": true, "rename_session": true, "list_models": true, "get_settings": true,
	"background_tasks": true, "get_task_output": true, "stop_task": true, "rewind_files": true, "generate_session_title": true,
	"reload_plugins": true, "reload_skills": true, "get_hooks_listing": true, "list_permission_rules": true,
	"set_max_thinking_tokens": true, "apply_flag_settings": true, "file_suggestions": true, "get_plan": true,
	"get_workspace_diff": true, "get_binary_version": true,
}

// ClaudeControl sends a control request to a Claude session, starting its
// process if needed.
func (m *Manager) ClaudeControl(ctx context.Context, id, subtype string, params map[string]any) (json.RawMessage, error) {
	if !claudeControls[subtype] {
		return nil, fmt.Errorf("%s is not available", subtype)
	}
	s := m.Get(id)
	if s == nil {
		var err error
		if s, err = m.Open(ctx, id); err != nil {
			return nil, err
		}
	}
	if !s.isClaudeSafe() {
		return nil, errors.New("not a Claude Code session")
	}
	if err := s.ensureLive(ctx, ""); err != nil {
		return nil, err
	}
	s.mu.Lock()
	a, _ := s.proc.(*claude.Agent)
	s.mu.Unlock()
	if a == nil {
		return nil, errors.New("Claude Code is not running for this session")
	}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	raw, err := a.Control(cctx, subtype, params)
	if err != nil {
		return nil, err
	}
	if subtype == "get_context_usage" {
		var v map[string]any
		if json.Unmarshal(raw, &v) == nil {
			delete(v, "gridRows") // a visual grid for the terminal; large
			raw, _ = json.Marshal(v)
		}
	}
	s.mu.Lock()
	s.lastActivity = time.Now()
	s.mu.Unlock()
	return raw, nil
}

func (s *Session) isClaudeSafe() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.isClaude()
}

// ---- conversation operations ----

func (m *Manager) claudePath(s *Session) (string, error) {
	s.mu.Lock()
	id, cwd, forkOf := s.meta.ID, s.meta.Cwd, s.meta.ForkOf
	s.mu.Unlock()
	p, err := m.claudeTranscript(id, cwd)
	if err != nil && forkOf != "" {
		return m.claudeTranscript(forkOf, cwd)
	}
	if err != nil {
		return "", errors.New("Claude hasn't saved this conversation yet")
	}
	return p, nil
}

// claudeCut finds where a conversation is cut for an item: for a user message,
// the entry before it (and the message text); for anything else the entry
// before the next user message ("" = the end).
func (m *Manager) claudeCut(s *Session, itemID string) (resumeAt, prefill string, idx int, err error) {
	path, err := m.claudePath(s)
	if err != nil {
		return "", "", -1, err
	}
	s.mu.Lock()
	pos := -1
	for i, it := range s.tr.Items {
		if it.ID == itemID {
			pos = i
		}
	}
	if pos < 0 {
		s.mu.Unlock()
		return "", "", -1, errors.New("message not found in transcript")
	}
	target := -1
	if s.tr.Items[pos].Kind == "user" {
		target = pos
	} else {
		for i := pos + 1; i < len(s.tr.Items); i++ {
			if it := s.tr.Items[i]; it.Kind == "user" && it.State != "queued" {
				target = i
				break
			}
		}
	}
	if target < 0 {
		n := len(s.tr.Items)
		s.mu.Unlock()
		return "", "", n, nil // the whole conversation
	}
	it := s.tr.Items[target]
	ref, text := it.Ref, it.Text
	s.mu.Unlock()
	if ref == "" {
		return "", text, target, errors.New("this message can't be matched to Claude's transcript; reload the session and try again")
	}
	e, err := claude.FindEntry(path, ref)
	if err != nil {
		return "", text, target, errors.New("this message isn't in Claude's transcript (yet)")
	}
	if e.Type == "user" {
		if p := userParent(e); p != "" {
			return p, text, target, nil
		}
		return "", text, target, errFirstMessage
	}
	// A message sent mid-turn is stored as an attachment; cut before it.
	if p := userParent(e); p != "" {
		return p, text, target, nil
	}
	return "", text, target, errFirstMessage
}

var errFirstMessage = errors.New("this is the first message")

func userParent(e *claude.Entry) string {
	if e.ParentUUID != nil {
		return *e.ParentUUID
	}
	return e.LogicalParentUUID
}

func (m *Manager) forkClaude(ctx context.Context, src *Session, itemID string) (*Session, string, error) {
	resumeAt, prefill, idx := "", "", -1
	if itemID != "" {
		var err error
		resumeAt, prefill, idx, err = m.claudeCut(src, itemID)
		if errors.Is(err, errFirstMessage) {
			return nil, prefill, errors.New("this is the first message; start a new session instead")
		}
		if err != nil {
			return nil, prefill, err
		}
	} else if _, err := m.claudePath(src); err != nil {
		return nil, "", err
	}
	src.mu.Lock()
	base := src.meta
	parent := base.ID
	if base.ForkOf != "" && !m.claudeTranscriptExists(base.ID, base.Cwd) {
		parent = base.ForkOf // forking a fork that was never continued
		if resumeAt == "" {
			resumeAt = base.ResumeAt
		}
	}
	if idx < 0 || idx > len(src.tr.Items) {
		idx = len(src.tr.Items)
	}
	var items []*Item
	for _, it := range src.tr.Items[:idx] {
		if it.Kind == "user" && it.State == "queued" {
			continue
		}
		c := cloneItem(it)
		if c.Kind == "perm" && c.State == "pending" {
			c.State = "cancelled"
		}
		items = append(items, c)
	}
	src.mu.Unlock()
	title := "Fork: " + firstNonEmpty(base.Title, "conversation")
	ts := now()
	meta := SessionMeta{ID: claude.NewUUID(), Agent: "claude", Cwd: base.Cwd, AddDirs: base.AddDirs, SkipPerms: base.SkipPerms,
		AgentName: base.AgentName, ExtraArgs: base.ExtraArgs, Title: title, TitleLocked: true, CreatedAt: ts, UpdatedAt: ts,
		Origin: "fork", ForkedFrom: base.ID, ForkOf: parent, ResumeAt: resumeAt, WantModel: base.WantModel, WantMode: base.WantMode,
		WantThought: base.WantThought, Models: base.Models, Modes: base.Modes, ConfigOptions: base.ConfigOptions, Commands: base.Commands,
		Usage: base.Usage}
	ns := newSession(m, meta)
	ns.tr.LoadItems(items, 1)
	ns.tr.Seq = 1
	m.mu.Lock()
	m.sessions[meta.ID] = ns
	m.mu.Unlock()
	m.saveSession(ns)
	m.emitSummary(ns)
	return ns, prefill, nil
}

func (m *Manager) revertPreviewClaude(ctx context.Context, s *Session, itemID string) (json.RawMessage, int, error) {
	s.mu.Lock()
	var ref string
	for _, it := range s.tr.Items {
		if it.ID == itemID && it.Kind == "user" {
			ref = it.Ref
		}
	}
	s.mu.Unlock()
	if ref == "" {
		_, _, _, err := m.claudeCut(s, itemID)
		if err == nil {
			err = errors.New("pick one of your messages to see what rewinding to it would undo")
		}
		return nil, 0, err
	}
	raw, err := m.ClaudeControl(ctx, s.ID(), "rewind_files", map[string]any{"user_message_id": ref, "dry_run": true})
	return raw, 0, err
}

func (m *Manager) rewindClaude(ctx context.Context, s *Session, itemID string, conversationOnly bool) (string, error) {
	resumeAt, prefill, idx, err := m.claudeCut(s, itemID)
	first := errors.Is(err, errFirstMessage)
	if err != nil && !first {
		return prefill, err
	}
	s.mu.Lock()
	var ref string
	if idx >= 0 && idx < len(s.tr.Items) {
		ref = s.tr.Items[idx].Ref
	}
	id, cwd := s.meta.ID, s.meta.Cwd
	s.mu.Unlock()
	filesNote := ""
	if !conversationOnly && ref != "" {
		raw, err := m.ClaudeControl(ctx, id, "rewind_files", map[string]any{"user_message_id": ref})
		if err != nil {
			return prefill, fmt.Errorf("restore files: %w", err)
		}
		var r struct {
			CanRewind    *bool    `json:"canRewind"`
			Error        string   `json:"error"`
			FilesChanged []string `json:"filesChanged"`
		}
		_ = json.Unmarshal(raw, &r)
		if r.Error != "" && (r.CanRewind == nil || !*r.CanRewind) {
			return prefill, fmt.Errorf("can't restore files: %s", r.Error)
		}
		filesNote = fmt.Sprintf(" and %d file(s)", len(r.FilesChanged))
		if len(r.FilesChanged) == 0 {
			filesNote = " (no file changes to undo)"
		}
	}
	s.Hibernate()
	if first {
		// Nothing precedes the first message: set the transcript aside and
		// start the conversation over under the same id.
		if path, err := m.claudeTranscript(id, cwd); err == nil {
			if err := os.Rename(path, path+fmt.Sprintf(".rewound-%d", time.Now().Unix())); err != nil {
				return prefill, err
			}
		}
	}
	s.mu.Lock()
	if idx >= 0 && idx <= len(s.tr.Items) {
		keep := make([]*Item, 0, idx)
		for _, it := range s.tr.Items[:idx] {
			keep = append(keep, it)
		}
		s.queueOpsLocked(s.tr.ResetWith(keep))
	}
	s.meta.ResumeAt = resumeAt
	if first {
		s.meta.ResumeAt = ""
	}
	s.meta.NeedsReload = false
	s.meta.Attention = ""
	s.queueOpsLocked(s.tr.AddNotice("info", "Rewound conversation"+filesNote+"."))
	s.mu.Unlock()
	s.emit()
	m.saveSession(s)
	return prefill, nil
}

func (m *Manager) renameClaude(ctx context.Context, s *Session, title string) error {
	s.mu.Lock()
	a, _ := s.proc.(*claude.Agent)
	id := s.meta.ID
	s.mu.Unlock()
	if a != nil {
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if _, err := a.Control(cctx, "rename_session", map[string]any{"title": title}); err == nil {
			return nil
		}
	}
	path, err := m.claudePath(s)
	if err != nil {
		return nil // nothing on disk yet; the daemon keeps the title
	}
	if strings.HasSuffix(path, id+".jsonl") {
		return claude.AppendTitle(path, id, title)
	}
	return nil
}

// exportMarkdown renders a transcript as Markdown.
func exportMarkdown(title string, items []*Item) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", firstNonEmpty(title, "Conversation"))
	for _, it := range items {
		switch it.Kind {
		case "user":
			fmt.Fprintf(&b, "## You\n\n%s\n\n", strings.TrimSpace(it.Text))
		case "agent":
			fmt.Fprintf(&b, "%s\n\n", strings.TrimSpace(it.Text))
		case "tool":
			if it.Tool != nil {
				fmt.Fprintf(&b, "> **%s** %s\n\n", firstNonEmpty(it.Tool.Name, it.Tool.Kind, "tool"), strings.ReplaceAll(it.Tool.Title, "\n", " "))
			}
		case "plan":
			var entries []struct {
				Content string `json:"content"`
				Status  string `json:"status"`
			}
			_ = json.Unmarshal(it.Plan, &entries)
			for _, e := range entries {
				mark := " "
				if e.Status == "completed" {
					mark = "x"
				}
				fmt.Fprintf(&b, "- [%s] %s\n", mark, e.Content)
			}
			b.WriteString("\n")
		case "notice":
			fmt.Fprintf(&b, "_%s_\n\n", it.Text)
		}
	}
	return b.String()
}

// claudeChanges summarizes files the agent edited, from its tool calls.
func claudeChanges(items []*Item) []map[string]any {
	type change struct {
		path  string
		edits int
		ts    int64
	}
	byPath := map[string]*change{}
	var order []string
	for _, it := range items {
		if it.Kind != "tool" || it.Tool == nil || it.Tool.Status != "completed" {
			continue
		}
		switch it.Tool.Name {
		case "Write", "Edit", "MultiEdit", "NotebookEdit":
		default:
			continue
		}
		var in struct {
			FilePath     string `json:"file_path"`
			NotebookPath string `json:"notebook_path"`
		}
		_ = json.Unmarshal(it.Tool.Input, &in)
		p := firstNonEmpty(in.FilePath, in.NotebookPath)
		if p == "" {
			continue
		}
		c := byPath[p]
		if c == nil {
			c = &change{path: p}
			byPath[p] = c
			order = append(order, p)
		}
		c.edits++
		c.ts = it.Ts
	}
	sort.SliceStable(order, func(i, j int) bool { return byPath[order[i]].ts > byPath[order[j]].ts })
	out := []map[string]any{}
	for _, p := range order {
		c := byPath[p]
		out = append(out, map[string]any{"path": p, "uri": "file://" + p, "edits": c.edits, "updated": c.ts})
	}
	return out
}

func logIf(err error, format string, args ...any) {
	if err != nil {
		log.Printf(format+": %v", append(args, err)...)
	}
}

// openClaude adopts a Claude conversation from history. Its transcript is
// read from disk; the claude process starts with the first message.
func (m *Manager) openClaude(h *HistEntry, flags *Flags) (*Session, error) {
	cwd := h.Cwd
	if st, err := os.Stat(cwd); cwd == "" || err != nil || !st.IsDir() {
		cwd = m.cfg.HomeDir
	}
	meta := SessionMeta{ID: h.ID, Agent: "claude", Cwd: cwd, SkipPerms: m.cfg.DefaultSkip, Title: firstNonEmpty(h.Title, "Conversation"),
		CreatedAt: h.UpdatedAt, UpdatedAt: h.UpdatedAt, Origin: "history", Archived: h.Archived, Pinned: h.Pinned}
	if flags != nil {
		// An existing conversation keeps Claude's own defaults for model,
		// effort and mode; process options (permissions, dirs…) apply.
		f := *flags
		f.Model, f.Mode, f.Thought, f.AgentName = "", "", "", ""
		m.applyFlags(&meta, &f)
	}
	s := newSession(m, meta)
	m.mu.Lock()
	if existing := m.sessions[h.ID]; existing != nil {
		m.mu.Unlock()
		return existing, nil
	}
	m.sessions[h.ID] = s
	m.mu.Unlock()
	if err := s.replayClaude(); err != nil {
		log.Printf("open %s: %v", h.ID, err)
		s.mu.Lock()
		s.queueOpsLocked(s.tr.AddNotice("error", "Could not read the conversation: "+err.Error()))
		s.mu.Unlock()
	}
	m.saveSession(s)
	m.emitSummary(s)
	return s, nil
}

// isClaudeID reports whether id is a Claude Code conversation.
func (m *Manager) isClaudeID(id string) bool {
	if s := m.Get(id); s != nil {
		return s.isClaudeSafe()
	}
	return claude.IsSessionID(id) && m.claudeTranscriptExists(id, "")
}

func (m *Manager) setFlagClaude(id, flag string, on bool) error {
	cwd := ""
	if s := m.Get(id); s != nil {
		s.mu.Lock()
		cwd = s.meta.Cwd
		s.mu.Unlock()
	}
	switch flag {
	case "pinned":
		return m.setClaudePinned(id, on)
	case "archived":
		path, err := m.claudeTranscript(id, cwd)
		if err != nil {
			return errors.New("Claude hasn't saved this conversation yet")
		}
		return claude.SetArchived(path, on)
	}
	return fmt.Errorf("unknown flag %q", flag)
}

func (m *Manager) deleteClaude(id string) error {
	if live := m.claudeLiveElsewhere(id); live != nil {
		return fmt.Errorf("this conversation is open in another Claude Code process (pid %d); close it there first", live.Pid)
	}
	cwd := ""
	if s := m.Get(id); s != nil {
		s.mu.Lock()
		cwd = s.meta.Cwd
		s.mu.Unlock()
	}
	path, pathErr := m.claudeTranscript(id, cwd)
	if s := m.Get(id); s != nil {
		s.Hibernate()
		s.mu.Lock()
		s.removed = true
		s.mu.Unlock()
		m.mu.Lock()
		delete(m.sessions, id)
		m.mu.Unlock()
		_ = os.Remove(m.sessionFile(id))
		_ = os.RemoveAll(m.attachmentDir(id))
	}
	if pathErr == nil {
		if err := claude.DeleteSession(path); err != nil {
			return err
		}
	}
	_ = m.setClaudePinned(id, false)
	m.invalidateHistory()
	m.hub.Broadcast("session.removed", map[string]any{"id": id})
	return nil
}

// Comment delivers review comments. For a Claude plan waiting for approval
// they answer the plan itself: "proceed" approves it and anything else sends
// it back with the feedback (Claude stays in plan mode and revises it).
func (s *Session) Comment(cs []Comment, text, delivery string) (map[string]any, error) {
	if s.isClaudeSafe() {
		s.mu.Lock()
		planReq := ""
		for id, p := range s.perms {
			if p.kind == "plan" {
				planReq = id
			}
		}
		skip := s.meta.SkipPerms
		s.mu.Unlock()
		if planReq != "" {
			approve := len(cs) > 0
			for _, c := range cs {
				if !c.Approve || strings.TrimSpace(c.Comment) != "" {
					approve = false
				}
			}
			if approve {
				opt := "plan_accept_edits"
				if skip {
					opt = "plan_bypass"
				}
				return map[string]any{"answered": "approved"}, s.RespondPermissionWith(planReq, opt, nil)
			}
			return map[string]any{"answered": "feedback"}, s.RespondPermissionWith(planReq, "deny", &PermAnswer{Message: BuildCommentPrompt(cs, text)})
		}
	}
	return s.Prompt(PromptInput{Text: BuildCommentPrompt(cs, text), Delivery: delivery})
}

// ClaudeQuery runs a read-only control request against any running Claude
// session, or a short-lived claude process when none is running.
func (m *Manager) ClaudeQuery(ctx context.Context, subtype string, params map[string]any) (json.RawMessage, error) {
	switch subtype {
	case "get_usage", "mcp_status", "list_models", "get_settings", "get_hooks_listing", "list_permission_rules", "get_binary_version":
	default:
		return nil, fmt.Errorf("%s is not available", subtype)
	}
	for _, s := range m.all() {
		s.mu.Lock()
		a, _ := s.proc.(*claude.Agent)
		s.mu.Unlock()
		if a != nil && a.Alive() && a.SessionID() != "" {
			cctx, cancel := context.WithTimeout(ctx, time.Minute)
			raw, err := a.Control(cctx, subtype, params)
			cancel()
			if err == nil {
				return raw, nil
			}
		}
	}
	cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	p, err := claude.StartProc(claude.ProcOptions{Bin: m.claudeBin(), Cwd: m.cfg.HomeDir, Env: m.claudeEnv(),
		Args: []string{"--no-session-persistence"}, LogFile: filepath.Join(m.cfg.StateDir, "logs", "claude-query.log")})
	if err != nil {
		return nil, err
	}
	defer p.Close(3 * time.Second)
	if _, err := p.Control(cctx, "initialize", nil); err != nil {
		return nil, fmt.Errorf("claude did not start: %w", err)
	}
	return p.Control(cctx, subtype, params)
}

// ClaudeLiveSessions lists Claude Code processes running outside agentc.
func (m *Manager) ClaudeLiveSessions() []claude.LiveSession {
	own := m.ownPids()
	var out []claude.LiveSession
	for _, ls := range claude.LiveSessions(m.claudeDir()) {
		if !own[ls.Pid] {
			out = append(out, ls)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out
}

// ClaudeSettingsPath is the user's Claude Code settings file.
func (m *Manager) ClaudeSettingsPath() string { return filepath.Join(m.claudeDir(), "settings.json") }

// ClaudeOpenElsewhere reports whether another Claude Code process (outside
// agentc) has the conversation open.
func (m *Manager) ClaudeOpenElsewhere(id string) bool { return m.claudeLiveElsewhere(id) != nil }

var (
	catMu sync.Mutex
	cat   map[string]json.RawMessage
	catAt time.Time
)

// ClaudeCatalog returns what Claude Code offers on this computer: models,
// custom agents, slash commands and output styles (cached for 10 minutes).
func (m *Manager) ClaudeCatalog(ctx context.Context, force bool) (map[string]json.RawMessage, error) {
	catMu.Lock()
	if !force && cat != nil && time.Since(catAt) < 10*time.Minute {
		c := cat
		catMu.Unlock()
		return c, nil
	}
	catMu.Unlock()
	cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	p, err := claude.StartProc(claude.ProcOptions{Bin: m.claudeBin(), Cwd: m.cfg.HomeDir, Env: m.claudeEnv(),
		Args: []string{"--no-session-persistence"}, LogFile: filepath.Join(m.cfg.StateDir, "logs", "claude-query.log")})
	if err != nil {
		return nil, err
	}
	defer p.Close(3 * time.Second)
	raw, err := p.Control(cctx, "initialize", nil)
	if err != nil {
		return nil, fmt.Errorf("claude did not start: %w", err)
	}
	var ir map[string]json.RawMessage
	if err := json.Unmarshal(raw, &ir); err != nil {
		return nil, err
	}
	out := map[string]json.RawMessage{}
	for _, k := range []string{"models", "agents", "commands", "output_style", "available_output_styles", "claude_code_version"} {
		if v, ok := ir[k]; ok {
			out[k] = v
		}
	}
	if acc, ok := ir["account"]; ok {
		var am map[string]any
		if json.Unmarshal(acc, &am) == nil {
			delete(am, "email")
			out["account"], _ = json.Marshal(am)
		}
	}
	catMu.Lock()
	cat, catAt = out, time.Now()
	catMu.Unlock()
	return out, nil
}

// claudeFileDiffs lists the files a Claude session edited with their content
// before Claude's first edit and now (the shape agy's file diffs use).
func (m *Manager) claudeFileDiffs(s *Session) (json.RawMessage, error) {
	path, err := m.claudePath(s)
	if err != nil {
		return json.Marshal([]any{})
	}
	s.mu.Lock()
	cwd := s.meta.Cwd
	s.mu.Unlock()
	changes, err := claude.FileChanges(path, m.claudeDir(), cwd)
	if err != nil {
		return nil, err
	}
	const limit = 200 * 1024
	out := []map[string]any{}
	for _, c := range changes {
		if strings.HasPrefix(c.Path, filepath.Join(m.claudeDir(), "plans")+"/") {
			continue // plan files are shown as artifacts
		}
		d := map[string]any{"uri": "file://" + c.Path, "hasModelEdited": true}
		orig := ""
		if c.Original != nil {
			orig = *c.Original
		}
		mod := ""
		if b, err := os.ReadFile(c.Path); err == nil {
			mod = string(b)
		}
		if orig == mod {
			continue // edited and then restored
		}
		if len(orig) > limit {
			orig, d["truncated"] = orig[:limit], true
		}
		if len(mod) > limit {
			mod, d["truncated"] = mod[:limit], true
		}
		d["originalContent"], d["modifiedContent"] = orig, mod
		out = append(out, d)
	}
	return json.Marshal(out)
}

func (s *Session) firstUserTextLocked() string {
	for _, it := range s.tr.Items {
		if it.Kind == "user" && strings.TrimSpace(it.Text) != "" {
			t := it.Text
			if len(t) > 2000 {
				t = t[:2000]
			}
			return t
		}
	}
	return ""
}

// autoTitle names a new Claude session after its first message, the way
// Claude's own apps do, and saves the title in Claude's session store.
func (s *Session) autoTitle(desc string) {
	if strings.TrimSpace(desc) == "" {
		return
	}
	s.mu.Lock()
	a, _ := s.proc.(*claude.Agent)
	s.mu.Unlock()
	if a == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	raw, err := a.Control(ctx, "generate_session_title", map[string]any{"description": desc})
	if err != nil {
		log.Printf("session %s: title: %v", s.ID(), err)
		return
	}
	var r struct {
		Title string `json:"title"`
	}
	if json.Unmarshal(raw, &r) != nil || strings.TrimSpace(r.Title) == "" {
		return
	}
	title := strings.TrimSpace(r.Title)
	s.mu.Lock()
	if s.meta.TitleLocked || (s.meta.Title != "" && s.meta.Title != "New session") {
		s.mu.Unlock()
		return
	}
	s.meta.Title = title
	s.mu.Unlock()
	if _, err := a.Control(ctx, "rename_session", map[string]any{"title": title}); err != nil {
		log.Printf("session %s: save title: %v", s.ID(), err)
	}
	s.emit()
	s.m.saveSession(s)
	s.m.invalidateHistory()
}

// claudeStale reports whether Claude's transcript has prompts the daemon's
// copy doesn't (the conversation continued in a terminal). It clears a false
// NeedsReload (Claude also appends titles and bookkeeping to the file).
func (s *Session) claudeStale() bool {
	s.mu.Lock()
	need := s.meta.NeedsReload
	id, cwd := s.meta.ID, s.meta.Cwd
	lastRef := ""
	for i := len(s.tr.Items) - 1; i >= 0; i-- {
		if it := s.tr.Items[i]; it.Kind == "user" && it.State != "queued" && it.Ref != "" {
			lastRef = it.Ref
			break
		}
	}
	empty := len(s.tr.Items) == 0
	s.mu.Unlock()
	if !need {
		return false
	}
	path, err := s.m.claudeTranscript(id, cwd)
	if err != nil {
		return false
	}
	if empty || lastRef == "" {
		return true
	}
	n, err := claude.PromptsAfter(path, lastRef)
	if err != nil || n < 0 {
		return true
	}
	if n == 0 {
		s.mu.Lock()
		s.meta.NeedsReload = false
		s.mu.Unlock()
		return false
	}
	return true
}

var (
	liveCacheMu sync.Mutex
	liveCache   map[string]claude.LiveSession
	liveCacheAt time.Time
)

// claudeLiveCached is claudeLive for hot paths (session summaries): Claude
// Code processes outside agentc, re-read at most every two seconds.
func (m *Manager) claudeLiveCached() map[string]claude.LiveSession {
	liveCacheMu.Lock()
	defer liveCacheMu.Unlock()
	if liveCache != nil && time.Since(liveCacheAt) < 2*time.Second {
		return liveCache
	}
	all := claude.LiveSessions(m.claudeDir())
	own := claude.OwnPids()
	for id, ls := range all {
		if own[ls.Pid] {
			delete(all, id)
		}
	}
	liveCache, liveCacheAt = all, time.Now()
	return all
}
