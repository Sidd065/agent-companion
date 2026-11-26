package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/flynn/noise"

	"agentc/internal/core"
	"agentc/internal/secure"
	"agentc/internal/term"
)

func nonTmpServerTestDir(t *testing.T, prefix string) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil || home == "" || strings.HasPrefix(home, "/tmp") {
		t.Fatalf("expected non-/tmp user home, got %q (%v)", home, err)
	}
	base := filepath.Join(home, ".cache", "agentc-gotest")
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", base, err)
	}
	dir, err := os.MkdirTemp(base, prefix+"-*")
	if err != nil {
		t.Fatalf("mkdirtemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func (c *testRPCClient) callTimeout(t *testing.T, timeout time.Duration, method string, params any) any {
	t.Helper()
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan map[string]any, 1)
	c.pend[id] = ch
	c.mu.Unlock()

	req, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err := c.conn.WriteMessage(secure.TextMessage, req); err != nil {
		t.Fatalf("write %s: %v", method, err)
	}
	select {
	case resp := <-ch:
		if errObj, ok := resp["error"].(map[string]any); ok {
			t.Fatalf("rpc %s error: %v", method, errObj["message"])
		}
		return resp["result"]
	case <-time.After(timeout):
		t.Fatalf("rpc %s timed out after %s", method, timeout)
		return nil
	}
}

func pairAndConnectRPC(t *testing.T, srv *Server, tsURL string) *testRPCClient {
	t.Helper()
	pairReq := httptest.NewRequest("POST", "/local/pair", strings.NewReader(`{"ttlSeconds":120}`))
	pairRec := httptest.NewRecorder()
	srv.LocalHandler().ServeHTTP(pairRec, pairReq)
	if pairRec.Code != http.StatusOK {
		t.Fatalf("local/pair failed: %d %s", pairRec.Code, pairRec.Body.String())
	}
	var pairResp struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(pairRec.Body.Bytes(), &pairResp); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(pairResp.URL)
	if err != nil {
		t.Fatal(err)
	}
	pairID := strings.TrimPrefix(parsed.Path, "/p/")
	frag, _ := url.ParseQuery(parsed.Fragment)
	daemonPub, _ := base64.RawURLEncoding.DecodeString(frag.Get("k"))
	psk, _ := base64.RawURLEncoding.DecodeString(frag.Get("s"))
	devKey, _ := noise.DH25519.GenerateKeypair(rand.Reader)

	pairClient := dialTestRPC(t, tsURL, daemonPub, devKey, secure.ModePair, pairID, psk)
	_ = pairClient.conn.Close()

	rpc := dialTestRPC(t, tsURL, daemonPub, devKey, secure.ModeConnect, "", nil)
	t.Cleanup(func() { _ = rpc.conn.Close() })
	return rpc
}

