package codex

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestBridgeAuthAndSecretIsolation(t *testing.T) {
	var seenAuth atomic.Value
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_1","output":[]}`))
	}))
	defer upstream.Close()

	br, err := NewBridge("sk-or-super-secret-in-memory", upstream.URL, 4096)
	if err != nil {
		t.Fatal(err)
	}
	defer br.Close()

	// 1. Missing/wrong bridge token -> 401.
	reqBad, _ := http.NewRequest(http.MethodPost, br.BaseURL()+"/responses", strings.NewReader(`{"model":"google/gemini-2.5-flash"}`))
	reqBad.Header.Set("Authorization", "Bearer wrong-token")
	resBad, err := http.DefaultClient.Do(reqBad)
	if err != nil {
		t.Fatal(err)
	}
	_ = resBad.Body.Close()
	if resBad.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for bad bridge token, got %d", resBad.StatusCode)
	}

	// 2. Valid bridge token -> injects real OpenRouter bearer key upstream.
	reqOK, _ := http.NewRequest(http.MethodPost, br.BaseURL()+"/responses", strings.NewReader(`{"model":"google/gemini-2.5-flash"}`))
	reqOK.Header.Set("Authorization", "Bearer "+br.BridgeToken())
	resOK, err := http.DefaultClient.Do(reqOK)
	if err != nil {
		t.Fatal(err)
	}
	_ = resOK.Body.Close()
	if resOK.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resOK.StatusCode)
	}
	if got, _ := seenAuth.Load().(string); got != "Bearer sk-or-super-secret-in-memory" {
		t.Fatalf("upstream auth header mismatch: %q", got)
	}
}

