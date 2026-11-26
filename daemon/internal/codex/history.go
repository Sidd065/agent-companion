package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Thread matches Codex 0.172.0 v2/Thread.json (timestamps in Unix SECONDS).
type Thread struct {
	ID              string  `json:"id"`
	Name            *string `json:"name,omitempty"`
	Preview         string  `json:"preview,omitempty"`
	Cwd             string  `json:"cwd"`
	CreatedAt       int64   `json:"createdAt"`
	UpdatedAt       int64   `json:"updatedAt"`
	Model           *string `json:"model,omitempty"`
	ModelProvider   string  `json:"modelProvider,omitempty"`
	ReasoningEffort *string `json:"reasoningEffort,omitempty"`
	ForkedFromID    *string `json:"forkedFromId,omitempty"`
	ParentThreadID  *string `json:"parentThreadId,omitempty"`
	Path            *string `json:"path,omitempty"`
	Turns           []Turn  `json:"turns,omitempty"`
}

// Turn matches Codex 0.172.0 v2/Turn.json.
type Turn struct {
	ID     string            `json:"id"`
	Status string            `json:"status"` // inProgress | completed | interrupted | failed
	Items  []json.RawMessage `json:"items,omitempty"`
	Error  *TurnError        `json:"error,omitempty"`
}

// TurnError matches Codex Turn.error.
type TurnError struct {
	Message           string  `json:"message"`
	AdditionalDetails *string `json:"additionalDetails,omitempty"`
}

// RolloutSummary is a lightweight zero-spawn thread summary scanned from disk.
type RolloutSummary struct {
	ID            string
	Title         string
	Cwd           string
	Model         string
	ModelProvider string
	UpdatedAt     int64 // Unix milliseconds
	CreatedAt     int64 // Unix milliseconds
	Archived      bool
	Path          string
	Steps         int
}

var rolloutUUIDRe = regexp.MustCompile(`([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})\.jsonl$`)

// ExtractRolloutID extracts the trailing thread UUID from a Codex rollout filename.
func ExtractRolloutID(filename string) string {
	m := rolloutUUIDRe.FindStringSubmatch(filepath.Base(filename))
	if len(m) == 2 {
		return strings.ToLower(m[1])
	}
	return ""
}

// FindRolloutPath checks <codexHome>/sessions and <codexHome>/archived_sessions for thread id.
func FindRolloutPath(id string, codexHomes ...string) (path string, archived bool, ok bool) {
	id = strings.TrimSpace(id)
	if id == "" || strings.ContainsAny(id, "/\\") || strings.Contains(id, "..") {
		return "", false, false
	}
	wantSuffix := "-" + strings.ToLower(id) + ".jsonl"
	exactName := strings.ToLower(id) + ".jsonl"
	for _, home := range codexHomes {
		if strings.TrimSpace(home) == "" {
			continue
		}
		for _, sub := range []struct {
			dir  string
			arch bool
		}{
			{filepath.Join(home, "sessions"), false},
			{filepath.Join(home, "archived_sessions"), true},
		} {
			var found string
			_ = filepath.WalkDir(sub.dir, func(p string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return nil
				}
				low := strings.ToLower(d.Name())
				if strings.HasSuffix(low, wantSuffix) || low == exactName {
					found = p
					return fs.SkipAll
				}
				return nil
			})
			if found != "" {
				return found, sub.arch, true
			}
		}
	}
	return "", false, false
}

// HasThread reports whether thread id exists in any of the given Codex home directories.
func HasThread(id string, codexHomes ...string) bool {
	_, _, ok := FindRolloutPath(id, codexHomes...)
	return ok
}

// ReadRolloutSummary parses header metadata (cwd, model, model_provider, title) for thread id.
func ReadRolloutSummary(id string, codexHomes ...string) (RolloutSummary, bool) {
	p, arch, ok := FindRolloutPath(id, codexHomes...)
	if !ok {
		return RolloutSummary{}, false
	}
	info, err := os.Stat(p)
	if err != nil {
		return RolloutSummary{}, false
	}
	sum, isSubagent := parseRolloutHeader(p, strings.TrimSpace(id), info, arch)
	if isSubagent || sum.ID == "" {
		return RolloutSummary{}, false
	}
	return sum, true
}