func writeServerFakeCodex(t *testing.T, dir string) string {
	t.Helper()
	bin := filepath.Join(dir, "fake-codex-srv")
	script := `#!/usr/bin/env python3
import json, os, sys

if len(sys.argv) > 1 and sys.argv[1] == "--version":
    print("codex-cli 0.172.0")
    sys.exit(0)
if len(sys.argv) > 1 and sys.argv[1] == "resume":
    import time
    time.sleep(2)
    sys.exit(0)

def send(obj):
    sys.stdout.write(json.dumps(obj) + "\n")
    sys.stdout.flush()

for raw in sys.stdin:
    raw = raw.strip()
    if not raw:
        continue
    msg = json.loads(raw)
    rid = msg.get("id")
    method = msg.get("method", "")
    params = msg.get("params") or {}
    if rid is not None and method == "":
        continue
    if method == "initialize":
        send({"id": rid, "result": {"userAgent": "codex/0.172.0", "codexHome": os.environ.get("CODEX_HOME", "")}})
    elif method == "initialized":
        pass
    elif method == "model/list":
        send({"id": rid, "result": {"data": [{"id": "gpt-5.3-codex", "displayName": "GPT-5.3 Codex", "isDefault": True}]}})
    elif method in ("thread/start", "thread/resume"):
        tid = params.get("threadId") or "019csrv0-0000-7000-8000-000000000001"
        send({"id": rid, "result": {"thread": {"id": tid, "cwd": params.get("cwd", ""), "createdAt": 1700000000, "updatedAt": 1700000000, "turns": []}, "model": params.get("model", "google/gemini-3-flash-preview")}})
    elif method == "thread/compact/start":
        tid = params.get("threadId") or "019csrv0-0000-7000-8000-000000000001"
        send({"id": rid, "result": {}})
        send({"method": "thread/compacted", "params": {"threadId": tid}})
    else:
        send({"id": rid, "result": {}})
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestCodexProtocolV2AndSecretRedaction(t *testing.T) {
	root := nonTmpServerTestDir(t, "srv-hermetic")
	fakeCodex := writeServerFakeCodex(t, root)

	// Fake OpenRouter catalog endpoint.
	fakeOR := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/models/user", "/api/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[
				{"id":"google/gemini-3-flash-preview","name":"Google: Gemini 3 Flash Preview","context_length":1048576,"supported_parameters":["tools","reasoning"],"architecture":{"input_modalities":["text","image"]}},
				{"id":"google/gemini-2.5-flash","name":"Google: Gemini 2.5 Flash","context_length":1048576,"supported_parameters":["tools","reasoning"],"architecture":{"input_modalities":["text","image"]}}
			]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer fakeOR.Close()

	stateDir := filepath.Join(root, "state")
	cfgDir := filepath.Join(root, "config")
	wtDir := filepath.Join(root, "worktrees")
	_ = os.MkdirAll(stateDir, 0o700)

	// Create fake native ~/.codex/auth.json so native model/list is also merged.
	_ = os.MkdirAll(filepath.Join(root, ".codex"), 0o700)
	_ = os.WriteFile(filepath.Join(root, ".codex", "auth.json"), []byte(`{"tokens":{"access_token":"fake"}}`), 0o600)

	id, err := secure.LoadOrCreateIdentity(filepath.Join(stateDir, "identity.json"))
	if err != nil {
		t.Fatal(err)
	}
	devs, err := secure.OpenRegistry(filepath.Join(stateDir, "devices.json"))
	if err != nil {
		t.Fatal(err)
	}
	hub := core.NewHub()
	mgr := core.NewManager(core.Config{
		AgyPath:           "nonexistent-agy",
		ClaudePath:        "nonexistent-claude",
		CodexPath:         fakeCodex,
		OpenRouterAPIKey:  "sk-or-v1-initial-test-secret",
		OpenRouterBaseURL: fakeOR.URL + "/api/v1",
		StateDir:          stateDir,
		ConfigDir:         cfgDir,
		WorktreeDir:       wtDir,
		HomeDir:           root,
	}, hub)
	defer mgr.Shutdown()

	terms := &term.Manager{
		Sock:        filepath.Join(root, "tmux.sock"),
		Conf:        filepath.Join(root, "tmux.conf"),
		CodexPath:   mgr.CodexBin(),
		CodexLaunch: mgr.CodexTermLaunch,
	}
	srv := &Server{
		Mgr: mgr, Terms: terms, Identity: id, Devices: devs,
		Tokens: secure.NewTokens(), Started: time.Now(),
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	srv.PublicURL = func() string { return ts.URL }

	rpc := pairAndConnectRPC(t, srv, ts.URL)

	// 1. daemon.info: openRouterConfigured, capabilities, and codex AgentProfile.
	info := rpc.call(t, "daemon.info", map[string]any{}).(map[string]any)
	if info["openRouterConfigured"] != true {
		t.Fatalf("expected openRouterConfigured=true in daemon.info, got %+v", info["openRouterConfigured"])
	}
	capsAny, _ := info["capabilities"].([]any)
	capSet := map[string]bool{}
	for _, c := range capsAny {
		if s, ok := c.(string); ok {
			capSet[s] = true
		}
	}
	for _, reqCap := range []string{"providers.codex", "providers.openrouter", "customModels"} {
		if !capSet[reqCap] {
			t.Fatalf("missing capability %q in daemon.info: %v", reqCap, capsAny)
		}
	}
	var codexAgent map[string]any
	for _, rawAg := range info["agents"].([]any) {
		ag := rawAg.(map[string]any)
		if ag["id"] == "codex" {
			codexAgent = ag
			break
		}
	}
	if codexAgent == nil || codexAgent["available"] != true {
		t.Fatalf("expected available codex agent profile, got %+v", codexAgent)
	}
	featSet := map[string]bool{}
	for _, rf := range codexAgent["features"].([]any) {
		if fs, ok := rf.(string); ok {
			featSet[fs] = true
		}
	}
	for _, fk := range []string{"openrouter", "customModels", "thinking", "diffs", "permissions", "questions", "plans", "fork", "rewind", "steer", "resume", "history", "terminal", "asides", "modes", "effort", "checkpoints"} {
		if !featSet[fk] {
			t.Fatalf("expected codex feature %q in %v", fk, featSet)
		}
	}

	// 2. Secret redaction in providers.upsert & providers.list + preservation on "***" and nil Env.
	const testSecret = "sk-or-v1-updated-hermetic-secret"
	upserted := rpc.call(t, "providers.upsert", map[string]any{
		"id":           "codex",
		"name":         "Codex",
		"driver":       "codex",
		"enabled":      true,
		"defaultModel": "google/gemini-3-flash-preview",
		"env": map[string]any{
			"OPENROUTER_API_KEY": testSecret,
			"CODEX_EXTRA_FLAG":   "plain-val",
		},
		"customModels": []map[string]any{
			{"slug": "customvendor/alpha-1", "name": "Alpha 1"},
		},
	}).(map[string]any)

	rawUpsertJSON, _ := json.Marshal(upserted)
	if strings.Contains(string(rawUpsertJSON), testSecret) {
		t.Fatalf("providers.upsert leaked raw secret in RPC response")
	}
	upEnv := upserted["env"].(map[string]any)
	if upEnv["OPENROUTER_API_KEY"] != "***" || upEnv["CODEX_EXTRA_FLAG"] != "plain-val" {
		t.Fatalf("unexpected redacted env in providers.upsert: %+v", upEnv)
	}

	listed := rpc.call(t, "providers.list", map[string]any{}).(map[string]any)
	rawListJSON, _ := json.Marshal(listed)
	if strings.Contains(string(rawListJSON), testSecret) {
		t.Fatalf("providers.list leaked raw secret in RPC response")
	}
	if mgr.OpenRouterAPIKey() != testSecret {
		t.Fatalf("expected real secret preserved in manager memory")
	}

	// Upsert with "***" sentinel must keep real secret.
	rpc.call(t, "providers.upsert", map[string]any{
		"id":           "codex",
		"name":         "Codex",
		"driver":       "codex",
		"enabled":      true,
		"defaultModel": "google/gemini-2.5-flash",
		"env":          map[string]any{"OPENROUTER_API_KEY": "***"},
		"customModels": []map[string]any{
			{"slug": "customvendor/alpha-1", "name": "Alpha 1"},
		},
	})
	if mgr.OpenRouterAPIKey() != testSecret {
		t.Fatalf("secret lost after providers.upsert with '***' placeholder")
	}

	// Upsert with nil env must also keep real secret.
	rpc.call(t, "providers.upsert", map[string]any{
		"id":           "codex",
		"name":         "Codex",
		"driver":       "codex",
		"enabled":      true,
		"defaultModel": "google/gemini-3-flash-preview",
		"customModels": []map[string]any{
			{"slug": "customvendor/alpha-1", "name": "Alpha 1"},
		},
	})
	if mgr.OpenRouterAPIKey() != testSecret {
		t.Fatalf("secret lost after providers.upsert with nil env")
	}

	// 3. providers.health & providers.models exact schema.
	hlthList := rpc.call(t, "providers.health", map[string]any{"id": "codex"}).([]any)
	if len(hlthList) != 1 || hlthList[0].(map[string]any)["available"] != true {
		t.Fatalf("expected codex providers.health available=true, got %+v", hlthList)
	}

	pmodels := rpc.call(t, "providers.models", map[string]any{"provider": "codex", "refresh": true}).(map[string]any)
	if pmodels["openRouterConfigured"] != true || pmodels["defaultModelId"] != "google/gemini-3-flash-preview" {
		t.Fatalf("unexpected providers.models top-level fields: %+v", pmodels)
	}
	modelsSlice := pmodels["models"].([]any)
	seenModel := map[string]bool{}
	for _, rawM := range modelsSlice {
		m := rawM.(map[string]any)
		seenModel[m["modelId"].(string)] = true
	}
	for _, wantID := range []string{"google/gemini-3-flash-preview", "google/gemini-2.5-flash", "gpt-5.3-codex", "customvendor/alpha-1"} {
		if !seenModel[wantID] {
			t.Fatalf("expected model %q in providers.models, got %v", wantID, seenModel)
		}
	}
	var gotModeIDs []string
	for _, rawMode := range pmodels["availableModes"].([]any) {
		gotModeIDs = append(gotModeIDs, rawMode.(map[string]any)["id"].(string))
	}
	wantModes := []string{"default", "plan", "acceptEdits", "auto", "full-access"}
	if !reflect.DeepEqual(gotModeIDs, wantModes) {
		t.Fatalf("availableModes mismatch: got %v want %v", gotModeIDs, wantModes)
	}
	var gotEfforts []string
	for _, e := range pmodels["reasoningEfforts"].([]any) {
		gotEfforts = append(gotEfforts, e.(string))
	}
	wantEfforts := []string{"low", "medium", "high", "xhigh"}
	if !reflect.DeepEqual(gotEfforts, wantEfforts) {
		t.Fatalf("reasoningEfforts mismatch: got %v want %v", gotEfforts, wantEfforts)
	}

	// 4. session.create with nested flags + session.compact + session.openInTerminal.
	projDir := filepath.Join(root, "proj")
	_ = os.MkdirAll(projDir, 0o755)
	created := rpc.call(t, "session.create", map[string]any{
		"agent": "codex",
		"cwd":   projDir,
		"flags": map[string]any{
			"model":  "google/gemini-3-flash-preview",
			"mode":   "acceptEdits",
			"effort": "high",
		},
	}).(map[string]any)
	sessMap := created["session"].(map[string]any)
	sid := sessMap["id"].(string)
	if sessMap["agent"] != "codex" || sessMap["model"] != "google/gemini-3-flash-preview" {
		t.Fatalf("unexpected created codex session: %+v", sessMap)
	}

	compactRes := rpc.call(t, "session.compact", map[string]any{"id": sid}).(map[string]any)
	if compactRes["ok"] != true {
		t.Fatalf("session.compact failed: %+v", compactRes)
	}

	if _, err := exec.LookPath("tmux"); err == nil {
		termRes := rpc.call(t, "session.openInTerminal", map[string]any{"id": sid, "cols": 80, "rows": 24}).(map[string]any)
		if termRes["kind"] != "codex" {
			t.Fatalf("expected terminal kind=codex, got %+v", termRes)
		}
		if tid, ok := termRes["id"].(string); ok && tid != "" {
			_ = terms.Kill(context.Background(), tid)
		}
	}
}

func TestCodexOpenRouterLiveBYOK(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live OpenRouter BYOK E2E in -short mode")
	}
	repoRoot, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(repoRoot, ".open_router_api_key")
	if st, err := os.Stat(keyFile); err != nil || st.Size() == 0 {
		t.Skipf("skipping live OpenRouter test: key file not present at %s", keyFile)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	codexBin := filepath.Join(home, ".local", "bin", "codex")
	if _, err := os.Stat(codexBin); err != nil {
		if lp, lerr := exec.LookPath("codex"); lerr == nil {
			codexBin = lp
		} else {
			t.Skip("skipping live OpenRouter test: codex binary not installed")
		}
	}

	root := nonTmpServerTestDir(t, "live-openrouter")
	stateDir := filepath.Join(root, "state")
	cfgDir := filepath.Join(root, "config")
	wtDir := filepath.Join(root, "worktrees")
	_ = os.MkdirAll(stateDir, 0o700)

	id, err := secure.LoadOrCreateIdentity(filepath.Join(stateDir, "identity.json"))
	if err != nil {
		t.Fatal(err)
	}
	devs, err := secure.OpenRegistry(filepath.Join(stateDir, "devices.json"))
	if err != nil {
		t.Fatal(err)
	}
	hub := core.NewHub()
	mgr := core.NewManager(core.Config{
		AgyPath:              "nonexistent-agy",
		ClaudePath:           "nonexistent-claude",
		CodexPath:            codexBin,
		OpenRouterAPIKeyFile: keyFile,
		StateDir:             stateDir,
		ConfigDir:            cfgDir,
		WorktreeDir:          wtDir,
		HomeDir:              root,
	}, hub)
	defer mgr.Shutdown()

	if !mgr.OpenRouterConfigured() {
		t.Fatalf("expected OpenRouterConfigured() == true from key file")
	}
	rawKeyInMem := mgr.OpenRouterAPIKey()
	assertNoSecretLeak := func(label string, v any) {
		t.Helper()
		b, _ := json.Marshal(v)
		if rawKeyInMem != "" && strings.Contains(string(b), rawKeyInMem) {
			t.Fatalf("SECURITY VIOLATION: %s contained raw OpenRouter API key", label)
		}
	}

	terms := &term.Manager{
		Sock:        filepath.Join(root, "tmux.sock"),
		Conf:        filepath.Join(root, "tmux.conf"),
		CodexPath:   mgr.CodexBin(),
		CodexLaunch: mgr.CodexTermLaunch,
	}
	srv := &Server{
		Mgr: mgr, Terms: terms, Identity: id, Devices: devs,
		Tokens: secure.NewTokens(), Started: time.Now(),
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	srv.PublicURL = func() string { return ts.URL }

	rpc := pairAndConnectRPC(t, srv, ts.URL)

	// 1. Live providers.models against real OpenRouter API.
	info := rpc.callTimeout(t, 30*time.Second, "daemon.info", map[string]any{}).(map[string]any)
	assertNoSecretLeak("daemon.info", info)
	if info["openRouterConfigured"] != true {
		t.Fatalf("expected openRouterConfigured=true")
	}

	pmodels := rpc.callTimeout(t, 45*time.Second, "providers.models", map[string]any{"provider": "codex", "refresh": true}).(map[string]any)
	assertNoSecretLeak("providers.models", pmodels)
	modelsList := pmodels["models"].([]any)
	if len(modelsList) < 10 {
		t.Fatalf("expected live OpenRouter catalog with >=10 models, got %d", len(modelsList))
	}
	liveSeen := map[string]bool{}
	for _, rm := range modelsList {
		m := rm.(map[string]any)
		liveSeen[m["modelId"].(string)] = true
	}
	for _, mustHave := range []string{"google/gemini-3-flash-preview", "google/gemini-2.5-flash"} {
		if !liveSeen[mustHave] {
			t.Fatalf("expected live OpenRouter model %q in providers.models", mustHave)
		}
	}

	// 2. Real git repo with buggy calc.py.
	projDir := filepath.Join(root, "calc-repo")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = projDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, string(out))
		}
	}
	gitRun("init")
	gitRun("config", "user.email", "live-test@example.com")
	gitRun("config", "user.name", "Live Test")
	calcPath := filepath.Join(projDir, "calc.py")
	initialCalc := "VERSION = 1\n\ndef add(a, b):\n    return a - b\n\ndef sub(a, b):\n    return a - b\n"
	if err := os.WriteFile(calcPath, []byte(initialCalc), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun("add", "calc.py")
	gitRun("commit", "-m", "buggy multi-line calc")

	// 3. Create & subscribe to real Codex + OpenRouter session using google/gemini-3-flash-preview.
	created := rpc.callTimeout(t, 45*time.Second, "session.create", map[string]any{
		"agent": "codex",
		"cwd":   projDir,
		"flags": map[string]any{
			"model":  "google/gemini-3-flash-preview",
			"mode":   "full-access",
			"effort": "low",
		},
	}).(map[string]any)
	assertNoSecretLeak("session.create", created)
	sid := created["session"].(map[string]any)["id"].(string)

	rpc.callTimeout(t, 30*time.Second, "session.subscribe", map[string]any{"id": sid, "since": 0})
	rpc.waitForEvent(t, 20*time.Second, func(ev string, d map[string]any) bool {
		return ev == "session.snapshot" && d["id"] == sid
	})

	// Turn 1: google/gemini-3-flash-preview updates multi-line calc.py via apply_patch and runs python3 verification!
	rpc.callTimeout(t, 30*time.Second, "session.prompt", map[string]any{
		"id":   sid,
		"text": "Update `calc.py` using `apply_patch` so `VERSION = 2` and `add(a, b)` returns `a + b` (keep `sub(a, b)` intact), then run `python3 -c \"import calc; assert calc.VERSION == 2 and calc.add(2, 3) == 5 and calc.sub(5, 2) == 3; print('CALC_ADD_OK')\"` to verify, and reply with DONE_TURN_1.",
	})

	sawOps := false
	deadline := time.After(150 * time.Second)
