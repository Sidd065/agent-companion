package claude

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
)

// replay streams a stored conversation to the session engine as ACP updates.
func (a *Agent) replay(sid, cwd string) error {
	cfg := ConfigDir(a.o.HomeDir)
	path, err := FindSession(cfg, sid, cwd)
	cut := a.o.ResumeAt
	if err != nil && a.o.ForkFrom != "" {
		// A fork that hasn't been written yet: show the parent up to the fork point.
		path, err = FindSession(cfg, a.o.ForkFrom, cwd)
	}
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	chain, truncated, err := ReadChain(path, a.o.ReplayLimit, 96<<20)
	if err != nil {
		return err
	}
	if cut != "" {
		for i, e := range chain {
			if e.UUID == cut {
				chain = chain[:i+1]
				break
			}
		}
	}
	if truncated {
		a.notify(ExtNotice, map[string]any{"level": "info", "text": "Earlier messages of this long conversation aren't shown."})
	}
	ReplayUpdates(chain, cwd, a.update, func(level, text string) {
		a.notify(ExtNotice, map[string]any{"level": level, "text": text})
	})
	return nil
}

var (
	localOutRe   = regexp.MustCompile(`(?s)<local-command-std(?:out|err)>(.*?)</local-command-std(?:out|err)>`)
	taskSumRe    = regexp.MustCompile(`(?s)<summary>(.*?)</summary>`)
	bashInRe     = regexp.MustCompile(`(?s)<bash-input>(.*?)</bash-input>`)
	bashOutRe    = regexp.MustCompile(`(?s)<bash-stdout>(.*?)</bash-stdout>`)
	bashErrRe    = regexp.MustCompile(`(?s)<bash-stderr>(.*?)</bash-stderr>`)
	ansiRe       = regexp.MustCompile("\x1b\\[[0-9;]*[A-Za-z]")
	noResponseRe = regexp.MustCompile(`^\(?no (content|response)( requested)?\.?\)?$`)
)

