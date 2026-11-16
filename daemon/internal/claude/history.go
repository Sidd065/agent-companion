package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// ConfigDir is Claude Code's state directory (~/.claude or $CLAUDE_CONFIG_DIR).
func ConfigDir(home string) string {
	if v := os.Getenv("CLAUDE_CONFIG_DIR"); v != "" {
		return v
	}
	return filepath.Join(home, ".claude")
}

// SanitizePath mirrors how Claude Code names per-project directories.
func SanitizePath(p string) string {
	var b strings.Builder
	for _, r := range p {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

// SessionInfo is one Claude Code conversation on disk.
type SessionInfo struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	FirstPrompt string `json:"firstPrompt,omitempty"`
	Cwd         string `json:"cwd"`
	GitBranch   string `json:"gitBranch,omitempty"`
	Tag         string `json:"tag,omitempty"`
	Entrypoint  string `json:"entrypoint,omitempty"`
	Path        string `json:"-"`
	Created     int64  `json:"createdAt"`
	Updated     int64  `json:"updatedAt"`
	Size        int64  `json:"size"`
	Archived    bool   `json:"archived"`
}

type cacheEntry struct {
	size  int64
	mtime int64
	info  SessionInfo
	ok    bool
}

var (
	listMu    sync.Mutex
	listCache = map[string]cacheEntry{}
)

// ListSessions returns every conversation under configDir/projects, newest first.
func ListSessions(configDir string) []SessionInfo {
	files, _ := filepath.Glob(filepath.Join(configDir, "projects", "*", "*.jsonl"))
	var out []SessionInfo
	seen := map[string]bool{}
	listMu.Lock()
	defer listMu.Unlock()
	for _, f := range files {
		st, err := os.Stat(f)
		if err != nil || st.Size() == 0 {
			continue
		}
		seen[f] = true
		c, ok := listCache[f]
		archFile := filepath.Join(strings.TrimSuffix(f, ".jsonl"), "archived.json")
		var amt int64
		if ast, err := os.Stat(archFile); err == nil {
			amt = ast.ModTime().UnixNano()
		}
		key := st.ModTime().UnixNano() ^ amt
		if !ok || c.size != st.Size() || c.mtime != key {
			info, good := ReadSessionInfo(f)
			c = cacheEntry{size: st.Size(), mtime: key, info: info, ok: good}
			listCache[f] = c
		}
		if c.ok {
			out = append(out, c.info)
		}
	}
	for f := range listCache {
		if !seen[f] {
			delete(listCache, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated > out[j].Updated })
	return out
}

// FindSession locates a conversation's transcript file.
func FindSession(configDir, id, cwdHint string) (string, error) {
	if !uuidRe.MatchString(id) {
		return "", fmt.Errorf("not a Claude session id: %s", id)
	}
	if cwdHint != "" {
		p := filepath.Join(configDir, "projects", SanitizePath(cwdHint), id+".jsonl")
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	m, _ := filepath.Glob(filepath.Join(configDir, "projects", "*", id+".jsonl"))
	if len(m) > 0 {
		return m[0], nil
	}
	return "", os.ErrNotExist
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// IsSessionID reports whether id looks like a Claude session id (a UUID).
func IsSessionID(id string) bool { return uuidRe.MatchString(id) }

func readHeadTail(path string, n int64) (head, tail []byte, size int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, nil, 0, err
	}
	size = st.Size()
	if size <= 2*n {
		b, err := io.ReadAll(f)
		return b, b, size, err
	}
	head = make([]byte, n)
	if _, err := io.ReadFull(f, head); err != nil {
		return nil, nil, 0, err
	}
	tail = make([]byte, n)
	if _, err := f.ReadAt(tail, size-n); err != nil && err != io.EOF {
		return nil, nil, 0, err
	}
	if i := bytes.IndexByte(tail, '\n'); i >= 0 {
		tail = tail[i+1:]
	}
	return head, tail, size, nil
}

type infoLine struct {
	Type          string          `json:"type"`
	Subtype       string          `json:"subtype"`
	SessionID     string          `json:"sessionId"`
	Cwd           string          `json:"cwd"`
	Timestamp     string          `json:"timestamp"`
	GitBranch     string          `json:"gitBranch"`
	Entrypoint    string          `json:"entrypoint"`
	IsSidechain   bool            `json:"isSidechain"`
	IsMeta        bool            `json:"isMeta"`
	IsCompact     bool            `json:"isCompactSummary"`
	CustomTitle   string          `json:"customTitle"`
	AITitle       string          `json:"aiTitle"`
	LastPrompt    string          `json:"lastPrompt"`
	Summary       string          `json:"summary"`
	Tag           *string         `json:"tag"`
	Message       json.RawMessage `json:"message"`
	Attachment    json.RawMessage `json:"attachment"`
	RelocatedCwd  string          `json:"relocatedCwd"`
	ContinuedInID string          `json:"continuedInSessionId"`
}

func lines(b []byte) [][]byte {
	var out [][]byte
	for len(b) > 0 {
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			out = append(out, b)
			break
		}
		if i > 0 {
			out = append(out, b[:i])
		}
		b = b[i+1:]
	}
	return out
}

// ReadSessionInfo extracts list metadata from a transcript's head and tail.
func ReadSessionInfo(path string) (SessionInfo, bool) {
	head, tail, size, err := readHeadTail(path, 256<<10)
	if err != nil {
		return SessionInfo{}, false
	}
	st, _ := os.Stat(path)
	info := SessionInfo{ID: strings.TrimSuffix(filepath.Base(path), ".jsonl"), Path: path, Size: size}
	if st != nil {
		info.Updated = st.ModTime().UnixMilli()
	}
	hl := lines(head)
	for i, l := range hl {
		var e infoLine
		if json.Unmarshal(l, &e) != nil {
			continue
		}
		if i == 0 && e.IsSidechain {
			return info, false
		}
		if info.Cwd == "" && e.Cwd != "" {
			info.Cwd = e.Cwd
		}
		if info.Entrypoint == "" && e.Entrypoint != "" {
			info.Entrypoint = e.Entrypoint
		}
		if info.GitBranch == "" && e.GitBranch != "" {
			info.GitBranch = e.GitBranch
		}
		if info.Created == 0 && e.Timestamp != "" {
			if t, err := time.Parse(time.RFC3339Nano, e.Timestamp); err == nil {
				info.Created = t.UnixMilli()
			}
		}
		if info.FirstPrompt == "" {
			if p := promptOf(&e); p != "" {
				info.FirstPrompt = p
			}
		}
	}
	var custom, ai, last, summary string
	tl := lines(tail)
	for i := len(tl) - 1; i >= 0; i-- {
		l := tl[i]
		var e infoLine
		// Cheap pre-filter: most tail lines are big user/assistant entries.
		if len(l) > 64<<10 && !bytes.Contains(l[:256], []byte(`"type":"user"`)) {
			continue
		}
		if json.Unmarshal(l, &e) != nil {
			continue
		}
		switch e.Type {
		case "custom-title":
			if custom == "" {
				custom = e.CustomTitle
			}
		case "ai-title":
			if ai == "" {
				ai = e.AITitle
			}
		case "last-prompt":
			if last == "" {
				last = e.LastPrompt
			}
		case "summary":
			if summary == "" {
				summary = e.Summary
			}
		case "tag":
			if info.Tag == "" && e.Tag != nil {
				info.Tag = *e.Tag
			}
		}
		if e.Cwd != "" && info.Cwd == "" {
			info.Cwd = e.Cwd
		}
		if e.RelocatedCwd != "" {
			info.Cwd = e.RelocatedCwd
		}
		if e.GitBranch != "" && i == len(tl)-1 {
			info.GitBranch = e.GitBranch
		}
	}
	if custom == "" {
		if b, err := os.ReadFile(filepath.Join(strings.TrimSuffix(path, ".jsonl"), "custom-title.json")); err == nil {
			var ct struct {
				CustomTitle string `json:"customTitle"`
			}
			if json.Unmarshal(b, &ct) == nil {
				custom = ct.CustomTitle
			}
		}
	}
	info.Title = firstNonEmpty(custom, ai, oneLine(last), oneLine(summary), info.FirstPrompt)
	if info.Title == "" {
		return info, false
	}
	info.Archived = archivedState(path, tl)
	return info, true
}

func oneLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len([]rune(s)) > 120 {
		s = string([]rune(s)[:120]) + "…"
	}
	return s
}

var (
	cmdNameRe = regexp.MustCompile(`<command-name>([^<]*)</command-name>`)
	cmdArgsRe = regexp.MustCompile(`(?s)<command-args>(.*?)</command-args>`)
	tagRe     = regexp.MustCompile(`(?s)<[a-z-]+>.*?</[a-z-]+>`)
)

// promptText turns a user message's text into what the user typed.
// skip reports text the CLI generated rather than the user.
func promptText(s string) (text string, skip bool) {
	t := strings.TrimSpace(s)
	switch {
	case t == "":
		return "", true
	case strings.HasPrefix(t, "<command-name>") || strings.Contains(t[:min(len(t), 300)], "<command-name>"):
		name := ""
		if m := cmdNameRe.FindStringSubmatch(t); m != nil {
			name = strings.TrimSpace(m[1])
		}
		args := ""
		if m := cmdArgsRe.FindStringSubmatch(t); m != nil {
			args = strings.TrimSpace(m[1])
		}
		if !strings.HasPrefix(name, "/") {
			name = "/" + name
		}
		return strings.TrimSpace(name + " " + args), false
	case strings.HasPrefix(t, "<local-command-stdout>"), strings.HasPrefix(t, "<local-command-stderr>"),
		strings.HasPrefix(t, "Caveat: "), strings.HasPrefix(t, "<system-reminder>"), strings.HasPrefix(t, "<task-notification>"),
		strings.HasPrefix(t, "[Request interrupted"), strings.HasPrefix(t, "<bash-stdout>"), strings.HasPrefix(t, "<bash-stderr>"):
		return "", true
	case strings.HasPrefix(t, "<bash-input>"):
		return "! " + strings.TrimSuffix(strings.TrimPrefix(t, "<bash-input>"), "</bash-input>"), false
	}
	return s, false
}

func promptOf(e *infoLine) string {
	if e.Type != "user" || e.IsMeta || e.IsCompact || len(e.Message) == 0 {
		return ""
	}
	var m struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(e.Message, &m) != nil {
		return ""
	}
	var s string
	if json.Unmarshal(m.Content, &s) == nil {
		t, skip := promptText(s)
		if skip {
			return ""
		}
		return oneLine(t)
	}
	var blocks []block
	if json.Unmarshal(m.Content, &blocks) != nil {
		return ""
	}
	for _, b := range blocks {
		switch b.Type {
		case "tool_result":
			return ""
		case "text":
			if t, skip := promptText(b.Text); !skip {
				return oneLine(t)
			}
		case "image":
			return "Image"
		}
	}
	return ""
}

func archivedState(path string, tail [][]byte) bool {
	b, err := os.ReadFile(filepath.Join(strings.TrimSuffix(path, ".jsonl"), "archived.json"))
	if err != nil {
		return false
	}
	var a struct {
		Archived bool   `json:"archived"`
		At       string `json:"at"`
	}
	if json.Unmarshal(b, &a) != nil || !a.Archived {
		return false
	}
	at, err := time.Parse(time.RFC3339Nano, a.At)
	if err != nil {
		return true
	}
	// A prompt sent after archiving un-archives the session (Claude's rule).
	for _, l := range tail {
		if !bytes.Contains(l, []byte(`"type":"user"`)) || bytes.Contains(l, []byte(`"tool_result"`)) {
			continue
		}
		var e infoLine
		if json.Unmarshal(l, &e) == nil && e.Type == "user" && !e.IsMeta {
			if t, err := time.Parse(time.RFC3339Nano, e.Timestamp); err == nil && t.After(at) {
				return false
			}
		}
	}
	return true
}

// SetArchived writes Claude's archive mark next to the transcript.
func SetArchived(path string, on bool) error {
	dir := strings.TrimSuffix(path, ".jsonl")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, _ := json.Marshal(map[string]any{"archived": on, "at": time.Now().UTC().Format(time.RFC3339Nano), "by": "agentc"})
	return os.WriteFile(filepath.Join(dir, "archived.json"), b, 0o600)
}

// AppendTitle renames a conversation the way the Agent SDK does.
func AppendTitle(path, id, title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return errors.New("title is empty")
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	b, _ := json.Marshal(map[string]any{"type": "custom-title", "customTitle": title, "sessionId": id, "uuid": NewUUID(),
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano)})
	_, err = f.Write(append(b, '\n'))
	if err == nil {
		dir := strings.TrimSuffix(path, ".jsonl")
		if _, err := os.Stat(filepath.Join(dir, "custom-title.json")); err == nil {
			cb, _ := json.Marshal(map[string]any{"customTitle": title})
			_ = os.WriteFile(filepath.Join(dir, "custom-title.json"), cb, 0o600)
		}
	}
	return err
}

