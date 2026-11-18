package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"agentc/internal/acp"
	"agentc/internal/agyproc"
)

// agyAsideGuard frames a side question for agy, which has no side-question
// mode over ACP (its /btw lives in the terminal UI).
const agyAsideGuard = "<side_question>\n" +
	"This is a side question (like /btw) about the conversation above, asked while the main task may still be running. " +
	"Answer it directly and concisely from what you already know and, if needed, by reading files. Do not write a plan, " +
	"do not create or edit files, do not run commands that change anything, and do not continue or restart the main task.\n" +
	"</side_question>\n\n"

// asideAgy answers a side question about an agy conversation the way agy's
// own /btw does: on a copy. The conversation is forked at its newest finished
// step and the question is asked there by a separate agy process that runs in
// plan mode without --dangerously-skip-permissions and has every permission
// request denied, so it can read files but not change anything. The fork is
// deleted afterwards, so neither the conversation nor agy's history changes.
func (m *Manager) asideAgy(ctx context.Context, s *Session, question string) (string, error) {
	s.mu.Lock()
	id, cwd := s.meta.ID, s.meta.Cwd
	addDirs, agentName, noTel := s.meta.AddDirs, s.meta.AgentName, s.meta.NoTelemetry
	model := s.meta.WantModel
	if s.meta.Models != nil && s.meta.Models.CurrentModelID != "" {
		model = s.meta.Models.CurrentModelID
	}
	s.mu.Unlock()
	ls, err := m.LS(ctx, id)
	if err != nil {
		return "", err
	}
	steps, err := ls.Steps(ctx, id)
	if err != nil {
		return "", fmt.Errorf("read the conversation: %w", err)
	}
	idx := len(steps) - 1
	for idx > 0 && steps[idx].Status != "CORTEX_STEP_STATUS_DONE" {
		idx-- // the main conversation may be mid-step
	}
	if idx < 0 {
		return "", errors.New("the conversation is empty")
	}
	var fork struct {
		NewCascadeID string `json:"newCascadeId"`
	}
	if err := ls.Call(ctx, "ForkConversation", map[string]any{"sourceCascadeId": id, "forkAtStepIndex": idx}, &fork); err != nil {
		return "", fmt.Errorf("copy the conversation: %w", err)
	}
	if fork.NewCascadeID == "" {
		return "", errors.New("agy did not copy the conversation")
	}
	defer func() {
		dctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		dls, err := m.LS(dctx, id)
		if err == nil {
			err = dls.Call(dctx, "DeleteCascadeTrajectory", map[string]any{"cascadeId": fork.NewCascadeID}, nil)
		}
		if err == nil {
			m.invalidateHistory()
		}
	}()

	h := &asideHandler{}
	p, err := agyproc.Start(ctx, agyproc.Options{
		AgyPath: m.cfg.AgyPath, Cwd: cwd, AddDirs: addDirs, AgentName: agentName, NoTelemetry: noTel,
		LogDir: filepath.Join(m.cfg.StateDir, "logs"), Env: m.agyEnv(), Handler: h,
	})
	if err != nil {
		return "", err
	}
	defer p.Close(5 * time.Second)
	conn := p.ACP()
	var setup acp.SessionSetup
	if err := conn.Call(ctx, "session/resume", map[string]any{"sessionId": fork.NewCascadeID, "cwd": cwd, "mcpServers": []any{}}, &setup); err != nil {
		return "", fmt.Errorf("open the copy: %w", err)
	}
	if model != "" {
		// A new process starts on agy's default model, which the account may not offer.
		_ = conn.Call(ctx, "session/set_model", map[string]any{"sessionId": fork.NewCascadeID, "modelId": model}, nil)
	}
	if err := conn.Call(ctx, "session/set_mode", map[string]any{"sessionId": fork.NewCascadeID, "modeId": "plan"}, nil); err != nil {
		return "", fmt.Errorf("switch the copy to plan mode: %w", err)
	}
	var res acp.PromptResult
	err = conn.Call(ctx, "session/prompt", map[string]any{"sessionId": fork.NewCascadeID,
		"prompt": []map[string]any{{"type": "text", "text": agyAsideGuard + question}}}, &res)
	answer := strings.TrimSpace(h.answer())
	if err != nil && answer == "" {
		return "", err
	}
	if answer == "" {
		return "", errors.New("agy returned no answer")
	}
	return answer, nil
}

// asideHandler collects a side question's answer and refuses everything that
// would need the user: permission requests, questions, anything else.
type asideHandler struct {
	mu   sync.Mutex
	text strings.Builder
}

func (h *asideHandler) answer() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.text.String()
}

func (h *asideHandler) HandleNotification(method string, params json.RawMessage) {
	if method != "session/update" {
		return
	}
	var n acp.SessionNotification
	if json.Unmarshal(params, &n) != nil {
		return
	}
	var u struct {
		Kind    string           `json:"sessionUpdate"`
		Content acp.ContentBlock `json:"content"`
	}
	if json.Unmarshal(n.Update, &u) != nil || u.Kind != "agent_message_chunk" {
		return
	}
	if u.Content.Type == "text" || u.Content.Type == "" {
		h.mu.Lock()
		h.text.WriteString(u.Content.Text)
		h.mu.Unlock()
	}
}

func (h *asideHandler) HandleRequest(c *acp.Conn, id json.RawMessage, method string, params json.RawMessage) {
	if method != "session/request_permission" {
		_ = c.ReplyError(id, -32601, "not available while answering a side question")
		return
	}
	var req struct {
		Options []acp.PermissionOption `json:"options"`
	}
	_ = json.Unmarshal(params, &req)
	for _, o := range req.Options {
		if strings.HasPrefix(o.Kind, "reject") {
			_ = c.Reply(id, map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": o.OptionID}})
			return
		}
	}
	_ = c.Reply(id, map[string]any{"outcome": map[string]any{"outcome": "cancelled"}})
}