func TestBridgeRequestSanitizationAndApplyPatchSSETranslation(t *testing.T) {
	var capturedReq map[string]any
	patchPayload := "*** Begin Patch\n*** Add File: hello.txt\n+hello world\n*** End Patch"

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &capturedReq)

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)

		argsJSON, _ := json.Marshal(map[string]string{"patch": patchPayload})
		half := len(argsJSON) / 2

		events := []struct {
			ev   string
			data map[string]any
		}{
			{"response.created", map[string]any{"type": "response.created", "response": map[string]any{"id": "r1"}}},
			{"response.output_item.added", map[string]any{
				"type":         "response.output_item.added",
				"output_index": 0,
				"item": map[string]any{
					"type":      "function_call",
					"id":        "fc_patch_1",
					"call_id":   "call_patch_1",
					"name":      "apply_patch",
					"status":    "in_progress",
					"arguments": "",
				},
			}},
			{"response.function_call_arguments.delta", map[string]any{
				"type":         "response.function_call_arguments.delta",
				"item_id":      "fc_patch_1",
				"output_index": 0,
				"delta":        string(argsJSON[:half]),
			}},
			{"response.function_call_arguments.delta", map[string]any{
				"type":         "response.function_call_arguments.delta",
				"item_id":      "fc_patch_1",
				"output_index": 0,
				"delta":        string(argsJSON[half:]),
			}},
			{"response.function_call_arguments.done", map[string]any{
				"type":         "response.function_call_arguments.done",
				"item_id":      "fc_patch_1",
				"output_index": 0,
				"arguments":    string(argsJSON),
			}},
			{"response.output_item.done", map[string]any{
				"type":         "response.output_item.done",
				"output_index": 0,
				"item": map[string]any{
					"type":      "function_call",
					"id":        "fc_patch_1",
					"call_id":   "call_patch_1",
					"name":      "apply_patch",
					"status":    "completed",
					"arguments": string(argsJSON),
				},
			}},
			{"response.completed", map[string]any{
				"type": "response.completed",
				"response": map[string]any{
					"id": "r1",
					"output": []any{
						map[string]any{
							"type":      "function_call",
							"id":        "fc_patch_1",
							"call_id":   "call_patch_1",
							"name":      "apply_patch",
							"status":    "completed",
							"arguments": string(argsJSON),
						},
					},
				},
			}},
		}
		for _, e := range events {
			b, _ := json.Marshal(e.data)
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.ev, string(b))
			if fl != nil {
				fl.Flush()
			}
		}
	}))
	defer upstream.Close()

	br, err := NewBridge("sk-test-key", upstream.URL, 8192)
	if err != nil {
		t.Fatal(err)
	}
	defer br.Close()

	reqBody := map[string]any{
		"model":             "openrouter/google/gemini-3-flash-preview",
		"stream":            true,
		"client_metadata":   map[string]any{"foo": "bar"},
		"prompt_cache_key":  "pc_123",
		"include":           []any{"reasoning.encrypted_content"},
		"max_output_tokens": 65536,
		"tools": []any{
			map[string]any{
				"type":        "custom",
				"name":        "apply_patch",
				"description": "The `apply_patch` tool can be used to edit files. This is a FREEFORM tool, so do not wrap the patch in JSON.",
			},
			map[string]any{
				"type": "namespace",
				"name": "multi_agent_v1",
				"tools": []any{
					map[string]any{"type": "function", "name": "spawn_agent"},
				},
			},
		},
		"input": []any{
			map[string]any{
				"type":    "custom_tool_call",
				"id":      "ctc_prev",
				"call_id": "call_prev",
				"name":    "apply_patch",
				"input":   "*** Begin Patch\n*** End Patch",
			},
			map[string]any{
				"type":    "custom_tool_call_output",
				"id":      "ctco_prev",
				"call_id": "call_prev",
				"output":  "Success",
			},
		},
	}
	rawBody, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest(http.MethodPost, br.BaseURL()+"/responses", bytes.NewReader(rawBody))
	req.Header.Set("Authorization", "Bearer "+br.BridgeToken())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	sseBytes, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if capturedReq["model"] != "google/gemini-3-flash-preview" {
		t.Fatalf("expected stripped model slug, got %v", capturedReq["model"])
	}
	if _, has := capturedReq["client_metadata"]; has {
		t.Fatal("client_metadata was not stripped")
	}
	if _, has := capturedReq["prompt_cache_key"]; has {
		t.Fatal("prompt_cache_key was not stripped")
	}
	if _, has := capturedReq["include"]; has {
		t.Fatalf("reasoning.encrypted_content was not stripped: %v", capturedReq["include"])
	}
	if int(capturedReq["max_output_tokens"].(float64)) != 8192 {
		t.Fatalf("expected max_output_tokens=8192, got %v", capturedReq["max_output_tokens"])
	}

	// Verify outbound apply_patch schema matches PROJECT.md § Interface Contracts 3 ("patch" property).
	tools := capturedReq["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("expected 2 flattened tools, got %d: %+v", len(tools), tools)
	}
	apTool := tools[0].(map[string]any)
	if apTool["type"] != "function" || apTool["name"] != "apply_patch" {
		t.Fatalf("apply_patch tool not rewritten to function: %+v", apTool)
	}
	apDesc, _ := apTool["description"].(string)
	for _, forbidden := range []string{"do not wrap the patch in JSON", "FREEFORM"} {
		if strings.Contains(apDesc, forbidden) {
			t.Fatalf("apply_patch description retained contradictory text %q: %s", forbidden, apDesc)
		}
	}
	for _, required := range []string{"*** Begin Patch", "*** Add File:", "*** Update File:", "*** Delete File:", "@@"} {
		if !strings.Contains(apDesc, required) {
			t.Fatalf("apply_patch description missing V4A rule %q: %s", required, apDesc)
		}
	}
	props := apTool["parameters"].(map[string]any)["properties"].(map[string]any)
	if _, ok := props["patch"]; !ok {
		t.Fatalf("expected 'patch' property in apply_patch schema: %+v", props)
	}

	// Verify historical custom_tool_call* items rewritten to function_call*.
	inputs := capturedReq["input"].([]any)
	if inputs[0].(map[string]any)["type"] != "function_call" || inputs[1].(map[string]any)["type"] != "function_call_output" {
		t.Fatalf("historical input items not rewritten: %+v", inputs)
	}

	// Verify SSE stream translated function_call back into custom_tool_call with intact patch.
	sseStr := string(sseBytes)
	for _, wantSub := range []string{
		"event: response.output_item.added",
		`"type":"custom_tool_call"`,
		"event: response.custom_tool_call_input.delta",
		"event: response.custom_tool_call_input.done",
		"*** Add File: hello.txt",
	} {
		if !strings.Contains(sseStr, wantSub) {
			t.Fatalf("SSE output missing %q:\n%s", wantSub, sseStr)
		}
	}
}