// ReplayUpdates converts transcript entries into ACP session updates.
func ReplayUpdates(chain []*Entry, cwd string, emit func(map[string]any), notice func(level, text string)) {
	type tool struct {
		name   string
		input  map[string]any
		hidden bool
	}
	tools := map[string]*tool{}
	userText := func(e *Entry, text string) {
		emit(map[string]any{"sessionUpdate": "user_message_chunk", "content": map[string]any{"type": "text", "text": text},
			"_meta": map[string]any{"agentc": map[string]any{"uuid": e.UUID, "ts": e.ts()}}})
	}
	agentText := func(text string) {
		emit(map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": text}})
	}
	for _, e := range chain {
		switch e.Type {
		case "attachment":
			var at struct {
				Type        string          `json:"type"`
				Prompt      json.RawMessage `json:"prompt"`
				CommandMode string          `json:"commandMode"`
			}
			if json.Unmarshal(e.Attachment, &at) != nil || at.Type != "queued_command" {
				continue
			}
			t, _ := contentText(at.Prompt)
			if strings.HasPrefix(strings.TrimSpace(t), "<task-notification>") {
				if m := taskSumRe.FindStringSubmatch(t); m != nil {
					notice("info", strings.TrimSpace(m[1]))
				}
				continue
			}
			if at.CommandMode != "" && at.CommandMode != "prompt" {
				continue
			}
			if strings.TrimSpace(t) != "" {
				userText(e, t)
			}
		case "system":
			switch e.Subtype {
			case "local_command":
				var s string
				_ = json.Unmarshal(e.Content, &s)
				if m := localOutRe.FindStringSubmatch(s); m != nil {
					s = m[1]
				}
				s = strings.TrimSpace(ansiRe.ReplaceAllString(s, ""))
				if s != "" {
					agentText(s)
				}
			case "compact_boundary":
				notice("info", "Conversation compacted")
			default:
				if e.Level == "error" || e.Level == "warning" {
					var s string
					if json.Unmarshal(e.Content, &s) == nil && strings.TrimSpace(s) != "" {
						lv := "warn"
						if e.Level == "error" {
							lv = "error"
						}
						notice(lv, s)
					}
				}
			}
		case "user":
			if e.IsMeta {
				continue
			}
			if e.IsCompactSummary {
				continue // the compact_boundary notice stands for it
			}
			var m struct {
				Content json.RawMessage `json:"content"`
			}
			if json.Unmarshal(e.Message, &m) != nil {
				continue
			}
			var s string
			if json.Unmarshal(m.Content, &s) == nil {
				replayUserString(e, s, userText, agentText, notice)
				continue
			}
			var blocks []block
			if json.Unmarshal(m.Content, &blocks) != nil {
				continue
			}
			var results []block
			first := true
			for _, b := range blocks {
				switch b.Type {
				case "tool_result":
					results = append(results, b)
				case "text":
					if first {
						replayUserString(e, b.Text, userText, agentText, notice)
						first = false
					} else if t, skip := promptText(b.Text); !skip {
						userText(e, t)
					}
				}
			}
			// Images (the block type above doesn't keep their source).
			var imgs []struct {
				Type   string `json:"type"`
				Source struct {
					Type      string `json:"type"`
					MediaType string `json:"media_type"`
					Data      string `json:"data"`
				} `json:"source"`
			}
			if json.Unmarshal(m.Content, &imgs) == nil {
				for _, im := range imgs {
					if im.Type == "image" && im.Source.Type == "base64" && im.Source.Data != "" && len(im.Source.Data) < 12<<20 {
						emit(map[string]any{"sessionUpdate": "user_message_chunk", "content": map[string]any{"type": "image", "mimeType": im.Source.MediaType, "data": im.Source.Data},
							"_meta": map[string]any{"agentc": map[string]any{"uuid": e.UUID, "ts": e.ts()}}})
					}
				}
			}
			for _, b := range results {
				t := tools[b.ToolUseID]
				if t == nil || t.hidden {
					continue
				}
				var tur json.RawMessage
				if len(results) == 1 {
					tur = e.ToolUseResult
				}
				status, rawOut, content := ToolResult(t.name, t.input, b.Content, b.IsError, tur)
				u := map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": b.ToolUseID, "status": status}
				if rawOut != nil {
					u["rawOutput"] = rawOut
				}
				if content != nil {
					u["content"] = content
				}
				emit(u)
			}
		case "assistant":
			var m struct {
				Model   string            `json:"model"`
				Content []json.RawMessage `json:"content"`
			}
			if json.Unmarshal(e.Message, &m) != nil {
				continue
			}
			for _, rb := range m.Content {
				var b block
				if json.Unmarshal(rb, &b) != nil {
					continue
				}
				switch b.Type {
				case "text":
					t := strings.TrimSpace(b.Text)
					if t == "" || (m.Model == "<synthetic>" && noResponseRe.MatchString(strings.ToLower(t))) {
						continue
					}
					if e.IsAPIError {
						notice("error", t)
						continue
					}
					agentText(b.Text)
				case "thinking":
					if strings.TrimSpace(b.Thinking) != "" {
						emit(map[string]any{"sessionUpdate": "agent_thought_chunk", "content": map[string]any{"type": "text", "text": b.Thinking}})
					}
				case "tool_use":
					var input map[string]any
					_ = json.Unmarshal(b.Input, &input)
					tools[b.ID] = &tool{name: b.Name, input: input, hidden: b.Name == "TodoWrite"}
					if b.Name == "TodoWrite" {
						emit(map[string]any{"sessionUpdate": "plan", "entries": TodosToPlan(input)})
						continue
					}
					ti := DescribeTool(b.Name, input, cwd)
					u := map[string]any{"sessionUpdate": "tool_call", "toolCallId": b.ID, "name": b.Name, "title": ti.Title,
						"kind": ti.Kind, "status": "completed", "rawInput": input}
					if len(ti.Locations) > 0 {
						u["locations"] = ti.Locations
					}
					if len(ti.Content) > 0 {
						u["content"] = ti.Content
					}
					emit(u)
				}
			}
		}
	}
}

func replayUserString(e *Entry, s string, userText func(*Entry, string), agentText func(string), notice func(string, string)) {
	t := strings.TrimSpace(s)
	switch {
	case strings.HasPrefix(t, "<local-command-stdout>") || strings.HasPrefix(t, "<local-command-stderr>"):
		if m := localOutRe.FindStringSubmatch(t); m != nil {
			if out := strings.TrimSpace(ansiRe.ReplaceAllString(m[1], "")); out != "" {
				agentText(out)
			}
		}
		return
	case strings.HasPrefix(t, "<bash-stdout>") || strings.HasPrefix(t, "<bash-stderr>"):
		var parts []string
		if m := bashOutRe.FindStringSubmatch(t); m != nil && strings.TrimSpace(m[1]) != "" {
			parts = append(parts, strings.TrimSpace(m[1]))
		}
		if m := bashErrRe.FindStringSubmatch(t); m != nil && strings.TrimSpace(m[1]) != "" {
			parts = append(parts, strings.TrimSpace(m[1]))
		}
		if len(parts) > 0 {
			agentText("```\n" + ansiRe.ReplaceAllString(strings.Join(parts, "\n"), "") + "\n```")
		}
		return
	case strings.HasPrefix(t, "<bash-input>"):
		if m := bashInRe.FindStringSubmatch(t); m != nil {
			userText(e, "! "+strings.TrimSpace(m[1]))
		}
		return
	case strings.HasPrefix(t, "[Request interrupted"):
		notice("info", "Interrupted")
		return
	case strings.HasPrefix(t, "<task-notification>"):
		if m := taskSumRe.FindStringSubmatch(t); m != nil {
			notice("info", strings.TrimSpace(m[1]))
		}
		return
	}
	text, skip := promptText(s)
	if skip {
		return
	}
	userText(e, text)
}