waitTurn1:
	for {
		select {
		case m := <-rpc.events:
			ev, _ := m["event"].(string)
			data, _ := m["data"].(map[string]any)
			if ev == "session.ops" && data["id"] == sid {
				sawOps = true
			}
			if ev == "session.changed" && data["id"] == sid && data["state"] == "idle" {
				if errMsg, _ := data["error"].(string); errMsg != "" {
					if rawKeyInMem != "" {
						errMsg = strings.ReplaceAll(errMsg, rawKeyInMem, "***")
					}
					t.Fatalf("Turn 1 failed with session error: %s", errMsg)
				}
				if turns, ok := data["turns"].(float64); ok && int(turns) >= 1 {
					break waitTurn1
				}
			}
		case <-deadline:
			var stSummary any
			if ss := mgr.Get(sid); ss != nil {
				stSummary = ss.Summary()
			}
			t.Fatalf("timed out waiting for live Turn 1: sawOps=%v summary=%+v", sawOps, stSummary)
		}
	}
	if !sawOps {
		t.Fatalf("expected live streaming session.ops events during Turn 1")
	}

	verifyPy := exec.Command("python3", "-c", "import calc; assert calc.VERSION == 2 and calc.add(2, 3) == 5 and calc.sub(5, 2) == 3")
	verifyPy.Dir = projDir
	if out, err := verifyPy.CombinedOutput(); err != nil {
		calcBytes, _ := os.ReadFile(calcPath)
		t.Fatalf("calc.py multi-line Turn 1 check failed: %v\nout=%s\ncalc.py:\n%s", err, string(out), string(calcBytes))
	}

	tdiff := rpc.callTimeout(t, 30*time.Second, "session.turnDiff", map[string]any{"id": sid, "scope": "latest"}).(map[string]any)
	diffFiles, _ := tdiff["files"].([]any)
	if len(diffFiles) < 1 {
		t.Fatalf("expected non-empty session.turnDiff after Turn 1 edit, got %+v", tdiff)
	}

	rpc.callTimeout(t, 30*time.Second, "session.subscribe", map[string]any{"id": sid, "since": 0})
	snapEv := rpc.waitForEvent(t, 20*time.Second, func(ev string, d map[string]any) bool {
		return ev == "session.snapshot" && d["id"] == sid
	})
	assertNoSecretLeak("session.snapshot(turn1)", snapEv)
	snapSess, _ := snapEv["session"].(map[string]any)
	usgMap, _ := snapSess["usage"].(map[string]any)
	totalTok, _ := usgMap["totalTokens"].(float64)
	if totalTok <= 0 {
		t.Fatalf("expected non-zero token usage telemetry after Turn 1, got %+v", usgMap)
	}
	sawEditTool := false
	sawBashTool := false
	rawItems, _ := snapEv["items"].([]any)
	for _, rawIt := range rawItems {
		it, _ := rawIt.(map[string]any)
		if it["kind"] == "tool" {
			if toolMap, ok := it["tool"].(map[string]any); ok {
				if toolMap["kind"] == "edit" {
					sawEditTool = true
				}
				if toolMap["kind"] == "execute" {
					sawBashTool = true
				}
			}
		}
	}
	if !sawEditTool || !sawBashTool {
		t.Fatalf("expected both edit (apply_patch) and execute (shell) tools in transcript: sawEdit=%v sawBash=%v", sawEditTool, sawBashTool)
	}

	// 4. Arbitrary unlisted custom slug check + switch to openrouter/google/gemini-2.5-flash for Turn 2!
	customModelSum := rpc.callTimeout(t, 45*time.Second, "session.setModel", map[string]any{
		"id":    sid,
		"model": "vendor/custom-unlisted-slug-check",
	}).(map[string]any)
	if customModelSum["model"] != "vendor/custom-unlisted-slug-check" {
		t.Fatalf("expected session.setModel to accept unlisted custom slug, got %+v", customModelSum["model"])
	}

	turn2ModelSum := rpc.callTimeout(t, 45*time.Second, "session.setModel", map[string]any{
		"id":    sid,
		"model": "openrouter/google/gemini-2.5-flash",
	}).(map[string]any)
	if turn2ModelSum["model"] != "openrouter/google/gemini-2.5-flash" {
		t.Fatalf("expected model=openrouter/google/gemini-2.5-flash, got %+v", turn2ModelSum["model"])
	}

	rpc.callTimeout(t, 30*time.Second, "session.prompt", map[string]any{
		"id":   sid,
		"text": "Update `calc.py` using `apply_patch` to set `VERSION = 3` and add `def mul(a, b):\n    return a * b`, run `python3 -c \"import calc; assert calc.VERSION == 3 and calc.add(2, 3) == 5 and calc.sub(5, 2) == 3 and calc.mul(3, 4) == 12; print('CALC_MUL_OK')\"`, and reply with DONE_TURN_2.",
	})

	deadline2 := time.After(150 * time.Second)
