package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"agentc/internal/acp"
	"agentc/internal/acpproc"
	"agentc/internal/checkpoints"
	"agentc/internal/gitops"
	"agentc/internal/providers"
	"agentc/internal/scheduler"
	"agentc/internal/usage"
)

// SkillEntry describes a discovered skill from project or user directories.
type SkillEntry struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Path        string `json:"path"`
	Scope       string `json:"scope"` // project | user | agy | claude
}

// SearchHit represents one match from global search.
type SearchHit struct {
	Kind      string `json:"kind"` // session | message | file
	SessionID string `json:"sessionId,omitempty"`
	ProjectID string `json:"projectId,omitempty"`
	Title     string `json:"title"`
	Snippet   string `json:"snippet,omitempty"`
	Path      string `json:"path,omitempty"`
	UpdatedAt int64  `json:"updatedAt,omitempty"`
}

// ManagedProcess reports a live agent process managed by this daemon.
type ManagedProcess struct {
	SessionID string `json:"sessionId"`
	Title     string `json:"title"`
	Agent     string `json:"agent"`
	State     string `json:"state"`
	Pid       int    `json:"pid"`
	RSSKiB    int64  `json:"rssKiB"`
}

// HostHealth reports zero-spawn Linux memory, PSI pressure, load, and agent RSS.
type HostHealth struct {
	MemTotalKiB     int64            `json:"memTotalKiB"`
	MemAvailableKiB int64            `json:"memAvailableKiB"`
	MemUsedPct      float64          `json:"memUsedPct"`
	PSISomeAvg10    float64          `json:"psiSomeAvg10"`
	PSIFullAvg10    float64          `json:"psiFullAvg10"`
	LoadAvg1        float64          `json:"loadAvg1"`
	LoadAvg5        float64          `json:"loadAvg5"`
	Processes       []ManagedProcess `json:"processes"`
	CheckedAt       int64            `json:"checkedAt"`
}

func (m *Manager) isAgyID(id string) bool {
	if s := m.Get(id); s != nil {
		return s.isAgySafe()
	}
	if m.isClaudeID(id) || m.isCodexID(id) {
		return false
	}
	agyDir := m.cfg.AgyDataDir
	if agyDir == "" && m.cfg.HomeDir != "" {
		agyDir = filepath.Join(m.cfg.HomeDir, ".gemini", "antigravity")
	}
	if agyDir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(agyDir, "conversations", id+".db"))
	return err == nil
}

func (s *Session) isAgy() bool   { return s.meta.Agent == "" || s.meta.Agent == "agy" }
func (s *Session) isCodex() bool { return s.meta.Agent == "codex" }

func (s *Session) isAgySafe() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.isAgy()
}

func (m *Manager) providerConfig(id string) (providers.ProviderConfig, bool) {
	if m.Providers != nil {
		if p, ok := m.Providers.Get(id); ok {
			return p, true
		}
	}
	if id == "fakeacp" {
		if bin := os.Getenv("AGENTC_FAKE_ACP"); bin != "" {
			return providers.ProviderConfig{
				ID:      "fakeacp",
				Name:    "Fake ACP",
				Driver:  "acp",
				Enabled: true,
				Command: bin,
				Features: []string{
					"rename", "archive", "pin", "delete", "export", "changes",
					"terminal", "modes", "effort", "plans", "questions", "asides",
				},
			}, true
		}
	}
	return providers.ProviderConfig{}, false
}

// InvalidateAgentsCache clears the cached agent profile list after provider changes.
func (m *Manager) InvalidateAgentsCache() {
	m.agentsMu.Lock()
	m.agentsList = nil
	m.agentsAt = time.Time{}
	m.agentsMu.Unlock()
	m.hub.Broadcast("providers.changed", m.Agents())
}

// ---- Session settle / snooze / draft ----

// Settle marks a session settled (moved out of active inbox) or active again.
func (m *Manager) Settle(id string, settled bool) error {
	s := m.Get(id)
	if s == nil {
		return errors.New("unknown session")
	}
	s.mu.Lock()
	s.meta.Settled = settled
	if settled {
		s.meta.Attention = ""
	}
	s.meta.UpdatedAt = now()
	s.mu.Unlock()
	s.emit()
	m.saveSession(s)
	return nil
}