// DeleteSession removes a transcript and its sidecar directory.
func DeleteSession(path string) error {
	if err := os.Remove(path); err != nil {
		return err
	}
	return os.RemoveAll(strings.TrimSuffix(path, ".jsonl"))
}

// ---- transcript chain ----

// Entry is one conversation entry of a transcript.
type Entry struct {
	Type              string          `json:"type"`
	Subtype           string          `json:"subtype"`
	UUID              string          `json:"uuid"`
	ParentUUID        *string         `json:"parentUuid"`
	LogicalParentUUID string          `json:"logicalParentUuid"`
	IsSidechain       bool            `json:"isSidechain"`
	IsMeta            bool            `json:"isMeta"`
	IsCompactSummary  bool            `json:"isCompactSummary"`
	IsAPIError        bool            `json:"isApiErrorMessage"`
	Timestamp         string          `json:"timestamp"`
	Message           json.RawMessage `json:"message"`
	ToolUseResult     json.RawMessage `json:"toolUseResult"`
	Content           json.RawMessage `json:"content"`
	Level             string          `json:"level"`
	Attachment        json.RawMessage `json:"attachment"`
}

func (e *Entry) parent() string {
	if e.ParentUUID != nil && *e.ParentUUID != "" {
		return *e.ParentUUID
	}
	return e.LogicalParentUUID
}