func TestBridgeHTTP402AffordRetry(t *testing.T) {
	var attempts atomic.Int32
	var seenMaxTokens []int

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		raw, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		mt := int(m["max_output_tokens"].(float64))
		seenMaxTokens = append(seenMaxTokens, mt)

		if n == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusPaymentRequired)
			_, _ = w.Write([]byte(`{"error":{"message":"This request requires more credits, or fewer max_tokens. You requested up to 8192 tokens, but can only afford 1024."}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"resp_ok","output":[]}`))
	}))
	defer upstream.Close()

	br, err := NewBridge("sk-test", upstream.URL, 8192)
	if err != nil {
		t.Fatal(err)
	}
	defer br.Close()

	req, _ := http.NewRequest(http.MethodPost, br.BaseURL()+"/responses", strings.NewReader(`{"model":"google/gemini-2.5-flash"}`))
	req.Header.Set("Authorization", "Bearer "+br.BridgeToken())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected retry 200 OK, got %d", resp.StatusCode)
	}
	if len(seenMaxTokens) != 2 || seenMaxTokens[0] != 8192 || seenMaxTokens[1] != 960 {
		t.Fatalf("expected max_output_tokens [8192, 960], got %v", seenMaxTokens)
	}

	// Verify afford < 256 does NOT retry and surfaces 402 cleanly.
	lowUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"error":{"message":"can only afford 128"}}`))
	}))
	defer lowUpstream.Close()

	attempts.Store(0)
	br.SetUpstreamURL(lowUpstream.URL)
	reqLow, _ := http.NewRequest(http.MethodPost, br.BaseURL()+"/responses", strings.NewReader(`{"model":"google/gemini-2.5-flash"}`))
	reqLow.Header.Set("Authorization", "Bearer "+br.BridgeToken())
	respLow, err := http.DefaultClient.Do(reqLow)
	if err != nil {
		t.Fatal(err)
	}
	_ = respLow.Body.Close()
	if respLow.StatusCode != http.StatusPaymentRequired || attempts.Load() != 1 {
		t.Fatalf("expected single 402 without retry when afford < 256, got status=%d attempts=%d", respLow.StatusCode, attempts.Load())
	}
}

func TestExtractPatchInputFallbacks(t *testing.T) {
	p := "*** Begin Patch\n*** Delete File: old.go\n*** End Patch"
	for _, raw := range []string{
		fmt.Sprintf(`{"patch":%q}`, p),
		fmt.Sprintf(`{"input":%q}`, p),
		fmt.Sprintf(`{"diff":%q}`, p),
		p,
	} {
		if got := extractPatchInput(raw); got != p {
			t.Fatalf("extractPatchInput(%s) = %q, want %q", raw, got, p)
		}
	}
}

func TestBridgeRetriesWithoutEncryptedReasoningOnModelSwitch(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		inp, _ := body["input"].([]any)
		for _, item := range inp {
			im, _ := item.(map[string]any)
			if _, ok := im["encrypted_content"]; ok {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":{"message":"Your request contains encrypted reasoning or compaction content that can only be processed by the provider endpoint that created it"}}`))
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_ok","output":[{"type":"message","content":[{"type":"output_text","text":"switched ok"}]}]}`))
	}))
	defer upstream.Close()

	br, err := NewBridge("sk-or-v1-unit-test", upstream.URL, 8192)
	if err != nil {
		t.Fatal(err)
	}
	defer br.Close()

	reqBody := `{
		"model": "openrouter/google/gemini-2.5-flash",
		"stream": false,
		"input": [
			{"type": "message", "role": "user", "content": "hi"},
			{"type": "reasoning", "id": "rs_1", "summary": [], "encrypted_content": "gemini3-thought-sig"},
			{"type": "compaction", "id": "cmp_1", "encrypted_content": "enc-compaction"},
			{"type": "message", "role": "assistant", "content": "hello"}
		]
	}`
	req, _ := http.NewRequest(http.MethodPost, br.BaseURL()+"/responses", strings.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+br.BridgeToken())
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 after stripping encrypted items, got %d", resp.StatusCode)
	}
	if calls != 2 {
		t.Fatalf("expected exactly 2 upstream calls (initial 404 + clean retry), got %d", calls)
	}
}