// Snooze defers a session until the given unix ms timestamp (0 clears snooze).
func (m *Manager) Snooze(id string, until int64) error {
	s := m.Get(id)
	if s == nil {
		return errors.New("unknown session")
	}
	s.mu.Lock()
	s.meta.SnoozedUntil = until
	s.meta.UpdatedAt = now()
	s.mu.Unlock()
	s.emit()
	m.saveSession(s)
	return nil
}

// SetDraft stores a synced unsent composer draft on the session.
func (m *Manager) SetDraft(id, draft string) error {
	s := m.Get(id)
	if s == nil {
		return errors.New("unknown session")
	}
	s.mu.Lock()
	s.meta.Draft = draft
	s.mu.Unlock()
	m.saveSession(s)
	return nil
}

// ---- Auto-title heuristic (for all providers) ----

func heuristicTitle(prompt string) string {
	s := strings.TrimSpace(prompt)
	if s == "" {
		return ""
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	s = strings.Join(strings.Fields(s), " ")
	runes := []rune(s)
	if len(runes) > 60 {
		s = strings.TrimSpace(string(runes[:57])) + "…"
	}
	return s
}

func (s *Session) maybeHeuristicTitleLocked(promptText string) {
	if s.meta.TitleLocked {
		return
	}
	if s.meta.Title != "" && s.meta.Title != "New session" && s.meta.Title != "Untitled conversation" {
		return
	}
	if t := heuristicTitle(promptText); t != "" {
		s.meta.Title = t
	}
}

// ---- Per-turn checkpoints & diffs ----

func (s *Session) capturePreCheckpoint(ctx context.Context) (string, string) {
	s.mu.Lock()
	cwd, sid, turnIdx := s.meta.Cwd, s.meta.ID, s.meta.Turns+1
	s.mu.Unlock()
	if cwd == "" || sid == "" {
		return "", ""
	}
	ref, sha, _, err := checkpoints.Capture(ctx, cwd, sid, fmt.Sprintf("t%d-pre", turnIdx))
	if err != nil {
		return "", ""
	}
	return ref, sha
}

func (s *Session) capturePostCheckpoint(ctx context.Context, turnIdx int, preRef, preSHA string) *checkpoints.TurnCheckpoint {
	s.mu.Lock()
	cwd, sid := s.meta.Cwd, s.meta.ID
	s.mu.Unlock()
	if cwd == "" || sid == "" || preRef == "" {
		return nil
	}
	postRef, postSHA, skipped, err := checkpoints.Capture(ctx, cwd, sid, fmt.Sprintf("t%d-post", turnIdx))
	if err != nil || postRef == "" {
		return nil
	}
	files, add, del, _ := checkpoints.SummarizeDiff(ctx, cwd, preRef, postRef)
	return &checkpoints.TurnCheckpoint{
		TurnIndex:    turnIdx,
		PreRef:       preRef,
		PostRef:      postRef,
		PreSHA:       preSHA,
		PostSHA:      postSHA,
		Files:        files,
		TotalAdd:     add,
		TotalDel:     del,
		SkippedLarge: skipped,
		CreatedAt:    now(),
	}
}

// TurnDiff returns a scoped diff for a session:
// scope = "latest" | "turn" | "session" | "uncommitted" | "staged" | "branch".
func (m *Manager) TurnDiff(ctx context.Context, id, scope string, turnIndex int) (gitops.DiffResult, error) {
	s := m.Get(id)
	if s == nil {
		var err error
		s, err = m.Open(ctx, id)
		if err != nil {
			return gitops.DiffResult{}, err
		}
	}
	s.mu.Lock()
	cwd := s.meta.Cwd
	cps := append([]checkpoints.TurnCheckpoint(nil), s.meta.Checkpoints...)
	s.mu.Unlock()

	if scope == "" {
		scope = "uncommitted"
	}
	switch scope {
	case "uncommitted", "staged", "unstaged", "branch":
		return m.Git.ScopedDiff(ctx, cwd, scope, "", "")
	case "latest":
		if len(cps) == 0 {
			return m.Git.ScopedDiff(ctx, cwd, "uncommitted", "", "")
		}
		last := cps[len(cps)-1]
		return checkpoints.Diff(ctx, cwd, "latest", last.PreRef, last.PostRef)
	case "session":
		if len(cps) == 0 {
			return m.Git.ScopedDiff(ctx, cwd, "uncommitted", "", "")
		}
		first, last := cps[0], cps[len(cps)-1]
		return checkpoints.Diff(ctx, cwd, "session", first.PreRef, last.PostRef)
	case "turn":
		for _, cp := range cps {
			if cp.TurnIndex == turnIndex {
				return checkpoints.Diff(ctx, cwd, fmt.Sprintf("turn:%d", turnIndex), cp.PreRef, cp.PostRef)
			}
		}
		return gitops.DiffResult{}, fmt.Errorf("checkpoint for turn %d not found", turnIndex)
	default:
		return gitops.DiffResult{}, fmt.Errorf("unknown diff scope %q", scope)
	}
}

// CheckpointRevert restores the session's workspace files to before or after turnIndex.
func (m *Manager) CheckpointRevert(ctx context.Context, id string, turnIndex int, toPre bool) (map[string]any, error) {
	s := m.Get(id)
	if s == nil {
		return nil, errors.New("unknown session")
	}
	s.mu.Lock()
	cwd := s.meta.Cwd
	var target string
	for _, cp := range s.meta.Checkpoints {
		if cp.TurnIndex == turnIndex {
			if toPre {
				target = cp.PreRef
			} else {
				target = cp.PostRef
			}
			break
		}
	}
	s.mu.Unlock()
	if target == "" {
		return nil, fmt.Errorf("checkpoint for turn %d not found", turnIndex)
	}
	backupRef, err := checkpoints.Revert(ctx, cwd, id, target)
	if err != nil {
		return nil, err
	}
	m.Git.Invalidate(cwd)
	s.mu.Lock()
	s.queueOpsLocked(s.tr.AddNotice("info", fmt.Sprintf("Workspace files restored to checkpoint turn %d (backup saved).", turnIndex)))
	s.mu.Unlock()
	s.emit()
	return map[string]any{"ok": true, "restoredRef": target, "backupRef": backupRef}, nil
}

// ---- Inline !command execution (T3 PR #31101) ----

// ExecInline executes a shell command in the session's cwd and records it in the transcript.
func (s *Session) ExecInline(ctx context.Context, command string) (map[string]any, error) {
	command = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(command), "!"))
	if command == "" {
		return nil, errors.New("empty command")
	}
	s.mu.Lock()
	cwd := s.meta.Cwd
	callID := fmt.Sprintf("inline-%d", time.Now().UnixNano())
	inRaw, _ := json.Marshal(map[string]any{"command": command})
	it := &Item{
		ID:   "tc:" + callID,
		Kind: "tool",
		Ts:   now(),
		Tool: &Tool{
			CallID:  callID,
			Name:    "Bash",
			Kind:    "execute",
			Title:   "! " + command,
			Status:  "in_progress",
			Input:   inRaw,
			StartTs: now(),
		},
	}
	s.queueOpsLocked(s.tr.add(it))
	s.mu.Unlock()
	s.emit()

	cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}
	cmd := exec.CommandContext(cctx, shell, "-lc", command)
	cmd.Dir = cwd
	outBytes, err := cmd.CombinedOutput()
	exitCode := 0
	status := "completed"
	if err != nil {
		status = "failed"
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			exitCode = 1
		}
	}
	outStr := string(outBytes)
	if len(outStr) > 128*1024 {
		outStr = outStr[:128*1024] + "\n…(truncated)"
	}
	outRaw, _ := json.Marshal(map[string]any{"output": outStr, "exit_code": exitCode})

	s.mu.Lock()
	if cur := s.tr.Get(it.ID); cur != nil && cur.Tool != nil {
		cur.Tool.Status = status
		cur.Tool.Output = outRaw
		cur.Tool.EndTs = now()
		s.queueOpsLocked(s.tr.set(cur))
	}
	s.meta.UpdatedAt = now()
	s.mu.Unlock()
	s.m.Git.Invalidate(cwd)
	s.emit()
	s.m.saveSession(s)
	return map[string]any{"ok": exitCode == 0, "exitCode": exitCode, "output": outStr, "itemId": it.ID}, nil
}