func (e *Entry) ts() int64 {
	if t, err := time.Parse(time.RFC3339Nano, e.Timestamp); err == nil {
		return t.UnixMilli()
	}
	return 0
}

var chainTypes = map[string]bool{"user": true, "assistant": true, "system": true, "attachment": true}

// backwardLines yields a file's lines from the end.
type backwardLines struct {
	f    *os.File
	pos  int64
	buf  []byte
	done bool
}

func (r *backwardLines) next() ([]byte, bool) {
	for {
		if i := bytes.LastIndexByte(r.buf, '\n'); i >= 0 {
			line := r.buf[i+1:]
			r.buf = r.buf[:i]
			if len(line) > 0 {
				return line, true
			}
			continue
		}
		if r.pos == 0 {
			if len(r.buf) > 0 {
				line := r.buf
				r.buf = nil
				return line, true
			}
			return nil, false
		}
		n := int64(1 << 20)
		if r.pos < n {
			n = r.pos
		}
		r.pos -= n
		chunk := make([]byte, n, int(n)+len(r.buf))
		if _, err := r.f.ReadAt(chunk, r.pos); err != nil && err != io.EOF {
			return nil, false
		}
		r.buf = append(chunk, r.buf...)
	}
}

// ReadChain returns the main thread ending at the transcript's latest entry,
// oldest first. At most maxEntries entries and about maxBytes of entry JSON are
// kept (huge sessions are trimmed from the start); truncated reports that.
func ReadChain(path string, maxEntries int, maxBytes int64) ([]*Entry, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	const maxScan = 768 << 20 // bound the time spent on giant transcripts
	r := &backwardLines{f: f, pos: st.Size()}
	byID := map[string]*Entry{}
	var chain []*Entry
	var kept, scanned int64
	leaf, cur := "", ""
	for {
		for cur != "" {
			e := byID[cur]
			if e == nil {
				break
			}
			chain = append(chain, e)
			delete(byID, cur)
			cur = e.parent()
			if len(chain) >= maxEntries {
				return reverse(chain), cur != "", nil
			}
		}
		if leaf != "" && cur == "" {
			return reverse(chain), false, nil
		}
		line, ok := r.next()
		if !ok {
			return reverse(chain), cur != "" && len(chain) > 0, nil
		}
		scanned += int64(len(line))
		if leaf != "" && (kept > maxBytes || scanned > maxScan) {
			return reverse(chain), true, nil
		}
		// Only conversation entries matter; skip the rest without a full parse.
		if !bytes.Contains(line, []byte(`"parentUuid"`)) {
			continue
		}
		var e Entry
		if json.Unmarshal(line, &e) != nil || !chainTypes[e.Type] || e.IsSidechain || e.UUID == "" {
			continue
		}
		kept += int64(len(line))
		byID[e.UUID] = &e
		if leaf == "" {
			leaf, cur = e.UUID, e.UUID
		}
	}
}

