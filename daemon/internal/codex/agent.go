// Package codex adapts OpenAI Codex CLI (`codex exec --json` / `codex exec resume`)
// behind an in-process ACP connection so core.Session can drive Codex identically
// to Claude Code, agy, and generic ACP agents.
package codex

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"agentc/internal/acp"
)

// Options configure a Codex ACP adapter instance.
type Options struct {
	Bin             string
	Env             []string
	Cwd             string
	SkipPermissions bool
	Model           string
	Effort          string
	Mode            string
	ExtraArgs       []string
	Handler         acp.Handler
	Trace           func(dir string, line []byte)
}

// Agent implements an in-process ACP server backed by `codex exec --json`.
type Agent struct {
	o      Options
	Conn   *acp.Conn
	agent  *acp.Conn
	cw     *io.PipeWriter
	aw     *io.PipeWriter
	mu     sync.Mutex
	sid    string
	cwd    string
	model  string
	mode   string
	effort string
	cmd    *exec.Cmd
	closed bool
	exited chan struct{}
}

// Start creates a new in-process Codex ACP adapter.
func Start(o Options) *Agent {
	if strings.TrimSpace(o.Bin) == "" {
		o.Bin = "codex"
	}
	cr, aw := io.Pipe()
	ar, cw := io.Pipe()
	a := &Agent{
		o:      o,
		cw:     cw,
		aw:     aw,
		cwd:    o.Cwd,
		model:  firstNonEmpty(o.Model, "gpt-5.3-codex"),
		mode:   firstNonEmpty(o.Mode, "auto"),
		effort: firstNonEmpty(o.Effort, "medium"),
		exited: make(chan struct{}),
	}
	a.Conn = acp.NewConn(cr, cw, o.Handler)
	a.Conn.Trace = o.Trace
	a.agent = acp.NewConn(ar, aw, agentHandler{a: a})
	return a
}

// ACP returns the client-side ACP connection.
func (a *Agent) ACP() *acp.Conn { return a.Conn }

// Pid returns the active codex exec process PID (0 when idle between turns).
func (a *Agent) Pid() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cmd != nil && a.cmd.Process != nil {
		return a.cmd.Process.Pid
	}
	return 0
}

// Alive reports whether the adapter is still open.
func (a *Agent) Alive() bool {
	select {
	case <-a.exited:
		return false
	default:
		return true
	}
}

// Exited is closed when Close finishes.
func (a *Agent) Exited() <-chan struct{} { return a.exited }

// ExitErr always returns nil for clean adapter close.
func (a *Agent) ExitErr() error { return nil }

// RSSKiB returns 0 when idle or the running exec process RSS.
func (a *Agent) RSSKiB() int64 { return 0 }

// Close tears down any active codex turn and closes pipes.
func (a *Agent) Close(_ time.Duration) {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return
	}
	a.closed = true
	cmd := a.cmd
	a.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
	_ = a.cw.Close()
	_ = a.aw.Close()
	close(a.exited)
}

type agentHandler struct{ a *Agent }

func (h agentHandler) HandleNotification(method string, params json.RawMessage) {
	if method == "session/cancel" {
		h.a.cancelActive()
	}
}

func (h agentHandler) HandleRequest(c *acp.Conn, id json.RawMessage, method string, params json.RawMessage) {
	switch method {
	case "initialize":
		_ = c.Reply(id, map[string]any{
			"protocolVersion": 1,
			"agentInfo":       map[string]any{"name": "codex", "title": "Codex", "version": "1.0"},
		})
	case "session/new", "session/resume", "session/load":
		var p struct {
			SessionID string `json:"sessionId"`
			Cwd       string `json:"cwd"`
		}
		_ = json.Unmarshal(params, &p)
		h.a.mu.Lock()
		if p.Cwd != "" {
			h.a.cwd = p.Cwd
		}
		if p.SessionID != "" {
			h.a.sid = p.SessionID
		} else if h.a.sid == "" {
			h.a.sid = newUUID()
		}
		setup := h.a.setupLocked()
		h.a.mu.Unlock()
		_ = c.Reply(id, setup)
	case "session/set_model":
		var p struct {
			ModelID string `json:"modelId"`
		}
		_ = json.Unmarshal(params, &p)
		h.a.mu.Lock()
		if p.ModelID != "" {
			h.a.model = p.ModelID
		}
		h.a.mu.Unlock()
		_ = c.Reply(id, map[string]any{"ok": true})
	case "session/set_mode":
		var p struct {
			ModeID string `json:"modeId"`
		}
		_ = json.Unmarshal(params, &p)
		h.a.mu.Lock()
		if p.ModeID != "" {
			h.a.mode = p.ModeID
		}
		h.a.mu.Unlock()
		_ = c.Reply(id, map[string]any{"ok": true})
	case "session/set_config_option":
		var p struct {
			ConfigID string `json:"configId"`
			Value    string `json:"value"`
		}
		_ = json.Unmarshal(params, &p)
		h.a.mu.Lock()
		if p.ConfigID == "thought_level" && p.Value != "" {
			h.a.effort = p.Value
		}
		opts := h.a.configOptionsLocked()
		h.a.mu.Unlock()
		_ = c.Reply(id, map[string]any{"configOptions": opts})
	case "session/cancel":
		h.a.cancelActive()
		_ = c.Reply(id, map[string]any{"ok": true})
	case "session/prompt":
		go h.a.handlePrompt(c, id, params)
	default:
		_ = c.ReplyError(id, -32601, "unsupported method: "+method)
	}
}