// ---- Portable Provider Handoff (T3 context-handoffs algorithm) ----

// BuildPortableHandoff compiles a deterministic, compacted context summary from a session's transcript.
func BuildPortableHandoff(title string, items []*Item, cps []checkpoints.TurnCheckpoint) string {
	var sb strings.Builder
	sb.WriteString("# Handoff context from previous thread")
	if title != "" {
		sb.WriteString(": " + title)
	}
	sb.WriteString("\n\n")
	compact := func(s string) string {
		s = strings.Join(strings.Fields(s), " ")
		runes := []rune(s)
		if len(runes) > 240 {
			return string(runes[:237]) + "..."
		}
		return s
	}
	count := 0
	for _, it := range items {
		if count >= 80 {
			break
		}
		switch it.Kind {
		case "user":
			if t := compact(it.Text); t != "" {
				sb.WriteString("- **User**: " + t + "\n")
				count++
			}
		case "agent":
			if t := compact(it.Text); t != "" {
				sb.WriteString("- **Assistant**: " + t + "\n")
				count++
			}
		case "tool":
			if it.Tool != nil {
				if it.Tool.Kind == "execute" {
					sb.WriteString("- **Command**: " + compact(it.Tool.Title) + " (" + it.Tool.Status + ")\n")
					count++
				} else if it.Tool.Kind == "edit" {
					sb.WriteString("- **File change**: " + compact(it.Tool.Title) + "\n")
					count++
				}
			}
		}
	}
	if len(cps) > 0 {
		last := cps[len(cps)-1]
		sb.WriteString(fmt.Sprintf("\nLatest checkpoint (turn %d): %d files changed (+%d -%d).\n", last.TurnIndex, len(last.Files), last.TotalAdd, last.TotalDel))
	}
	sb.WriteString("\nContinue seamlessly from this state without repeating completed work.\n")
	return sb.String()
}