func TestBridgeSSEParallelApplyPatchAtIndexZeroAndExecCommand(t *testing.T) {
	sseStream := strings.Join([]string{
		`event: response.output_item.added`,
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_patch_0","call_id":"call_patch_0","name":"apply_patch","arguments":""}}`,
		``,
		`event: response.function_call_arguments.delta`,
		`data: {"type":"response.function_call_arguments.delta","item_id":"fc_patch_0","delta":"{\"patch\":\"*** Begin Patch\\n*** End Patch\"}"}`,
		``,
		`event: response.function_call_arguments.done`,
		`data: {"type":"response.function_call_arguments.done","item_id":"fc_patch_0","arguments":"{\"patch\":\"*** Begin Patch\\n*** End Patch\"}"}`,
		``,
		`event: response.output_item.done`,
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc_patch_0","call_id":"call_patch_0","name":"apply_patch","arguments":"{\"patch\":\"*** Begin Patch\\n*** End Patch\"}"}}`,
		``,
		`event: response.output_item.added`,
		`data: {"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"fc_exec_1","call_id":"call_exec_1","name":"exec_command","arguments":""}}`,
		``,
		// Omit output_index here to verify outIdx=0 default NEVER collides with byIdx[0]!
		`event: response.function_call_arguments.delta`,
		`data: {"type":"response.function_call_arguments.delta","item_id":"fc_exec_1","delta":"{\"cmd\":\"pytest\"}"}`,
		``,
		`event: response.output_item.done`,
		`data: {"type":"response.output_item.done","item":{"type":"function_call","id":"fc_exec_1","call_id":"call_exec_1","name":"exec_command","arguments":"{\"cmd\":\"pytest\"}"}}`,
		``,
	}, "\n") + "\n"

	rec := httptest.NewRecorder()
	streamResponsesSSE(rec, strings.NewReader(sseStream))
	out := rec.Body.String()
	if !strings.Contains(out, `"type":"custom_tool_call"`) {
		t.Fatalf("expected custom_tool_call for apply_patch, got:\n%s", out)
	}
	if !strings.Contains(out, `"item_id":"fc_exec_1","delta":"{\"cmd\":\"pytest\"}"`) {
		t.Fatalf("exec_command delta without output_index was swallowed by index 0 state! got:\n%s", out)
	}
	if !strings.Contains(out, `"name":"exec_command"`) {
		t.Fatalf("exec_command output_item.done was overwritten! got:\n%s", out)
	}
}

