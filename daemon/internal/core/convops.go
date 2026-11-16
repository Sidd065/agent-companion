package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"agentc/internal/acp"
	"agentc/internal/lsapi"
)

// stepTarget maps a transcript item to a trajectory step index.
// For a user message it returns the step just before that message (so the
// conversation ends right before it) plus the message text; for any other
// item it returns the last step of that item's turn. Empty itemID = end.
func (s *Session) stepTarget(steps []lsapi.Step, itemID string) (int, string, error) {
	if len(steps) == 0 {
		return -1, "", errors.New("conversation has no steps yet")
	}
	if itemID == "" {
		return len(steps) - 1, "", nil
	}
	type us struct {
		idx  int
		text string
	}
	var userSteps []us
	for i, st := range steps {
		if st.Type != "CORTEX_STEP_TYPE_USER_INPUT" {
			continue
		}
		var ui struct {
			UserResponse string `json:"userResponse"`
			Items        []struct {
				Text string `json:"text"`
			} `json:"items"`
		}
		_ = json.Unmarshal(st.UserInput, &ui)
		t := ui.UserResponse
		if t == "" {
			for _, it := range ui.Items {
				t += it.Text
			}
		}
		userSteps = append(userSteps, us{i, normText(t)})
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Greedily align delivered user items with USER_INPUT steps by text.
	assigned := map[string]int{}
	j := 0
	for _, it := range s.tr.Items {
		if it.Kind != "user" || it.State == "queued" || it.State == "failed" {
			continue
		}
		want := normText(it.Text)
		for k := j; k < len(userSteps); k++ {
			have := userSteps[k].text
			if want == have || (want != "" && (strings.Contains(have, want) || strings.Contains(want, have) && have != "")) {
				assigned[it.ID] = userSteps[k].idx
				j = k + 1
				break
			}
		}
	}
	pos := -1
	for i, it := range s.tr.Items {
		if it.ID == itemID {
			pos = i
		}
	}
	if pos < 0 {
		return -1, "", errors.New("message not found in transcript")
	}
	target := s.tr.Items[pos]
	if target.Kind == "user" {
		st, ok := assigned[target.ID]
		if !ok {
			return -1, "", errors.New("couldn't match this message to agy's history; try reloading the session")
		}
		if st == 0 {
			return -1, target.Text, errors.New("this is the first message; start a new session instead")
		}
		return st - 1, target.Text, nil
	}
	for i := pos + 1; i < len(s.tr.Items); i++ {
		it := s.tr.Items[i]
		if it.Kind == "user" {
			if st, ok := assigned[it.ID]; ok {
				return st - 1, "", nil
			}
		}
	}
	return len(steps) - 1, "", nil
}

// Fork branches a conversation at an item (or at the end) into a new session.
func (m *Manager) Fork(ctx context.Context, id, itemID string) (*Session, string, error) {
	src := m.Get(id)
	if src == nil {
		var err error
		if src, err = m.Open(ctx, id); err != nil {
			return nil, "", err
		}
	}
	ls, err := m.LS(ctx, id)
	if err != nil {
		return nil, "", err
	}
	steps, err := ls.Steps(ctx, id)
	if err != nil {
		return nil, "", fmt.Errorf("read history: %w", err)
	}
	idx, prefill, err := src.stepTarget(steps, itemID)
	if err != nil {
		return nil, prefill, err
	}
	// A fork can't end on a user message; walk back to the previous step.
	for idx > 0 && steps[idx].Type == "CORTEX_STEP_TYPE_USER_INPUT" {
		idx--
	}
	var r struct {
		NewCascadeID string   `json:"newCascadeId"`
		Warnings     []string `json:"warnings"`
	}
	if err := ls.Call(ctx, "ForkConversation", map[string]any{"sourceCascadeId": id, "forkAtStepIndex": idx}, &r); err != nil {
		return nil, prefill, fmt.Errorf("fork: %w", err)
	}
	if r.NewCascadeID == "" {
		return nil, prefill, errors.New("agy did not return a forked conversation id")
	}
	src.mu.Lock()
	base := src.meta
	src.mu.Unlock()
	title := "Fork: " + firstNonEmpty(base.Title, "conversation")
	_ = ls.Call(ctx, "UpdateConversationAnnotations", map[string]any{"cascadeId": r.NewCascadeID,
		"annotations": map[string]any{"title": title}, "mergeAnnotations": true}, nil)
	m.invalidateHistory()
	ts := now()
	meta := SessionMeta{ID: r.NewCascadeID, Agent: firstNonEmpty(base.Agent, "agy"), Cwd: base.Cwd, AddDirs: base.AddDirs, SkipPerms: base.SkipPerms,
		Sandbox: base.Sandbox, AgentName: base.AgentName, NoTelemetry: base.NoTelemetry, ExtraArgs: base.ExtraArgs, Title: title, TitleLocked: true,
		CreatedAt: ts, UpdatedAt: ts, Origin: "fork", ForkedFrom: id, WantModel: base.WantModel, WantMode: base.WantMode,
		WantThought: base.WantThought, Models: base.Models, Modes: base.Modes, ConfigOptions: base.ConfigOptions, Commands: base.Commands,
		NeedsReload: true}
	ns := newSession(m, meta)
	m.mu.Lock()
	m.sessions[r.NewCascadeID] = ns
	m.mu.Unlock()
	m.emitSummary(ns)
	if err := ns.ensureLive(ctx, "load"); err != nil {
		return ns, prefill, err
	}
	m.enforceMaxLive()
	return ns, prefill, nil
}

// RevertPreview lists the file changes a rewind would undo.
func (m *Manager) RevertPreview(ctx context.Context, id, itemID string) (json.RawMessage, int, error) {
	s := m.Get(id)
	if s == nil {
		return nil, 0, fmt.Errorf("unknown session %s", id)
	}
	ls, err := m.LS(ctx, id)
	if err != nil {
		return nil, 0, err
	}
	steps, err := ls.Steps(ctx, id)
	if err != nil {
		return nil, 0, err
	}
	idx, _, err := s.stepTarget(steps, itemID)
	if err != nil {
		return nil, 0, err
	}
	raw, err := ls.CallRaw(ctx, "GetRevertPreview", map[string]any{"cascadeId": id, "stepIndex": idx})
	return raw, idx, err
}

// Rewind rolls a conversation (and optionally the workspace files) back to
// just before a user message, or to the end of an agent turn.
func (m *Manager) Rewind(ctx context.Context, id, itemID string, conversationOnly bool) (string, error) {
	s := m.Get(id)
	if s == nil {
		return "", fmt.Errorf("unknown session %s", id)
	}
	s.mu.Lock()
	busy := s.inflight > 0
	s.mu.Unlock()
	if busy {
		return "", errors.New("stop the current turn before rewinding")
	}
	if err := s.ensureLive(ctx, ""); err != nil {
		return "", err
	}
	ls, err := m.LS(ctx, id)
	if err != nil {
		return "", err
	}
	steps, err := ls.Steps(ctx, id)
	if err != nil {
		return "", err
	}
	idx, prefill, err := s.stepTarget(steps, itemID)
	if err != nil {
		return prefill, err
	}
	if err := ls.Call(ctx, "RevertToCascadeStep", map[string]any{"cascadeId": id, "stepIndex": idx, "conversationOnly": conversationOnly}, nil); err != nil {
		return prefill, fmt.Errorf("rewind: %w", err)
	}
	if err := s.reloadLive(ctx); err != nil {
		// Fall back to a fresh process with a full replay.
		s.Hibernate()
		s.mu.Lock()
		s.meta.NeedsReload = true
		s.mu.Unlock()
		if err := s.ensureLive(ctx, "load"); err != nil {
			return prefill, err
		}
	}
	s.mu.Lock()
	s.queueOpsLocked(s.tr.AddNotice("info", "Rewound conversation"+map[bool]string{true: "", false: " and files"}[conversationOnly]+"."))
	s.meta.Attention = ""
	s.mu.Unlock()
	s.emit()
	m.saveSession(s)
	return prefill, nil
}

// reloadLive replays the conversation into the transcript using the running process.
func (s *Session) reloadLive(ctx context.Context) error {
	s.mu.Lock()
	p := s.proc
	if p == nil {
		s.mu.Unlock()
		return errors.New("not running")
	}
	id, cwd := s.meta.ID, s.meta.Cwd
	var keep []*Item
	for _, it := range s.tr.Items {
		if it.Kind == "user" && it.State == "queued" {
			keep = append(keep, it)
		}
	}
	s.queueOpsLocked(s.tr.Reset())
	s.mu.Unlock()
	var setup acp.SessionSetup
	cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	err := p.Conn.Call(cctx, "session/load", map[string]any{"sessionId": id, "cwd": cwd, "mcpServers": []any{}}, &setup)
	s.mu.Lock()
	for _, it := range keep {
		s.queueOpsLocked(s.tr.add(it))
	}
	if err == nil {
		if setup.Models != nil {
			s.meta.Models = setup.Models
		}
		if setup.Modes != nil {
			s.meta.Modes = setup.Modes
		}
		if len(setup.ConfigOptions) > 0 {
			s.meta.ConfigOptions = setup.ConfigOptions
		}
	}
	s.mu.Unlock()
	return err
}

// annotate merges conversation annotations (title/archived/pinned).
func (m *Manager) annotate(ctx context.Context, id string, ann map[string]any) error {
	ls, err := m.LS(ctx, id)
	if err != nil {
		return err
	}
	if err := ls.Call(ctx, "UpdateConversationAnnotations", map[string]any{"cascadeId": id, "annotations": ann, "mergeAnnotations": true}, nil); err != nil {
		return err
	}
	// proto3 merges ignore false values; verify and rewrite fully when needed.
	cur := m.readAnnotations(id)
	needFull := false
	if v, ok := ann["archived"].(bool); ok && cur.Archived != v {
		needFull = true
	}
	if v, ok := ann["pinned"].(bool); ok && cur.Pinned != v {
		needFull = true
	}
	if needFull {
		full := map[string]any{"title": cur.Title, "archived": cur.Archived, "pinned": cur.Pinned, "starred": cur.Starred}
		for k, v := range ann {
			full[k] = v
		}
		if err := ls.Call(ctx, "UpdateConversationAnnotations", map[string]any{"cascadeId": id, "annotations": full, "mergeAnnotations": false}, nil); err != nil {
			return err
		}
	}
	m.invalidateHistory()
	return nil
}

// Rename sets a conversation title.
func (m *Manager) Rename(ctx context.Context, id, title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return errors.New("title is empty")
	}
	if err := m.annotate(ctx, id, map[string]any{"title": title}); err != nil {
		return err
	}
	if s := m.Get(id); s != nil {
		s.mu.Lock()
		s.meta.Title, s.meta.TitleLocked = title, true
		s.mu.Unlock()
		s.emit()
		m.saveSession(s)
	}
	m.hub.Broadcast("sessions.invalidate", map[string]any{"id": id})
	return nil
}

