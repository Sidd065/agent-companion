// fakeacp is a deterministic, scripted ACP (Agent Client Protocol) agent for
// hermetic integration tests and Playwright desktop E2E tests.
//
// It speaks JSON-RPC 2.0 over newline-delimited stdio and supports:
//   - initialize, session/new, session/resume, session/load, session/list
//   - session/set_model, session/set_mode, session/set_config_option
//   - session/prompt, session/cancel
//   - session/request_permission (tool approval, AskUserQuestion, plan review)
//
// Prompt directives:
//
//	[edit:path:content]  writes file in cwd and emits ACP tool_call + diff
//	[perm]               requests approval for a shell command (or [perm:cmd])
//	[question]           sends an AskUserQuestion permission card
//	[plan]               sends a plan review card
//	[bash:cmd]           runs a command tool call (simulated or real with [realbash:cmd])
//	[slow:ms]            streams chunks slowly over ms milliseconds (cancellable!)
//	[error:msg]          fails the turn with an error
//	[markdown]           emits rich Markdown (code, table, inline & block TeX, mermaid)
//	[subagent]           emits a subagent tool call
package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type rpcMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcErr         `json:"error,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type sessionState struct {
	ID      string   `json:"id"`
	Cwd     string   `json:"cwd"`
	Model   string   `json:"model"`
	Mode    string   `json:"mode"`
	Thought string   `json:"thought"`
	History []string `json:"history"`
}

type server struct {
	wmu      sync.Mutex
	w        io.Writer
	mu       sync.Mutex
	sessions map[string]*sessionState
	pending  map[string]chan rpcMsg
	nextID   int
	cancels  map[string]chan struct{}
	stateDir string
}

func newServer(w io.Writer) *server {
	sd := os.Getenv("FAKEACP_STATE_DIR")
	if sd == "" {
		sd = filepath.Join(os.TempDir(), "fakeacp-state")
	}
	_ = os.MkdirAll(sd, 0o700)
	return &server{
		w:        w,
		sessions: map[string]*sessionState{},
		pending:  map[string]chan rpcMsg{},
		cancels:  map[string]chan struct{}{},
		stateDir: sd,
	}
}

func (s *server) send(m rpcMsg) {
	m.JSONRPC = "2.0"
	b, _ := json.Marshal(m)
	b = append(b, '\n')
	s.wmu.Lock()
	_, _ = s.w.Write(b)
	s.wmu.Unlock()
}

func (s *server) reply(id json.RawMessage, result any) {
	s.send(rpcMsg{ID: id, Result: result})
}

func (s *server) replyErr(id json.RawMessage, code int, msg string) {
	s.send(rpcMsg{ID: id, Error: &rpcErr{Code: code, Message: msg}})
}

func (s *server) notify(method string, params any) {
	raw, _ := json.Marshal(params)
	s.send(rpcMsg{Method: method, Params: raw})
}

func (s *server) update(sid string, upd any) {
	s.notify("session/update", map[string]any{"sessionId": sid, "update": upd})
}

func (s *server) callClient(method string, params any, cancelCh <-chan struct{}) (rpcMsg, error) {
	s.mu.Lock()
	s.nextID++
	idStr := strconv.Itoa(s.nextID)
	ch := make(chan rpcMsg, 1)
	s.pending[idStr] = ch
	s.mu.Unlock()

	raw, _ := json.Marshal(params)
	s.send(rpcMsg{ID: json.RawMessage(idStr), Method: method, Params: raw})

	select {
	case resp := <-ch:
		return resp, nil
	case <-cancelCh:
		s.mu.Lock()
		delete(s.pending, idStr)
		s.mu.Unlock()
		return rpcMsg{}, fmt.Errorf("cancelled")
	case <-time.After(120 * time.Second):
		s.mu.Lock()
		delete(s.pending, idStr)
		s.mu.Unlock()
		return rpcMsg{}, fmt.Errorf("permission request timed out")
	}
}

func randID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "fake-" + hex.EncodeToString(b)
}

func (s *server) saveSession(st *sessionState) {
	b, _ := json.Marshal(st)
	_ = os.WriteFile(filepath.Join(s.stateDir, st.ID+".json"), b, 0o600)
}

func (s *server) loadSession(id string) *sessionState {
	s.mu.Lock()
	if st, ok := s.sessions[id]; ok {
		s.mu.Unlock()
		return st
	}
	s.mu.Unlock()
	b, err := os.ReadFile(filepath.Join(s.stateDir, id+".json"))
	if err != nil {
		return nil
	}
	var st sessionState
	if json.Unmarshal(b, &st) != nil {
		return nil
	}
	s.mu.Lock()
	s.sessions[id] = &st
	s.mu.Unlock()
	return &st
}

func setupFor(st *sessionState) map[string]any {
	return map[string]any{
		"sessionId": st.ID,
		"models": map[string]any{
			"currentModelId": st.Model,
			"availableModels": []map[string]any{
				{"modelId": "fake-fast", "name": "Fake Fast", "description": "Fast scripted test model"},
				{"modelId": "fake-pro", "name": "Fake Pro", "description": "Balanced scripted test model"},
				{"modelId": "fake-max", "name": "Fake Max", "description": "Deep reasoning test model"},
			},
		},
		"modes": map[string]any{
			"currentModeId": st.Mode,
			"availableModes": []map[string]any{
				{"id": "supervised", "name": "Supervised", "description": "Ask before commands and edits"},
				{"id": "auto-edits", "name": "Auto-accept edits", "description": "Auto-approve file edits"},
				{"id": "auto", "name": "Auto", "description": "Approve routine actions automatically"},
				{"id": "full-access", "name": "Full access", "description": "Allow all actions without prompts"},
				{"id": "plan", "name": "Plan", "description": "Read-only planning mode"},
			},
		},
		"configOptions": []map[string]any{
			{
				"id":           "thought_level",
				"name":         "Reasoning effort",
				"category":     "reasoning",
				"type":         "select",
				"currentValue": st.Thought,
				"options": []map[string]any{
					{"value": "low", "name": "Low"},
					{"value": "medium", "name": "Medium"},
					{"value": "high", "name": "High"},
					{"value": "max", "name": "Max"},
				},
			},
		},
	}
}

func (s *server) emitCommands(sid string) {
	s.update(sid, map[string]any{
		"sessionUpdate": "available_commands_update",
		"availableCommands": []map[string]any{
			{"name": "help", "description": "Show fakeacp test commands"},
			{"name": "compact", "description": "Compact conversation context"},
			{"name": "review", "description": "Review uncommitted changes"},
			{"name": "test", "description": "Run project test suite"},
		},
	})
}

func (s *server) handle(m rpcMsg) {
	// Client response to one of our requests (e.g. session/request_permission).
	if m.Method == "" && len(m.ID) > 0 {
		idStr := strings.Trim(string(m.ID), `"`)
		s.mu.Lock()
		ch := s.pending[idStr]
		delete(s.pending, idStr)
		s.mu.Unlock()
		if ch != nil {
			ch <- m
		}
		return
	}

	switch m.Method {
	case "initialize":
		s.reply(m.ID, map[string]any{
			"protocolVersion": 1,
			"agentInfo":       map[string]any{"name": "fakeacp", "title": "Fake ACP Test Agent", "version": "1.0.0"},
			"agentCapabilities": map[string]any{
				"loadSession": true,
				"promptCapabilities": map[string]any{
					"image": true, "embeddedContext": true,
				},
			},
		})
	case "session/new":
		var p struct {
			Cwd       string `json:"cwd"`
			SessionID string `json:"sessionId"`
		}
		_ = json.Unmarshal(m.Params, &p)
		sid := p.SessionID
		if sid == "" {
			sid = randID()
		}
		st := &sessionState{ID: sid, Cwd: p.Cwd, Model: "fake-pro", Mode: "supervised", Thought: "medium"}
		s.mu.Lock()
		s.sessions[sid] = st
		s.mu.Unlock()
		s.saveSession(st)
		s.reply(m.ID, setupFor(st))
		s.emitCommands(sid)
	case "session/resume", "session/load":
		var p struct {
			SessionID string `json:"sessionId"`
			Cwd       string `json:"cwd"`
		}
		_ = json.Unmarshal(m.Params, &p)
		st := s.loadSession(p.SessionID)
		if st == nil {
			st = &sessionState{ID: p.SessionID, Cwd: p.Cwd, Model: "fake-pro", Mode: "supervised", Thought: "medium"}
			s.mu.Lock()
			s.sessions[st.ID] = st
			s.mu.Unlock()
			s.saveSession(st)
		}
		if p.Cwd != "" {
			st.Cwd = p.Cwd
		}
		if m.Method == "session/load" {
			for i, entry := range st.History {
				if i%2 == 0 {
					s.update(st.ID, map[string]any{"sessionUpdate": "user_message_chunk", "content": map[string]any{"type": "text", "text": entry}})
				} else {
					s.update(st.ID, map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": entry}})
				}
			}
		}
		s.reply(m.ID, setupFor(st))
		s.emitCommands(st.ID)
	case "session/list":
		s.mu.Lock()
		var list []map[string]any
		for _, st := range s.sessions {
			list = append(list, map[string]any{"sessionId": st.ID, "cwd": st.Cwd, "title": "Fake Session", "updatedAt": time.Now().UTC().Format(time.RFC3339)})
		}
		s.mu.Unlock()
		s.reply(m.ID, map[string]any{"sessions": list})
	case "session/set_model":
		var p struct {
			SessionID string `json:"sessionId"`
			ModelID   string `json:"modelId"`
		}
		_ = json.Unmarshal(m.Params, &p)
		if st := s.loadSession(p.SessionID); st != nil {
			st.Model = p.ModelID
			s.saveSession(st)
		}
		s.reply(m.ID, map[string]any{"ok": true})
	case "session/set_mode":
		var p struct {
			SessionID string `json:"sessionId"`
			ModeID    string `json:"modeId"`
		}
		_ = json.Unmarshal(m.Params, &p)
		if st := s.loadSession(p.SessionID); st != nil {
			st.Mode = p.ModeID
			s.saveSession(st)
		}
		s.reply(m.ID, map[string]any{"ok": true})
	case "session/set_config_option":
		var p struct {
			SessionID string `json:"sessionId"`
			ConfigID  string `json:"configId"`
			Value     string `json:"value"`
		}
		_ = json.Unmarshal(m.Params, &p)
		st := s.loadSession(p.SessionID)
		if st != nil && p.ConfigID == "thought_level" {
			st.Thought = p.Value
			s.saveSession(st)
		}
		if st != nil {
			s.reply(m.ID, map[string]any{"configOptions": setupFor(st)["configOptions"]})
		} else {
			s.reply(m.ID, map[string]any{"configOptions": []any{}})
		}
	case "session/cancel":
		var p struct {
			SessionID string `json:"sessionId"`
		}
		_ = json.Unmarshal(m.Params, &p)
		s.mu.Lock()
		if ch := s.cancels[p.SessionID]; ch != nil {
			select {
			case <-ch:
			default:
				close(ch)
			}
		}
		s.mu.Unlock()
		if len(m.ID) > 0 {
			s.reply(m.ID, map[string]any{"ok": true})
		}
	case "session/prompt":
		go s.runPrompt(m.ID, m.Params)
	default:
		if len(m.ID) > 0 {
			s.replyErr(m.ID, -32601, "method not found: "+m.Method)
		}
	}
}