func TestBridgeHTTP402AffordCeilingCacheAndReset(t *testing.T) {
	var mu sync.Mutex
	var seenMaxTokens []int
	var ceiling = 1024

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		mt := int(m["max_output_tokens"].(float64))

		mu.Lock()
		seenMaxTokens = append(seenMaxTokens, mt)
		curCeil := ceiling
		mu.Unlock()

		if mt > curCeil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusPaymentRequired)
			_, _ = fmt.Fprintf(w, `{"error":{"message":"You requested up to %d tokens, but can only afford %d."}}`, mt, curCeil)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"resp_ok","output":[]}`))
	}))
	defer upstream.Close()

	br, err := NewBridge("sk-test", upstream.URL, 8192)
	if err != nil {
		t.Fatal(err)
	}
	defer br.Close()

	postOnce := func() {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, br.BaseURL()+"/responses", strings.NewReader(`{"model":"google/gemini-2.5-flash"}`))
		req.Header.Set("Authorization", "Bearer "+br.BridgeToken())
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
		}
	}

	// Request 1: sends 8192 -> 402 (afford 1024) -> retries with 960 -> caches 960.
	postOnce()
	// Request 2: uses cached 960 immediately (only 1 upstream call, no 402!).
	postOnce()

	mu.Lock()
	got1 := append([]int(nil), seenMaxTokens...)
	seenMaxTokens = nil
	ceiling = 600 // Spend drops further mid-session -> 960 hits 402 (afford 600) -> retries 536 -> caches 536.
	mu.Unlock()

	if fmt.Sprint(got1) != "[8192 960 960]" {
		t.Fatalf("expected [8192 960 960], got %v", got1)
	}

	postOnce()
	postOnce()

	mu.Lock()
	got2 := append([]int(nil), seenMaxTokens...)
	seenMaxTokens = nil
	ceiling = 10000
	mu.Unlock()

	if fmt.Sprint(got2) != "[960 536 536]" {
		t.Fatalf("expected [960 536 536], got %v", got2)
	}

	// SetAPIKey, SetMaxOutputTokens, and SetUpstreamURL must each clear the cached ceiling.
	br.SetAPIKey("sk-test-new")
	postOnce()
	br.recordAffordCap(500)
	br.SetMaxOutputTokens(4096)
	postOnce()
	br.recordAffordCap(500)
	br.SetUpstreamURL(upstream.URL + "/v2")
	postOnce()

	mu.Lock()
	got3 := append([]int(nil), seenMaxTokens...)
	mu.Unlock()

	if fmt.Sprint(got3) != "[8192 4096 4096]" {
		t.Fatalf("expected reset max_output_tokens [8192 4096 4096], got %v", got3)
	}
}

func TestNormalizeV4APatchQuirksAndRealCodexExecution(t *testing.T) {
	// 1. Valid V4A patch (with Move to + End of File + SQL/C++ operators) must remain byte-for-byte identical.
	validV4A := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: query.sql",
		"*** Move to: sql/query_v2.sql",
		"@@",
		" SELECT 1;",
		"---- old sql comment",
		"++++ new marker",
		"*** End of File",
		"*** End Patch",
	}, "\n")
	if got := normalizeV4APatch(validV4A); got != validV4A {
		t.Fatalf("valid V4A patch mutated:\ngot:\n%s\nwant:\n%s", got, validV4A)
	}

	codexBin := ""
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		cand := filepath.Join(home, ".local", "bin", "codex")
		if st, err := os.Stat(cand); err == nil && !st.IsDir() {
			codexBin = cand
		}
	}
	if codexBin == "" {
		if lp, err := exec.LookPath("codex"); err == nil {
			codexBin = lp
		}
	}

	runCodexApply := func(name string, files map[string]string, rawArg string) map[string]string {
		t.Helper()
		patch := extractPatchInput(rawArg)
		if !strings.HasPrefix(patch, "*** Begin Patch\n") || !strings.HasSuffix(patch, "\n*** End Patch") {
			t.Fatalf("[%s] normalized patch missing Begin/End framing:\n%s", name, patch)
		}
		if codexBin == "" {
			return nil
		}
		work := nonTmpCodexTestDir(t)
		for rel, content := range files {
			full := filepath.Join(work, rel)
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		cmd := exec.Command(codexBin, "--codex-run-as-apply-patch", patch)
		cmd.Dir = work
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("[%s] real codex --codex-run-as-apply-patch failed: %v\nout=%s\npatch:\n%s", name, err, string(out), patch)
		}
		res := map[string]string{}
		_ = filepath.Walk(work, func(p string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() {
				rel, _ := filepath.Rel(work, p)
				b, _ := os.ReadFile(p)
				res[rel] = string(b)
			}
			return nil
		})
		return res
	}

	// Case A: Observed R1 Auditor/Reviewer failure (---/+++ + @@ -1,2 +1,2 @@ inside Update File).
	if res := runCodexApply("auditor_unified_hunk", map[string]string{
		"calc.py": "def add(a, b):\n    return a - b\n",
	}, `{"patch":"*** Begin Patch\n*** Update File: calc.py\n--- calc.py\n+++ calc.py\n@@ -1,2 +1,2 @@\n def add(a, b):\n-    return a - b\n+    return a + b\n*** End Patch"}`); res != nil {
		if res["calc.py"] != "def add(a, b):\n    return a + b\n" {
			t.Fatalf("unexpected calc.py: %q", res["calc.py"])
		}
	}

	// Case B: Observed Challenger failure (omitted @@ + unprefixed context lines).
	if res := runCodexApply("challenger_missing_at_and_space", map[string]string{
		"app.py": "X = 1\n\ndef f(n):\n    return n + 5\n",
	}, `{"patch":"*** Begin Patch\n*** Update File: app.py\n-X = 1\n+X = 2\n\ndef f(n):\n-    return n + 5\n+    return n * 10\n*** End Patch"}`); res != nil {
		if res["app.py"] != "X = 2\n\ndef f(n):\n    return n * 10\n" {
			t.Fatalf("unexpected app.py: %q", res["app.py"])
		}
	}

	// Case C: Repeated anchor on first hunk line (@@ -2,2 +2,2 @@ def add(self, a, b): + "    def add(self, a, b):") -> bare @@.
	if res := runCodexApply("repeated_anchor_in_context", map[string]string{
		"cls.py": "class Calc:\n    def add(self, a, b):\n        return a - b\n",
	}, "```diff\n*** Update File: cls.py\n@@ -2,2 +2,2 @@ def add(self, a, b):\n     def add(self, a, b):\n-        return a - b\n+        return a + b\n```"); res != nil {
		if res["cls.py"] != "class Calc:\n    def add(self, a, b):\n        return a + b\n" {
			t.Fatalf("unexpected cls.py: %q", res["cls.py"])
		}
	}

	// Case D: Non-repeated enclosing anchor disambiguating duplicate blocks.
	if res := runCodexApply("disambiguating_anchor", map[string]string{
		"dup.py": "def first(a, b):\n    return a - b\n\ndef sub(a, b):\n    return a - b\n",
	}, "*** Begin Patch\n*** Update File: dup.py\n@@ -5,1 +5,1 @@ def sub(a, b):\n-    return a - b\n+    return a + b\n*** End Patch"); res != nil {
		want := "def first(a, b):\n    return a - b\n\ndef sub(a, b):\n    return a + b\n"
		if res["dup.py"] != want {
			t.Fatalf("unexpected dup.py:\ngot:\n%s\nwant:\n%s", res["dup.py"], want)
		}
	}

	// Case E: Fenced pure multi-file diff --git (modify + add + delete).
	multiGitDiff := "```diff\n" + strings.Join([]string{
		"diff --git a/mod.txt b/mod.txt",
		"index 1111111..2222222 100644",
		"--- a/mod.txt",
		"+++ b/mod.txt",
		"@@ -1,2 +1,2 @@",
		" keep",
		"-old",
		"+new",
		"diff --git a/new.txt b/new.txt",
		"new file mode 100644",
		"--- /dev/null",
		"+++ b/new.txt",
		"@@ -0,0 +1,2 @@",
		"+created",
		"+line2",
		"diff --git a/dead.txt b/dead.txt",
		"deleted file mode 100644",
		"--- a/dead.txt",
		"+++ /dev/null",
		"@@ -1 +0,0 @@",
		"-gone",
	}, "\n") + "\n```"
	if res := runCodexApply("pure_multi_git_diff", map[string]string{
		"mod.txt":  "keep\nold\n",
		"dead.txt": "gone\n",
	}, multiGitDiff); res != nil {
		if _, exists := res["dead.txt"]; exists {
			t.Fatalf("expected dead.txt deleted, got %+v", res)
		}
		if res["mod.txt"] != "keep\nnew\n" || res["new.txt"] != "created\nline2\n" {
			t.Fatalf("unexpected files after multi git diff: %+v", res)
		}
	}

	// Case F: Unprefixed Add File with YAML frontmatter + invalid *** End of File + JSON path hint fallback.
	if res := runCodexApply("unprefixed_add_yaml", nil, "*** Begin Patch\n*** Add File: doc.md\n---\ntitle: Test\n---\nHello\n*** End of File\n*** End Patch"); res != nil {
		if res["doc.md"] != "---\ntitle: Test\n---\nHello\n" {
			t.Fatalf("unexpected doc.md: %q", res["doc.md"])
		}
	}
	if res := runCodexApply("json_path_hint", map[string]string{
		"calc.py": "def add(a, b):\n    return a - b\n",
	}, `{"path":"calc.py","patch":"@@ -1,2 +1,2 @@\n def add(a, b):\n-    return a - b\n+    return a + b"}`); res != nil {
		if res["calc.py"] != "def add(a, b):\n    return a + b\n" {
			t.Fatalf("unexpected calc.py from path hint: %q", res["calc.py"])
		}
	}
}