// SetFlag archives/unarchives or pins/unpins a conversation.
func (m *Manager) SetFlag(ctx context.Context, id, flag string, on bool) error {
	if flag != "archived" && flag != "pinned" {
		return fmt.Errorf("unknown flag %q", flag)
	}
	ann := map[string]any{flag: on}
	if flag == "archived" && on {
		ann["archivalStatusTimestamp"] = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if err := m.annotate(ctx, id, ann); err != nil {
		return err
	}
	if s := m.Get(id); s != nil {
		s.mu.Lock()
		if flag == "archived" {
			s.meta.Archived = on
		} else {
			s.meta.Pinned = on
		}
		s.mu.Unlock()
		if flag == "archived" && on {
			s.Hibernate()
		}
		s.emit()
		m.saveSession(s)
	}
	m.hub.Broadcast("sessions.invalidate", map[string]any{"id": id})
	return nil
}

// Delete permanently deletes a conversation from agy.
func (m *Manager) Delete(ctx context.Context, id string) error {
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
	ls, err := m.UtilityLS(ctx)
	if err != nil {
		return err
	}
	if err := ls.Call(ctx, "DeleteCascadeTrajectory", map[string]any{"cascadeId": id}, nil); err != nil {
		return err
	}
	m.invalidateHistory()
	m.hub.Broadcast("session.removed", map[string]any{"id": id})
	return nil
}

// Export renders a conversation as Markdown.
func (m *Manager) Export(ctx context.Context, id string) (string, error) {
	ls, err := m.LS(ctx, id)
	if err != nil {
		return "", err
	}
	var r struct {
		Markdown string `json:"markdown"`
	}
	if err := ls.Call(ctx, "ConvertTrajectoryToMarkdown", map[string]any{"conversationId": id}, &r); err != nil {
		return "", err
	}
	return r.Markdown, nil
}

// AgentChanges returns files the agent touched with original/modified content.
func (m *Manager) AgentChanges(ctx context.Context, id string) (json.RawMessage, error) {
	ls, err := m.LS(ctx, id)
	if err != nil {
		return nil, err
	}
	var r struct {
		Diffs []map[string]any `json:"diffs"`
	}
	if err := ls.Call(ctx, "GetTrajectoryFileDiffs", map[string]any{"conversationId": id}, &r); err != nil {
		return nil, err
	}
	const limit = 200 * 1024
	for _, d := range r.Diffs {
		for _, k := range []string{"originalContent", "modifiedContent"} {
			if s, ok := d[k].(string); ok && len(s) > limit {
				d[k] = s[:limit]
				d["truncated"] = true
			}
		}
		delete(d, "originalContentHash")
	}
	b, err := json.Marshal(r.Diffs)
	return b, err
}

// UserStatus returns account, plan, credits and quota information.
func (m *Manager) UserStatus(ctx context.Context) (map[string]any, error) {
	ls, err := m.UtilityLS(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	if raw, err := ls.CallRaw(ctx, "GetUserStatus", map[string]any{}); err == nil {
		var v map[string]any
		_ = json.Unmarshal(raw, &v)
		if us, ok := v["userStatus"].(map[string]any); ok {
			delete(us, "cascadeModelConfigData")
			out["user"] = us
		}
	} else {
		out["userError"] = err.Error()
	}
	if raw, err := ls.CallRaw(ctx, "GetAuthStatus", map[string]any{}); err == nil {
		out["auth"] = json.RawMessage(raw)
	}
	if raw, err := ls.CallRaw(ctx, "RetrieveUserQuotaSummary", map[string]any{"forceRefresh": true}); err == nil {
		out["quota"] = json.RawMessage(raw)
	}
	return out, nil
}

// Artifact is a file in a conversation's brain directory.
type Artifact struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Size  int64  `json:"size"`
	Mtime int64  `json:"mtime"`
}

// Artifacts lists files the agent saved for a conversation (plans, tasks, media).
func (m *Manager) Artifacts(id string) ([]Artifact, error) {
	if strings.ContainsAny(id, "/\\") || strings.Contains(id, "..") {
		return nil, errors.New("bad id")
	}
	root := filepath.Join(m.cfg.AgyDataDir, "brain", id)
	var out []Artifact
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") && p != root {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		out = append(out, Artifact{Name: rel, Path: p, Size: info.Size(), Mtime: info.ModTime().UnixMilli()})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Mtime > out[j].Mtime })
	return out, nil
}

// GitDiff summarizes uncommitted changes in the repository containing dir.
func GitDiff(ctx context.Context, dir string) (map[string]any, error) {
	git := func(args ...string) (string, error) {
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(cctx, "git", append([]string{"-C", dir, "-c", "core.quotepath=off"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_PAGER=cat", "PAGER=cat", "LC_ALL=C")
		b, err := cmd.Output()
		return string(b), err
	}
	root, err := git("rev-parse", "--show-toplevel")
	if err != nil {
		return map[string]any{"repo": false}, nil
	}
	root = strings.TrimSpace(root)
	branch, _ := git("rev-parse", "--abbrev-ref", "HEAD")
	_, headErr := git("rev-parse", "--verify", "HEAD")
	base := "HEAD"
	if headErr != nil {
		base = "--cached" // no commits yet
	}
	diffArgs := []string{"diff", "--no-color", "--no-ext-diff", "-M"}
	numArgs := []string{"diff", "--numstat", "-M"}
	if base == "HEAD" {
		diffArgs = append(diffArgs, "HEAD")
		numArgs = append(numArgs, "HEAD")
	} else {
		diffArgs = append(diffArgs, "--cached")
		numArgs = append(numArgs, "--cached")
	}
	diff, _ := git(diffArgs...)
	nums, _ := git(numArgs...)
	status, _ := git("status", "--porcelain=v1", "-uall")
	type file struct {
		Path    string `json:"path"`
		Status  string `json:"status"`
		Added   int    `json:"added"`
		Removed int    `json:"removed"`
	}
	stats := map[string][2]int{}
	for _, line := range strings.Split(nums, "\n") {
		f := strings.SplitN(line, "\t", 3)
		if len(f) == 3 {
			a, _ := strconv.Atoi(f[0])
			r, _ := strconv.Atoi(f[1])
			p := f[2]
			if i := strings.Index(p, " => "); i >= 0 {
				p = strings.TrimSuffix(p[i+4:], "}")
			}
			stats[p] = [2]int{a, r}
		}
	}
	var files []file
	var untracked []string
	for _, line := range strings.Split(status, "\n") {
		if len(line) < 4 {
			continue
		}
		code, p := strings.TrimSpace(line[:2]), line[3:]
		if i := strings.Index(p, " -> "); i >= 0 {
			p = p[i+4:]
		}
		st := stats[p]
		files = append(files, file{Path: p, Status: code, Added: st[0], Removed: st[1]})
		if code == "??" {
			untracked = append(untracked, p)
		}
	}
	// Include untracked files as new-file diffs (bounded).
	var sb strings.Builder
	sb.WriteString(diff)
	for i, p := range untracked {
		if i >= 40 || sb.Len() > 2<<20 {
			break
		}
		full := filepath.Join(root, p)
		if st, err := os.Stat(full); err != nil || st.Size() > 256*1024 || st.IsDir() {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		out, _ := exec.CommandContext(cctx, "git", "-C", root, "diff", "--no-color", "--no-index", "--", "/dev/null", p).Output()
		cancel()
		sb.Write(out)
		for j := range files {
			if files[j].Path == p {
				files[j].Added = strings.Count(string(out), "\n+") - 1
			}
		}
	}
	text := sb.String()
	truncated := false
	if len(text) > 3<<20 {
		text = text[:3<<20]
		truncated = true
	}
	return map[string]any{"repo": true, "root": root, "branch": strings.TrimSpace(branch), "files": files, "diff": text, "truncated": truncated}, nil
}
