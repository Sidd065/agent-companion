package claude

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// ToolInfo is how a Claude tool call is presented as an ACP tool_call.
type ToolInfo struct {
	Title     string
	Kind      string // read|edit|delete|move|search|execute|think|fetch|other
	Locations []map[string]any
	Content   []map[string]any
	Hidden    bool // shown another way (TodoWrite becomes the plan card)
}

func str(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

func short(s string, n int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i] + " …"
	}
	if len([]rune(s)) > n {
		return string([]rune(s)[:n]) + "…"
	}
	return s
}

func loc(path string, line int) map[string]any {
	m := map[string]any{"path": path}
	if line > 0 {
		m["line"] = line
	}
	return m
}

func diffContent(path, oldText, newText string, isNew bool) map[string]any {
	m := map[string]any{"type": "diff", "path": path, "newText": newText}
	if !isNew {
		m["oldText"] = oldText
	}
	return m
}

// McpName splits mcp__server__tool.
func McpName(name string) (server, tool string, ok bool) {
	if !strings.HasPrefix(name, "mcp__") {
		return "", "", false
	}
	rest := strings.TrimPrefix(name, "mcp__")
	i := strings.Index(rest, "__")
	if i < 0 {
		return rest, "", true
	}
	return rest[:i], rest[i+2:], true
}