func TestBridgeRouterAndMixedCaseSlugsOnWire(t *testing.T) {
	var mu sync.Mutex
	var seenModels []string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		mod, _ := m["model"].(string)
		mu.Lock()
		seenModels = append(seenModels, mod)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"resp_ok","output":[]}`))
	}))
	defer upstream.Close()

	br, err := NewBridge("sk-router-test", upstream.URL, 8192)
	if err != nil {
		t.Fatal(err)
	}
	defer br.Close()

	inputs := []string{
		"openrouter/free",
		"openrouter/auto",
		"OpenRouter/free",
		"openrouter/google/gemini-3-flash-preview",
		"OpenRouter/google/gemini-2.5-flash:thinking",
	}
	for _, slug := range inputs {
		body, _ := json.Marshal(map[string]any{"model": slug})
		req, _ := http.NewRequest(http.MethodPost, br.BaseURL()+"/responses", strings.NewReader(string(body)))
		req.Header.Set("Authorization", "Bearer "+br.BridgeToken())
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("post %s: %v", slug, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("post %s status=%d", slug, resp.StatusCode)
		}
	}

	want := "[openrouter/free openrouter/auto openrouter/free google/gemini-3-flash-preview google/gemini-2.5-flash:thinking]"
	mu.Lock()
	got := fmt.Sprint(seenModels)
	mu.Unlock()
	if got != want {
		t.Fatalf("bridge wire models = %s, want %s", got, want)
	}
}

func TestBridgeAffordCapPreservedOnNoOpConfigUpdate(t *testing.T) {
	var mu sync.Mutex
	var seenMaxTokens []int
	ceiling := 512

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		mt := int(m["max_output_tokens"].(float64))
		mu.Lock()
		seenMaxTokens = append(seenMaxTokens, mt)
		curCeil := ceiling
		mu.Unlock()
		if mt > curCeil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusPaymentRequired)
			_, _ = fmt.Fprintf(w, `{"error":{"message":"You requested up to %d tokens, but can only afford %d."}}`, mt, curCeil)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"resp_ok","output":[]}`))
	}))
	defer upstream.Close()

	br, err := NewBridge("sk-same-key", upstream.URL, 8192)
	if err != nil {
		t.Fatal(err)
	}
	defer br.Close()

	post := func() {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, br.BaseURL()+"/responses", strings.NewReader(`{"model":"google/gemini-2.5-flash"}`))
		req.Header.Set("Authorization", "Bearer "+br.BridgeToken())
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
	}

	// First request hits 402 (8192 -> 448) and caches affordCap=448.
	post()
	mu.Lock()
	ceiling = 10000
	mu.Unlock()

	// No-op config updates must preserve affordCap=448!
	br.SetAPIKey("  sk-same-key  ")
	br.SetUpstreamURL(upstream.URL + "/")
	br.SetMaxOutputTokens(8192)
	post()

	// Actual key change must reset affordCap back to 8192!
	br.SetAPIKey("sk-different-key")
	post()

	mu.Lock()
	got := fmt.Sprint(seenMaxTokens)
	mu.Unlock()
	if got != "[8192 448 448 8192]" {
		t.Fatalf("expected [8192 448 448 8192], got %s", got)
	}
}