func reverse(c []*Entry) []*Entry {
	for i, j := 0, len(c)-1; i < j; i, j = i+1, j-1 {
		c[i], c[j] = c[j], c[i]
	}
	return c
}

// FindEntry returns one entry by uuid (scanning from the end).
func FindEntry(path, uuid string) (*Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	r := &backwardLines{f: f, pos: st.Size()}
	needle := []byte(uuid)
	for {
		line, ok := r.next()
		if !ok {
			return nil, os.ErrNotExist
		}
		if !bytes.Contains(line, needle) {
			continue
		}
		var e Entry
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		if e.UUID == uuid {
			return &e, nil
		}
		// A message queued mid-turn is stored as an attachment pointing at the
		// uuid the client sent.
		if e.Type == "attachment" && bytes.Contains(e.Attachment, []byte(`"source_uuid":"`+uuid+`"`)) {
			return &e, nil
		}
	}
}

// PromptOf returns a user entry's typed text ("" for tool results/meta).
func PromptOf(e *Entry) string {
	if e == nil {
		return ""
	}
	if e.Type == "attachment" {
		var a struct {
			Type   string          `json:"type"`
			Prompt json.RawMessage `json:"prompt"`
		}
		if json.Unmarshal(e.Attachment, &a) == nil && a.Type == "queued_command" {
			t, _ := contentText(a.Prompt)
			return t
		}
		return ""
	}
	var m struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(e.Message, &m) != nil {
		return ""
	}
	t, _ := contentText(m.Content)
	if pt, skip := promptText(t); !skip {
		return pt
	}
	return ""
}