var (
	editRe     = regexp.MustCompile(`\[edit:([^:\]]+):([^\]]*)\]`)
	permRe     = regexp.MustCompile(`\[perm(?::([^\]]*))?\]`)
	bashRe     = regexp.MustCompile(`\[bash:([^\]]+)\]`)
	realBashRe = regexp.MustCompile(`\[realbash:([^\]]+)\]`)
	slowRe     = regexp.MustCompile(`\[slow:(\d+)\]`)
	errRe      = regexp.MustCompile(`\[error:([^\]]+)\]`)
)

func (s *server) runPrompt(reqID json.RawMessage, params json.RawMessage) {
	var p struct {
		SessionID string `json:"sessionId"`
		Prompt    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
			Name string `json:"name"`
		} `json:"prompt"`
	}
	_ = json.Unmarshal(params, &p)
	st := s.loadSession(p.SessionID)
	if st == nil {
		s.replyErr(reqID, -32000, "unknown session")
		return
	}

	cancelCh := make(chan struct{})
	s.mu.Lock()
	s.cancels[st.ID] = cancelCh
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.cancels, st.ID)
		s.mu.Unlock()
	}()

	var textParts []string
	var attNames []string
	for _, b := range p.Prompt {
		if b.Type == "text" || b.Type == "" {
			textParts = append(textParts, b.Text)
		} else if b.Name != "" {
			attNames = append(attNames, b.Name)
		}
	}
	promptText := strings.TrimSpace(strings.Join(textParts, "\n"))

	// Error directive.
	if mm := errRe.FindStringSubmatch(promptText); mm != nil {
		s.replyErr(reqID, -32001, mm[1])
		return
	}

	// Emit thinking first.
	s.update(st.ID, map[string]any{
		"sessionUpdate": "agent_thought_chunk",
		"content":       map[string]any{"type": "text", "text": fmt.Sprintf("Analyzing prompt on model %s (mode=%s, effort=%s)...", st.Model, st.Mode, st.Thought)},
	})

	var replyBuf strings.Builder
	callIdx := 0
	nextCall := func(prefix string) string {
		callIdx++
		return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixNano()%100000, callIdx)
	}

	// [slow:ms]
	if mm := slowRe.FindStringSubmatch(promptText); mm != nil {
		ms, _ := strconv.Atoi(mm[1])
		if ms <= 0 {
			ms = 1000
		}
		steps := 5
		stepDur := time.Duration(ms/steps) * time.Millisecond
		for i := 1; i <= steps; i++ {
			select {
			case <-cancelCh:
				s.reply(reqID, map[string]any{"stopReason": "cancelled"})
				return
			case <-time.After(stepDur):
				chunk := fmt.Sprintf("Step %d/%d streamed.\n", i, steps)
				replyBuf.WriteString(chunk)
				s.update(st.ID, map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": chunk}})
			}
		}
	}

	// [perm] or [perm:cmd]
	if mm := permRe.FindStringSubmatch(promptText); mm != nil {
		cmdStr := mm[1]
		if cmdStr == "" {
			cmdStr = "echo 'hello from supervised tool'"
		}
		cid := nextCall("perm")
		toolCall := map[string]any{
			"toolCallId": cid,
			"name":       "Bash",
			"kind":       "execute",
			"title":      "Run " + cmdStr,
			"status":     "in_progress",
			"rawInput":   map[string]any{"command": cmdStr},
		}
		s.update(st.ID, map[string]any{
			"sessionUpdate": "tool_call",
			"toolCallId":    cid,
			"name":          "Bash",
			"kind":          "execute",
			"title":         "Run " + cmdStr,
			"status":        "in_progress",
			"rawInput":      map[string]any{"command": cmdStr},
		})
		resp, err := s.callClient("session/request_permission", map[string]any{
			"sessionId": st.ID,
			"toolCall":  toolCall,
			"options": []map[string]any{
				{"optionId": "allow", "name": "Approve", "kind": "allow_once"},
				{"optionId": "allow_always", "name": "Always allow this session", "kind": "allow_always"},
				{"optionId": "deny", "name": "Decline", "kind": "reject_once"},
			},
			"_meta": map[string]any{
				"agentc": map[string]any{"kind": "tool", "tool": "Bash", "reason": "Command requires approval"},
			},
		}, cancelCh)
		if err != nil {
			s.reply(reqID, map[string]any{"stopReason": "cancelled"})
			return
		}
		outcome, feedback := parsePermOutcome(resp.Result)
		if outcome == "allow" || outcome == "allow_always" {
			s.update(st.ID, map[string]any{
				"sessionUpdate": "tool_call_update",
				"toolCallId":    cid,
				"status":        "completed",
				"rawOutput":     map[string]any{"output": "Approved and executed: " + cmdStr, "exit_code": 0},
			})
			replyBuf.WriteString("Permission approved (" + outcome + "). Ran `" + cmdStr + "`.\n")
		} else {
			s.update(st.ID, map[string]any{
				"sessionUpdate": "tool_call_update",
				"toolCallId":    cid,
				"status":        "failed",
				"rawOutput":     map[string]any{"error": "Denied by user", "feedback": feedback},
			})
			msg := "Permission declined."
			if feedback != "" {
				msg += " User feedback: " + feedback
			}
			replyBuf.WriteString(msg + "\n")
		}
	}

	// [question]
	if strings.Contains(promptText, "[question]") {
		cid := nextCall("ask")
		qInput := map[string]any{
			"questions": []map[string]any{
				{
					"header":   "Framework",
					"question": "Which test mode should we use?",
					"options": []map[string]any{
						{"label": "Fast", "description": "Run unit tests only"},
						{"label": "Full", "description": "Run full E2E suite"},
					},
					"multiSelect": false,
				},
			},
		}
		resp, err := s.callClient("session/request_permission", map[string]any{
			"sessionId": st.ID,
			"toolCall":  map[string]any{"toolCallId": cid, "name": "AskUserQuestion", "kind": "other", "title": "Questions", "rawInput": qInput},
			"options": []map[string]any{
				{"optionId": "allow", "name": "Submit answers", "kind": "allow_once"},
				{"optionId": "deny", "name": "Skip", "kind": "reject_once"},
			},
			"_meta": map[string]any{"agentc": map[string]any{"kind": "question", "tool": "AskUserQuestion"}},
		}, cancelCh)
		if err != nil {
			s.reply(reqID, map[string]any{"stopReason": "cancelled"})
			return
		}
		_, feedback := parsePermOutcome(resp.Result)
		ansStr := extractAnswers(resp.Result)
		replyBuf.WriteString("Received your answer: " + ansStr)
		if feedback != "" {
			replyBuf.WriteString(" (" + feedback + ")")
		}
		replyBuf.WriteString("\n")
	}

	// [plan]
	if strings.Contains(promptText, "[plan]") {
		cid := nextCall("plan")
		planText := "# Implementation Plan\n\n1. Update module configuration\n2. Add comprehensive unit tests\n3. Verify edge cases\n"
		resp, err := s.callClient("session/request_permission", map[string]any{
			"sessionId": st.ID,
			"toolCall":  map[string]any{"toolCallId": cid, "name": "ExitPlanMode", "kind": "other", "title": "Plan ready for review", "rawInput": map[string]any{"plan": planText}},
			"options": []map[string]any{
				{"optionId": "allow", "name": "Approve plan", "kind": "allow_once"},
				{"optionId": "deny", "name": "Request changes", "kind": "reject_once"},
			},
			"_meta": map[string]any{"agentc": map[string]any{"kind": "plan", "tool": "ExitPlanMode"}},
		}, cancelCh)
		if err != nil {
			s.reply(reqID, map[string]any{"stopReason": "cancelled"})
			return
		}
		outcome, feedback := parsePermOutcome(resp.Result)
		if outcome == "allow" {
			replyBuf.WriteString("Plan approved! Proceeding with execution.\n")
		} else {
			replyBuf.WriteString("Plan revision requested: " + feedback + "\n")
		}
	}

	// [edit:path:content]
	for _, mm := range editRe.FindAllStringSubmatch(promptText, -1) {
		relPath := strings.TrimSpace(mm[1])
		newContent := strings.ReplaceAll(mm[2], `\n`, "\n")
		fullPath := relPath
		if !filepath.IsAbs(fullPath) && st.Cwd != "" {
			fullPath = filepath.Join(st.Cwd, relPath)
		}
		oldBytes, _ := os.ReadFile(fullPath)
		oldText := string(oldBytes)
		_ = os.MkdirAll(filepath.Dir(fullPath), 0o755)
		_ = os.WriteFile(fullPath, []byte(newContent), 0o644)

		cid := nextCall("edit")
		s.update(st.ID, map[string]any{
			"sessionUpdate": "tool_call",
			"toolCallId":    cid,
			"name":          "Edit",
			"kind":          "edit",
			"title":         "Edit " + relPath,
			"status":        "completed",
			"locations":     []map[string]any{{"path": fullPath}},
			"rawInput":      map[string]any{"file_path": fullPath},
			"content": []map[string]any{
				{"type": "diff", "path": fullPath, "oldText": oldText, "newText": newContent},
			},
		})
		replyBuf.WriteString(fmt.Sprintf("Updated `%s`.\n", relPath))
	}

	// [bash:cmd] (simulated) and [realbash:cmd] (actually executed)
	for _, mm := range bashRe.FindAllStringSubmatch(promptText, -1) {
		cid := nextCall("bash")
		s.update(st.ID, map[string]any{
			"sessionUpdate": "tool_call",
			"toolCallId":    cid,
			"name":          "Bash",
			"kind":          "execute",
			"title":         mm[1],
			"status":        "completed",
			"rawInput":      map[string]any{"command": mm[1]},
			"rawOutput":     map[string]any{"output": "simulated output for: " + mm[1], "exit_code": 0},
		})
		replyBuf.WriteString(fmt.Sprintf("Ran `%s`.\n", mm[1]))
	}
	for _, mm := range realBashRe.FindAllStringSubmatch(promptText, -1) {
		cid := nextCall("rbash")
		cmd := exec.Command("sh", "-c", mm[1])
		cmd.Dir = st.Cwd
		out, err := cmd.CombinedOutput()
		code := 0
		status := "completed"
		if err != nil {
			code = 1
			status = "failed"
		}
		s.update(st.ID, map[string]any{
			"sessionUpdate": "tool_call",
			"toolCallId":    cid,
			"name":          "Bash",
			"kind":          "execute",
			"title":         mm[1],
			"status":        status,
			"rawInput":      map[string]any{"command": mm[1]},
			"rawOutput":     map[string]any{"output": string(out), "exit_code": code},
		})
		replyBuf.WriteString(fmt.Sprintf("Executed `%s` (exit %d).\n", mm[1], code))
	}

	// [subagent]
	if strings.Contains(promptText, "[subagent]") {
		cid := nextCall("sub")
		s.update(st.ID, map[string]any{
			"sessionUpdate": "tool_call",
			"toolCallId":    cid,
			"name":          "Agent",
			"kind":          "other",
			"title":         "Subagent: Codebase Audit",
			"status":        "completed",
			"rawInput":      map[string]any{"description": "Audit tests"},
			"rawOutput":     map[string]any{"summary": "All 12 checks passed"},
		})
		replyBuf.WriteString("Subagent completed audit.\n")
	}

	// [markdown]
	if strings.Contains(promptText, "[markdown]") {
		replyBuf.WriteString("## Rich Output\n\nInline math $O(n \\log n)$ and block math:\n\n$$\n\\sum_{i=1}^n i = \\frac{n(n+1)}{2}\n$$\n\n```typescript\nexport const greet = (name: string): string => `Hello, ${name}`;\n```\n\n| Feature | Status |\n|---|---|\n| Noise E2E | Ready |\n| Checkpoints | Ready |\n")
	}

	if replyBuf.Len() == 0 {
		replyBuf.WriteString("FakeACP reply to: " + promptText)
		if len(attNames) > 0 {
			replyBuf.WriteString(" [attachments: " + strings.Join(attNames, ", ") + "]")
		}
	}

	finalText := strings.TrimSpace(replyBuf.String())
	if !strings.Contains(promptText, "[slow:") {
		// Stream in two chunks to exercise append coalescing.
		mid := len(finalText) / 2
		if mid > 0 && mid < len(finalText) {
			s.update(st.ID, map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": finalText[:mid]}})
			s.update(st.ID, map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": finalText[mid:]}})
		} else {
			s.update(st.ID, map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": finalText}})
		}
	}

	s.update(st.ID, map[string]any{"sessionUpdate": "usage_update", "used": 1240, "size": 200000})

	st.History = append(st.History, promptText, finalText)
	s.saveSession(st)

	s.reply(reqID, map[string]any{
		"stopReason": "end_turn",
		"usage": map[string]any{
			"inputTokens":  120,
			"outputTokens": 64,
			"totalTokens":  184,
			"costUsd":      0.0012,
		},
	})
}

func parsePermOutcome(res any) (string, string) {
	b, _ := json.Marshal(res)
	var r struct {
		Outcome struct {
			Outcome  string `json:"outcome"`
			OptionID string `json:"optionId"`
			Meta     struct {
				Message string `json:"message"`
			} `json:"_meta"`
		} `json:"outcome"`
	}
	_ = json.Unmarshal(b, &r)
	opt := r.Outcome.OptionID
	if opt == "" {
		opt = r.Outcome.Outcome
	}
	return opt, r.Outcome.Meta.Message
}

func extractAnswers(res any) string {
	b, _ := json.Marshal(res)
	var r struct {
		Outcome struct {
			Meta struct {
				Answers map[string]any `json:"answers"`
			} `json:"_meta"`
		} `json:"outcome"`
	}
	_ = json.Unmarshal(b, &r)
	if len(r.Outcome.Meta.Answers) == 0 {
		return "(none)"
	}
	ab, _ := json.Marshal(r.Outcome.Meta.Answers)
	return string(ab)
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println("fakeacp 1.0.0")
		return
	}
	srv := newServer(os.Stdout)
	br := bufio.NewReaderSize(os.Stdin, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			var m rpcMsg
			if json.Unmarshal(line, &m) == nil {
				srv.handle(m)
			}
		}
		if err != nil {
			break
		}
	}
}