// Handoff creates a new session on targetAgent (or targetModel) carrying portable context from sourceID.
func (m *Manager) Handoff(ctx context.Context, sourceID, targetAgent, targetModel, targetMode, cwd, userPrompt string) (*Session, string, error) {
	src := m.Get(sourceID)
	if src == nil {
		var err error
		src, err = m.Open(ctx, sourceID)
		if err != nil {
			return nil, "", err
		}
	}
	src.mu.Lock()
	srcTitle := src.meta.Title
	srcCwd := src.meta.Cwd
	srcProj := src.meta.ProjectID
	srcWt := src.meta.WorktreePath
	srcBranch := src.meta.Branch
	items := append([]*Item(nil), src.tr.Items...)
	cps := append([]checkpoints.TurnCheckpoint(nil), src.meta.Checkpoints...)
	src.mu.Unlock()

	if cwd == "" {
		cwd = srcCwd
	}
	handoffText := BuildPortableHandoff(srcTitle, items, cps)
	cp := CreateParams{
		Agent:        targetAgent,
		Cwd:          cwd,
		ProjectID:    srcProj,
		GroupedUnder: sourceID,
		Title:        srcTitle + " (" + targetAgent + ")",
		Flags:        Flags{Model: targetModel, Mode: targetMode},
	}
	ns, err := m.Create(ctx, cp)
	if ns != nil {
		ns.mu.Lock()
		ns.meta.ForkedFrom = sourceID
		if filepath.Clean(ns.meta.Cwd) == filepath.Clean(srcCwd) {
			ns.meta.WorktreePath = srcWt
			ns.meta.Branch = srcBranch
		}
		if handoffText != "" {
			ns.meta.SeedHandoff = handoffText
		}
		ns.queueOpsLocked(ns.tr.AddNotice("info", fmt.Sprintf("Handed off context from %q (%d entries).", srcTitle, len(items))))
		ns.mu.Unlock()
		ns.emit()
		m.saveSession(ns)
		if err == nil {
			if trimmedPrompt := strings.TrimSpace(userPrompt); trimmedPrompt != "" {
				_, err = ns.Prompt(PromptInput{Text: trimmedPrompt})
			}
		}
	}
	return ns, handoffText, err
}

// ---- Generic ACP side questions (/btw) ----