func contentText(raw json.RawMessage) (string, int) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, 0
	}
	var blocks []block
	if json.Unmarshal(raw, &blocks) != nil {
		return "", 0
	}
	var parts []string
	images := 0
	for _, b := range blocks {
		switch b.Type {
		case "text":
			parts = append(parts, b.Text)
		case "image":
			images++
		}
	}
	return strings.Join(parts, "\n"), images
}

// ---- subagents ----

// SubStep is one step of a subagent transcript.
type SubStep struct {
	Type     string    `json:"type"` // user|assistant|tool
	Text     string    `json:"text,omitempty"`
	Thinking string    `json:"thinking,omitempty"`
	Tools    []SubTool `json:"tools,omitempty"`
	Ts       int64     `json:"ts"`
	Model    string    `json:"model,omitempty"`
}

// SubTool is a tool call made by a subagent.
type SubTool struct {
	Name string `json:"name"`
	Args string `json:"args"`
}

// SubagentPath returns a subagent's transcript path.
func SubagentPath(sessionPath, agentID string) string {
	return filepath.Join(strings.TrimSuffix(sessionPath, ".jsonl"), "subagents", "agent-"+agentID+".jsonl")
}

// SubagentMeta is the sidecar written next to a subagent transcript.
type SubagentMeta struct {
	AgentType   string `json:"agentType"`
	Description string `json:"description"`
	ToolUseID   string `json:"toolUseId"`
	Finished    bool   `json:"finished"`
}

