package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentc/internal/codex"
	"agentc/internal/providers"
)

func writeStrictHomeFakeCodex(t *testing.T, dir string) (binPath, logPath string) {
	t.Helper()
	binPath = filepath.Join(dir, "strict-home-codex")
	logPath = filepath.Join(dir, "strict-home-codex.jsonl")
	script := `#!/usr/bin/env python3
import json, os, pathlib, sys

if len(sys.argv) > 1 and sys.argv[1] == "--version":
    print("codex-cli 0.172.0")
    sys.exit(0)

codex_home = os.environ.get("CODEX_HOME", "")
log_path = os.environ.get("STRICT_CODEX_LOG", "")

def log_ev(obj):
    if log_path:
        with open(log_path, "a", encoding="utf-8") as f:
            f.write(json.dumps(obj) + "\n")

def find_in_codex_home_only(tid):
    if not tid or not codex_home:
        return None
    want_sfx = f"-{tid.lower()}.jsonl"
    exact = f"{tid.lower()}.jsonl"
    for sub in ("sessions", "archived_sessions"):
        base = pathlib.Path(codex_home) / sub
        if not base.exists():
            continue
        for p in base.rglob("*.jsonl"):
            low = p.name.lower()
            if low.endswith(want_sfx) or low == exact:
                return p
    return None

def hydrate(tid, rp):
    cwd = ""
    model = "gpt-5.3-codex"
    prov = "openai"
    turns = []
    for line in rp.read_text(encoding="utf-8").splitlines():
        if not line.strip():
            continue
        rec = json.loads(line)
        pl = rec.get("payload") or {}
        if rec.get("type") == "session_meta":
            cwd = pl.get("cwd", cwd)
            model = pl.get("model") or model
            prov = pl.get("model_provider") or pl.get("modelProvider") or prov
        elif rec.get("type") == "event_msg" and pl.get("type") == "user_message":
            idx = len(turns) + 1
            msg = pl.get("message", "")
            turns.append({
                "id": f"turn_{idx}",
                "status": "completed",
                "items": [
                    {"id": f"u_{idx}", "type": "userMessage", "content": [{"type": "text", "text": msg}]},
                    {"id": f"a_{idx}", "type": "agentMessage", "text": f"Strict reply {idx}"},
                ],
            })
    return {
        "id": tid,
        "cwd": cwd,
        "model": model,
        "modelProvider": prov,
        "createdAt": 1700000000,
        "updatedAt": 1700000010,
        "path": str(rp),
        "turns": turns,
    }

def send(obj):
    sys.stdout.write(json.dumps(obj) + "\n")
    sys.stdout.flush()

active_threads = {}

for raw in sys.stdin:
    raw = raw.strip()
    if not raw:
        continue
    msg = json.loads(raw)
    rid = msg.get("id")
    method = msg.get("method", "")
    params = msg.get("params") or {}
    if method:
        log_ev({
            "method": method,
            "codex_home": codex_home,
            "argv": sys.argv[1:],
            "custom_env": os.environ.get("CUSTOM_CODEX_PROVIDER_ENV", ""),
            "bridge_tok": os.environ.get("AGENTC_BRIDGE_TOKEN", ""),
            "params": params,
        })
    if rid is None or method == "":
        continue
    if method == "initialize":
        send({"id": rid, "result": {"userAgent": "codex/0.172.0", "codexHome": codex_home}})
    elif method in ("thread/read", "thread/resume"):
        tid = params.get("threadId", "")
        rp = find_in_codex_home_only(tid)
        if not rp:
            send({"id": rid, "error": {"code": -32600, "message": f"rollout {tid} missing in CODEX_HOME={codex_home}"}})
            continue
        th = hydrate(tid, rp)
        active_threads[tid] = th
        resp_th = dict(th)
        if method == "thread/resume" and params.get("excludeTurns"):
            resp_th["turns"] = []
        send({"id": rid, "result": {"thread": resp_th, "model": th["model"], "modelProvider": th["modelProvider"]}})
    elif method == "thread/start":
        tid = "019c9999-0000-7000-8000-000000000001"
        th = {
            "id": tid,
            "cwd": params.get("cwd", ""),
            "model": params.get("model", "gpt-5.3-codex"),
            "modelProvider": params.get("modelProvider", "openai"),
            "createdAt": 1700000100,
            "updatedAt": 1700000100,
            "turns": [],
        }
        active_threads[tid] = th
        send({"id": rid, "result": {"thread": th, "model": th["model"], "modelProvider": th["modelProvider"]}})
    elif method == "thread/fork":
        tid = params.get("threadId", "")
        rp = find_in_codex_home_only(tid)
        if not rp:
            send({"id": rid, "error": {"code": -32600, "message": f"fork source {tid} missing in CODEX_HOME={codex_home}"}})
            continue
        th = hydrate(tid, rp)
        new_id = "019c8888-0000-7000-8000-000000000002"
        rdir = pathlib.Path(codex_home) / "sessions" / "2026" / "11" / "25"
        rdir.mkdir(parents=True, exist_ok=True)
        (rdir / f"rollout-2026-11-25T10-00-00-{new_id}.jsonl").write_text(rp.read_text(encoding="utf-8"), encoding="utf-8")
        forked = dict(th)
        forked["id"] = new_id
        forked["forkedFromId"] = tid
        send({"id": rid, "result": {"thread": forked, "model": forked["model"], "modelProvider": forked["modelProvider"]}})
    elif method in ("thread/revert", "thread/name/set", "thread/archive", "thread/unarchive"):
        tid = params.get("threadId", "")
        rp = find_in_codex_home_only(tid)
        if not rp:
            send({"id": rid, "error": {"code": -32600, "message": f"{method} {tid} missing in CODEX_HOME={codex_home}"}})
            continue
        th = hydrate(tid, rp)
        send({"id": rid, "result": {"thread": th}})
    elif method == "turn/start":
        tid = params.get("threadId", "")
        turn_id = "turn_aside_or_prompt"
        send({"id": rid, "result": {"turn": {"id": turn_id, "status": "inProgress"}}})
        send({"method": "turn/started", "params": {"threadId": tid, "turn": {"id": turn_id, "status": "inProgress"}}})
        msg_id = "msg_1"
        send({"method": "item/started", "params": {"threadId": tid, "turnId": turn_id, "item": {"id": msg_id, "type": "agentMessage", "text": ""}}})
        send({"method": "item/agentMessage/delta", "params": {"threadId": tid, "turnId": turn_id, "itemId": msg_id, "delta": "External home answer OK"}})
        send({"method": "item/completed", "params": {"threadId": tid, "turnId": turn_id, "item": {"id": msg_id, "type": "agentMessage", "text": "External home answer OK"}}})
        send({"method": "turn/completed", "params": {"threadId": tid, "turn": {"id": turn_id, "status": "completed"}}})
    else:
        send({"id": rid, "result": {}})
`
	if err := os.WriteFile(binPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return binPath, logPath
}

func TestCodexExternalHomeResolution(t *testing.T) {
	root := nonTmpTestDir(t, "codex-ext-home")
	stateDir := filepath.Join(root, "state")
	cfgDir := filepath.Join(root, "cfg")
	homeDir := filepath.Join(root, "home")
	workDir := filepath.Join(root, "work")
	for _, d := range []string{stateDir, cfgDir, homeDir, workDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	// 1. Verify EnsureCodexHome self-copy guard (direct ~/.codex and symlinked ~/.codex).
	userCodex := filepath.Join(homeDir, ".codex")
	if err := os.MkdirAll(userCodex, 0o700); err != nil {
		t.Fatal(err)
	}
	authContent := `{"OPENAI_API_KEY":"sk-native-openai-auth"}`
	authPath := filepath.Join(userCodex, "auth.json")
	if err := os.WriteFile(authPath, []byte(authContent), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := codex.EnsureCodexHome(userCodex, homeDir); err != nil {
		t.Fatalf("EnsureCodexHome(userCodex): %v", err)
	}
	symCodex := filepath.Join(root, "sym-codex")
	if err := os.Symlink(userCodex, symCodex); err != nil {
		t.Fatal(err)
	}
	if err := codex.EnsureCodexHome(symCodex, homeDir); err != nil {
		t.Fatalf("EnsureCodexHome(symCodex): %v", err)
	}
	if gotAuth, err := os.ReadFile(authPath); err != nil || string(gotAuth) != authContent {
		t.Fatalf("auth.json corrupted after EnsureCodexHome self-copy guard: %q (%v)", string(gotAuth), err)
	}

	// 2. Seed standalone ~/.codex rollout (native "openai" provider, 2 turns).
	extID := "01970000-0000-7000-8000-000000000099"
	sessDir := filepath.Join(userCodex, "sessions", "2026", "11", "25")
	if err := os.MkdirAll(sessDir, 0o700); err != nil {
		t.Fatal(err)
	}
	rolloutPath := filepath.Join(sessDir, "rollout-2026-11-25T10-00-00-"+extID+".jsonl")
	rolloutLines := strings.Join([]string{
		`{"timestamp":"2026-11-25T10:00:00Z","type":"session_meta","payload":{"id":"` + extID + `","timestamp":"2026-11-25T10:00:00Z","cwd":"` + workDir + `","cli_version":"0.172.0","source":"cli","model_provider":"openai","model":"gpt-5.3-codex"}}`,
		`{"timestamp":"2026-11-25T10:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"First external turn"}}`,
		`{"timestamp":"2026-11-25T10:00:02Z","type":"event_msg","payload":{"type":"user_message","message":"Second external turn"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(rolloutPath, []byte(rolloutLines), 0o600); err != nil {
		t.Fatal(err)
	}

	fakeBin, logPath := writeStrictHomeFakeCodex(t, root)
	provStore, err := providers.Open(filepath.Join(cfgDir, "providers.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provStore.Upsert(providers.ProviderConfig{
		ID:      "codex",
		Name:    "Codex",
		Enabled: true,
		Command: fakeBin,
		Args:    []string{"-c", "custom_flag=1"},
		Env:     map[string]string{"CUSTOM_CODEX_PROVIDER_ENV": "merged-ok"},
	}); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(Config{
		AgyPath:          "nonexistent-agy",
		ClaudePath:       "nonexistent-claude",
		CodexPath:        "nonexistent-default-codex",
		OpenRouterAPIKey: "sk-or-v1-configured-but-must-not-hijack-native",
		StateDir:         stateDir,
		ConfigDir:        cfgDir,
		HomeDir:          homeDir,
		Env:              map[string]string{"STRICT_CODEX_LOG": logPath},
	}, NewHub())
	mgr.Providers = provStore
	defer mgr.Shutdown()

	if gotHome := mgr.codexHomeForThread(extID); filepath.Clean(gotHome) != filepath.Clean(userCodex) {
		t.Fatalf("codexHomeForThread(%s) = %q, want %q", extID, gotHome, userCodex)
	}

	// 3. CodexTermLaunch BEFORE opening session: must resolve ~/.codex from rollout & preserve native openai (NO OpenRouter hijack!).
	_, termArgsPre, termEnvPre := mgr.CodexTermLaunch(extID, false, nil)
	if filepath.Clean(termEnvPre["CODEX_HOME"]) != filepath.Clean(userCodex) {
		t.Fatalf("CodexTermLaunch pre-open CODEX_HOME = %q, want %q", termEnvPre["CODEX_HOME"], userCodex)
	}
	if _, hasTok := termEnvPre["AGENTC_BRIDGE_TOKEN"]; hasTok {
		t.Fatalf("CodexTermLaunch hijacked native openai rollout with AGENTC_BRIDGE_TOKEN: %+v", termEnvPre)
	}
	for _, a := range termArgsPre {
		if strings.Contains(a, `model_provider="openrouter"`) {
			t.Fatalf("CodexTermLaunch injected OpenRouter override on native openai rollout: %v", termArgsPre)
		}
	}

	// Explicit extraArgs `-c model_provider="openai"` on blank convID must also prevent OpenRouter hijack.
	_, termArgsExplicit, termEnvExplicit := mgr.CodexTermLaunch("", false, []string{"-c", `model_provider="openai"`})
	if _, hasTok := termEnvExplicit["AGENTC_BRIDGE_TOKEN"]; hasTok {
		t.Fatalf("CodexTermLaunch ignored explicit model_provider=openai in extraArgs: %+v", termEnvExplicit)
	}
	for _, a := range termArgsExplicit {
		if strings.Contains(a, `model_provider="openrouter"`) {
			t.Fatalf("CodexTermLaunch injected OpenRouter override despite explicit model_provider=openai: %v", termArgsExplicit)
		}
	}

	// Explicit OpenRouter model in extraArgs MUST still inject OpenRouter overrides.
	_, termArgsOR, termEnvOR := mgr.CodexTermLaunch("", false, []string{"-m", "google/gemini-3-flash-preview"})
	if termEnvOR["AGENTC_BRIDGE_TOKEN"] == "" {
		t.Fatalf("expected AGENTC_BRIDGE_TOKEN when launching OpenRouter model in terminal: %+v (args=%v)", termEnvOR, termArgsOR)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// 4. Open external session -> hydrates items from ~/.codex!
	s, err := mgr.Open(ctx, extID)
	if err != nil {
		t.Fatalf("Open(%s): %v", extID, err)
	}
	if s.Provider() != "openai" {
		t.Fatalf("expected opened external session Provider()=openai, got %q", s.Provider())
	}
	s.mu.Lock()
	items := append([]*Item(nil), s.tr.Items...)
	s.mu.Unlock()
	if len(items) < 4 {
		t.Fatalf("expected >=4 hydrated transcript items from ~/.codex rollout, got %d", len(items))
	}

	// 5. Rename & Archive while sleeping -> uses ~/.codex!
	if err := mgr.Rename(ctx, extID, "Renamed External Session"); err != nil {
		t.Fatalf("Rename(%s): %v", extID, err)
	}
	if err := mgr.SetFlag(ctx, extID, "archived", true); err != nil {
		t.Fatalf("SetFlag(archived): %v", err)
	}
	if err := mgr.SetFlag(ctx, extID, "archived", false); err != nil {
		t.Fatalf("SetFlag(unarchive): %v", err)
	}

	// 6. Fork before 2nd user turn -> resolves beforeTurnId & forks in ~/.codex!
	var secondUserItemID string
	userSeen := 0
	for _, it := range items {
		if it.Kind == "user" {
			userSeen++
			if userSeen == 2 {
				secondUserItemID = it.ID
				break
			}
		}
	}
	if secondUserItemID == "" {
		t.Fatalf("missing second user item in hydrated items: %+v", items)
	}
	forkSess, _, err := mgr.Fork(ctx, extID, secondUserItemID)
	if err != nil {
		t.Fatalf("Fork(%s): %v", extID, err)
	}
	if forkSess.ID() != "019c8888-0000-7000-8000-000000000002" {
		t.Fatalf("expected real RPC fork ID 019c8888-0000-7000-8000-000000000002 (not synthetic fallback), got %q", forkSess.ID())
	}

	// 7. Rewind sleeping external session -> resolves beforeTurnId & reverts in ~/.codex!
	if _, err := mgr.Rewind(ctx, extID, secondUserItemID, true); err != nil {
		t.Fatalf("Rewind(%s): %v", extID, err)
	}

	// 8. AskAside -> merges Providers.Get("codex") Command/Env/Args and honors s.Provider() ("openai")!
	asidePtr, err := mgr.AskAside(ctx, extID, "Quick side question?", "")
	if err != nil {
		t.Fatalf("AskAside(%s): %v", extID, err)
	}
	asideRes := *asidePtr
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		asides, _ := mgr.Asides(extID)
		if len(asides) > 0 && asides[0].State == "done" {
			asideRes = asides[0]
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if asideRes.State != "done" || !strings.Contains(asideRes.Answer, "External home answer OK") {
		t.Fatalf("unexpected aside state/answer: %+v", asideRes)
	}

	// 9. Wake/Resume via Prompt -> runs in ~/.codex with native openai provider!
	if _, err := s.Prompt(PromptInput{Text: "Continue external thread"}); err != nil {
		t.Fatalf("Prompt(%s): %v", extID, err)
	}
	waitDl := time.Now().Add(10 * time.Second)
	for time.Now().Before(waitDl) {
		if s.Summary().State == "idle" && s.Summary().Turns >= 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Inspect strict fake-codex JSONL log: every thread op MUST have used userCodex, merged env/args on aside, and ZERO OpenRouter hijack!
	rawLog, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read strict log: %v", err)
	}
	sawAsideMergedEnv := false
	sawResumeInUserCodex := false
	for _, line := range strings.Split(strings.TrimSpace(string(rawLog)), "\n") {
		if line == "" {
			continue
		}
		var ev struct {
			Method    string   `json:"method"`
			CodexHome string   `json:"codex_home"`
			Argv      []string `json:"argv"`
			CustomEnv string   `json:"custom_env"`
			BridgeTok string   `json:"bridge_tok"`
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("unmarshal log line: %v", err)
		}
		if filepath.Clean(ev.CodexHome) != filepath.Clean(userCodex) {
			t.Fatalf("method %s ran with wrong CODEX_HOME=%q, want %q", ev.Method, ev.CodexHome, userCodex)
		}
		if ev.BridgeTok != "" {
			t.Fatalf("method %s leaked AGENTC_BRIDGE_TOKEN onto native openai thread!", ev.Method)
		}
		for _, a := range ev.Argv {
			if strings.Contains(a, `model_provider="openrouter"`) {
				t.Fatalf("method %s hijacked native openai thread with %q", ev.Method, a)
			}
		}
		if ev.Method == "thread/start" && ev.CustomEnv == "merged-ok" {
			hasCustomArg := false
			for _, a := range ev.Argv {
				if a == "custom_flag=1" {
					hasCustomArg = true
				}
			}
			if hasCustomArg {
				sawAsideMergedEnv = true
			}
		}
		if ev.Method == "thread/resume" {
			sawResumeInUserCodex = true
		}
	}
	if !sawAsideMergedEnv {
		t.Fatalf("expected asideCodex to merge Providers.Get(codex) Command/Env/Args; log:\n%s", string(rawLog))
	}
	if !sawResumeInUserCodex {
		t.Fatalf("expected Prompt wake to call thread/resume in ~/.codex; log:\n%s", string(rawLog))
	}

	// 10. R2-D04: CodexTermLaunch propagates ProviderConfig.DefaultModel / sessModel via -c model=...,
	// prepends filepath.Dir(bin) to PATH, merges non-secret ProviderConfig Args/Env without leaking secrets,
	// and omits resume on pre-Turn-1 unmaterialized sessions!
	if _, err := provStore.Upsert(providers.ProviderConfig{
		ID:           "codex",
		Enabled:      true,
		Command:      fakeBin,
		DefaultModel: "google/gemini-2.5-pro",
		Args:         []string{"-c", "custom_flag=1"},
		Env: map[string]string{
			"CUSTOM_CODEX_PROVIDER_ENV": "merged-ok",
			"OPENROUTER_API_KEY":        "sk-or-v1-secret-must-never-appear-in-term-env",
			"OPENROUTER_API_KEY_FILE":   "/secret/key/file",
			"REDACTED_SAMPLE":           providers.RedactedSecret,
		},
	}); err != nil {
		t.Fatal(err)
	}

	gotBin, defArgs, defEnv := mgr.CodexTermLaunch("", false, nil)
	if filepath.Clean(gotBin) != filepath.Clean(fakeBin) {
		t.Fatalf("CodexTermLaunch bin = %q, want %q", gotBin, fakeBin)
	}
	wantBinDir := filepath.Dir(fakeBin)
	if !strings.HasPrefix(defEnv["PATH"], wantBinDir+string(os.PathListSeparator)) && defEnv["PATH"] != wantBinDir {
		t.Fatalf("expected extraEnv[PATH] to start with %q, got %q", wantBinDir, defEnv["PATH"])
	}
	if defEnv["CUSTOM_CODEX_PROVIDER_ENV"] != "merged-ok" {
		t.Fatalf("expected CUSTOM_CODEX_PROVIDER_ENV=merged-ok in extraEnv, got %+v", defEnv)
	}
	for _, forbiddenKey := range []string{"OPENROUTER_API_KEY", "OPENROUTER_API_KEY_FILE", "REDACTED_SAMPLE"} {
		if v, exists := defEnv[forbiddenKey]; exists {
			t.Fatalf("CodexTermLaunch leaked forbidden key %s=%q into extraEnv", forbiddenKey, v)
		}
	}
	defArgsJoined := strings.Join(defArgs, " ")
	if !strings.Contains(defArgsJoined, `-c model="google/gemini-2.5-pro"`) {
		t.Fatalf("expected DefaultModel -c model=\"google/gemini-2.5-pro\" in args, got %v", defArgs)
	}
	if !strings.Contains(defArgsJoined, "-c custom_flag=1") {
		t.Fatalf("expected ProviderConfig.Args (-c custom_flag=1) in args, got %v", defArgs)
	}
	if !mgr.CodexCatalog().HasCatalogSlug("google/gemini-2.5-pro") {
		t.Fatalf("expected google/gemini-2.5-pro ensured in CodexCatalog")
	}

	// Pre-Turn-1 unmaterialized session: must propagate sessModel and OMIT invalid resume <id>!
	unmatSess, err := mgr.Create(ctx, CreateParams{
		Agent: "codex",
		Cwd:   workDir,
		Flags: Flags{Model: "deepseek/deepseek-v4.1-flash"},
	})
	if err != nil {
		t.Fatalf("Create unmaterialized codex session: %v", err)
	}
	_, unmatArgs, _ := mgr.CodexTermLaunch(unmatSess.ID(), false, nil)
	unmatJoined := strings.Join(unmatArgs, " ")
	if !strings.Contains(unmatJoined, `-c model="deepseek/deepseek-v4.1-flash"`) {
		t.Fatalf("expected sessModel -c model=\"deepseek/deepseek-v4.1-flash\" in args, got %v", unmatArgs)
	}
	for _, a := range unmatArgs {
		if a == "resume" || a == unmatSess.ID() {
			t.Fatalf("expected pre-Turn-1 unmaterialized session to omit resume, got %v", unmatArgs)
		}
	}
}