waitTurn2:
	for {
		select {
		case m := <-rpc.events:
			ev, _ := m["event"].(string)
			data, _ := m["data"].(map[string]any)
			if ev == "session.changed" && data["id"] == sid && data["state"] == "idle" {
				if errMsg, _ := data["error"].(string); errMsg != "" {
					if rawKeyInMem != "" {
						errMsg = strings.ReplaceAll(errMsg, rawKeyInMem, "***")
					}
					t.Fatalf("Turn 2 failed with session error: %s", errMsg)
				}
				if turns, ok := data["turns"].(float64); ok && int(turns) >= 2 {
					break waitTurn2
				}
			}
		case <-deadline2:
			t.Fatalf("timed out waiting for live Turn 2 (openrouter/google/gemini-2.5-flash) to complete")
		}
	}

	verifyPy2 := exec.Command("python3", "-c", "import calc; assert calc.VERSION == 3 and calc.add(2, 3) == 5 and calc.sub(5, 2) == 3 and calc.mul(3, 4) == 12")
	verifyPy2.Dir = projDir
	if out, err := verifyPy2.CombinedOutput(); err != nil {
		calcBytes, _ := os.ReadFile(calcPath)
		t.Fatalf("calc.py multi-line Turn 2 check failed: %v\nout=%s\ncalc.py:\n%s", err, string(out), string(calcBytes))
	}

	// Verify >= 2 completed edit tool items across the 2 live turns.
	rpc.callTimeout(t, 30*time.Second, "session.subscribe", map[string]any{"id": sid, "since": 0})
	snapEv2 := rpc.waitForEvent(t, 20*time.Second, func(ev string, d map[string]any) bool {
		return ev == "session.snapshot" && d["id"] == sid
	})
	assertNoSecretLeak("session.snapshot(turn2)", snapEv2)
	completedEdits := 0
	if items2, ok := snapEv2["items"].([]any); ok {
		for _, rawIt := range items2 {
			it, _ := rawIt.(map[string]any)
			if it["kind"] == "tool" {
				if tm, ok := it["tool"].(map[string]any); ok && tm["kind"] == "edit" && tm["status"] == "completed" {
					completedEdits++
				}
			}
		}
	}
	if completedEdits < 2 {
		t.Fatalf("expected >=2 completed edit (apply_patch) tool items across Turns 1 & 2, got %d", completedEdits)
	}

	// 5. Verify native session.fork on the live Codex session.
	forkRes := rpc.callTimeout(t, 45*time.Second, "session.fork", map[string]any{"id": sid}).(map[string]any)
	forkSess := forkRes["session"].(map[string]any)
	if forkSess["agent"] != "codex" || forkSess["forkedFrom"] != sid {
		t.Fatalf("unexpected forked codex session: %+v", forkSess)
	}
}