// DescribeTool maps a tool_use (name + input) to ACP presentation.
func DescribeTool(name string, input map[string]any, cwd string) ToolInfo {
	rel := func(p string) string {
		if cwd != "" && strings.HasPrefix(p, cwd+"/") {
			return strings.TrimPrefix(p, cwd+"/")
		}
		return p
	}
	num := func(k string) int {
		if f, ok := input[k].(float64); ok {
			return int(f)
		}
		return 0
	}
	switch name {
	case "Bash":
		cmd := str(input, "command")
		ti := ToolInfo{Title: short(firstNonEmpty(cmd, str(input, "description")), 200), Kind: "execute"}
		return ti
	case "BashOutput", "TaskOutput":
		return ToolInfo{Title: "Read output of " + firstNonEmpty(str(input, "bash_id"), str(input, "task_id"), str(input, "shell_id")), Kind: "other"}
	case "KillShell", "KillBash", "TaskStop":
		return ToolInfo{Title: "Stop " + firstNonEmpty(str(input, "shell_id"), str(input, "bash_id"), str(input, "task_id")), Kind: "other"}
	case "Read":
		p := str(input, "file_path")
		t := rel(p)
		if off := num("offset"); off > 0 {
			t += fmt.Sprintf(" (from line %d)", off)
		}
		return ToolInfo{Title: t, Kind: "read", Locations: []map[string]any{loc(p, num("offset"))}}
	case "Write":
		p := str(input, "file_path")
		return ToolInfo{Title: rel(p), Kind: "edit", Locations: []map[string]any{loc(p, 0)},
			Content: []map[string]any{diffContent(p, "", str(input, "content"), true)}}
	case "Edit":
		p := str(input, "file_path")
		return ToolInfo{Title: rel(p), Kind: "edit", Locations: []map[string]any{loc(p, 0)},
			Content: []map[string]any{diffContent(p, str(input, "old_string"), str(input, "new_string"), false)}}
	case "MultiEdit":
		p := str(input, "file_path")
		ti := ToolInfo{Title: rel(p), Kind: "edit", Locations: []map[string]any{loc(p, 0)}}
		if edits, ok := input["edits"].([]any); ok {
			for _, e := range edits {
				if em, ok := e.(map[string]any); ok {
					ti.Content = append(ti.Content, diffContent(p, str(em, "old_string"), str(em, "new_string"), false))
				}
			}
		}
		return ti
	case "NotebookEdit":
		p := str(input, "notebook_path")
		return ToolInfo{Title: rel(p), Kind: "edit", Locations: []map[string]any{loc(p, 0)}}
	case "Glob":
		t := str(input, "pattern")
		if p := str(input, "path"); p != "" {
			t += " in " + rel(p)
		}
		return ToolInfo{Title: t, Kind: "search"}
	case "Grep":
		t := "\"" + str(input, "pattern") + "\""
		if p := str(input, "path"); p != "" {
			t += " in " + rel(p)
		}
		if g := str(input, "glob"); g != "" {
			t += " (" + g + ")"
		}
		return ToolInfo{Title: t, Kind: "search"}
	case "WebFetch":
		return ToolInfo{Title: str(input, "url"), Kind: "fetch"}
	case "WebSearch":
		return ToolInfo{Title: str(input, "query"), Kind: "fetch"}
	case "TodoWrite":
		return ToolInfo{Title: "Update to-dos", Kind: "think", Hidden: true}
	case "Task", "Agent":
		t := firstNonEmpty(str(input, "description"), short(str(input, "prompt"), 80))
		if st := str(input, "subagent_type"); st != "" {
			t = st + ": " + t
		}
		return ToolInfo{Title: t, Kind: "think"}
	case "AskUserQuestion":
		n := 0
		if qs, ok := input["questions"].([]any); ok {
			n = len(qs)
		}
		if n == 1 {
			if q, ok := input["questions"].([]any)[0].(map[string]any); ok {
				return ToolInfo{Title: short(str(q, "question"), 160), Kind: "other"}
			}
		}
		return ToolInfo{Title: fmt.Sprintf("%d questions", n), Kind: "other"}
	case "ExitPlanMode":
		return ToolInfo{Title: "Plan ready for review", Kind: "other"}
	case "EnterPlanMode":
		return ToolInfo{Title: "Enter plan mode", Kind: "other"}
	case "Skill":
		return ToolInfo{Title: firstNonEmpty(str(input, "skill"), str(input, "command"), str(input, "name")), Kind: "other"}
	case "SlashCommand":
		return ToolInfo{Title: str(input, "command"), Kind: "other"}
	case "ToolSearch":
		return ToolInfo{Title: str(input, "query"), Kind: "search"}
	case "LSP":
		return ToolInfo{Title: firstNonEmpty(str(input, "operation"), "LSP") + " " + rel(str(input, "filePath")), Kind: "search"}
	}
	if server, tool, ok := McpName(name); ok {
		args := ""
		if len(input) > 0 {
			b, _ := json.Marshal(input)
			args = short(string(b), 100)
		}
		t := server + " · " + tool
		if args != "" && args != "{}" {
			t += " " + args
		}
		return ToolInfo{Title: t, Kind: "other"}
	}
	// Unknown tool: show its most descriptive argument.
	for _, k := range []string{"description", "command", "path", "file_path", "url", "query", "prompt", "name"} {
		if v := str(input, k); v != "" {
			return ToolInfo{Title: short(v, 160), Kind: "other"}
		}
	}
	return ToolInfo{Title: name, Kind: "other"}
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

var exitCodeRe = regexp.MustCompile(`(?m)^Exit code (-?\d+)`)

// ResultText flattens a tool_result content (string or blocks).
func ResultText(content json.RawMessage) (string, int) {
	if len(content) == 0 {
		return "", 0
	}
	var s string
	if json.Unmarshal(content, &s) == nil {
		return s, 0
	}
	var blocks []map[string]any
	if json.Unmarshal(content, &blocks) != nil {
		return string(content), 0
	}
	var parts []string
	images := 0
	for _, b := range blocks {
		switch b["type"] {
		case "text":
			parts = append(parts, str(b, "text"))
		case "image":
			images++
		case "tool_reference":
			parts = append(parts, "→ "+str(b, "tool_name"))
		}
	}
	return strings.Join(parts, "\n"), images
}

// ToolResult builds the tool_call_update fields for a finished tool.
// toolUseResult is the structured `tool_use_result` (may be empty, e.g. replayed history).
func ToolResult(name string, input map[string]any, content json.RawMessage, isError bool, toolUseResult json.RawMessage) (status string, rawOutput any, acpContent []map[string]any) {
	status = "completed"
	if isError {
		status = "failed"
	}
	text, images := ResultText(content)
	var tur map[string]any
	if len(toolUseResult) > 0 && toolUseResult[0] == '{' {
		_ = json.Unmarshal(toolUseResult, &tur)
	}
	textContent := func(t string) []map[string]any {
		if strings.TrimSpace(t) == "" && images == 0 {
			return nil
		}
		out := []map[string]any{}
		if strings.TrimSpace(t) != "" {
			out = append(out, map[string]any{"type": "content", "content": map[string]any{"type": "text", "text": t}})
		}
		if images > 0 {
			out = append(out, map[string]any{"type": "content", "content": map[string]any{"type": "text", "text": fmt.Sprintf("[%d image(s)]", images)}})
		}
		return out
	}
	switch name {
	case "Bash":
		out := map[string]any{}
		body := text
		if tur != nil {
			so, se := str(tur, "stdout"), str(tur, "stderr")
			body = strings.TrimRight(so, "\n")
			if strings.TrimSpace(se) != "" {
				if body != "" {
					body += "\n"
				}
				body += strings.TrimRight(se, "\n")
			}
			if v, ok := tur["interrupted"].(bool); ok && v {
				out["interrupted"] = true
			}
			if id := str(tur, "backgroundTaskId"); id != "" {
				out["backgroundTaskId"] = id
				if body == "" {
					body = "Running in the background (" + id + ")"
				}
			}
			if isError && body == "" {
				body = text
			}
		}
		code := 0
		if m := exitCodeRe.FindStringSubmatch(text); m != nil {
			fmt.Sscanf(m[1], "%d", &code)
			if tur == nil {
				body = strings.TrimSpace(exitCodeRe.ReplaceAllString(text, ""))
			}
		} else if isError {
			code = 1
		}
		if _, bg := out["backgroundTaskId"]; !bg {
			out["exit_code"] = code
		}
		out["output"] = body
		return status, out, textContent(body)
	case "Read":
		out := map[string]any{}
		if f, ok := tur["file"].(map[string]any); ok {
			for _, k := range []string{"filePath", "numLines", "startLine", "totalLines"} {
				if v, ok := f[k]; ok {
					out[k] = v
				}
			}
		}
		if t := str(tur, "type"); t != "" {
			out["type"] = t
		}
		return status, out, textContent(text)
	case "Write", "Edit", "MultiEdit":
		out := map[string]any{}
		for _, k := range []string{"filePath", "type", "structuredPatch", "userModified", "replaceAll"} {
			if v, ok := tur[k]; ok && v != nil {
				out[k] = v
			}
		}
		if isError {
			return status, out, textContent(text)
		}
		var c []map[string]any
		p := str(input, "file_path")
		switch name {
		case "Write":
			orig, hasOrig := tur["originalFile"].(string)
			c = append(c, diffContent(p, orig, str(input, "content"), !hasOrig || tur["originalFile"] == nil))
		case "Edit":
			c = append(c, diffContent(p, str(input, "old_string"), str(input, "new_string"), false))
		case "MultiEdit":
			if edits, ok := input["edits"].([]any); ok {
				for _, e := range edits {
					if em, ok := e.(map[string]any); ok {
						c = append(c, diffContent(p, str(em, "old_string"), str(em, "new_string"), false))
					}
				}
			}
		}
		return status, out, c
	case "Task", "Agent":
		out := map[string]any{}
		for _, k := range []string{"status", "agentId", "totalDurationMs", "totalTokens", "totalToolUseCount", "agentType"} {
			if v, ok := tur[k]; ok && v != nil {
				out[k] = v
			}
		}
		return status, out, textContent(stripHandBack(text))
	case "AskUserQuestion":
		out := map[string]any{}
		if a, ok := tur["answers"]; ok {
			out["answers"] = a
		}
		return status, out, textContent(text)
	case "ExitPlanMode":
		return status, map[string]any{"approved": !isError}, textContent(text)
	}
	var raw any
	if len(toolUseResult) > 0 && len(toolUseResult) < 16<<10 {
		var v any
		if json.Unmarshal(toolUseResult, &v) == nil {
			raw = v
		}
	}
	return status, raw, textContent(text)
}

// stripHandBack drops the wrapper line Claude puts before a subagent's report.
func stripHandBack(s string) string {
	if strings.HasPrefix(s, "[Subagent hand-back]") {
		if i := strings.Index(s, "\n"); i >= 0 {
			return strings.TrimSpace(s[i+1:])
		}
	}
	return s
}

// TodosToPlan converts TodoWrite input to ACP plan entries.
func TodosToPlan(input map[string]any) []map[string]any {
	var out []map[string]any
	todos, _ := input["todos"].([]any)
	for _, t := range todos {
		tm, ok := t.(map[string]any)
		if !ok {
			continue
		}
		st := str(tm, "status")
		if st == "" {
			st = "pending"
		}
		out = append(out, map[string]any{"content": firstNonEmpty(str(tm, "content"), str(tm, "activeForm")), "status": st,
			"priority": "medium", "activeForm": str(tm, "activeForm")})
	}
	return out
}

// shortPath shows a path relative to home.
func shortPath(p, home string) string {
	if home != "" && strings.HasPrefix(p, home+"/") {
		return "~/" + strings.TrimPrefix(p, home+"/")
	}
	return filepath.Clean(p)
}
