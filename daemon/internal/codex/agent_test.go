package codex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"agentc/internal/acp"
)

type captureHandler struct {
	mu      sync.Mutex
	updates []string
}

func (h *captureHandler) HandleNotification(method string, params json.RawMessage) {
	if method == "session/update" {
		h.mu.Lock()
		h.updates = append(h.updates, string(params))
		h.mu.Unlock()
	}
}
func (h *captureHandler) HandleRequest(c *acp.Conn, id json.RawMessage, method string, _ json.RawMessage) {
	_ = c.ReplyError(id, -32601, "unsupported")
}

func TestCodexAdapterWithMockBinary(t *testing.T) {
	dir := t.TempDir()
	mockBin := filepath.Join(dir, "codex")
	script := `#!/bin/sh
echo '{"type":"thread.started","thread_id":"codex-test-1"}'
echo '{"type":"item.completed","item":{"id":"r1","type":"reasoning","text":"Checking files"}}'
echo '{"type":"item.completed","item":{"id":"c1","type":"command_execution","command":"ls","aggregated_output":"ok","exit_code":0}}'
echo '{"type":"item.completed","item":{"id":"m1","type":"agent_message","text":"All good from codex!"}}'
echo '{"type":"turn.completed","usage":{"input_tokens":50,"output_tokens":20}}'
`
	if err := os.WriteFile(mockBin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	h := &captureHandler{}
	ag := Start(Options{Bin: mockBin, Cwd: dir, Handler: h})
	defer ag.Close(time.Second)

	ctx := context.Background()
	var setup acp.SessionSetup
	if err := ag.ACP().Call(ctx, "session/new", map[string]any{"cwd": dir}, &setup); err != nil {
		t.Fatal(err)
	}
	var res acp.PromptResult
	if err := ag.ACP().Call(ctx, "session/prompt", map[string]any{
		"sessionId": setup.SessionID,
		"prompt":    []acp.ContentBlock{{Type: "text", Text: "hello"}},
	}, &res); err != nil {
		t.Fatal(err)
	}
	if res.Usage == nil || res.Usage.TotalTokens != 70 {
		t.Fatalf("unexpected usage: %+v", res.Usage)
	}
	joined := strings.Join(h.updates, "\n")
	if !strings.Contains(joined, "All good from codex!") || !strings.Contains(joined, "Checking files") {
		t.Fatalf("missing updates: %s", joined)
	}
}