func (m *Manager) answerGenericAside(ctx context.Context, s *Session, question string) (string, error) {
	s.mu.Lock()
	agent := s.meta.Agent
	cwd := s.meta.Cwd
	title := s.meta.Title
	items := append([]*Item(nil), s.tr.Items...)
	cps := append([]checkpoints.TurnCheckpoint(nil), s.meta.Checkpoints...)
	s.mu.Unlock()

	pc, ok := m.providerConfig(agent)
	if !ok || pc.Command == "" {
		return "Side questions are not available for provider " + agent, nil
	}
	ctxSummary := BuildPortableHandoff(title, items, cps)
	prompt := "Answer this side question concisely based ONLY on the conversation context below. Do NOT modify any files or run commands.\n\n" +
		ctxSummary + "\nQuestion: " + question

	var answer strings.Builder
	h := asideCaptureHandler{onText: func(t string) { answer.WriteString(t) }}
	p, err := acpproc.Start(ctx, acpproc.Options{
		Command: pc.Command, Args: pc.Args, Cwd: cwd,
		LogDir: filepath.Join(m.cfg.StateDir, "logs"), LogPrefix: "aside-" + agent,
		Env: providers.MergeEnv(m.agyEnv(), pc.Env), Handler: h,
	})
	if err != nil {
		return "", err
	}
	defer p.Close(2 * time.Second)
	var setup acp.SessionSetup
	if err := p.ACP().Call(ctx, "session/new", map[string]any{"cwd": cwd, "mcpServers": []any{}}, &setup); err != nil {
		return "", err
	}
	var res acp.PromptResult
	if err := p.ACP().Call(ctx, "session/prompt", map[string]any{
		"sessionId": setup.SessionID,
		"prompt":    []acp.ContentBlock{{Type: "text", Text: prompt}},
	}, &res); err != nil {
		return "", err
	}
	return strings.TrimSpace(answer.String()), nil
}

type asideCaptureHandler struct{ onText func(string) }

func (h asideCaptureHandler) HandleNotification(method string, params json.RawMessage) {
	if method != "session/update" {
		return
	}
	var n acp.SessionNotification
	if json.Unmarshal(params, &n) != nil {
		return
	}
	var uh acp.UpdateHeader
	if json.Unmarshal(n.Update, &uh) != nil || uh.SessionUpdate != "agent_message_chunk" {
		return
	}
	if b, ok := textOf(n.Update); ok && b.Text != "" {
		h.onText(b.Text)
	}
}

func (h asideCaptureHandler) HandleRequest(c *acp.Conn, id json.RawMessage, _ string, _ json.RawMessage) {
	_ = c.Reply(id, map[string]any{"outcome": map[string]any{"outcome": "cancelled"}})
}

// ---- Global Search ----

// parseSearchTerms splits a search query into normalized terms, preserving
// double-quoted exact phrases ("foo bar") alongside whitespace-separated tokens.
func parseSearchTerms(raw string) []string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil
	}
	var terms []string
	i := 0
	for i < len(s) {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
			i++
		}
		if i >= len(s) {
			break
		}
		if s[i] == '"' {
			i++
			start := i
			for i < len(s) && s[i] != '"' {
				i++
			}
			phrase := foldSearchText(s[start:i])
			if strings.TrimSpace(phrase) != "" {
				terms = append(terms, phrase)
			}
			if i < len(s) && s[i] == '"' {
				i++
			}
		} else {
			start := i
			for i < len(s) && s[i] != ' ' && s[i] != '\t' && s[i] != '\n' && s[i] != '\r' && s[i] != '"' {
				i++
			}
			tok := foldSearchText(s[start:i])
			if tok != "" {
				terms = append(terms, tok)
			}
		}
	}
	return terms
}

// foldSearchRune normalizes a rune for case-insensitive search while folding
// Turkish dotted capital İ (U+0130 -> "i\u0307" in Unicode ToLower) and dotless ı (U+0131)
// to plain "i" so ASCII/Unicode queries match cleanly without byte-offset drift.
func foldSearchRune(r rune) string {
	switch r {
	case '\u0130', '\u0131':
		return "i"
	case '\u0307':
		return ""
	}
	return strings.ReplaceAll(strings.ToLower(string(r)), "\u0307", "")
}

func foldSearchText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		b.WriteString(foldSearchRune(r))
	}
	return b.String()
}

func matchAllTerms(foldedHaystack string, terms []string) bool {
	if len(terms) == 0 {
		return false
	}
	for _, t := range terms {
		if !strings.Contains(foldedHaystack, t) {
			return false
		}
	}
	return true
}