func TestNormalizeV4APatchAddFileMixedLeadingPlus(t *testing.T) {
	rawPatch := strings.Join([]string{
		"*** Begin Patch",
		"*** Add File: notes.md",
		"# Heading",
		"",
		"+ literal plus bullet",
		"plain line",
		"*** Add File: main.go",
		"+package main",
		"",
		"+func main() {",
		"+\tprintln(\"hello\")",
		"+}",
		"*** End Patch",
	}, "\n")

	norm := normalizeV4APatch(rawPatch)
	wantNorm := strings.Join([]string{
		"*** Begin Patch",
		"*** Add File: notes.md",
		"+# Heading",
		"+",
		"++ literal plus bullet",
		"+plain line",
		"*** Add File: main.go",
		"+package main",
		"+",
		"+func main() {",
		"+\tprintln(\"hello\")",
		"+}",
		"*** End Patch",
	}, "\n")
	if norm != wantNorm {
		t.Fatalf("normalizeV4APatch mismatch:\ngot:\n%s\nwant:\n%s", norm, wantNorm)
	}

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Fatalf("UserHomeDir failed: %v", err)
	}
	codexBin := ResolveBinary("", home)
	if codexBin == "" {
		t.Fatal("expected real codex binary installed for --codex-run-as-apply-patch verification")
	}
	workDir := nonTmpCodexTestDir(t)
	cmd := exec.Command(codexBin, "--codex-run-as-apply-patch", norm)
	cmd.Dir = workDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("codex --codex-run-as-apply-patch failed: %v\n%s", err, string(out))
	}
	diskBytes, err := os.ReadFile(filepath.Join(workDir, "notes.md"))
	if err != nil {
		t.Fatalf("read notes.md: %v", err)
	}
	wantDisk := "# Heading\n\n+ literal plus bullet\nplain line\n"
	if string(diskBytes) != wantDisk {
		t.Fatalf("notes.md content = %q, want %q", string(diskBytes), wantDisk)
	}
	mainBytes, err := os.ReadFile(filepath.Join(workDir, "main.go"))
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	wantMain := "package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n"
	if string(mainBytes) != wantMain {
		t.Fatalf("main.go content = %q, want %q", string(mainBytes), wantMain)
	}
}