// ScanRolloutThreads scans Codex rollout JSONL files without spawning any child processes.
func ScanRolloutThreads(codexHomes ...string) []RolloutSummary {
	seen := map[string]bool{}
	var out []RolloutSummary

	for _, home := range codexHomes {
		if strings.TrimSpace(home) == "" {
			continue
		}
		for _, sub := range []struct {
			dir  string
			arch bool
		}{
			{filepath.Join(home, "sessions"), false},
			{filepath.Join(home, "archived_sessions"), true},
		} {
			_ = filepath.WalkDir(sub.dir, func(p string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
					return nil
				}
				id := ExtractRolloutID(d.Name())
				info, ierr := d.Info()
				if ierr != nil {
					return nil
				}
				sum, isSubagent := parseRolloutHeader(p, id, info, sub.arch)
				if isSubagent || sum.ID == "" || seen[sum.ID] {
					return nil
				}
				seen[sum.ID] = true
				out = append(out, sum)
				return nil
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out
}

func parseRolloutHeader(path, fallbackID string, info fs.FileInfo, archived bool) (RolloutSummary, bool) {
	sum := RolloutSummary{
		ID:        fallbackID,
		UpdatedAt: info.ModTime().UnixMilli(),
		CreatedAt: info.ModTime().UnixMilli(),
		Archived:  archived,
		Path:      path,
	}
	f, err := os.Open(path)
	if err != nil {
		return sum, false
	}
	defer f.Close()

	br := bufio.NewReaderSize(f, 32*1024)
	for lineNo := 0; lineNo < 40; lineNo++ {
		line, rerr := br.ReadString('\n')
		if len(line) > 0 {
			var rec struct {
				Type      string          `json:"type"`
				Timestamp string          `json:"timestamp"`
				Payload   json.RawMessage `json:"payload"`
			}
			if json.Unmarshal([]byte(line), &rec) == nil {
				switch rec.Type {
				case "session_meta":
					var meta struct {
						ID            string `json:"id"`
						Cwd           string `json:"cwd"`
						Timestamp     string `json:"timestamp"`
						Source        any    `json:"source"`
						Model         string `json:"model"`
						ModelProvider string `json:"model_provider"`
						ModelProvAlt  string `json:"modelProvider"`
					}
					if json.Unmarshal(rec.Payload, &meta) == nil {
						if meta.ID != "" {
							sum.ID = meta.ID
						}
						if meta.Cwd != "" {
							sum.Cwd = meta.Cwd
						}
						if meta.Model != "" {
							sum.Model = meta.Model
						}
						if meta.ModelProvider != "" {
							sum.ModelProvider = meta.ModelProvider
						} else if meta.ModelProvAlt != "" {
							sum.ModelProvider = meta.ModelProvAlt
						}
						if ts, perr := time.Parse(time.RFC3339Nano, meta.Timestamp); perr == nil {
							sum.CreatedAt = ts.UnixMilli()
						}
						// Subagent rollouts specify {"subagent": ...} in source.
						if sm, ok := meta.Source.(map[string]any); ok && sm["subagent"] != nil {
							return sum, true
						}
					}
				case "turn_context":
					var tc struct {
						Model string `json:"model"`
					}
					if json.Unmarshal(rec.Payload, &tc) == nil && tc.Model != "" && sum.Model == "" {
						sum.Model = tc.Model
					}
				case "event_msg":
					var ev struct {
						Type    string `json:"type"`
						Message string `json:"message"`
					}
					if json.Unmarshal(rec.Payload, &ev) == nil && ev.Type == "user_message" && sum.Title == "" {
						sum.Title = clipTitle(ev.Message)
						sum.Steps++
					}
				case "response_item":
					var ri struct {
						Type    string `json:"type"`
						Role    string `json:"role"`
						Content []struct {
							Type string `json:"type"`
							Text string `json:"text"`
						} `json:"content"`
					}
					if json.Unmarshal(rec.Payload, &ri) == nil && ri.Role == "user" && sum.Title == "" {
						for _, c := range ri.Content {
							if (c.Type == "input_text" || c.Type == "text") && strings.TrimSpace(c.Text) != "" && !strings.HasPrefix(strings.TrimSpace(c.Text), "<environment_context>") && !strings.HasPrefix(strings.TrimSpace(c.Text), "# AGENTS.md") {
								sum.Title = clipTitle(c.Text)
								break
							}
						}
					}
				}
			}
		}
		if rerr != nil {
			break
		}
	}
	return sum, false
}

func clipTitle(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > 60 {
		return strings.TrimSpace(string(r[:57])) + "…"
	}
	return s
}

// ReadThreadRPC calls `thread/read` (`includeTurns: true`) via a short-lived control app-server.
func ReadThreadRPC(ctx context.Context, cfg ProcConfig, threadID string, includeTurns bool) (*Thread, error) {
	cfg.Handler = noopHandler{}
	var res struct {
		Thread Thread `json:"thread"`
	}
	err := RunControlCall(ctx, cfg, "thread/read", map[string]any{
		"threadId":     threadID,
		"includeTurns": includeTurns,
	}, &res)
	if err != nil {
		return nil, err
	}
	return &res.Thread, nil
}

// ForkThreadRPC calls `thread/fork` (`{threadId, beforeTurnId, cwd, ...}`) via control app-server.
func ForkThreadRPC(ctx context.Context, cfg ProcConfig, threadID, beforeTurnID, cwd, model string) (*Thread, error) {
	cfg.Handler = noopHandler{}
	params := map[string]any{
		"threadId": threadID,
	}
	if beforeTurnID != "" {
		params["beforeTurnId"] = beforeTurnID
	}
	if cwd != "" {
		params["cwd"] = cwd
	}
	if model != "" {
		params["model"] = model
	}
	if cfg.UseOpenRouter {
		params["modelProvider"] = "openrouter"
	}
	var res struct {
		Thread Thread `json:"thread"`
	}
	if err := RunControlCall(ctx, cfg, "thread/fork", params, &res); err != nil {
		return nil, err
	}
	return &res.Thread, nil
}

// RevertThreadRPC calls `thread/revert` (`{threadId, beforeTurnId}` — NOT thread/rollback!)
// via control app-server.
func RevertThreadRPC(ctx context.Context, cfg ProcConfig, threadID, beforeTurnID string) (*Thread, error) {
	cfg.Handler = noopHandler{}
	var res struct {
		Thread Thread `json:"thread"`
	}
	err := RunControlCall(ctx, cfg, "thread/revert", map[string]any{
		"threadId":     threadID,
		"beforeTurnId": beforeTurnID,
	}, &res)
	if err != nil {
		return nil, err
	}
	return &res.Thread, nil
}

// RenameThreadRPC calls `thread/name/set` (`{threadId, name}`).
func RenameThreadRPC(ctx context.Context, cfg ProcConfig, threadID, name string) error {
	cfg.Handler = noopHandler{}
	return RunControlCall(ctx, cfg, "thread/name/set", map[string]any{
		"threadId": threadID,
		"name":     name,
	}, nil)
}

// ArchiveThreadRPC calls `thread/archive` or `thread/unarchive`.
func ArchiveThreadRPC(ctx context.Context, cfg ProcConfig, threadID string, archived bool) error {
	cfg.Handler = noopHandler{}
	method := "thread/archive"
	if !archived {
		method = "thread/unarchive"
	}
	return RunControlCall(ctx, cfg, method, map[string]any{"threadId": threadID}, nil)
}

// DeleteThreadRPC removes persisted rollout files (both active and archived) and
// writer locks for threadID across codexHomes without spawning a control subprocess.
func DeleteThreadRPC(_ context.Context, cfg ProcConfig, threadID string, codexHomes ...string) error {
	id := strings.TrimSpace(threadID)
	if id == "" || strings.ContainsAny(id, "/\\") || strings.Contains(id, "..") {
		return nil
	}
	homes := make([]string, 0, len(codexHomes)+2)
	seen := map[string]bool{}
	addHome := func(h string) {
		h = strings.TrimSpace(h)
		if h != "" && !seen[h] {
			seen[h] = true
			homes = append(homes, h)
		}
	}
	for _, h := range codexHomes {
		addHome(h)
	}
	addHome(cfg.CodexHome)
	if strings.TrimSpace(cfg.UserHome) != "" {
		addHome(filepath.Join(cfg.UserHome, ".codex"))
	}
	var firstErr error
	for _, h := range homes {
		for {
			p, _, ok := FindRolloutPath(id, h)
			if !ok {
				break
			}
			if rerr := os.Remove(p); rerr != nil && !os.IsNotExist(rerr) {
				if firstErr == nil {
					firstErr = rerr
				}
				break
			}
		}
		_ = os.Remove(filepath.Join(h, "thread-writer-locks", id+".lock"))
	}
	return firstErr
}

// NativeModelsRPC queries `model/list` from `codex app-server` when native OpenAI auth is active.
func NativeModelsRPC(ctx context.Context, cfg ProcConfig) ([]CatalogModel, error) {
	cfg.UseOpenRouter = false
	cfg.Handler = noopHandler{}
	var res struct {
		Data []struct {
			ID                        string `json:"id"`
			Model                     string `json:"model"`
			DisplayName               string `json:"displayName"`
			Description               string `json:"description"`
			Hidden                    bool   `json:"hidden"`
			SupportedReasoningEfforts []struct {
				ReasoningEffort string `json:"reasoningEffort"`
			} `json:"supportedReasoningEfforts"`
			InputModalities []string `json:"inputModalities"`
		} `json:"data"`
	}
	if err := RunControlCall(ctx, cfg, "model/list", map[string]any{"limit": 200, "includeHidden": false}, &res); err != nil {
		return nil, err
	}
	var out []CatalogModel
	for _, m := range res.Data {
		if m.Hidden {
			continue
		}
		slug := firstNonEmpty(m.Model, m.ID)
		if slug == "" {
			continue
		}
		hasImg := false
		for _, mod := range m.InputModalities {
			if strings.EqualFold(mod, "image") {
				hasImg = true
			}
		}
		out = append(out, CatalogModel{
			ModelID:           slug,
			Name:              firstNonEmpty(m.DisplayName, slug),
			Description:       m.Description,
			Provider:          "openai",
			ContextWindow:     400000,
			SupportsReasoning: len(m.SupportedReasoningEfforts) > 0,
			SupportsTools:     true,
			SupportsImages:    hasImg,
		})
	}
	return out, nil
}

// ReplayThreadUpdates emits historical turns from a Codex Thread as ACP session/update notifications.
func ReplayThreadUpdates(thread *Thread, cwd string, emit func(map[string]any)) {
	if thread == nil || emit == nil {
		return
	}
	effectiveCwd := firstNonEmpty(thread.Cwd, cwd)
	for _, turn := range thread.Turns {
		for _, raw := range turn.Items {
			replayOneThreadItem(raw, effectiveCwd, emit)
		}
	}
}

func replayOneThreadItem(raw json.RawMessage, cwd string, emit func(map[string]any)) {
	var hdr struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &hdr) != nil || hdr.Type == "" {
		return
	}
	switch hdr.Type {
	case "userMessage":
		var um struct {
			ID      string `json:"id"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
				Path string `json:"path"`
				URL  string `json:"url"`
			} `json:"content"`
			StartedAtMs int64 `json:"startedAtMs"`
		}
		if json.Unmarshal(raw, &um) != nil {
			return
		}
		for _, c := range um.Content {
			switch c.Type {
			case "text":
				if c.Text != "" {
					emit(map[string]any{
						"sessionUpdate": "user_message_chunk",
						"content":       map[string]any{"type": "text", "text": c.Text},
						"_meta":         map[string]any{"agentc": map[string]any{"uuid": um.ID, "ts": um.StartedAtMs}},
					})
				}
			case "localImage":
				if c.Path != "" {
					emit(map[string]any{
						"sessionUpdate": "user_message_chunk",
						"content":       map[string]any{"type": "resource_link", "uri": "file://" + c.Path, "name": filepath.Base(c.Path)},
					})
				}
			}
		}
	case "agentMessage":
		var am struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(raw, &am) == nil && am.Text != "" {
			emit(map[string]any{
				"sessionUpdate": "agent_message_chunk",
				"content":       map[string]any{"type": "text", "text": am.Text},
			})
		}
	case "reasoning":
		var r struct {
			Summary []string `json:"summary"`
			Content []string `json:"content"`
		}
		if json.Unmarshal(raw, &r) == nil {
			txt := strings.Join(r.Summary, "\n")
			if txt == "" {
				txt = strings.Join(r.Content, "\n")
			}
			if txt != "" {
				emit(map[string]any{
					"sessionUpdate": "agent_thought_chunk",
					"content":       map[string]any{"type": "text", "text": txt},
				})
			}
		}
	case "plan":
		var pl struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(raw, &pl) == nil && pl.Text != "" {
			emit(map[string]any{
				"sessionUpdate": "agent_thought_chunk",
				"content":       map[string]any{"type": "text", "text": pl.Text},
			})
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
		if json.Unmarshal(raw, &ce) == nil {
			status := mapCodexToolStatus(ce.Status)
			outTxt := ""
			if ce.AggregatedOutput != nil {
				outTxt = *ce.AggregatedOutput
			}
			emit(map[string]any{
				"sessionUpdate": "tool_call",
				"toolCallId":    firstNonEmpty(ce.ID, "cmd"),
				"name":          "Bash",
				"kind":          "execute",
				"title":         "$ " + ce.Command,
				"status":        status,
				"rawInput":      map[string]any{"command": ce.Command, "cwd": firstNonEmpty(ce.Cwd, cwd)},
				"rawOutput":     map[string]any{"output": outTxt, "exitCode": ce.ExitCode, "durationMs": ce.DurationMs},
			})
		}
	case "fileChange":
		var fc struct {
			ID      string             `json:"id"`
			Status  string             `json:"status"`
			Changes []FileUpdateChange `json:"changes"`
		}
		if json.Unmarshal(raw, &fc) == nil {
			toolName, title, locs, content, rawIn := BuildFileChangePayloads(cwd, fc.Changes)
			emit(map[string]any{
				"sessionUpdate": "tool_call",
				"toolCallId":    firstNonEmpty(fc.ID, "edit"),
				"name":          toolName,
				"kind":          "edit",
				"title":         title,
				"status":        mapCodexToolStatus(fc.Status),
				"locations":     locs,
				"content":       content,
				"rawInput":      rawIn,
			})
		}
	}
}

func mapCodexToolStatus(st string) string {
	switch st {
	case "completed":
		return "completed"
	case "failed", "declined", "interrupted":
		return "failed"
	default:
		return "in_progress"
	}
}
