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

func writeFakeCodexAppServer(t *testing.T, dir string) (binPath, logPath string) {
	t.Helper()
	binPath = filepath.Join(dir, "fake-codex")
	logPath = filepath.Join(dir, "rpc.log")
	script := `#!/usr/bin/env python3
import json, os, sys, time

if len(sys.argv) > 1 and sys.argv[1] == "--version":
    print("codex-cli 0.172.0")
    sys.exit(0)

log_path = os.environ.get("FAKE_CODEX_LOG", "")
def log(rec):
    if log_path:
        with open(log_path, "a", encoding="utf-8") as f:
            f.write(json.dumps(rec) + "\n")

log({"event": "spawn", "argv": sys.argv[1:], "codex_home": os.environ.get("CODEX_HOME", ""), "has_raw_or_key": "OPENROUTER_API_KEY" in os.environ, "has_bridge_tok": "AGENTC_BRIDGE_TOKEN" in os.environ, "model_provider_arg": any("model_provider=" in a for a in sys.argv[1:]), "catalog_arg": any("model_catalog_json=" in a for a in sys.argv[1:])})

def send(obj):
    sys.stdout.write(json.dumps(obj) + "\n")
    sys.stdout.flush()

pending_srv_reqs = {}
turn_seq = 0
cum_in = 0
cum_out = 0

for raw in sys.stdin:
    raw = raw.strip()
    if not raw:
        continue
    msg = json.loads(raw)
    log({"dir": "in", "msg": msg})
    rid = msg.get("id")
    method = msg.get("method", "")
    params = msg.get("params") or {}

    if rid is not None and method == "":
        # Client reply to server-initiated request
        pending_srv_reqs[str(rid)] = msg.get("result")
        continue

    if method == "initialize":
        send({"id": rid, "result": {"userAgent": "codex/0.172.0", "codexHome": os.environ.get("CODEX_HOME", ""), "platformFamily": "unix", "platformOs": "linux"}})
    elif method == "initialized":
        pass
    elif method == "model/list":
        send({"id": rid, "result": {"data": [
            {"id": "gpt-5.3-codex", "model": "gpt-5.3-codex", "displayName": "GPT-5.3 Codex", "description": "Flagship Codex", "hidden": False, "supportedReasoningEfforts": [{"reasoningEffort": "low"}, {"reasoningEffort": "high"}], "inputModalities": ["text", "image"]},
            {"id": "o4-mini", "model": "o4-mini", "displayName": "o4-mini", "description": "Fast reasoning", "hidden": False, "supportedReasoningEfforts": [{"reasoningEffort": "medium"}], "inputModalities": ["text"]},
            {"id": "internal-hidden-model", "model": "internal-hidden-model", "displayName": "Hidden", "hidden": True}
        ]}})
    elif method == "thread/start":
        tid = "019c1111-2222-7333-8444-555555555555"
        send({"id": rid, "result": {"thread": {"id": tid, "cwd": params.get("cwd", ""), "createdAt": 1700000000, "updatedAt": 1700000000, "turns": []}, "model": params.get("model", "google/gemini-3-flash-preview")}})
    elif method == "thread/resume":
        tid = params.get("threadId") or "019c1111-2222-7333-8444-555555555555"
        send({"id": rid, "result": {"thread": {"id": tid, "cwd": params.get("cwd", ""), "createdAt": 1700000000, "updatedAt": 1700000001, "turns": []}, "model": params.get("model", "google/gemini-3-flash-preview")}})
    elif method == "thread/settings/update":
        send({"id": rid, "result": {}})
    elif method == "thread/compact/start":
        send({"id": rid, "result": {}})
        send({"method": "item/started", "params": {"threadId": params.get("threadId"), "turnId": "t_compact", "item": {"id": "cc_1", "type": "contextCompaction"}}})
        send({"method": "item/completed", "params": {"threadId": params.get("threadId"), "turnId": "t_compact", "item": {"id": "cc_1", "type": "contextCompaction"}}})
        send({"method": "thread/compacted", "params": {"threadId": params.get("threadId")}})
    elif method == "thread/fork":
        send({"id": rid, "result": {"thread": {"id": "019cf000-0000-7000-8000-forked000001", "cwd": params.get("cwd", ""), "forkedFromId": params.get("threadId"), "createdAt": 1700000010, "updatedAt": 1700000010}}})
    elif method == "thread/revert":
        send({"id": rid, "result": {"thread": {"id": params.get("threadId"), "createdAt": 1700000000, "updatedAt": 1700000020, "turns": []}}})
    elif method == "thread/name/set":
        send({"id": rid, "result": {}})
    elif method == "turn/steer":
        send({"id": rid, "result": {"turnId": params.get("expectedTurnId")}})
    elif method == "turn/interrupt":
        send({"id": rid, "result": {}})
        send({"method": "turn/completed", "params": {"threadId": params.get("threadId"), "turn": {"id": params.get("turnId"), "status": "interrupted"}}})
    elif method == "turn/start":
        turn_seq += 1
        turn_id = f"turn_{turn_seq}"
        tid = params.get("threadId")
        texts = [x.get("text", "") for x in (params.get("input") or []) if x.get("type") == "text"]
        prompt_text = " ".join(texts)

        send({"id": rid, "result": {"turn": {"id": turn_id, "status": "inProgress", "items": [], "error": None}}})
        send({"method": "turn/started", "params": {"threadId": tid, "turn": {"id": turn_id, "status": "inProgress"}}})

        if "WAIT_STEER" in prompt_text:
            # Wait briefly for a steer or interrupt before completing
            time.sleep(0.15)
            cum_in += 50
            cum_out += 20
            send({"method": "item/agentMessage/delta", "params": {"threadId": tid, "turnId": turn_id, "itemId": f"msg_{turn_seq}", "delta": "Steered turn done."}})
            send({"method": "thread/tokenUsage/updated", "params": {"threadId": tid, "turnId": turn_id, "tokenUsage": {"last": {"totalTokens": 70}, "total": {"inputTokens": cum_in, "outputTokens": cum_out, "cachedInputTokens": 10, "cacheWriteInputTokens": 0, "reasoningOutputTokens": 5, "totalTokens": cum_in + cum_out}, "modelContextWindow": 200000}}})
            send({"method": "turn/completed", "params": {"threadId": tid, "turn": {"id": turn_id, "status": "completed"}}})
            continue

        if "ASK_APPROVAL_AND_QUESTION" in prompt_text:
            # 1. Request command approval
            send({"id": 501, "method": "item/commandExecution/requestApproval", "params": {"threadId": tid, "turnId": turn_id, "itemId": "cmd_app_1", "command": "pytest -q", "cwd": "/work", "reason": "Run test suite"}})
            while "501" not in pending_srv_reqs:
                line2 = sys.stdin.readline()
                if not line2:
                    sys.exit(0)
                m2 = json.loads(line2)
                log({"dir": "in", "msg": m2})
                if m2.get("id") is not None and not m2.get("method"):
                    pending_srv_reqs[str(m2["id"])] = m2.get("result")
            app_dec = pending_srv_reqs["501"].get("decision")

            # 2. Request user input with duplicate headers to test deduplication + answer mapping
            send({"id": "q_req_1", "method": "item/tool/requestUserInput", "params": {
                "threadId": tid, "turnId": turn_id, "itemId": "call_q_1", "isBlocking": True,
                "questions": [
                    {"id": "db_choice", "header": "Config", "question": "Which database?", "options": [{"label": "SQLite", "description": "Embedded"}, {"label": "Postgres", "description": "Server"}]},
                    {"id": "cache_choice", "header": "Config", "question": "Which cache?", "options": [{"label": "Memory", "description": "In-proc"}]}
                ]
            }})
            while "q_req_1" not in pending_srv_reqs:
                line3 = sys.stdin.readline()
                if not line3:
                    sys.exit(0)
                m3 = json.loads(line3)
                log({"dir": "in", "msg": m3})
                if m3.get("id") is not None and not m3.get("method"):
                    pending_srv_reqs[str(m3["id"])] = m3.get("result")
            q_ans = pending_srv_reqs["q_req_1"].get("answers", {})
            db_val = (q_ans.get("db_choice") or {}).get("answers", ["none"])[0]
            cache_val = (q_ans.get("cache_choice") or {}).get("answers", ["none"])[0]
            send({"method": "item/agentMessage/delta", "params": {"threadId": tid, "turnId": turn_id, "itemId": f"msg_{turn_seq}", "delta": f"approval={app_dec} db={db_val} cache={cache_val}"}})
            cum_in += 100
            cum_out += 40
            send({"method": "thread/tokenUsage/updated", "params": {"threadId": tid, "turnId": turn_id, "tokenUsage": {"last": {"totalTokens": 140}, "total": {"inputTokens": cum_in, "outputTokens": cum_out, "cachedInputTokens": 20, "cacheWriteInputTokens": 0, "reasoningOutputTokens": 12, "totalTokens": cum_in + cum_out}, "modelContextWindow": 262144}}})
            send({"method": "turn/completed", "params": {"threadId": tid, "turn": {"id": turn_id, "status": "completed"}}})
            continue

        # Standard rich turn: reasoning + plan + command output + fileChange + diff + subagent + text + usage
        send({"method": "item/reasoning/summaryTextDelta", "params": {"threadId": tid, "turnId": turn_id, "itemId": f"rsn_{turn_seq}", "summaryIndex": 0, "delta": "Inspecting workspace..."}})
        send({"method": "turn/plan/updated", "params": {"threadId": tid, "turnId": turn_id, "plan": [{"step": "Edit file", "status": "inProgress"}, {"step": "Verify", "status": "pending"}]}})
        send({"method": "item/started", "params": {"threadId": tid, "turnId": turn_id, "item": {"id": f"cmd_{turn_seq}", "type": "commandExecution", "command": "echo hi", "cwd": "/work", "status": "inProgress"}}})
        send({"method": "item/commandExecution/outputDelta", "params": {"threadId": tid, "turnId": turn_id, "itemId": f"cmd_{turn_seq}", "delta": "hi\n"}})
        send({"method": "item/completed", "params": {"threadId": tid, "turnId": turn_id, "item": {"id": f"cmd_{turn_seq}", "type": "commandExecution", "command": "echo hi", "cwd": "/work", "status": "completed", "aggregatedOutput": "hi\n", "exitCode": 0, "durationMs": 12}}})
        send({"method": "turn/diff/updated", "params": {"threadId": tid, "turnId": turn_id, "diff": "diff --git a/hello.txt b/hello.txt\nnew file mode 100644\n--- /dev/null\n+++ b/hello.txt\n@@ -0,0 +1 @@\n+hello\n"}})
        send({"method": "item/completed", "params": {"threadId": tid, "turnId": turn_id, "item": {"id": f"fc_{turn_seq}", "type": "fileChange", "status": "completed", "changes": [{"path": "hello.txt", "kind": {"type": "add"}, "diff": "hello\n"}]}}})
        send({"method": "item/completed", "params": {"threadId": tid, "turnId": turn_id, "item": {"id": f"sub_{turn_seq}", "type": "collabAgentToolCall", "tool": "spawnAgent", "status": "completed", "receiverThreadIds": ["sub_t1"], "prompt": "Check tests", "agentsStates": {"sub_t1": {"status": "completed", "message": "All green"}}}}})
        send({"method": "item/agentMessage/delta", "params": {"threadId": tid, "turnId": turn_id, "itemId": f"msg_{turn_seq}", "delta": "Hello from Codex app-server!"}})
        send({"method": "item/completed", "params": {"threadId": tid, "turnId": turn_id, "item": {"id": f"msg_{turn_seq}", "type": "agentMessage", "text": "Hello from Codex app-server!"}}})

        cum_in += 120
        cum_out += 30
        send({"method": "thread/tokenUsage/updated", "params": {"threadId": tid, "turnId": turn_id, "tokenUsage": {"last": {"totalTokens": 150}, "total": {"inputTokens": cum_in, "outputTokens": cum_out, "cachedInputTokens": 25, "cacheWriteInputTokens": 0, "reasoningOutputTokens": 10, "totalTokens": cum_in + cum_out}, "modelContextWindow": 1048576}}})
        send({"method": "turn/completed", "params": {"threadId": tid, "turn": {"id": turn_id, "status": "completed"}}})
`
	if err := os.WriteFile(binPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return binPath, logPath
}

type mockACPClientHandler struct {
	mu          sync.Mutex
	updates     []map[string]any
	extensions  []string
	permHandler func(params json.RawMessage) any
}

func (h *mockACPClientHandler) HandleNotification(method string, params json.RawMessage) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if method != "session/update" {
		h.extensions = append(h.extensions, method)
		return
	}
	var sn acp.SessionNotification
	if json.Unmarshal(params, &sn) == nil {
		var u map[string]any
		if json.Unmarshal(sn.Update, &u) == nil {
			h.updates = append(h.updates, u)
		}
	}
}