// GlobalSearch searches across session titles and transcript messages, supporting
// quoted exact phrases ("...") and multi-token queries with Unicode-safe folding.
func (m *Manager) GlobalSearch(ctx context.Context, query, projectID string, limit int) []SearchHit {
	terms := parseSearchTerms(query)
	if len(terms) == 0 {
		return []SearchHit{}
	}
	if limit <= 0 || limit > 100 {
		limit = 40
	}
	var hits []SearchHit

	for _, s := range m.all() {
		s.mu.Lock()
		meta := s.meta
		items := append([]*Item(nil), s.tr.Items...)
		s.mu.Unlock()
		if projectID != "" && meta.ProjectID != projectID {
			continue
		}
		combinedMeta := foldSearchText(meta.Title + " " + meta.Branch)
		if matchAllTerms(combinedMeta, terms) {
			hits = append(hits, SearchHit{
				Kind: "session", SessionID: meta.ID, ProjectID: meta.ProjectID,
				Title: meta.Title, Snippet: meta.LastText, Path: meta.Cwd, UpdatedAt: meta.UpdatedAt,
			})
		}
		for i := len(items) - 1; i >= 0 && len(hits) < limit; i-- {
			it := items[i]
			if it.Kind != "user" && it.Kind != "agent" {
				continue
			}
			if snip, ok := caseFoldTermsSnippet(it.Text, terms, 40, 80); ok {
				hits = append(hits, SearchHit{
					Kind: "message", SessionID: meta.ID, ProjectID: meta.ProjectID,
					Title: meta.Title, Snippet: snip, UpdatedAt: it.Ts,
				})
				break
			}
		}
		if len(hits) >= limit {
			break
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].UpdatedAt > hits[j].UpdatedAt })
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

func caseFoldSnippet(text, queryLower string, contextRunesBefore, contextRunesAfter int) (string, bool) {
	if queryLower == "" {
		return "", false
	}
	return caseFoldTermsSnippet(text, []string{foldSearchText(queryLower)}, contextRunesBefore, contextRunesAfter)
}

func caseFoldTermsSnippet(text string, terms []string, contextRunesBefore, contextRunesAfter int) (string, bool) {
	if len(terms) == 0 {
		return "", false
	}
	var lowBuf strings.Builder
	lowBuf.Grow(len(text))
	foldedByteToOrigByte := make([]int, 0, len(text)+1)
	for origIdx, r := range text {
		folded := foldSearchRune(r)
		for k := 0; k < len(folded); k++ {
			foldedByteToOrigByte = append(foldedByteToOrigByte, origIdx)
		}
		lowBuf.WriteString(folded)
	}
	foldedByteToOrigByte = append(foldedByteToOrigByte, len(text))

	lowStr := lowBuf.String()
	firstIdx := -1
	firstLen := 0
	for _, term := range terms {
		idx := strings.Index(lowStr, term)
		if idx < 0 {
			return "", false
		}
		if firstIdx < 0 || idx < firstIdx {
			firstIdx = idx
			firstLen = len(term)
		}
	}
	if firstIdx < 0 {
		return "", false
	}
	matchStart := foldedByteToOrigByte[firstIdx]
	endFoldIdx := min(firstIdx+firstLen, len(foldedByteToOrigByte)-1)
	matchEnd := foldedByteToOrigByte[endFoldIdx]
	if matchEnd < len(text) && (matchEnd <= matchStart || (endFoldIdx > 0 && endFoldIdx < len(foldedByteToOrigByte)-1 && foldedByteToOrigByte[endFoldIdx] == foldedByteToOrigByte[endFoldIdx-1])) {
		_, sz := utf8.DecodeRuneInString(text[matchEnd:])
		matchEnd = min(len(text), matchEnd+sz)
	}

	start := matchStart
	for range contextRunesBefore {
		if start <= 0 {
			break
		}
		_, sz := utf8.DecodeLastRuneInString(text[:start])
		if sz <= 0 {
			break
		}
		start -= sz
	}
	end := matchEnd
	for range contextRunesAfter {
		if end >= len(text) {
			break
		}
		_, sz := utf8.DecodeRuneInString(text[end:])
		if sz <= 0 {
			break
		}
		end += sz
	}

	snip := strings.ReplaceAll(text[start:end], "\r\n", " ")
	snip = strings.ReplaceAll(snip, "\n", " ")
	snip = strings.ReplaceAll(snip, "\r", " ")
	return strings.TrimSpace(snip), true
}