func (a *Agent) cancelActive() {
	a.mu.Lock()
	cmd := a.cmd
	a.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGINT)
	}
}

func (a *Agent) configOptionsLocked() []acp.ConfigOption {
	return []acp.ConfigOption{
		{
			ID:           "thought_level",
			Name:         "Reasoning effort",
			Category:     "reasoning",
			Type:         "select",
			CurrentValue: a.effort,
			Options: []acp.ConfigOptionValue{
				{Value: "low", Name: "Low"},
				{Value: "medium", Name: "Medium"},
				{Value: "high", Name: "High"},
				{Value: "xhigh", Name: "Extra High"},
			},
		},
	}
}

func (a *Agent) setupLocked() acp.SessionSetup {
	return acp.SessionSetup{
		SessionID: a.sid,
		Models: &acp.Models{
			CurrentModelID: a.model,
			AvailableModels: []acp.Model{
				{ModelID: "gpt-5.3-codex", Name: "GPT-5.3 Codex", Description: "Default Codex coding model"},
				{ModelID: "gpt-5.3-codex-mini", Name: "GPT-5.3 Codex Mini", Description: "Fast lightweight Codex model"},
				{ModelID: "o4-mini", Name: "o4-mini", Description: "Reasoning model"},
			},
		},
		Modes: &acp.Modes{
			CurrentModeID: a.mode,
			AvailableModes: []acp.Mode{
				{ID: "supervised", Name: "Supervised", Description: "Read-only sandbox unless approved"},
				{ID: "auto-edits", Name: "Auto-accept edits", Description: "Workspace-write sandbox"},
				{ID: "auto", Name: "Auto", Description: "Full-auto workspace writes"},
				{ID: "full-access", Name: "Full access", Description: "Dangerously bypass approvals and sandbox"},
			},
		},
		ConfigOptions: a.configOptionsLocked(),
	}
}

func (a *Agent) update(upd any) {
	a.mu.Lock()
	sid := a.sid
	a.mu.Unlock()
	_ = a.agent.Notify("session/update", map[string]any{"sessionId": sid, "update": upd})
}

func (a *Agent) handlePrompt(c *acp.Conn, id json.RawMessage, params json.RawMessage) {
	var p struct {
		Prompt []acp.ContentBlock `json:"prompt"`
	}
	_ = json.Unmarshal(params, &p)
	var parts []string
	var images []string
	for _, b := range p.Prompt {
		switch b.Type {
		case "text", "":
			parts = append(parts, b.Text)
		case "resource_link":
			if strings.HasPrefix(b.URI, "file://") {
				parts = append(parts, "File: "+strings.TrimPrefix(b.URI, "file://"))
			}
		case "image":
			if strings.HasPrefix(b.URI, "file://") {
				images = append(images, strings.TrimPrefix(b.URI, "file://"))
			}
		}
	}
	prompt := strings.TrimSpace(strings.Join(parts, "\n"))

	a.mu.Lock()
	cwd, model, mode, effort, sid := a.cwd, a.model, a.mode, a.effort, a.sid
	skip := a.o.SkipPermissions || mode == "full-access"
	extra := append([]string(nil), a.o.ExtraArgs...)
	a.mu.Unlock()

	args := []string{"exec", "--json"}
	if model != "" {
		args = append(args, "-m", model)
	}
	if effort != "" {
		args = append(args, "-c", "model_reasoning_effort="+effort)
	}
	if skip {
		args = append(args, "--dangerously-bypass-approvals-and-sandbox")
	} else if mode == "auto" || mode == "auto-edits" {
		args = append(args, "--full-auto")
	} else {
		args = append(args, "-s", "read-only")
	}
	for _, img := range images {
		if _, err := os.Stat(img); err == nil {
			args = append(args, "-i", img)
		}
	}
	args = append(args, extra...)
	args = append(args, prompt)

	ctx := context.Background()
	cmd := exec.CommandContext(ctx, a.o.Bin, args...)
	cmd.Dir = cwd
	if len(a.o.Env) > 0 {
		cmd.Env = a.o.Env
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = c.ReplyError(id, -32000, err.Error())
		return
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		_ = c.ReplyError(id, -32000, fmt.Sprintf("start codex: %v", err))
		return
	}
	a.mu.Lock()
	a.cmd = cmd
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		if a.cmd == cmd {
			a.cmd = nil
		}
		a.mu.Unlock()
	}()

	var usage acp.Usage
	br := bufio.NewReaderSize(stdout, 1<<20)
	var plainFallback strings.Builder
	for {
		line, rerr := br.ReadBytes('\n')
		if len(line) > 0 {
			s := strings.TrimSpace(string(line))
			var ev map[string]any
			if json.Unmarshal([]byte(s), &ev) == nil && ev["type"] != nil {
				a.handleCodexEvent(ev, &usage)
			} else if s != "" {
				plainFallback.WriteString(s + "\n")
			}
		}
		if rerr != nil {
			break
		}
	}
	waitErr := cmd.Wait()
	if plainFallback.Len() > 0 {
		a.update(map[string]any{
			"sessionUpdate": "agent_message_chunk",
			"content":       map[string]any{"type": "text", "text": plainFallback.String()},
		})
	}
	if waitErr != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = waitErr.Error()
		}
		_ = c.ReplyError(id, -32000, fmt.Sprintf("codex (%s): %s", sid, msg))
		return
	}
	_ = c.Reply(id, acp.PromptResult{StopReason: "end_turn", Usage: &usage})
}