// ListSubagents returns the subagents a session spawned (newest last).
func ListSubagents(sessionPath string) map[string]SubagentMeta {
	out := map[string]SubagentMeta{}
	files, _ := filepath.Glob(filepath.Join(strings.TrimSuffix(sessionPath, ".jsonl"), "subagents", "agent-*.meta.json"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var m SubagentMeta
		if json.Unmarshal(b, &m) == nil {
			id := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(f), "agent-"), ".meta.json")
			out[id] = m
		}
	}
	return out
}

// SubagentSteps reads a subagent's transcript.
func SubagentSteps(path string, max int) ([]SubStep, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []SubStep
	for _, l := range lines(b) {
		var e Entry
		if json.Unmarshal(l, &e) != nil || (e.Type != "user" && e.Type != "assistant") {
			continue
		}
		var m struct {
			Model   string          `json:"model"`
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(e.Message, &m) != nil {
			continue
		}
		var s string
		if json.Unmarshal(m.Content, &s) == nil {
			out = append(out, SubStep{Type: e.Type, Text: s, Ts: e.ts()})
			continue
		}
		var blocks []block
		if json.Unmarshal(m.Content, &blocks) != nil {
			continue
		}
		step := SubStep{Type: e.Type, Ts: e.ts(), Model: m.Model}
		for _, bl := range blocks {
			switch bl.Type {
			case "text":
				step.Text += bl.Text
			case "thinking":
				step.Thinking += bl.Thinking
			case "tool_use":
				args := string(bl.Input)
				if len(args) > 400 {
					args = args[:400] + "…"
				}
				step.Tools = append(step.Tools, SubTool{Name: bl.Name, Args: args})
			case "tool_result":
				t, _ := ResultText(bl.Content)
				if len(t) > 2000 {
					t = t[:2000] + "…"
				}
				step.Type = "tool"
				step.Text += t
			}
		}
		if step.Text == "" && step.Thinking == "" && len(step.Tools) == 0 {
			continue
		}
		out = append(out, step)
	}
	if max > 0 && len(out) > max {
		out = out[len(out)-max:]
	}
	return out, nil
}

// ---- live sessions (Claude Code processes on this machine) ----

// LiveSession is a Claude Code process registered in ~/.claude/sessions.
type LiveSession struct {
	Pid       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
	Kind      string `json:"kind"`
	Status    string `json:"status"`
	Name      string `json:"name"`
	Tmux      string `json:"tmux"`
	StartedAt int64  `json:"startedAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

// LiveSessions returns running Claude Code sessions keyed by session id.
func LiveSessions(configDir string) map[string]LiveSession {
	out := map[string]LiveSession{}
	files, _ := filepath.Glob(filepath.Join(configDir, "sessions", "*.json"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var ls LiveSession
		if json.Unmarshal(b, &ls) != nil || ls.Pid <= 0 || ls.SessionID == "" {
			continue
		}
		cmd, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", ls.Pid))
		if err != nil || !bytes.Contains(cmd, []byte("claude")) {
			continue
		}
		out[ls.SessionID] = ls
	}
	return out
}

// FileChange is a file Claude edited in a session: its content before the
// first edit (nil when Claude created it) and where it lives.
type FileChange struct {
	Path     string
	Original *string
	Order    int
}

// FileChanges reads a session's file checkpoints (file-history entries in the
// transcript, backups in configDir/file-history/<session>).
func FileChanges(sessionPath, configDir, cwd string) ([]FileChange, error) {
	f, err := os.Open(sessionPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sid := strings.TrimSuffix(filepath.Base(sessionPath), ".jsonl")
	type first struct {
		version int
		backup  *string
		order   int
	}
	seen := map[string]*first{}
	br := bufioReader(f)
	n := 0
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 && bytes.Contains(line, []byte(`"file-history-delta"`)) {
			var e struct {
				Type         string `json:"type"`
				TrackingPath string `json:"trackingPath"`
				Backup       struct {
					BackupFileName *string `json:"backupFileName"`
					Version        int     `json:"version"`
					RealParentDir  string  `json:"realParentDir"`
				} `json:"backup"`
			}
			if json.Unmarshal(line, &e) == nil && e.Type == "file-history-delta" && e.TrackingPath != "" {
				p := e.TrackingPath
				if !filepath.IsAbs(p) {
					base := cwd
					if e.Backup.RealParentDir != "" && filepath.Base(p) == p {
						base = e.Backup.RealParentDir
					}
					p = filepath.Join(base, p)
				}
				if cur := seen[p]; cur == nil || e.Backup.Version < cur.version {
					n++
					seen[p] = &first{version: e.Backup.Version, backup: e.Backup.BackupFileName, order: n}
				}
			}
		}
		if err != nil {
			break
		}
	}
	var out []FileChange
	for p, fi := range seen {
		fc := FileChange{Path: p, Order: fi.order}
		if fi.backup != nil {
			if b, err := os.ReadFile(filepath.Join(configDir, "file-history", sid, *fi.backup)); err == nil {
				s := string(b)
				fc.Original = &s
			}
		}
		out = append(out, fc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Order < out[j].Order })
	return out, nil
}

func bufioReader(f *os.File) *bufio.Reader { return bufio.NewReaderSize(f, 1<<20) }

// PromptsAfter counts the user prompts written after the message with the
// given uuid (scanning from the end). It returns -1 when the uuid isn't found.
// A non-zero count means the conversation continued elsewhere (e.g. in a
// terminal) since agentc last saw it.
func PromptsAfter(path, uuid string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return 0, err
	}
	r := &backwardLines{f: f, pos: st.Size()}
	needle := []byte(uuid)
	n := 0
	for {
		line, ok := r.next()
		if !ok {
			return -1, nil
		}
		if bytes.Contains(line, needle) {
			var e Entry
			if json.Unmarshal(line, &e) == nil && (e.UUID == uuid || (e.Type == "attachment" && bytes.Contains(e.Attachment, []byte(`"source_uuid":"`+uuid+`"`)))) {
				return n, nil
			}
		}
		isUser := bytes.Contains(line, []byte(`"type":"user"`)) && !bytes.Contains(line, []byte(`"tool_result"`))
		isQueued := bytes.Contains(line, []byte(`"queued_command"`))
		if !isUser && !isQueued {
			continue
		}
		var e Entry
		if json.Unmarshal(line, &e) != nil || e.IsSidechain || e.IsMeta || e.IsCompactSummary {
			continue
		}
		if PromptOf(&e) != "" {
			n++
		}
	}
}
