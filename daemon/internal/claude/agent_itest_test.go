package claude

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"agentc/internal/acp"
)

// recorder plays the session engine's role in the integration test.
type recorder struct {
	mu      sync.Mutex
	updates []map[string]any
	ext     map[string][]json.RawMessage
	perms   []map[string]any
	answer  func(params map[string]any) map[string]any
}

func (r *recorder) HandleNotification(method string, params json.RawMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if method == "session/update" {
		var n struct {
			Update map[string]any `json:"update"`
		}
		_ = json.Unmarshal(params, &n)
		r.updates = append(r.updates, n.Update)
		return
	}
	if r.ext == nil {
		r.ext = map[string][]json.RawMessage{}
	}
	r.ext[method] = append(r.ext[method], params)
}

func (r *recorder) HandleRequest(c *acp.Conn, id json.RawMessage, method string, params json.RawMessage) {
	var p map[string]any
	_ = json.Unmarshal(params, &p)
	r.mu.Lock()
	r.perms = append(r.perms, p)
	ans := r.answer
	r.mu.Unlock()
	go func() {
		out := map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": "allow"}}
		if ans != nil {
			out = ans(p)
		}
		_ = c.Reply(id, out)
	}()
}

func (r *recorder) count(kind string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, u := range r.updates {
		if u["sessionUpdate"] == kind {
			n++
		}
	}
	return n
}

func testEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "CLAUDE") || strings.HasPrefix(k, "SSH_") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "CLAUDE_CODE_ENTRYPOINT=agentc", "CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING=1")
}

// TestAgentLive drives a real `claude` (haiku). Run with CLAUDE_ITEST=1.
func TestAgentLive(t *testing.T) {
	if os.Getenv("CLAUDE_ITEST") == "" {
		t.Skip("set CLAUDE_ITEST=1 to run against a real claude")
	}
	bin, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude not installed")
	}
	home, _ := os.UserHomeDir()
	dir := t.TempDir()
	rec := &recorder{}
	a := Start(Options{Bin: bin, Env: testEnv(), HomeDir: home, LogDir: dir, Model: "haiku", Handler: rec})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	var setup acp.SessionSetup
	if err := a.Conn.Call(ctx, "session/new", map[string]any{"cwd": dir}, &setup); err != nil {
		t.Fatalf("session/new: %v", err)
	}
	if setup.SessionID == "" || setup.Models == nil || len(setup.Models.AvailableModels) == 0 || setup.Modes == nil {
		t.Fatalf("bad setup: %+v", setup)
	}
	t.Logf("session %s model %s mode %s effort opts %d", setup.SessionID, setup.Models.CurrentModelID, setup.Modes.CurrentModeID, len(setup.ConfigOptions))
	uuid := NewUUID()
	var res acp.PromptResult
	err = a.Conn.Call(ctx, "session/prompt", map[string]any{"sessionId": setup.SessionID,
		"prompt": []map[string]any{{"type": "text", "text": "Use the Write tool to create hello.txt containing exactly: hi there. Then reply with the single word done."}},
		"_meta":  map[string]any{"agentc": map[string]any{"itemId": "u1", "uuid": uuid}}}, &res)
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if res.StopReason != "end_turn" || res.Usage == nil || res.Usage.CostUSD <= 0 {
		t.Fatalf("bad result: %+v %+v", res, res.Usage)
	}
	b, err := os.ReadFile(filepath.Join(dir, "hello.txt"))
	if err != nil || !strings.Contains(string(b), "hi there") {
		t.Fatalf("file not written: %v %q", err, b)
	}
	if rec.count("tool_call") == 0 || rec.count("tool_call_update") == 0 || rec.count("agent_message_chunk") == 0 {
		t.Fatalf("missing updates: tool_call=%d update=%d text=%d", rec.count("tool_call"), rec.count("tool_call_update"), rec.count("agent_message_chunk"))
	}
	rec.mu.Lock()
	nperm := len(rec.perms)
	delivered := len(rec.ext[ExtUserMessage])
	rec.mu.Unlock()
	if nperm == 0 {
		t.Fatalf("expected a permission request for Write")
	}
	if delivered == 0 {
		t.Fatalf("no delivery notification")
	}
	// The uuid we sent should be the stored user entry's uuid.
	path, err := FindSession(ConfigDir(home), setup.SessionID, dir)
	if err != nil {
		t.Fatalf("transcript: %v", err)
	}
	if e, err := FindEntry(path, uuid); err != nil || e.Type != "user" {
		t.Fatalf("user entry by uuid: %v %+v", err, e)
	}
	if err := a.Conn.Call(ctx, "session/set_mode", map[string]any{"sessionId": setup.SessionID, "modeId": "plan"}, nil); err != nil {
		t.Fatalf("set_mode: %v", err)
	}
	var cfg struct {
		ConfigOptions []acp.ConfigOption `json:"configOptions"`
	}
	if err := a.Conn.Call(ctx, "session/set_config_option", map[string]any{"sessionId": setup.SessionID, "configId": "thought_level", "value": "low"}, &cfg); err != nil {
		t.Fatalf("set effort: %v", err)
	}
	a.Close(10 * time.Second)

	// Load it back: the replay should show the user message (with its uuid) and the Write.
	rec2 := &recorder{}
	b2 := Start(Options{Bin: bin, Env: testEnv(), HomeDir: home, LogDir: dir, Model: "haiku", Handler: rec2})
	var setup2 acp.SessionSetup
	if err := b2.Conn.Call(ctx, "session/load", map[string]any{"sessionId": setup.SessionID, "cwd": dir}, &setup2); err != nil {
		t.Fatalf("session/load: %v", err)
	}
	defer b2.Close(10 * time.Second)
	sawUUID, sawWrite := false, false
	rec2.mu.Lock()
	for _, u := range rec2.updates {
		if u["sessionUpdate"] == "user_message_chunk" {
			if m, ok := u["_meta"].(map[string]any); ok {
				if ag, ok := m["agentc"].(map[string]any); ok && ag["uuid"] == uuid {
					sawUUID = true
				}
			}
		}
		if u["sessionUpdate"] == "tool_call" && u["name"] == "Write" {
			sawWrite = true
		}
	}
	rec2.mu.Unlock()
	if !sawUUID || !sawWrite {
		t.Fatalf("replay incomplete: uuid=%v write=%v", sawUUID, sawWrite)
	}
	_ = DeleteSession(path)
}