// ---- Unified Skills Discovery ----

// DiscoverSkills finds SKILL.md files in workspace and user directories.
func (m *Manager) DiscoverSkills(cwd string) []SkillEntry {
	var out []SkillEntry
	seen := map[string]bool{}
	scan := func(root, scope string) {
		if root == "" {
			return
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			return
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			skillMD := filepath.Join(root, e.Name(), "SKILL.md")
			b, err := os.ReadFile(skillMD)
			if err != nil {
				continue
			}
			name, desc := parseSkillFrontmatter(string(b), e.Name())
			key := scope + ":" + name
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, SkillEntry{Name: name, Description: desc, Path: skillMD, Scope: scope})
		}
	}
	if cwd != "" {
		scan(filepath.Join(cwd, ".claude", "skills"), "project")
		scan(filepath.Join(cwd, ".codex", "skills"), "project")
		scan(filepath.Join(cwd, ".agents", "skills"), "project")
		scan(filepath.Join(cwd, ".agentc", "skills"), "project")
		scan(filepath.Join(cwd, ".gemini", "skills"), "project")
	}
	scan(filepath.Join(m.cfg.HomeDir, ".claude", "skills"), "claude")
	scan(filepath.Join(m.cfg.HomeDir, ".codex", "skills"), "codex")
	scan(filepath.Join(m.cfg.HomeDir, ".gemini", "antigravity-cli", "skills"), "agy")
	scan(filepath.Join(m.cfg.HomeDir, ".gemini", "antigravity-cli", "builtin", "skills"), "agy")
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func parseSkillFrontmatter(content, fallback string) (name, desc string) {
	name = fallback
	lines := strings.Split(content, "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "---" {
		return name, ""
	}
	for _, l := range lines[1:] {
		if strings.TrimSpace(l) == "---" {
			break
		}
		if strings.HasPrefix(l, "name:") {
			name = strings.Trim(strings.TrimSpace(strings.TrimPrefix(l, "name:")), `"'`)
		} else if strings.HasPrefix(l, "description:") {
			desc = strings.Trim(strings.TrimSpace(strings.TrimPrefix(l, "description:")), `"'`)
		}
	}
	return name, desc
}

// ---- Zero-spawn Linux Host Health & Process Observability ----

// HostHealth reads /proc/meminfo, /proc/pressure/memory, /proc/loadavg and live agent RSS.
func (m *Manager) HostHealth() HostHealth {
	h := HostHealth{Processes: []ManagedProcess{}, CheckedAt: now()}

	if b, err := os.ReadFile("/proc/meminfo"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) < 2 {
				continue
			}
			v, _ := strconv.ParseInt(f[1], 10, 64)
			switch f[0] {
			case "MemTotal:":
				h.MemTotalKiB = v
			case "MemAvailable:":
				h.MemAvailableKiB = v
			}
		}
		if h.MemTotalKiB > 0 {
			h.MemUsedPct = float64(h.MemTotalKiB-h.MemAvailableKiB) * 100.0 / float64(h.MemTotalKiB)
		}
	}

	if b, err := os.ReadFile("/proc/pressure/memory"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			for _, tok := range strings.Fields(line) {
				if strings.HasPrefix(tok, "avg10=") {
					v, _ := strconv.ParseFloat(strings.TrimPrefix(tok, "avg10="), 64)
					if strings.HasPrefix(line, "some ") {
						h.PSISomeAvg10 = v
					} else if strings.HasPrefix(line, "full ") {
						h.PSIFullAvg10 = v
					}
				}
			}
		}
	}

	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		f := strings.Fields(string(b))
		if len(f) >= 2 {
			h.LoadAvg1, _ = strconv.ParseFloat(f[0], 64)
			h.LoadAvg5, _ = strconv.ParseFloat(f[1], 64)
		}
	}

	for _, s := range m.all() {
		s.mu.Lock()
		p := s.proc
		sid, title, agent, state := s.meta.ID, s.meta.Title, s.meta.Agent, s.stateLocked()
		s.mu.Unlock()
		if p != nil && p.Alive() && p.Pid() > 0 {
			h.Processes = append(h.Processes, ManagedProcess{
				SessionID: sid, Title: title, Agent: agent, State: state,
				Pid: p.Pid(), RSSKiB: p.RSSKiB(),
			})
		}
	}
	sort.Slice(h.Processes, func(i, j int) bool { return h.Processes[i].RSSKiB > h.Processes[j].RSSKiB })
	return h
}