func (a *Agent) handleCodexEvent(ev map[string]any, usage *acp.Usage) {
	t, _ := ev["type"].(string)
	switch t {
	case "thread.started":
		if tid, ok := ev["thread_id"].(string); ok && tid != "" {
			a.mu.Lock()
			if a.sid == "" {
				a.sid = tid
			}
			a.mu.Unlock()
		}
	case "item.started", "item.updated", "item.completed":
		item, _ := ev["item"].(map[string]any)
		if item == nil {
			return
		}
		itType, _ := item["type"].(string)
		itID, _ := item["id"].(string)
		switch itType {
		case "agent_message", "message":
			if txt, ok := item["text"].(string); ok && txt != "" && t == "item.completed" {
				a.update(map[string]any{
					"sessionUpdate": "agent_message_chunk",
					"content":       map[string]any{"type": "text", "text": txt},
				})
			}
		case "reasoning":
			if txt, ok := item["text"].(string); ok && txt != "" && t == "item.completed" {
				a.update(map[string]any{
					"sessionUpdate": "agent_thought_chunk",
					"content":       map[string]any{"type": "text", "text": txt},
				})
			}
		case "command_execution":
			cmdStr, _ := item["command"].(string)
			outStr, _ := item["aggregated_output"].(string)
			status := "in_progress"
			if t == "item.completed" {
				status = "completed"
				if ec, ok := item["exit_code"].(float64); ok && ec != 0 {
					status = "failed"
				}
			}
			su := "tool_call"
			if t != "item.started" {
				su = "tool_call_update"
			}
			a.update(map[string]any{
				"sessionUpdate": su,
				"toolCallId":    firstNonEmpty(itID, "cmd"),
				"name":          "Bash",
				"kind":          "execute",
				"title":         cmdStr,
				"status":        status,
				"rawInput":      map[string]any{"command": cmdStr},
				"rawOutput":     map[string]any{"output": outStr, "exit_code": item["exit_code"]},
			})
		case "file_change":
			changes, _ := item["changes"].([]any)
			var names []string
			for _, ch := range changes {
				if cm, ok := ch.(map[string]any); ok {
					if p, ok := cm["path"].(string); ok {
						names = append(names, filepath.Base(p))
					}
				}
			}
			a.update(map[string]any{
				"sessionUpdate": "tool_call",
				"toolCallId":    firstNonEmpty(itID, "edit"),
				"name":          "Edit",
				"kind":          "edit",
				"title":         "Edit " + strings.Join(names, ", "),
				"status":        "completed",
				"rawOutput":     item,
			})
		}
	case "turn.completed":
		if u, ok := ev["usage"].(map[string]any); ok {
			if v, ok := u["input_tokens"].(float64); ok {
				usage.InputTokens += int64(v)
			}
			if v, ok := u["output_tokens"].(float64); ok {
				usage.OutputTokens += int64(v)
			}
			if v, ok := u["cached_input_tokens"].(float64); ok {
				usage.CachedReadTokens += int64(v)
			}
			usage.TotalTokens = usage.InputTokens + usage.OutputTokens
		}
	}
}

func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "codex-" + hex.EncodeToString(b[:8])
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