func (c *testRPCClient) callRawResp(t *testing.T, timeout time.Duration, method string, params any) map[string]any {
	t.Helper()
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan map[string]any, 1)
	c.pend[id] = ch
	c.mu.Unlock()

	req, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err := c.conn.WriteMessage(secure.TextMessage, req); err != nil {
		t.Fatalf("write %s: %v", method, err)
	}
	select {
	case resp := <-ch:
		return resp
	case <-time.After(timeout):
		t.Fatalf("rpc %s timed out after %s", method, timeout)
		return nil
	}
}

func TestMultiHostServerFSIsolationAndUploadAbortRace(t *testing.T) {
	root := nonTmpServerTestDir(t, "multihost-fs")
	dirA := filepath.Join(root, "host-a-home")
	dirB := filepath.Join(root, "host-b-home")
	if err := os.MkdirAll(dirA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dirB, 0o755); err != nil {
		t.Fatal(err)
	}

	startHost := func(name, homeDir string) (*core.Manager, *Server, *httptest.Server, *testRPCClient) {
		stateDir := filepath.Join(root, name+"-state")
		cfgDir := filepath.Join(root, name+"-cfg")
		_ = os.MkdirAll(stateDir, 0o700)
		id, err := secure.LoadOrCreateIdentity(filepath.Join(stateDir, "identity.json"))
		if err != nil {
			t.Fatal(err)
		}
		devs, err := secure.OpenRegistry(filepath.Join(stateDir, "devices.json"))
		if err != nil {
			t.Fatal(err)
		}
		mgr := core.NewManager(core.Config{
			AgyPath:    "nonexistent-agy",
			ClaudePath: "nonexistent-claude",
			CodexPath:  "nonexistent-codex",
			StateDir:   stateDir,
			ConfigDir:  cfgDir,
			HomeDir:    homeDir,
		}, core.NewHub())
		srv := &Server{
			Mgr: mgr, Terms: &term.Manager{Sock: filepath.Join(stateDir, "tmux.sock"), Conf: filepath.Join(stateDir, "tmux.conf")},
			Identity: id, Devices: devs, Tokens: secure.NewTokens(), Started: time.Now(),
		}
		ts := httptest.NewServer(srv.Handler())
		srv.PublicURL = func() string { return ts.URL }
		rpc := pairAndConnectRPC(t, srv, ts.URL)
		return mgr, srv, ts, rpc
	}

	mgrA, _, tsA, rpcA := startHost("hostA", dirA)
	defer tsA.Close()
	defer mgrA.Shutdown()

	mgrB, _, tsB, rpcB := startHost("hostB", dirB)
	defer tsB.Close()
	defer mgrB.Shutdown()

	infoA := rpcA.call(t, "daemon.info", map[string]any{}).(map[string]any)
	infoB := rpcB.call(t, "daemon.info", map[string]any{}).(map[string]any)
	if infoA["home"] != filepath.Clean(dirA) || infoB["home"] != filepath.Clean(dirB) {
		t.Fatalf("daemon.info home mismatch: A=%v (want %s) B=%v (want %s)", infoA["home"], dirA, infoB["home"], dirB)
	}

	homeInfoA := rpcA.call(t, "fs.home", map[string]any{}).(map[string]any)
	homeInfoB := rpcB.call(t, "fs.home", map[string]any{}).(map[string]any)
	if homeInfoA["home"] != filepath.Clean(dirA) || homeInfoB["home"] != filepath.Clean(dirB) {
		t.Fatalf("fs.home mismatch: A=%v B=%v", homeInfoA["home"], homeInfoB["home"])
	}

	rpcA.call(t, "fs.write", map[string]any{"path": "~/alpha_only.txt", "text": "hello-from-host-A"})
	rpcB.call(t, "fs.write", map[string]any{"path": "~/bravo_only.txt", "text": "hello-from-host-B"})

	listA := rpcA.call(t, "fs.list", map[string]any{"path": "~"}).(map[string]any)
	listB := rpcB.call(t, "fs.list", map[string]any{"path": "~"}).(map[string]any)
	if listA["path"] != filepath.Clean(dirA) || listB["path"] != filepath.Clean(dirB) {
		t.Fatalf("fs.list(~) path mismatch: A=%v B=%v", listA["path"], listB["path"])
	}

	readA := rpcA.call(t, "fs.read", map[string]any{"path": "~/alpha_only.txt"}).(map[string]any)
	readB := rpcB.call(t, "fs.read", map[string]any{"path": "~/bravo_only.txt"}).(map[string]any)
	if readA["text"] != "hello-from-host-A" || readB["text"] != "hello-from-host-B" {
		t.Fatalf("fs.read content mismatch: A=%v B=%v", readA, readB)
	}

	// Host B must NOT see Host A's ~/alpha_only.txt!
	crossResp := rpcB.callRawResp(t, 10*time.Second, "fs.read", map[string]any{"path": "~/alpha_only.txt"})
	if crossResp["error"] == nil {
		t.Fatalf("expected error reading Host A file from Host B, got %+v", crossResp)
	}

	searchA := rpcA.call(t, "fs.search", map[string]any{"root": "~", "query": "_only"}).([]any)
	searchB := rpcB.call(t, "fs.search", map[string]any{"root": "~", "query": "_only"}).([]any)
	if len(searchA) != 1 || searchA[0].(map[string]any)["name"] != "alpha_only.txt" {
		t.Fatalf("unexpected searchA: %+v", searchA)
	}
	if len(searchB) != 1 || searchB[0].(map[string]any)["name"] != "bravo_only.txt" {
		t.Fatalf("unexpected searchB: %+v", searchB)
	}

	// Stress concurrent fs.uploadChunk + fs.uploadAbort under -race!
	payloadB64 := base64.StdEncoding.EncodeToString([]byte("concurrent-chunk-payload"))
	for i := 0; i < 20; i++ {
		st := rpcA.call(t, "fs.uploadStart", map[string]any{
			"path":      fmt.Sprintf("~/race_upload_%d.bin", i),
			"overwrite": true,
		}).(map[string]any)
		upID := st["id"].(string)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = rpcA.callRawResp(t, 10*time.Second, "fs.uploadChunk", map[string]any{
				"id":     upID,
				"offset": 0,
				"data":   payloadB64,
			})
		}()
		go func() {
			defer wg.Done()
			_ = rpcA.callRawResp(t, 10*time.Second, "fs.uploadAbort", map[string]any{"id": upID})
		}()
		wg.Wait()
	}

	// Start one dangling upload and verify Manager.Shutdown() reaps its .upload-* tempfile!
	_ = rpcA.call(t, "fs.uploadStart", map[string]any{"path": "~/dangling.bin", "overwrite": true})
	mgrA.Shutdown()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		matches, _ := filepath.Glob(filepath.Join(dirA, ".*.upload-*"))
		if len(matches) == 0 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if matches, _ := filepath.Glob(filepath.Join(dirA, ".*.upload-*")); len(matches) > 0 {
		t.Fatalf("leftover upload tempfiles after Shutdown: %v", matches)
	}
}