// KillManagedProcess terminates a stuck agent process ONLY if owned by one of our sessions.
func (m *Manager) KillManagedProcess(pid int) error {
	if pid <= 0 {
		return errors.New("invalid pid")
	}
	for _, s := range m.all() {
		s.mu.Lock()
		p := s.proc
		s.mu.Unlock()
		if p != nil && p.Pid() == pid {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
			_ = syscall.Kill(pid, syscall.SIGKILL)
			return nil
		}
	}
	return fmt.Errorf("pid %d is not a managed agent process", pid)
}

// ---- Scheduled Task Runner Integration ----

func (m *Manager) runScheduledTask(ctx context.Context, task scheduler.Task) (string, string, error) {
	cp := CreateParams{
		Agent:       firstNonEmpty(task.Agent, "claude"),
		Cwd:         task.Cwd,
		ProjectID:   task.ProjectID,
		UseWorktree: task.UseWorktree,
		Title:       "[Scheduled] " + task.Name,
		Flags:       Flags{Model: task.Model, Mode: task.Mode},
		Prompt:      &PromptInput{Text: task.Prompt},
	}
	ss, err := m.Create(ctx, cp)
	if ss == nil {
		m.notify(Notification{Kind: "schedule_error", Title: "Scheduled task failed: " + task.Name, Body: fmt.Sprint(err)})
		return "", "", err
	}
	sid := ss.ID()
	m.notify(Notification{SessionID: sid, Kind: "schedule_started", Title: "Scheduled: " + task.Name, Body: "Started in " + filepath.Base(task.Cwd)})
	return sid, "Started session " + sid, nil
}

// recordTurnUsage appends a turn's token/cost stats into usage.jsonl.
func (m *Manager) recordTurnUsage(s *Session, u *acp.Usage, durMs int64) {
	if m.Usage == nil || u == nil {
		return
	}
	s.mu.Lock()
	sid := s.meta.ID
	pid := s.meta.ProjectID
	agent := firstNonEmpty(s.meta.Agent, "agy")
	model := ""
	if s.meta.Models != nil {
		model = s.meta.Models.CurrentModelID
	}
	if model == "" {
		model = s.meta.WantModel
	}
	s.mu.Unlock()
	m.Usage.Append(usage.Record{
		Ts:                now(),
		SessionID:         sid,
		ProjectID:         pid,
		Agent:             agent,
		Model:             model,
		InputTokens:       u.InputTokens,
		OutputTokens:      u.OutputTokens,
		CachedReadTokens:  u.CachedReadTokens,
		CachedWriteTokens: u.CachedWriteTokens,
		ThoughtTokens:     u.ThoughtTokens,
		TotalTokens:       u.TotalTokens,
		CostUSD:           u.CostUSD,
		DurationMs:        durMs,
	})
}

// GenerateCommitMessage synthesizes a clean commit message suggestion from uncommitted changes.
func (m *Manager) GenerateCommitMessage(ctx context.Context, dir string) (string, error) {
	st, err := m.Git.Status(ctx, dir, true)
	if err != nil {
		return "", err
	}
	if len(st.Files) == 0 {
		return "", errors.New("no uncommitted changes")
	}
	var names []string
	added, modified, deleted := 0, 0, 0
	for _, f := range st.Files {
		names = append(names, filepath.Base(f.Path))
		if f.Untracked || f.Staged == "A" {
			added++
		} else if f.Staged == "D" || f.Unstaged == "D" {
			deleted++
		} else {
			modified++
		}
	}
	verb := "Update"
	if added > 0 && modified == 0 && deleted == 0 {
		verb = "Add"
	} else if deleted > 0 && added == 0 && modified == 0 {
		verb = "Remove"
	}
	if len(names) <= 3 {
		return fmt.Sprintf("%s %s", verb, strings.Join(names, ", ")), nil
	}
	return fmt.Sprintf("%s %s and %d more files", verb, strings.Join(names[:2], ", "), len(names)-2), nil
}