func (h *mockACPClientHandler) HandleRequest(c *acp.Conn, id json.RawMessage, method string, params json.RawMessage) {
	if method == "session/request_permission" && h.permHandler != nil {
		_ = c.Reply(id, h.permHandler(params))
		return
	}
	_ = c.Reply(id, map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": "accept"}})
}

func TestCodexAgentFullLifecycleApprovalsQuestionsSteerAndReload(t *testing.T) {
	dir := nonTmpCodexTestDir(t)
	fakeBin, logPath := writeFakeCodexAppServer(t, dir)
	codexHome := filepath.Join(dir, "codex-home")
	catPath := filepath.Join(dir, "codex-openrouter-catalog.json")

	br, err := NewBridge("sk-or-secret-never-in-child-env", "http://127.0.0.1:9", 8192)
	if err != nil {
		t.Fatal(err)
	}
	defer br.Close()
	cat := NewCatalog(catPath, "http://127.0.0.1:9")

	clientHandler := &mockACPClientHandler{
		permHandler: func(params json.RawMessage) any {
			var pr struct {
				Meta struct {
					Agentc struct {
						Kind string `json:"kind"`
					} `json:"agentc"`
				} `json:"_meta"`
			}
			_ = json.Unmarshal(params, &pr)
			if pr.Meta.Agentc.Kind == "question" {
				return map[string]any{
					"outcome": map[string]any{
						"outcome":  "selected",
						"optionId": "answer",
						"_meta": map[string]any{
							"answers": map[string]any{
								"Which database?": "Postgres",
								"Config (2)":      "Memory",
							},
						},
					},
				}
			}
			return map[string]any{
				"outcome": map[string]any{
					"outcome":  "selected",
					"optionId": "acceptForSession",
				},
			}
		},
	}

	env := append(os.Environ(), "FAKE_CODEX_LOG="+logPath, "OPENROUTER_API_KEY=sk-or-secret-never-in-child-env")
	ag := Start(Options{
		Bin:           fakeBin,
		Env:           env,
		Cwd:           dir,
		CodexHome:     codexHome,
		UserHome:      dir,
		Model:         "google/gemini-3-flash-preview",
		Effort:        "high",
		Mode:          "default",
		Bridge:        br,
		Catalog:       cat,
		HasOpenRouter: true,
		Handler:       clientHandler,
	})
	defer ag.Close(2 * time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var setup acp.SessionSetup
	if err := ag.ACP().Call(ctx, "session/new", map[string]any{"cwd": dir, "mcpServers": []any{}}, &setup); err != nil {
		t.Fatalf("session/new: %v", err)
	}
	if setup.SessionID == "" || setup.Modes == nil || len(setup.Modes.AvailableModes) != 5 {
		t.Fatalf("expected session setup with 5 modes, got %+v", setup)
	}

	// Turn 1: rich streaming + per-turn usage delta (120 in + 30 out = 150).
	var res1 acp.PromptResult
	if err := ag.ACP().Call(ctx, "session/prompt", map[string]any{
		"sessionId": setup.SessionID,
		"prompt":    []acp.ContentBlock{{Type: "text", Text: "Build hello.txt"}},
	}, &res1); err != nil {
		t.Fatalf("turn 1 prompt: %v", err)
	}
	if res1.Usage == nil || res1.Usage.InputTokens != 120 || res1.Usage.OutputTokens != 30 || res1.Usage.TotalTokens != 150 {
		t.Fatalf("turn 1 usage mismatch: %+v", res1.Usage)
	}

	// Turn 2: interactive approval + structured AskUserQuestion + verify usage delta is NOT double-counted!
	var res2 acp.PromptResult
	if err := ag.ACP().Call(ctx, "session/prompt", map[string]any{
		"sessionId": setup.SessionID,
		"prompt":    []acp.ContentBlock{{Type: "text", Text: "ASK_APPROVAL_AND_QUESTION"}},
	}, &res2); err != nil {
		t.Fatalf("turn 2 prompt: %v", err)
	}
	// Cumulative was 120+100=220 in, 30+40=70 out -> per-turn delta MUST be 100 in, 40 out, 140 total!
	if res2.Usage == nil || res2.Usage.InputTokens != 100 || res2.Usage.OutputTokens != 40 || res2.Usage.TotalTokens != 140 {
		t.Fatalf("turn 2 per-turn usage delta mismatch (double-counted?): %+v", res2.Usage)
	}

	// Turn 3: concurrent active mid-turn steering (`turn/steer`).
	errCh := make(chan error, 1)
	go func() {
		var r acp.PromptResult
		errCh <- ag.ACP().Call(ctx, "session/prompt", map[string]any{
			"sessionId": setup.SessionID,
			"prompt":    []acp.ContentBlock{{Type: "text", Text: "WAIT_STEER"}},
		}, &r)
	}()
	time.Sleep(40 * time.Millisecond)
	var steerRes acp.PromptResult
	if err := ag.ACP().Call(ctx, "session/prompt", map[string]any{
		"sessionId": setup.SessionID,
		"prompt":    []acp.ContentBlock{{Type: "text", Text: "Also check edge cases"}},
	}, &steerRes); err != nil {
		t.Fatalf("steer prompt: %v", err)
	}
	if steerRes.StopReason != "steered" {
		t.Fatalf("expected StopReason='steered', got %q", steerRes.StopReason)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("primary steered turn failed: %v", err)
	}

	// Slash /compact -> thread/compact/start.
	var compactRes acp.PromptResult
	if err := ag.ACP().Call(ctx, "session/prompt", map[string]any{
		"sessionId": setup.SessionID,
		"prompt":    []acp.ContentBlock{{Type: "text", Text: "/compact"}},
	}, &compactRes); err != nil {
		t.Fatalf("/compact: %v", err)
	}

	// Live fork & thread/revert (NOT thread/rollback!).
	var forked Thread
	if err := ag.ACP().Call(ctx, "_agentc/fork", map[string]any{"beforeTurnId": "turn_2", "cwd": dir}, &forked); err != nil || forked.ID == "" {
		t.Fatalf("_agentc/fork: %v %+v", err, forked)
	}
	var reverted Thread
	if err := ag.ACP().Call(ctx, "_agentc/revert", map[string]any{"beforeTurnId": "turn_2"}, &reverted); err != nil {
		t.Fatalf("_agentc/revert: %v", err)
	}

	// Switch to an uncataloged arbitrary custom slug while idle -> transparent app-server reload + thread/resume!
	if err := ag.ACP().Call(ctx, "session/set_model", map[string]any{
		"sessionId": setup.SessionID,
		"modelId":   "newvendor/ultra-coder-9000",
	}, nil); err != nil {
		t.Fatalf("session/set_model unlisted slug: %v", err)
	}

	// Inspect RPC log to verify:
	// 1. Child process NEVER had OPENROUTER_API_KEY in env (`has_raw_or_key == false`) and DID have `AGENTC_BRIDGE_TOKEN`.
	// 2. `thread/revert` (NOT `thread/rollback`) was called with `beforeTurnId: "turn_2"`.
	// 3. `collaborationMode` on `turn/start` was a struct `{mode, settings}` (not a bare string).
	// 4. Approval + AskUserQuestion answers were delivered (`approval=acceptForSession db=Postgres cache=Memory`).
	// 5. Idle custom slug switch spawned a 2nd app-server and called `thread/resume` with `excludeTurns: true`.
	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	logStr := string(logBytes)
	if strings.Contains(logStr, `"has_raw_or_key": true`) || strings.Contains(logStr, "sk-or-secret-never-in-child-env") {
		t.Fatalf("SECURITY VIOLATION: raw OpenRouter key leaked into child codex env/args!\n%s", logStr)
	}
	if !strings.Contains(logStr, `"method": "thread/revert"`) || strings.Contains(logStr, `thread/rollback`) {
		t.Fatalf("expected thread/revert (never thread/rollback) in wire log:\n%s", logStr)
	}
	if strings.Count(logStr, `"event": "spawn"`) < 2 {
		t.Fatalf("expected transparent app-server reload on uncataloged custom slug switch:\n%s", logStr)
	}

	clientHandler.mu.Lock()
	var fullAgentText strings.Builder
	sawDualDiff := false
	for _, u := range clientHandler.updates {
		if u["sessionUpdate"] == "agent_message_chunk" {
			if c, ok := u["content"].(map[string]any); ok {
				fullAgentText.WriteString(asString(c["text"]))
			}
		}
		if u["kind"] == "edit" {
			_, hasContent := u["content"]
			ri, _ := u["rawInput"].(map[string]any)
			if hasContent && ri != nil && ri["path"] != nil {
				sawDualDiff = true
			}
		}
	}
	clientHandler.mu.Unlock()

	if !strings.Contains(fullAgentText.String(), "approval=acceptForSession db=Postgres cache=Memory") {
		t.Fatalf("missing approval/question summary in agent text: %q", fullAgentText.String())
	}
	if !sawDualDiff {
		t.Fatalf("expected dual content + rawInput diff on fileChange update")
	}
}

func TestCodexAgentNativeOpenAIModeAndProviderSwitch(t *testing.T) {
	dir := nonTmpCodexTestDir(t)
	fakeBin, logPath := writeFakeCodexAppServer(t, dir)
	userHome := filepath.Join(dir, "user-home")
	codexHome := filepath.Join(dir, "codex-home")
	catPath := filepath.Join(dir, "codex-openrouter-catalog.json")

	if err := os.MkdirAll(filepath.Join(userHome, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userHome, ".codex", "auth.json"), []byte(`{"tokens":{"access_token":"fake-native-oauth-token"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if !HasNativeOpenAIAuth(codexHome, userHome, nil) {
		t.Fatal("expected HasNativeOpenAIAuth == true from userHome/.codex/auth.json")
	}
	if err := EnsureCodexHome(codexHome, userHome); err != nil {
		t.Fatalf("EnsureCodexHome: %v", err)
	}
	if st, err := os.Lstat(filepath.Join(codexHome, "auth.json")); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected symlinked auth.json in codexHome, st=%+v err=%v", st, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	nativeModels, err := NativeModelsRPC(ctx, ProcConfig{
		Bin:       fakeBin,
		Cwd:       dir,
		CodexHome: codexHome,
		UserHome:  userHome,
		Env:       []string{"FAKE_CODEX_LOG=" + logPath},
	})
	if err != nil {
		t.Fatalf("NativeModelsRPC: %v", err)
	}
	if len(nativeModels) != 2 || nativeModels[0].Provider != "openai" {
		t.Fatalf("expected 2 non-hidden native openai models, got %+v", nativeModels)
	}

	// 1. Pure Native OpenAI mode (HasOpenRouter: false, HasNativeAuth: true).
	_ = os.Remove(logPath)
	agNative := Start(Options{
		Bin:           fakeBin,
		Cwd:           dir,
		CodexHome:     codexHome,
		UserHome:      userHome,
		Env:           []string{"FAKE_CODEX_LOG=" + logPath},
		HasOpenRouter: false,
		HasNativeAuth: true,
		Handler:       &mockACPClientHandler{},
	})
	var setupNative acp.SessionSetup
	if err := agNative.ACP().Call(ctx, "session/new", map[string]any{"cwd": dir, "mcpServers": []any{}}, &setupNative); err != nil {
		t.Fatalf("native session/new: %v", err)
	}
	var resNative acp.PromptResult
	if err := agNative.ACP().Call(ctx, "session/prompt", map[string]any{
		"sessionId": setupNative.SessionID,
		"prompt":    []acp.ContentBlock{{Type: "text", Text: "Native hello"}},
	}, &resNative); err != nil {
		t.Fatalf("native session/prompt: %v", err)
	}
	agNative.Close(2 * time.Second)

	nativeLogBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	nativeLog := string(nativeLogBytes)
	if strings.Contains(nativeLog, `"model_provider_arg": true`) || strings.Contains(nativeLog, `"catalog_arg": true`) || strings.Contains(nativeLog, `"has_bridge_tok": true`) {
		t.Fatalf("native mode leaked OpenRouter config overrides:\n%s", nativeLog)
	}
	if strings.Contains(nativeLog, `"modelProvider": "openrouter"`) {
		t.Fatalf("native thread/start unexpectedly set modelProvider=openrouter:\n%s", nativeLog)
	}

	// 2. Dual-auth mid-session switch: OpenRouter -> Native OpenAI -> OpenRouter!
	_ = os.Remove(logPath)
	br, err := NewBridge("sk-or-dual-auth", "http://127.0.0.1:9", 8192)
	if err != nil {
		t.Fatal(err)
	}
	defer br.Close()
	cat := NewCatalog(catPath, "http://127.0.0.1:9")

	agDual := Start(Options{
		Bin:           fakeBin,
		Cwd:           dir,
		CodexHome:     codexHome,
		UserHome:      userHome,
		Env:           []string{"FAKE_CODEX_LOG=" + logPath},
		Model:         "google/gemini-2.5-flash",
		Bridge:        br,
		Catalog:       cat,
		HasOpenRouter: true,
		HasNativeAuth: true,
		Handler:       &mockACPClientHandler{},
	})
	defer agDual.Close(2 * time.Second)

	var setupDual acp.SessionSetup
	if err := agDual.ACP().Call(ctx, "session/new", map[string]any{"cwd": dir, "mcpServers": []any{}}, &setupDual); err != nil {
		t.Fatalf("dual session/new: %v", err)
	}
	if err := agDual.ACP().Call(ctx, "session/set_model", map[string]any{
		"sessionId": setupDual.SessionID,
		"modelId":   "gpt-5.3-codex",
	}, nil); err != nil {
		t.Fatalf("switch to native gpt-5.3-codex: %v", err)
	}
	if err := agDual.ACP().Call(ctx, "session/set_model", map[string]any{
		"sessionId": setupDual.SessionID,
		"modelId":   "openrouter/google/gemini-3-flash-preview",
	}, nil); err != nil {
		t.Fatalf("switch back to openrouter model: %v", err)
	}

	dualLogBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	var spawnProviders []bool
	for _, line := range strings.Split(strings.TrimSpace(string(dualLogBytes)), "\n") {
		var rec map[string]any
		if json.Unmarshal([]byte(line), &rec) == nil && rec["event"] == "spawn" {
			mp, _ := rec["model_provider_arg"].(bool)
			spawnProviders = append(spawnProviders, mp)
		}
	}
	if len(spawnProviders) != 3 || !spawnProviders[0] || spawnProviders[1] || !spawnProviders[2] {
		t.Fatalf("expected spawn model_provider_arg sequence [true, false, true], got %v", spawnProviders)
	}
}

func TestHandleSetModelConcurrentTurnTOCTOU(t *testing.T) {
	dir := nonTmpCodexTestDir(t)
	fakeBin, logPath := writeFakeCodexAppServer(t, dir)
	codexHome := filepath.Join(dir, "codex-home")
	catPath := filepath.Join(dir, "codex-openrouter-catalog.json")

	br, err := NewBridge("sk-or-toctou", "http://127.0.0.1:9", 8192)
	if err != nil {
		t.Fatal(err)
	}
	defer br.Close()
	cat := NewCatalog(catPath, "http://127.0.0.1:9")

	ag := Start(Options{
		Bin:           fakeBin,
		Cwd:           dir,
		CodexHome:     codexHome,
		UserHome:      dir,
		Env:           []string{"FAKE_CODEX_LOG=" + logPath},
		Model:         "google/gemini-3-flash-preview",
		Bridge:        br,
		Catalog:       cat,
		HasOpenRouter: true,
		Handler:       &mockACPClientHandler{},
	})
	defer ag.Close(2 * time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var setup acp.SessionSetup
	if err := ag.ACP().Call(ctx, "session/new", map[string]any{"cwd": dir, "mcpServers": []any{}}, &setup); err != nil {
		t.Fatalf("session/new: %v", err)
	}

	ag.mu.Lock()
	origProc := ag.proc
	ag.mu.Unlock()

	turnErrCh := make(chan error, 1)
	go func() {
		var r acp.PromptResult
		turnErrCh <- ag.ACP().Call(ctx, "session/prompt", map[string]any{
			"sessionId": setup.SessionID,
			"prompt":    []acp.ContentBlock{{Type: "text", Text: "WAIT_STEER"}},
		}, &r)
	}()

	time.Sleep(25 * time.Millisecond)
	if err := ag.ACP().Call(ctx, "session/set_model", map[string]any{
		"sessionId": setup.SessionID,
		"modelId":   "vendor/brand-new-concurrent-slug",
	}, nil); err != nil {
		t.Fatalf("concurrent set_model: %v", err)
	}

	if err := <-turnErrCh; err != nil {
		t.Fatalf("in-flight turn was killed or failed during concurrent set_model: %v", err)
	}
	ag.mu.Lock()
	sameProc := ag.proc == origProc && origProc.Alive()
	ag.mu.Unlock()
	if !sameProc {
		t.Fatalf("expected active proc to survive concurrent set_model without mid-turn close")
	}
}

func TestDeleteThreadRPCZeroSpawnMultiHome(t *testing.T) {
	dir := nonTmpCodexTestDir(t)
	home1 := filepath.Join(dir, "codex-home-1")
	home2 := filepath.Join(dir, "codex-home-2")
	userHome := filepath.Join(dir, "user-home")
	tid := "019cdead-beef-7000-8000-000000000123"

	paths := []string{
		filepath.Join(home1, "sessions", "2026", "11", "24", "rollout-2026-11-24T01-00-00-"+tid+".jsonl"),
		filepath.Join(home1, "archived_sessions", "rollout-2026-11-24T01-00-00-"+tid+".jsonl"),
		filepath.Join(home2, "sessions", "2026", "11", "24", "rollout-2026-11-24T02-00-00-"+tid+".jsonl"),
		filepath.Join(userHome, ".codex", "sessions", "2026", "11", "24", "rollout-2026-11-24T03-00-00-"+tid+".jsonl"),
		filepath.Join(home1, "thread-writer-locks", tid+".lock"),
	}
	for _, p := range paths {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	err := DeleteThreadRPC(context.Background(), ProcConfig{
		Bin:       "/nonexistent/codex-must-never-be-spawned",
		CodexHome: home1,
		UserHome:  userHome,
	}, tid, home1, home2)
	if err != nil {
		t.Fatalf("DeleteThreadRPC failed: %v", err)
	}
	if HasThread(tid, home1, home2, filepath.Join(userHome, ".codex")) {
		t.Fatalf("expected all rollout files deleted across homes")
	}
	if _, err := os.Stat(filepath.Join(home1, "thread-writer-locks", tid+".lock")); !os.IsNotExist(err) {
		t.Fatalf("expected writer lock removed, got err=%v", err)
	}
}