func TestCodexRealAppServerRewindCatalogAndShutdown(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	codexBin := filepath.Join(home, ".local", "bin", "codex")
	if _, err := os.Stat(codexBin); err != nil {
		if lp, lerr := exec.LookPath("codex"); lerr == nil {
			codexBin = lp
		} else {
			t.Skip("skipping real codex app-server test: codex binary not installed")
		}
	}

	var mu sync.Mutex
	var capturedInputs []string

	mockOR := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/models/user", "/api/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"google/gemini-3-flash-preview","name":"Gemini 3 Flash","context_length":1048576,"supported_parameters":["tools","reasoning"],"architecture":{"input_modalities":["text"]}}]}`))
			return
		case "/api/v1/responses":
			raw, _ := io.ReadAll(r.Body)
			rawStr := string(raw)
			mu.Lock()
			capturedInputs = append(capturedInputs, rawStr)
			seq := len(capturedInputs)
			mu.Unlock()

			var reqMap map[string]any
			_ = json.Unmarshal(raw, &reqMap)
			model, _ := reqMap["model"].(string)

			reply := fmt.Sprintf("ACK_%d", seq)
			if strings.Contains(rawStr, "RECALL_KIWI") {
				if strings.Contains(rawStr, "KIWI-888") && !strings.Contains(rawStr, "OLD-111") {
					reply = "RECALLED_KIWI-888_CLEAN"
				} else {
					reply = "MEMORY_MISMATCH"
				}
			} else if strings.Contains(rawStr, "KIWI-888") {
				reply = "OK_KIWI"
			} else if strings.Contains(rawStr, "OLD-111") {
				reply = "OK_OLD"
			}

			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.WriteHeader(http.StatusOK)
			flusher, _ := w.(http.Flusher)
			writeSSE := func(evName string, payload any) {
				b, _ := json.Marshal(payload)
				_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", evName, string(b))
				if flusher != nil {
					flusher.Flush()
				}
			}
			respID := fmt.Sprintf("resp_mock_%d", seq)
			msgID := fmt.Sprintf("msg_mock_%d", seq)
			writeSSE("response.created", map[string]any{
				"type":     "response.created",
				"response": map[string]any{"id": respID, "status": "in_progress", "model": model, "output": []any{}},
			})
			writeSSE("response.output_item.added", map[string]any{
				"type":         "response.output_item.added",
				"output_index": 0,
				"item":         map[string]any{"id": msgID, "type": "message", "role": "assistant", "status": "in_progress", "content": []any{}},
			})
			writeSSE("response.content_part.added", map[string]any{
				"type":          "response.content_part.added",
				"item_id":       msgID,
				"output_index":  0,
				"content_index": 0,
				"part":          map[string]any{"type": "output_text", "text": ""},
			})
			writeSSE("response.output_text.delta", map[string]any{
				"type":          "response.output_text.delta",
				"item_id":       msgID,
				"output_index":  0,
				"content_index": 0,
				"delta":         reply,
			})
			writeSSE("response.output_text.done", map[string]any{
				"type":          "response.output_text.done",
				"item_id":       msgID,
				"output_index":  0,
				"content_index": 0,
				"text":          reply,
			})
			msgOut := map[string]any{
				"id":      msgID,
				"type":    "message",
				"role":    "assistant",
				"status":  "completed",
				"content": []map[string]any{{"type": "output_text", "text": reply}},
			}
			writeSSE("response.output_item.done", map[string]any{
				"type":         "response.output_item.done",
				"output_index": 0,
				"item":         msgOut,
			})
			writeSSE("response.completed", map[string]any{
				"type": "response.completed",
				"response": map[string]any{
					"id":     respID,
					"status": "completed",
					"model":  model,
					"output": []map[string]any{msgOut},
					"usage":  map[string]any{"input_tokens": 40, "output_tokens": 8, "total_tokens": 48},
				},
			})
			_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
			if flusher != nil {
				flusher.Flush()
			}
			return
		default:
			http.NotFound(w, r)
		}
	}))
	defer mockOR.Close()

	root := nonTmpServerTestDir(t, "real-codex-rewind")
	stateDir := filepath.Join(root, "state")
	cfgDir := filepath.Join(root, "cfg")
	workDir := filepath.Join(root, "work")
	_ = os.MkdirAll(stateDir, 0o700)
	_ = os.MkdirAll(workDir, 0o755)

	logBuf := &syncTestLogBuf{}
	origOut := log.Writer()
	log.SetOutput(logBuf)
	defer log.SetOutput(origOut)

	// Pre-populate a historical ~/.codex/sessions rollout BEFORE starting Manager (Challenge 3 check on real Rust codex!).
	preID := "019ccafe-0000-7000-8000-000000000099"
	preDir := filepath.Join(root, ".codex", "sessions", "2026", "11", "24")
	_ = os.MkdirAll(preDir, 0o700)
	preRollout := filepath.Join(preDir, "rollout-2026-11-24T10-00-00-"+preID+".jsonl")
	preLines := strings.Join([]string{
		`{"timestamp":"2026-11-24T10:00:00Z","type":"session_meta","payload":{"id":"` + preID + `","timestamp":"2026-11-24T10:00:00Z","cwd":"` + workDir + `","cli_version":"0.172.0","source":"cli","model_provider":"openrouter"}}`,
		`{"timestamp":"2026-11-24T10:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"Hello historical"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(preRollout, []byte(preLines), 0o600); err != nil {
		t.Fatal(err)
	}

	id, err := secure.LoadOrCreateIdentity(filepath.Join(stateDir, "identity.json"))
	if err != nil {
		t.Fatal(err)
	}
	devs, err := secure.OpenRegistry(filepath.Join(stateDir, "devices.json"))
	if err != nil {
		t.Fatal(err)
	}
	mgr := core.NewManager(core.Config{
		AgyPath:           "nonexistent-agy",
		ClaudePath:        "nonexistent-claude",
		CodexPath:         codexBin,
		OpenRouterAPIKey:  "sk-or-v1-hermetic-real-codex-key",
		OpenRouterBaseURL: mockOR.URL + "/api/v1",
		StateDir:          stateDir,
		ConfigDir:         cfgDir,
		HomeDir:           root,
	}, core.NewHub())

	srv := &Server{
		Mgr: mgr, Terms: &term.Manager{Sock: filepath.Join(root, "tmux.sock"), Conf: filepath.Join(root, "tmux.conf"), CodexPath: mgr.CodexBin(), CodexLaunch: mgr.CodexTermLaunch},
		Identity: id, Devices: devs, Tokens: secure.NewTokens(), Started: time.Now(),
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	srv.PublicURL = func() string { return ts.URL }
	rpc := pairAndConnectRPC(t, srv, ts.URL)

	// 1. Fresh-daemon control RPC BEFORE providers.models or session.create!
	openRes := rpc.callTimeout(t, 20*time.Second, "session.open", map[string]any{"id": preID}).(map[string]any)
	if openRes["id"] != preID {
		t.Fatalf("unexpected session.open result: %+v", openRes)
	}
	_ = rpc.callTimeout(t, 20*time.Second, "session.delete", map[string]any{"id": preID})

	// 2. Real Rust codex app-server Turn-1 Rewind -> new Turn 1 (KIWI-888) -> Hibernate (session.stop) -> Resume Turn 2 (RECALL_KIWI)!
	created := rpc.callTimeout(t, 25*time.Second, "session.create", map[string]any{
		"agent": "codex",
		"cwd":   workDir,
		"flags": map[string]any{"model": "google/gemini-3-flash-preview", "mode": "full-access", "effort": "low"},
	}).(map[string]any)
	sid := created["session"].(map[string]any)["id"].(string)

	rpc.callTimeout(t, 15*time.Second, "session.subscribe", map[string]any{"id": sid, "since": 0})
	rpc.waitForEvent(t, 15*time.Second, func(ev string, d map[string]any) bool {
		return ev == "session.snapshot" && d["id"] == sid
	})

	waitIdle := func(wantTurns int) map[string]any {
		t.Helper()
		dl := time.After(30 * time.Second)
		for {
			select {
			case m := <-rpc.events:
				ev, _ := m["event"].(string)
				d, _ := m["data"].(map[string]any)
				if ev == "session.changed" && d["id"] == sid && d["state"] == "idle" {
					if turns, ok := d["turns"].(float64); ok && int(turns) >= wantTurns {
						return d
					}
				}
			case <-dl:
				t.Fatalf("timed out waiting for real codex idle turns>=%d", wantTurns)
			}
		}
	}

	rpc.callTimeout(t, 15*time.Second, "session.prompt", map[string]any{"id": sid, "text": "Remember OLD-111"})
	waitIdle(1)

	rpc.callTimeout(t, 15*time.Second, "session.subscribe", map[string]any{"id": sid, "since": 0})
	snap1 := rpc.waitForEvent(t, 15*time.Second, func(ev string, d map[string]any) bool {
		return ev == "session.snapshot" && d["id"] == sid
	})
	var firstUserItemID string
	for _, rawIt := range snap1["items"].([]any) {
		it := rawIt.(map[string]any)
		if it["kind"] == "user" {
			firstUserItemID = it["id"].(string)
			break
		}
	}
	if firstUserItemID == "" {
		t.Fatal("missing first user item in real codex snapshot")
	}

	// Rewind Turn 1 (isFirst == true) -> new prompt KIWI-888 -> session.stop -> resume prompt RECALL_KIWI!
	rpc.callTimeout(t, 20*time.Second, "session.rewind", map[string]any{
		"id":               sid,
		"itemId":           firstUserItemID,
		"conversationOnly": true,
	})
	rpc.callTimeout(t, 15*time.Second, "session.prompt", map[string]any{"id": sid, "text": "Remember KIWI-888"})
	waitIdle(1)

	rpc.callTimeout(t, 15*time.Second, "session.stop", map[string]any{"id": sid})
	rpc.callTimeout(t, 15*time.Second, "session.prompt", map[string]any{"id": sid, "text": "RECALL_KIWI"})
	changed2 := waitIdle(2)
	if lastTxt, _ := changed2["lastText"].(string); !strings.Contains(lastTxt, "RECALLED_KIWI-888_CLEAN") {
		t.Fatalf("expected resumed real codex thread to retain KIWI-888 without OLD-111, got lastText=%q", lastTxt)
	}

	// Verify sessions.list has zero duplicate rows!
	sessRows := rpc.callTimeout(t, 15*time.Second, "sessions.list", map[string]any{"force": true}).([]any)
	foundSidCount := 0
	for _, rawRow := range sessRows {
		row := rawRow.(map[string]any)
		if row["agent"] == "codex" {
			if row["id"] != sid {
				t.Fatalf("backing codex thread leaked as duplicate row in sessions.list: %+v", sessRows)
			}
			foundSidCount++
		}
	}
	if foundSidCount != 1 {
		t.Fatalf("expected exactly 1 codex session (%s) in sessions.list, got %d (%+v)", sid, foundSidCount, sessRows)
	}

	// Verify Shutdown + immediate StateDir removal produces ZERO post-shutdown save errors!
	_ = rpc.conn.Close()
	ts.Close()
	time.Sleep(100 * time.Millisecond)
	mgr.Shutdown()
	_ = os.RemoveAll(stateDir)
	time.Sleep(1700 * time.Millisecond)
	logged := logBuf.String()
	if strings.Contains(logged, "no such file or directory") {
		t.Fatalf("unexpected post-Shutdown save error logged:\n%s", logged)
	}
}

type syncTestLogBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncTestLogBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncTestLogBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestCodexRealAppServerPreTurn1SetModelUnlistedSlug(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	codexBin := filepath.Join(home, ".local", "bin", "codex")
	if _, err := os.Stat(codexBin); err != nil {
		lp, lerr := exec.LookPath("codex")
		if lerr != nil {
			t.Fatalf("codex binary required for real app-server test: %v", lerr)
		}
		codexBin = lp
	}

	var mu sync.Mutex
	var seenModels []string

	mockOR := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/models/user", "/api/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"google/gemini-3-flash-preview","name":"Gemini 3 Flash","context_length":1048576,"supported_parameters":["tools","reasoning"],"architecture":{"input_modalities":["text"]}}]}`))
			return
		case "/api/v1/responses":
			raw, _ := io.ReadAll(r.Body)
			var reqBody struct {
				Model string `json:"model"`
			}
			_ = json.Unmarshal(raw, &reqBody)
			mu.Lock()
			seenModels = append(seenModels, reqBody.Model)
			seq := len(seenModels)
			mu.Unlock()

			w.Header().Set("Content-Type", "text/event-stream")
			flusher, _ := w.(http.Flusher)
			writeSSE := func(event string, payload any) {
				b, _ := json.Marshal(payload)
				_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, string(b))
				if flusher != nil {
					flusher.Flush()
				}
			}
			respID := fmt.Sprintf("resp_r4_%d", seq)
			msgID := fmt.Sprintf("msg_r4_%d", seq)
			reply := "R4_PRETURN1_OK:" + reqBody.Model
			writeSSE("response.created", map[string]any{
				"type":     "response.created",
				"response": map[string]any{"id": respID, "status": "in_progress"},
			})
			writeSSE("response.output_item.added", map[string]any{
				"type":         "response.output_item.added",
				"output_index": 0,
				"item":         map[string]any{"id": msgID, "type": "message", "role": "assistant", "status": "in_progress", "content": []any{}},
			})
			writeSSE("response.content_part.added", map[string]any{
				"type":          "response.content_part.added",
				"item_id":       msgID,
				"output_index":  0,
				"content_index": 0,
				"part":          map[string]any{"type": "output_text", "text": ""},
			})
			writeSSE("response.output_text.delta", map[string]any{
				"type":          "response.output_text.delta",
				"item_id":       msgID,
				"output_index":  0,
				"content_index": 0,
				"delta":         reply,
			})
			writeSSE("response.output_text.done", map[string]any{
				"type":          "response.output_text.done",
				"item_id":       msgID,
				"output_index":  0,
				"content_index": 0,
				"text":          reply,
			})
			msgOut := map[string]any{
				"id":      msgID,
				"type":    "message",
				"role":    "assistant",
				"status":  "completed",
				"content": []map[string]any{{"type": "output_text", "text": reply}},
			}
			writeSSE("response.output_item.done", map[string]any{
				"type":         "response.output_item.done",
				"output_index": 0,
				"item":         msgOut,
			})
			writeSSE("response.completed", map[string]any{
				"type": "response.completed",
				"response": map[string]any{
					"id":     respID,
					"status": "completed",
					"output": []map[string]any{msgOut},
					"usage":  map[string]any{"input_tokens": 25, "output_tokens": 8, "total_tokens": 33},
				},
			})
			_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
			if flusher != nil {
				flusher.Flush()
			}
			return
		default:
			http.NotFound(w, r)
		}
	}))
	defer mockOR.Close()

	root := nonTmpServerTestDir(t, "real-codex-preturn1-setmodel")
	stateDir := filepath.Join(root, "state")
	cfgDir := filepath.Join(root, "cfg")
	workDir := filepath.Join(root, "work")
	for _, d := range []string{stateDir, cfgDir, workDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	mgr := core.NewManager(core.Config{
		AgyPath:           "nonexistent-agy",
		ClaudePath:        "nonexistent-claude",
		CodexPath:         codexBin,
		OpenRouterAPIKey:  "sk-or-v1-real-codex-r4-key",
		OpenRouterBaseURL: mockOR.URL + "/api/v1",
		StateDir:          stateDir,
		ConfigDir:         cfgDir,
		WorktreeDir:       filepath.Join(root, "wt"),
		HomeDir:           root,
	}, core.NewHub())
	defer mgr.Shutdown()

	ctx := context.Background()
	s, err := mgr.Create(ctx, core.CreateParams{
		Agent: "codex",
		Cwd:   workDir,
		Flags: core.Flags{Model: "google/gemini-3-flash-preview"},
	})
	if err != nil {
		t.Fatalf("mgr.Create: %v", err)
	}

	unlisted := "vendor-future/brand-new-model-v9"
	if err := s.SetModel(ctx, unlisted); err != nil {
		t.Fatalf("pre-Turn-1 SetModel(%q): %v", unlisted, err)
	}

	if _, err := s.Prompt(core.PromptInput{Text: "Run Turn 1 against real codex 0.172.0"}); err != nil {
		t.Fatalf("Prompt Turn 1: %v", err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		sum := s.Summary()
		if sum.State == "idle" && (sum.Turns >= 1 || sum.Error != "") {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	sum := s.Summary()
	if sum.Turns != 1 || sum.Error != "" {
		t.Fatalf("real codex Turn 1 after pre-Turn-1 SetModel failed: turns=%d lastErr=%q", sum.Turns, sum.Error)
	}

	mu.Lock()
	modelsCopy := append([]string(nil), seenModels...)
	mu.Unlock()
	if len(modelsCopy) == 0 || modelsCopy[0] != unlisted {
		t.Fatalf("expected OpenRouter request model %q, got %v", unlisted, modelsCopy)
	}
}
