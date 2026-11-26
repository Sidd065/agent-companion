package core

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentc/internal/acp"
)

func nonTmpTestDir(t *testing.T, prefix string) string {
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

func writeCoreFakeCodex(t *testing.T, dir string) (binPath, logPath string) {
	t.Helper()
	binPath = filepath.Join(dir, "fake-codex-core")
	logPath = filepath.Join(dir, "fake-codex-core.jsonl")
	script := `#!/usr/bin/env python3
import json, os, pathlib, sys

if len(sys.argv) > 1 and sys.argv[1] == "--version":
    print("codex-cli 0.172.0")
    sys.exit(0)

# Validate -c model_catalog_json=<path> exists & parses on startup just like codex 0.172.0!
argv = sys.argv[1:]
for idx, tok in enumerate(argv):
    if tok == "-c" and idx + 1 < len(argv) and argv[idx + 1].startswith("model_catalog_json="):
        raw_val = argv[idx + 1].split("=", 1)[1].strip()
        if raw_val.startswith('"') and raw_val.endswith('"'):
            raw_val = json.loads(raw_val)
        cp = pathlib.Path(raw_val)
        if not cp.is_file() or cp.stat().st_size == 0:
            sys.stderr.write("Error: No such file or directory (os error 2)\n")
            sys.exit(2)
        parsed_cat = json.loads(cp.read_text(encoding="utf-8"))
        if not isinstance(parsed_cat.get("models"), list) or len(parsed_cat["models"]) == 0:
            sys.stderr.write("Error: invalid model_catalog_json\n")
            sys.exit(2)

log_path = os.environ.get("FAKE_CODEX_CORE_LOG", "")
def log_event(obj):
    if log_path:
        with open(log_path, "a", encoding="utf-8") as f:
            f.write(json.dumps(obj) + "\n")

codex_home = os.environ.get("CODEX_HOME", "")
user_home = os.environ.get("HOME", "")
state_file = pathlib.Path(codex_home) / "mock_threads.json"

def find_rollout_file(tid):
    if not tid:
        return None
    want_sfx = f"-{tid.lower()}.jsonl"
    exact = f"{tid.lower()}.jsonl"
    roots = [codex_home]
    if user_home:
        roots.append(str(pathlib.Path(user_home) / ".codex"))
    for r in roots:
        if not r:
            continue
        for sub in ("sessions", "archived_sessions"):
            base_dir = pathlib.Path(r) / sub
            if not base_dir.exists():
                continue
            for p in base_dir.rglob("*.jsonl"):
                low = p.name.lower()
                if low.endswith(want_sfx) or low == exact:
                    return p
    return None

def hydrate_from_rollout(tid, rp):
    cwd = ""
    turns = []
    for line in rp.read_text(encoding="utf-8").splitlines():
        if not line.strip():
            continue
        rec = json.loads(line)
        if rec.get("type") == "session_meta":
            cwd = (rec.get("payload") or {}).get("cwd", "")
        elif rec.get("type") == "event_msg":
            pl = rec.get("payload") or {}
            if pl.get("type") == "user_message":
                t_idx = len(turns) + 1
                msg_txt = pl.get("message", "")
                turns.append({
                    "id": f"turn_{t_idx}",
                    "prompt": msg_txt,
                    "status": "completed",
                    "items": [
                        {"id": f"u_{t_idx}", "type": "userMessage", "content": [{"type": "text", "text": msg_txt}]},
                        {"id": f"a_{t_idx}", "type": "agentMessage", "text": f"Hydrated reply {t_idx}"},
                    ],
                })
    return {
        "id": tid,
        "cwd": cwd,
        "model": "google/gemini-3-flash-preview",
        "createdAt": 1700000000,
        "updatedAt": 1700000005,
        "path": str(rp),
        "turns": turns,
    }

def load_state():
    if state_file.exists():
        try:
            return json.loads(state_file.read_text(encoding="utf-8"))
        except Exception:
            pass
    return {"seq": 0, "threads": {}}

def save_state(st):
    state_file.parent.mkdir(parents=True, exist_ok=True)
    state_file.write_text(json.dumps(st), encoding="utf-8")

def write_rollout(th):
    rollout_dir = pathlib.Path(codex_home) / "sessions" / "2026" / "11" / "24"
    rollout_dir.mkdir(parents=True, exist_ok=True)
    rp = rollout_dir / f"rollout-2026-11-24T12-00-00-{th['id']}.jsonl"
    lines = [
        {"timestamp": "2026-11-24T12:00:00Z", "type": "session_meta", "payload": {
            "id": th["id"],
            "timestamp": "2026-11-24T12:00:00Z",
            "cwd": th.get("cwd", ""),
            "model_provider": "openrouter",
        }},
    ]
    for turn in th.get("turns", []):
        lines.append({"timestamp": "2026-11-24T12:00:01Z", "type": "event_msg", "payload": {
            "type": "user_message",
            "message": turn.get("prompt", ""),
        }})
    rp.write_text("\n".join(json.dumps(x) for x in lines) + "\n", encoding="utf-8")
    th["path"] = str(rp)

def send(obj):
    sys.stdout.write(json.dumps(obj) + "\n")
    sys.stdout.flush()

for raw in sys.stdin:
    raw = raw.strip()
    if not raw:
        continue
    msg = json.loads(raw)
    log_event(msg)
    rid = msg.get("id")
    method = msg.get("method", "")
    params = msg.get("params") or {}
    if rid is not None and method == "":
        continue

    st = load_state()
    if method == "initialize":
        send({"id": rid, "result": {"userAgent": "codex/0.172.0", "codexHome": codex_home}})
    elif method == "initialized":
        pass
    elif method == "model/list":
        send({"id": rid, "result": {"data": [{"id": "gpt-5.3-codex", "displayName": "GPT-5.3 Codex", "isDefault": True}]}})
    elif method == "thread/start":
        st["seq"] += 1
        tid = f"019c0000-0000-7000-8000-{st['seq']:012d}"
        th = {
            "id": tid,
            "cwd": params.get("cwd", ""),
            "model": params.get("model", "google/gemini-3-flash-preview"),
            "createdAt": 1700000000 + st["seq"],
            "updatedAt": 1700000000 + st["seq"],
            "turns": [],
        }
        globals().setdefault("_in_mem_threads", set()).add(tid)
        if os.environ.get("FAKE_CODEX_REAL_UNPERSISTED") != "1":
            write_rollout(th)
        st["threads"][tid] = th
        save_state(st)
        send({"id": rid, "result": {"thread": th, "model": th["model"]}})
    elif method == "thread/delete":
        send({"id": rid, "error": {"code": -32601, "message": "thread/delete must not be spawned"}})
    elif method == "thread/resume":
        tid = params.get("threadId")
        rp = find_rollout_file(tid)
        if not rp:
            st["threads"].pop(tid, None)
            save_state(st)
            send({"id": rid, "error": {"code": -32600, "message": f"no rollout found for thread id {tid}"}})
            continue
        th = st["threads"].get(tid)
        if not th:
            th = hydrate_from_rollout(tid, rp)
        if params.get("model"):
            th["model"] = params["model"]
        st["threads"][tid] = th
        save_state(st)
        resp_th = dict(th)
        if params.get("excludeTurns"):
            resp_th["turns"] = []
        send({"id": rid, "result": {"thread": resp_th, "model": th["model"]}})
    elif method == "thread/read":
        tid = params.get("threadId")
        rp = find_rollout_file(tid)
        if not rp:
            st["threads"].pop(tid, None)
            save_state(st)
            send({"id": rid, "error": {"code": -32600, "message": f"no rollout found for thread id {tid}"}})
            continue
        th = st["threads"].get(tid)
        if not th:
            th = hydrate_from_rollout(tid, rp)
            st["threads"][tid] = th
            save_state(st)
        send({"id": rid, "result": {"thread": th}})
    elif method == "thread/fork":
        src_id = params.get("threadId")
        src = st["threads"].get(src_id, {"cwd": params.get("cwd", ""), "model": "google/gemini-3-flash-preview", "turns": []})
        st["seq"] += 1
        new_id = f"019c0000-0000-7000-8000-{st['seq']:012d}"
        forked = {
            "id": new_id,
            "forkedFromId": src_id,
            "cwd": params.get("cwd") or src.get("cwd", ""),
            "model": src.get("model", "google/gemini-3-flash-preview"),
            "createdAt": 1700000100 + st["seq"],
            "updatedAt": 1700000100 + st["seq"],
            "turns": list(src.get("turns", [])),
        }
        write_rollout(forked)
        st["threads"][new_id] = forked
        save_state(st)
        send({"id": rid, "result": {"thread": forked, "model": forked["model"]}})
    elif method == "thread/revert":
        tid = params.get("threadId")
        before_turn = params.get("beforeTurnId", "")
        th = st["threads"].get(tid, {"id": tid, "turns": []})
        kept = []
        for t_obj in th.get("turns", []):
            if t_obj.get("id") == before_turn:
                break
            kept.append(t_obj)
        th["turns"] = kept
        write_rollout(th)
        st["threads"][tid] = th
        save_state(st)
        send({"id": rid, "result": {"thread": th}})
    elif method in ("thread/name/set", "thread/archive", "thread/unarchive", "thread/unsubscribe", "thread/settings/update"):
        tid = params.get("threadId")
        if tid and tid in st["threads"]:
            if method == "thread/name/set":
                st["threads"][tid]["name"] = params.get("name", "")
            elif method == "thread/unsubscribe":
                rp = st["threads"][tid].get("path", "")
                if rp and os.path.exists(rp):
                    os.remove(rp)
                del st["threads"][tid]
            save_state(st)
        send({"id": rid, "result": {}})
    elif method == "turn/start":
        tid = params.get("threadId")
        if os.environ.get("FAKE_CODEX_REAL_UNPERSISTED") == "1" and tid not in globals().get("_in_mem_threads", set()) and not find_rollout_file(tid):
            send({"id": rid, "error": {"code": -32600, "message": f"thread not found: {tid}"}})
            continue
        th = st["threads"].setdefault(tid, {"id": tid, "cwd": params.get("cwd", ""), "turns": []})
        turn_idx = len(th["turns"]) + 1
        turn_id = f"turn_{turn_idx}"
        prompt_text = ""
        for inp in params.get("input") or []:
            if inp.get("type") == "text":
                prompt_text += inp.get("text", "")
        cwd = params.get("cwd") or th.get("cwd") or ""
        # Support deterministic file mutation & memory retention checks across hibernate/resume/handoff
        if "# Handoff context from previous thread" in prompt_text and "HANDOFF_CODE:" in prompt_text:
            _, after_hc = prompt_text.split("HANDOFF_CODE:", 1)
            th["memory"] = after_hc.split()[0].strip()
        reply_text = f"Ack turn {turn_idx}"
        if "Quick check?" in prompt_text:
            reply_text = "Aside answer from codex"
        elif "REMEMBER:" in prompt_text:
            _, tok = prompt_text.split("REMEMBER:", 1)
            tok = tok.strip().split()[0]
            th["memory"] = tok
            reply_text = f"OK_REMEMBERED_{tok}"
        elif "RECALL" in prompt_text:
            reply_text = th.get("memory") or "NO_MEMORY"
        elif "WRITE:" in prompt_text and cwd:
            _, spec = prompt_text.split("WRITE:", 1)
            rel, content = spec.split(":", 1)
            target = pathlib.Path(cwd) / rel.strip()
            target.write_text(content, encoding="utf-8")
            reply_text = f"Updated {rel.strip()}"

        turn_rec = {
            "id": turn_id,
            "prompt": prompt_text,
            "status": "completed",
            "items": [
                {"id": f"u_{turn_idx}", "type": "userMessage", "content": [{"type": "text", "text": prompt_text}]},
                {"id": f"a_{turn_idx}", "type": "agentMessage", "text": reply_text},
            ],
        }
        th["turns"].append(turn_rec)
        write_rollout(th)
        st["threads"][tid] = th
        save_state(st)

        send({"id": rid, "result": {"turn": {"id": turn_id, "status": "inProgress"}}})
        send({"method": "turn/started", "params": {"threadId": tid, "turn": {"id": turn_id, "status": "inProgress"}}})
        send({"method": "item/started", "params": {"threadId": tid, "turnId": turn_id, "item": {"id": f"a_{turn_idx}", "type": "agentMessage", "text": ""}}})
        send({"method": "item/agentMessage/delta", "params": {"threadId": tid, "turnId": turn_id, "itemId": f"a_{turn_idx}", "delta": reply_text}})
        send({"method": "item/completed", "params": {"threadId": tid, "turnId": turn_id, "item": {"id": f"a_{turn_idx}", "type": "agentMessage", "text": reply_text}}})
        send({"method": "thread/tokenUsage/updated", "params": {"threadId": tid, "turnId": turn_id, "tokenUsage": {"total": {"inputTokens": 20 * turn_idx, "cachedInputTokens": 0, "outputTokens": 10 * turn_idx, "reasoningOutputTokens": 0, "totalTokens": 30 * turn_idx}}}})
        send({"method": "turn/completed", "params": {"threadId": tid, "turn": {"id": turn_id, "status": "completed"}}})
    else:
        send({"id": rid, "result": {}})
`
	if err := os.WriteFile(binPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	return binPath, logPath
}

func TestCreateParamsAndFlagsUnmarshalJSON(t *testing.T) {
	rawNested := `{
		"agent": "codex",
		"cwd": "/workspace/demo",
		"prompt": {"text": "fix tests"},
		"flags": {
			"model": "google/gemini-3-flash-preview",
			"effort": "xhigh",
			"mode": "acceptEdits",
			"approvalPolicy": "on-request",
			"sandbox": "workspace-write",
			"provider": "openrouter",
			"addDirs": ["/workspace/shared"],
			"extraArgs": ["--profile", "ci"]
		}
	}`
	var p CreateParams
	if err := json.Unmarshal([]byte(rawNested), &p); err != nil {
		t.Fatalf("unmarshal nested CreateParams: %v", err)
	}
	if p.Agent != "codex" || p.Cwd != "/workspace/demo" || (p.Prompt == nil || p.Prompt.Text != "fix tests") {
		t.Fatalf("top-level CreateParams fields lost: %+v", p)
	}
	if p.Model != "google/gemini-3-flash-preview" || p.Effort != "xhigh" || p.Mode != "acceptEdits" {
		t.Fatalf("nested flags model/effort/mode lost: %+v", p.Flags)
	}
	if p.ApprovalPolicy != "on-request" || p.SandboxMode != "workspace-write" || !p.Sandbox || p.Provider != "openrouter" {
		t.Fatalf("nested flags policy/sandbox/provider mismatch: %+v", p.Flags)
	}
	if len(p.AddDirs) != 1 || p.AddDirs[0] != "/workspace/shared" || len(p.ExtraArgs) != 2 {
		t.Fatalf("nested slices mismatch: %+v", p.Flags)
	}

	var boolSandbox CreateParams
	if err := json.Unmarshal([]byte(`{"agent":"agy","cwd":"/repo","sandbox":true}`), &boolSandbox); err != nil {
		t.Fatalf("unmarshal bool sandbox: %v", err)
	}
	if !boolSandbox.Sandbox || boolSandbox.SandboxMode != "" {
		t.Fatalf("expected bool sandbox=true, got %+v", boolSandbox.Flags)
	}
}

func TestBug1SleepingAndLiveCustomModelSlugs(t *testing.T) {
	root := nonTmpTestDir(t, "bug1")
	ctx := context.Background()

	// 1. Sleeping session (conn == nil) with arbitrary unlisted OpenRouter & -high slugs.
	m := &Manager{
		cfg:         Config{StateDir: filepath.Join(root, "state"), HomeDir: root, IdleHibernate: time.Minute},
		hub:         NewHub(),
		sessions:    map[string]*Session{},
		stopCh:      make(chan struct{}),
		liveWatches: map[string]*liveWatch{},
	}
	defer m.Shutdown()
	_ = os.MkdirAll(filepath.Join(m.cfg.StateDir, "sessions"), 0o700)
	s := newSession(m, SessionMeta{ID: "sleep-codex", Agent: "codex", WantModel: "gpt-5.3-codex", WantThought: "medium"})
	s.meta.Models = &acp.Models{
		CurrentModelID:  "gpt-5.3-codex",
		AvailableModels: []acp.Model{{ModelID: "gpt-5.3-codex", Name: "GPT-5.3 Codex"}},
	}
	s.meta.ConfigOptions = []acp.ConfigOption{{
		ID:           "thought_level",
		Name:         "Reasoning Effort",
		Category:     "thought_level",
		CurrentValue: "medium",
		Options: []acp.ConfigOptionValue{
			{Value: "low", Name: "Low"},
			{Value: "medium", Name: "Medium"},
			{Value: "high", Name: "High"},
			{Value: "xhigh", Name: "Extra High"},
		},
	}}
	m.sessions[s.ID()] = s

	if err := s.SetModel(ctx, "google/gemini-99-experimental"); err != nil {
		t.Fatalf("sleeping SetModel custom slug: %v", err)
	}
	sum := s.Summary()
	if sum.Model != "google/gemini-99-experimental" {
		t.Fatalf("expected Summary().Model updated immediately, got %q", sum.Model)
	}

	// Ensure a model slug ending in -high is NEVER split into base + thought_level when no "model" ConfigOption exists!
	if err := s.SetModel(ctx, "openai/o3-mini-high"); err != nil {
		t.Fatalf("sleeping SetModel o3-mini-high: %v", err)
	}
	if got := s.Summary().Model; got != "openai/o3-mini-high" {
		t.Fatalf("expected o3-mini-high preserved verbatim, got %q", got)
	}
	if got := s.Summary().Thought; got != "medium" {
		t.Fatalf("expected effort unchanged (medium) after o3-mini-high SetModel, got %q", got)
	}

	// Verify both "thought" and "effort" aliases update thought_level without clobbering WantModel.
	if err := s.SetConfig(ctx, "thought", "xhigh"); err != nil {
		t.Fatalf("SetConfig(thought, xhigh): %v", err)
	}
	if s.Summary().Thought != "xhigh" || s.Summary().Model != "openai/o3-mini-high" {
		t.Fatalf("unexpected state after SetConfig(thought): model=%q effort=%q", s.Summary().Model, s.Summary().Thought)
	}
	if err := s.SetConfig(ctx, "effort", "high"); err != nil {
		t.Fatalf("SetConfig(effort, high): %v", err)
	}
	if s.Summary().Thought != "high" {
		t.Fatalf("expected effort=high, got %q", s.Summary().Thought)
	}

	// 2. Live session (conn != nil) switching to an arbitrary unlisted custom slug.
	binPath, _ := writeCoreFakeCodex(t, root)
	liveMgr := NewManager(Config{
		AgyPath:          "nonexistent-agy",
		ClaudePath:       "nonexistent-claude",
		CodexPath:        binPath,
		OpenRouterAPIKey: "sk-or-v1-unit-test-fake-key",
		StateDir:         filepath.Join(root, "live-state"),
		ConfigDir:        filepath.Join(root, "live-cfg"),
		WorktreeDir:      filepath.Join(root, "live-wt"),
		HomeDir:          root,
	}, NewHub())
	defer liveMgr.Shutdown()

	workDir := filepath.Join(root, "proj")
	_ = os.MkdirAll(workDir, 0o755)
	liveSess, err := liveMgr.Create(ctx, CreateParams{
		Agent: "codex",
		Cwd:   workDir,
		Flags: Flags{Model: "google/gemini-3-flash-preview", Mode: "full-access"},
	})
	if err != nil {
		t.Fatalf("Create live codex session: %v", err)
	}
	if err := liveMgr.Wake(ctx, liveSess.ID()); err != nil {
		t.Fatalf("Wake live codex session: %v", err)
	}
	if err := liveSess.SetModel(ctx, "openrouter/deepseek/r9-zero"); err != nil {
		t.Fatalf("live SetModel custom slug: %v", err)
	}
	if got := liveSess.Summary().Model; got != "openrouter/deepseek/r9-zero" {
		t.Fatalf("expected live Summary().Model=openrouter/deepseek/r9-zero, got %q", got)
	}
	if err := liveSess.SetConfig(ctx, "thought", "xhigh"); err != nil {
		t.Fatalf("live SetConfig(thought, xhigh): %v", err)
	}
	if got := liveSess.Summary().Thought; got != "xhigh" {
		t.Fatalf("expected live ThoughtLevel=xhigh, got %q", got)
	}
}

func TestBug2CodexLifecycleForkRewindAsideWithoutAgy(t *testing.T) {
	root := nonTmpTestDir(t, "bug2")
	binPath, logPath := writeCoreFakeCodex(t, root)
	t.Setenv("FAKE_CODEX_CORE_LOG", logPath)

	// Verify CodexStateDir allocates outside /tmp even when StateDir points at a /tmp path,
	// and cleans up on Shutdown.
	tmpStyledMgr := NewManager(Config{
		AgyPath:    "nonexistent-agy",
		ClaudePath: "nonexistent-claude",
		CodexPath:  binPath,
		StateDir:   filepath.Join(root, "state-check"),
		ConfigDir:  filepath.Join(root, "cfg-check"),
		HomeDir:    root,
	}, NewHub())
	// Force check of /tmp avoidance on a lightweight struct before shutting down:
	tmpProbe := &Manager{cfg: Config{StateDir: "/tmp/should-not-be-used-by-codex", HomeDir: root}}
	allocDir := tmpProbe.CodexStateDir()
	if strings.HasPrefix(allocDir, "/tmp") {
		t.Fatalf("CodexStateDir placed directory under /tmp: %s", allocDir)
	}
	_ = os.RemoveAll(allocDir)
	tmpStyledMgr.Shutdown()

	// Real git repository for checkpoint + Rewind verification.
	repoDir := filepath.Join(root, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, string(out))
		}
	}
	runGit("init")
	runGit("config", "user.email", "test@example.com")
	runGit("config", "user.name", "Test User")
	helloPath := filepath.Join(repoDir, "hello.txt")
	if err := os.WriteFile(helloPath, []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "hello.txt")
	runGit("commit", "-m", "initial")

	mgr := NewManager(Config{
		AgyPath:          "nonexistent-agy-must-never-be-called",
		ClaudePath:       "nonexistent-claude-must-never-be-called",
		CodexPath:        binPath,
		OpenRouterAPIKey: "sk-or-v1-unit-test-fake-key",
		StateDir:         filepath.Join(root, "state"),
		ConfigDir:        filepath.Join(root, "config"),
		WorktreeDir:      filepath.Join(root, "worktrees"),
		HomeDir:          root,
	}, NewHub())
	defer mgr.Shutdown()

	// Verify isAgyID distinguishes real Agy .db files from Codex UUIDv7 thread IDs.
	codexSampleID := "019c1111-2222-7333-8444-555555555555"
	if mgr.isAgyID(codexSampleID) {
		t.Fatalf("expected isAgyID(%s) == false when conversations/<id>.db does not exist", codexSampleID)
	}
	agyConvDir := filepath.Join(root, ".gemini", "antigravity", "conversations")
	_ = os.MkdirAll(agyConvDir, 0o755)
	agySampleID := "aaaa1111-2222-4333-8444-555555555555"
	_ = os.WriteFile(filepath.Join(agyConvDir, agySampleID+".db"), []byte("sqlite"), 0o644)
	if !mgr.isAgyID(agySampleID) {
		t.Fatalf("expected isAgyID(%s) == true when conversations/<id>.db exists", agySampleID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sess, err := mgr.Create(ctx, CreateParams{
		Agent: "codex",
		Cwd:   repoDir,
		Flags: Flags{Model: "google/gemini-3-flash-preview", Mode: "full-access"},
	})
	if err != nil {
		t.Fatalf("Create codex session: %v", err)
	}
	sid := sess.ID()

	waitIdleTurns := func(wantTurns int) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			sum := sess.Summary()
			if sum.State == "idle" && sum.Turns >= wantTurns {
				return
			}
			time.Sleep(30 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for idle turns>=%d (got %+v)", wantTurns, sess.Summary())
	}

	// Turn 1: mutate hello.txt -> v2
	if _, err := sess.Prompt(PromptInput{Text: "WRITE:hello.txt:v2\n"}); err != nil {
		t.Fatalf("SendPrompt turn 1: %v", err)
	}
	waitIdleTurns(1)
	if got, _ := os.ReadFile(helloPath); string(got) != "v2\n" {
		t.Fatalf("expected hello.txt=v2 after turn 1, got %q", string(got))
	}

	// Turn 2: mutate hello.txt -> v3
	if _, err := sess.Prompt(PromptInput{Text: "WRITE:hello.txt:v3\n"}); err != nil {
		t.Fatalf("SendPrompt turn 2: %v", err)
	}
	waitIdleTurns(2)
	if got, _ := os.ReadFile(helloPath); string(got) != "v3\n" {
		t.Fatalf("expected hello.txt=v3 after turn 2, got %q", string(got))
	}

	// LiveState must never invoke agy LS.
	live, err := mgr.LiveState(ctx, sid)
	if err != nil || live == nil || !live.Running {
		t.Fatalf("LiveState expected Running=true without agy LS, got %+v err=%v", live, err)
	}

	// AskAside (/btw) runs asynchronously via ephemeral read-only Codex turn without incrementing session Turns.
	aside, err := mgr.AskAside(ctx, sid, "Quick check?", "")
	if err != nil || aside == nil {
		t.Fatalf("AskAside start failed: %+v err=%v", aside, err)
	}
	asideDeadline := time.Now().Add(10 * time.Second)
	var doneAside *Aside
	for time.Now().Before(asideDeadline) {
		list, _ := mgr.Asides(sid)
		for i := range list {
			if list[i].ID == aside.ID && list[i].State != "pending" {
				cp := list[i]
				doneAside = &cp
				break
			}
		}
		if doneAside != nil {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if doneAside == nil || doneAside.State != "done" || !strings.Contains(doneAside.Answer, "Aside answer from codex") {
		t.Fatalf("AskAside unexpected completion: %+v", doneAside)
	}
	if sess.Summary().Turns != 2 {
		t.Fatalf("AskAside should not mutate main session turns, got %d", sess.Summary().Turns)
	}

	// Fork must invoke native thread/fork and produce a codex session.
	forked, _, err := mgr.Fork(ctx, sid, "")
	if err != nil {
		t.Fatalf("Fork codex session: %v", err)
	}
	if forked.Summary().Agent != "codex" || forked.Summary().ForkedFrom != sid {
		t.Fatalf("unexpected forked summary: %+v", forked.Summary())
	}

	// Locate second user message item ID to test RevertPreview + Rewind (thread/revert + workspace file restore).
	sess.mu.Lock()
	trItems := sess.tr.TransportItems()
	sess.mu.Unlock()
	var secondUserItemID string
	userCount := 0
	for _, it := range trItems {
		if it.Kind == "user" {
			userCount++
			if userCount == 2 {
				secondUserItemID = it.ID
				break
			}
		}
	}
	if secondUserItemID == "" {
		t.Fatalf("could not find second user item in transcript (%d items)", len(trItems))
	}

	previewRaw, removed, err := mgr.RevertPreview(ctx, sid, secondUserItemID)
	if err != nil || removed < 1 || len(previewRaw) == 0 {
		t.Fatalf("RevertPreview failed: removed=%d raw=%s err=%v", removed, string(previewRaw), err)
	}

	prefill, err := mgr.Rewind(ctx, sid, secondUserItemID, false)
	if err != nil {
		t.Fatalf("Rewind failed: %v", err)
	}
	if !strings.Contains(prefill, "WRITE:hello.txt:v3") {
		t.Fatalf("expected prefill from rewound user turn, got %q", prefill)
	}
	if got, _ := os.ReadFile(helloPath); string(got) != "v2\n" {
		t.Fatalf("expected Rewind to restore hello.txt back to v2, got %q", string(got))
	}

	// Verify thread/revert RPC was sent to codex app-server.
	rpcLogBytes, _ := os.ReadFile(logPath)
	if !strings.Contains(string(rpcLogBytes), `"method": "thread/revert"`) && !strings.Contains(string(rpcLogBytes), `"method":"thread/revert"`) {
		t.Fatalf("expected thread/revert RPC in fake codex log")
	}

	// History & OpenWith on uncached rollout thread:
	mgr.Forget(forked.ID())
	_ = os.Remove(filepath.Join(mgr.cfg.StateDir, "sessions", forked.ID()+".json"))
	hist := mgr.History(ctx, true)
	foundForkInHist := false
	for _, h := range hist {
		if h.ID == forked.ID() && h.Agent == "codex" {
			foundForkInHist = true
			break
		}
	}
	if !foundForkInHist {
		t.Fatalf("expected uncached rollout thread %s in History with Agent=codex", forked.ID())
	}
	reopened, err := mgr.OpenWith(ctx, forked.ID(), nil)
	if err != nil || reopened.Summary().Agent != "codex" {
		t.Fatalf("OpenWith uncached codex rollout failed: %+v err=%v", reopened, err)
	}

	// Rename, SetFlag(archived), Delete without agy:
	if err := mgr.Rename(ctx, sid, "Codex Renamed Title"); err != nil {
		t.Fatalf("Rename codex: %v", err)
	}
	if sess.Summary().Title != "Codex Renamed Title" {
		t.Fatalf("expected title updated, got %q", sess.Summary().Title)
	}
	if err := mgr.SetFlag(ctx, sid, "archived", true); err != nil {
		t.Fatalf("SetFlag archived: %v", err)
	}
	if err := mgr.Delete(ctx, sid); err != nil {
		t.Fatalf("Delete codex: %v", err)
	}
}

func TestCodexTurn1RewindAndSyntheticForkThreadSync(t *testing.T) {
	root := nonTmpTestDir(t, "rewind-sync")
	binPath, logPath := writeCoreFakeCodex(t, root)
	t.Setenv("FAKE_CODEX_CORE_LOG", logPath)
	t.Setenv("HOME", root)

	workDir := filepath.Join(root, "work")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(Config{
		AgyPath:          "nonexistent-agy",
		ClaudePath:       "nonexistent-claude",
		CodexPath:        binPath,
		OpenRouterAPIKey: "sk-or-v1-unit-test-fake-key",
		StateDir:         filepath.Join(root, "state"),
		ConfigDir:        filepath.Join(root, "config"),
		WorktreeDir:      filepath.Join(root, "worktrees"),
		HomeDir:          root,
	}, NewHub())
	defer mgr.Shutdown()

	ctx := context.Background()
	waitIdleTurns := func(s *Session, wantTurns int) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			sum := s.Summary()
			if sum.State == "idle" && sum.Turns >= wantTurns {
				return
			}
			time.Sleep(25 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for idle turns>=%d (got %+v)", wantTurns, s.Summary())
	}

	// 1. Create session & run Turn 1 ("REMEMBER:OLD-111").
	sess, err := mgr.Create(ctx, CreateParams{
		Agent: "codex",
		Cwd:   workDir,
		Flags: Flags{Model: "google/gemini-3-flash-preview", Mode: "full-access"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := sess.Prompt(PromptInput{Text: "REMEMBER:OLD-111"}); err != nil {
		t.Fatalf("Prompt 1: %v", err)
	}
	waitIdleTurns(sess, 1)
	origID := sess.ID()
	if origID == "" {
		t.Fatal("expected non-empty session ID after Turn 1")
	}

	// 2. Rewind Turn 1 (isFirst == true): deletes backing thread while keeping origID stable.
	sess.mu.Lock()
	var firstUserID string
	for _, it := range sess.tr.Items {
		if it.Kind == "user" {
			firstUserID = it.ID
			break
		}
	}
	sess.mu.Unlock()
	if firstUserID == "" {
		t.Fatal("missing first user item")
	}
	if _, err := mgr.Rewind(ctx, origID, firstUserID, false); err != nil {
		t.Fatalf("Rewind Turn 1: %v", err)
	}
	if gotTID := sess.codexThreadID(); gotTID != "" {
		t.Fatalf("expected empty backing codexThreadID after Turn-1 rewind, got %q", gotTID)
	}
	// Terminal launch before re-materialization must NOT pass dead resume origID.
	_, argsBefore, _ := mgr.CodexTermLaunch(origID, false, nil)
	for _, a := range argsBefore {
		if a == origID {
			t.Fatalf("CodexTermLaunch passed deleted origID %q before re-materialization: %v", origID, argsBefore)
		}
	}

	// 3. Send new Turn 1 ("REMEMBER:KIWI-888") -> allocates a brand-new backing Codex thread UUID != origID.
	if _, err := sess.Prompt(PromptInput{Text: "REMEMBER:KIWI-888"}); err != nil {
		t.Fatalf("Prompt after rewind: %v", err)
	}
	waitIdleTurns(sess, 1)
	if sess.ID() != origID {
		t.Fatalf("expected session ID to stay %q, got %q", origID, sess.ID())
	}
	newTID := sess.codexThreadID()
	if newTID == "" || newTID == origID {
		t.Fatalf("expected new backing CodexThreadID != %q, got %q", origID, newTID)
	}

	// CodexTermLaunch must now resume newTID (not stale origID).
	_, argsAfter, _ := mgr.CodexTermLaunch(origID, false, nil)
	joinedArgs := strings.Join(argsAfter, " ")
	if !strings.Contains(joinedArgs, "resume "+newTID) {
		t.Fatalf("expected CodexTermLaunch to resume %s, got %v", newTID, argsAfter)
	}

	// 4. Hibernate (session.stop) + send Turn 2 ("RECALL") -> MUST retain KIWI-888 memory!
	sess.Hibernate()
	if _, err := sess.Prompt(PromptInput{Text: "RECALL"}); err != nil {
		t.Fatalf("Prompt RECALL after hibernate: %v", err)
	}
	waitIdleTurns(sess, 2)
	if !strings.Contains(sess.Summary().LastText, "KIWI-888") {
		t.Fatalf("expected resumed Codex thread to recall KIWI-888, got LastText=%q", sess.Summary().LastText)
	}

	// 5. Manager.List and codexHistory must deduplicate newTID, and OpenWith(newTID) must return sess.
	list := mgr.List(ctx, true)
	origCount := 0
	for _, row := range list {
		if row.ID == newTID {
			t.Fatalf("backing CodexThreadID %s leaked as duplicate row in Manager.List: %+v", newTID, list)
		}
		if row.ID == origID {
			origCount++
		}
	}
	if origCount != 1 {
		t.Fatalf("expected exactly 1 row for %s in Manager.List, got %d", origID, origCount)
	}
	aliasedOpen, err := mgr.OpenWith(ctx, newTID, nil)
	if err != nil || aliasedOpen.ID() != origID {
		t.Fatalf("expected OpenWith(%s) to return session %s, got %+v err=%v", newTID, origID, aliasedOpen, err)
	}

	// 6. Sleeping Rename, SetFlag, and Delete must target newTID and leave zero orphan rollout files.
	sess.Hibernate()
	if err := mgr.Rename(ctx, origID, "Post-Rewind Synced"); err != nil {
		t.Fatalf("sleeping Rename after Turn-1 rewind: %v", err)
	}
	if err := mgr.SetFlag(ctx, origID, "archived", true); err != nil {
		t.Fatalf("sleeping SetFlag after Turn-1 rewind: %v", err)
	}
	if err := mgr.Delete(ctx, origID); err != nil {
		t.Fatalf("Delete after Turn-1 rewind: %v", err)
	}
	for _, h := range mgr.History(ctx, true) {
		if h.ID == origID || h.ID == newTID {
			t.Fatalf("orphan rollout %s remained in History after Delete", h.ID)
		}
	}

	// 7. Synthetic 019c0000-... fork of an unmaterialized session preserves context via SeedHandoff across hibernate/resume.
	unmat := newSession(mgr, SessionMeta{
		ID:            "unmat-parent-001",
		CodexThreadID: codexFreshThread,
		Agent:         "codex",
		Cwd:           workDir,
		Title:         "Unmaterialized Source",
		WantModel:     "google/gemini-3-flash-preview",
		Turns:         2,
	})
	unmat.tr.LoadItems([]*Item{
		{ID: "u1", Kind: "user", Text: "HANDOFF_CODE:ORBIT-777"},
		{ID: "a1", Kind: "agent", Text: "Noted ORBIT-777."},
		{ID: "u2", Kind: "user", Text: "Continue step 2"},
		{ID: "a2", Kind: "agent", Text: "Done step 2."},
	}, 4)
	mgr.mu.Lock()
	mgr.sessions[unmat.ID()] = unmat
	mgr.mu.Unlock()

	forked, _, err := mgr.Fork(ctx, unmat.ID(), "")
	if err != nil {
		t.Fatalf("Fork unmaterialized codex session: %v", err)
	}
	if !strings.HasPrefix(forked.ID(), "019c0000-") {
		t.Fatalf("expected synthetic 019c0000- fork ID, got %q", forked.ID())
	}
	if forked.codexThreadID() != "" {
		t.Fatalf("expected unmaterialized fork codexThreadID empty before first turn, got %q", forked.codexThreadID())
	}

	if _, err := forked.Prompt(PromptInput{Text: "RECALL"}); err != nil {
		t.Fatalf("Prompt on synthetic fork: %v", err)
	}
	waitIdleTurns(forked, 3)
	if !strings.Contains(forked.Summary().LastText, "ORBIT-777") {
		t.Fatalf("expected synthetic fork to receive SeedHandoff ORBIT-777, got %q", forked.Summary().LastText)
	}
	forkBackingTID := forked.codexThreadID()
	if forkBackingTID == "" || forkBackingTID == forked.ID() {
		t.Fatalf("expected synthetic fork to record real backing CodexThreadID, got %q", forkBackingTID)
	}
	forked.Hibernate()
	if _, err := forked.Prompt(PromptInput{Text: "RECALL"}); err != nil {
		t.Fatalf("Prompt 2 on synthetic fork after hibernate: %v", err)
	}
	waitIdleTurns(forked, 4)
	if !strings.Contains(forked.Summary().LastText, "ORBIT-777") {
		t.Fatalf("expected synthetic fork to retain ORBIT-777 after hibernate/resume, got %q", forked.Summary().LastText)
	}
}

func TestCodexFreshManagerControlBeforeCatalogWrite(t *testing.T) {
	root := nonTmpTestDir(t, "fresh-ctrl")
	binPath, logPath := writeCoreFakeCodex(t, root)
	t.Setenv("FAKE_CODEX_CORE_LOG", logPath)
	t.Setenv("HOME", root)

	workDir := filepath.Join(root, "proj")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Pre-populate a historical ~/.codex/sessions rollout BEFORE starting Manager.
	preID := "019cf00d-cafe-7000-8000-000000000042"
	rolloutDir := filepath.Join(root, ".codex", "sessions", "2026", "11", "24")
	if err := os.MkdirAll(rolloutDir, 0o700); err != nil {
		t.Fatal(err)
	}
	rolloutFile := filepath.Join(rolloutDir, "rollout-2026-11-24T10-00-00-"+preID+".jsonl")
	rolloutContent := strings.Join([]string{
		`{"timestamp":"2026-11-24T10:00:00Z","type":"session_meta","payload":{"id":"` + preID + `","timestamp":"2026-11-24T10:00:00Z","cwd":"` + workDir + `","model_provider":"openrouter"}}`,
		`{"timestamp":"2026-11-24T10:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"Historical prompt before catalog"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(rolloutFile, []byte(rolloutContent), 0o600); err != nil {
		t.Fatal(err)
	}

	// Brand-new Manager with OpenRouter configured; DO NOT call ProviderModels or Create first!
	mgr := NewManager(Config{
		AgyPath:          "nonexistent-agy",
		ClaudePath:       "nonexistent-claude",
		CodexPath:        binPath,
		OpenRouterAPIKey: "sk-or-v1-fresh-ctrl-key",
		StateDir:         filepath.Join(root, "state"),
		ConfigDir:        filepath.Join(root, "config"),
		HomeDir:          root,
	}, NewHub())
	defer mgr.Shutdown()

	ctx := context.Background()
	opened, err := mgr.Open(ctx, preID)
	if err != nil {
		t.Fatalf("Open historical Codex rollout on fresh Manager failed: %v", err)
	}
	opened.mu.Lock()
	itemCount := len(opened.tr.Items)
	opened.mu.Unlock()
	if itemCount < 2 {
		t.Fatalf("expected ReadThreadRPC to hydrate transcript items on fresh Manager (got %d items — catalog missing?)", itemCount)
	}

	if err := mgr.Rename(ctx, preID, "Renamed Immediately"); err != nil {
		t.Fatalf("Rename on fresh Manager: %v", err)
	}
	if err := mgr.SetFlag(ctx, preID, "archived", true); err != nil {
		t.Fatalf("SetFlag on fresh Manager: %v", err)
	}
	if err := mgr.Delete(ctx, preID); err != nil {
		t.Fatalf("Delete on fresh Manager: %v", err)
	}
	if _, err := os.Stat(rolloutFile); !os.IsNotExist(err) {
		t.Fatalf("expected historical rollout deleted from ~/.codex/sessions, stat err=%v", err)
	}
}

func TestManagerShutdownCancelsSaveAndFlushTimers(t *testing.T) {
	root := nonTmpTestDir(t, "shutdown-timers")
	binPath, _ := writeCoreFakeCodex(t, root)
	stateDir := filepath.Join(root, "state")

	var logBuf bytes.Buffer
	origOut := log.Writer()
	log.SetOutput(&logBuf)
	defer log.SetOutput(origOut)

	mgr := NewManager(Config{
		AgyPath:          "nonexistent-agy",
		ClaudePath:       "nonexistent-claude",
		CodexPath:        binPath,
		OpenRouterAPIKey: "sk-or-v1-shutdown-test",
		StateDir:         stateDir,
		ConfigDir:        filepath.Join(root, "config"),
		HomeDir:          root,
		IdleHibernate:    time.Minute,
	}, NewHub())

	s, err := mgr.Create(context.Background(), CreateParams{
		Agent: "codex",
		Cwd:   root,
		Flags: Flags{Model: "google/gemini-3-flash-preview"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Arm both flushTimer (60ms) and saveTimer (1500ms) right before Shutdown.
	s.mu.Lock()
	s.queueOpsLocked(s.tr.AddNotice("info", "pending before shutdown"))
	hasFlush := s.flushTimer != nil
	hasSave := s.saveTimer != nil
	s.mu.Unlock()
	if !hasFlush || !hasSave {
		t.Fatalf("expected flushTimer & saveTimer armed before Shutdown (flush=%v save=%v)", hasFlush, hasSave)
	}

	mgr.Shutdown()
	// Verify second Shutdown() call is 100% idempotent (no panic on closed stopCh).
	mgr.Shutdown()

	if err := os.RemoveAll(stateDir); err != nil {
		t.Fatal(err)
	}
	// Wait past the 1500ms saveTimer deadline.
	time.Sleep(1750 * time.Millisecond)

	logged := logBuf.String()
	if strings.Contains(logged, "save "+s.ID()) || strings.Contains(logged, "no such file or directory") {
		t.Fatalf("unexpected post-Shutdown saveTimer error logged:\n%s", logged)
	}
}

func TestCodexRevertPreviewBoundariesMatchRewind(t *testing.T) {
	root := nonTmpTestDir(t, "codex-revert-preview")
	binPath, logPath := writeCoreFakeCodex(t, root)
	t.Setenv("FAKE_CODEX_CORE_LOG", logPath)
	t.Setenv("HOME", root)

	workDir := filepath.Join(root, "work")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = workDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, string(out))
		}
	}
	runGit("init")
	runGit("config", "user.email", "test@example.com")
	runGit("config", "user.name", "Test User")
	fooPath := filepath.Join(workDir, "foo.txt")
	if err := os.WriteFile(fooPath, []byte("v0-initial\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "foo.txt")
	runGit("commit", "-m", "initial")

	mgr := NewManager(Config{
		AgyPath:          "nonexistent-agy",
		ClaudePath:       "nonexistent-claude",
		CodexPath:        binPath,
		OpenRouterAPIKey: "sk-or-v1-revert-preview-test",
		StateDir:         filepath.Join(root, "state"),
		ConfigDir:        filepath.Join(root, "config"),
		WorktreeDir:      filepath.Join(root, "worktrees"),
		HomeDir:          root,
	}, NewHub())
	defer mgr.Shutdown()

	ctx := context.Background()
	waitIdleTurns := func(s *Session, wantTurns int) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			sum := s.Summary()
			if sum.State == "idle" && sum.Turns >= wantTurns {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for idle turns>=%d (got %+v)", wantTurns, s.Summary())
	}

	sess, err := mgr.Create(ctx, CreateParams{
		Agent: "codex",
		Cwd:   workDir,
		Flags: Flags{Model: "google/gemini-3-flash-preview", Mode: "full-access"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Turn 1 modifies foo.txt -> creates git checkpoint with TurnIndex == 1.
	if _, err := sess.Prompt(PromptInput{Text: "WRITE:foo.txt:v1-modified-in-turn1\n"}); err != nil {
		t.Fatalf("Prompt Turn 1: %v", err)
	}
	waitIdleTurns(sess, 1)
	if got, err := os.ReadFile(fooPath); err != nil || string(got) != "v1-modified-in-turn1\n" {
		t.Fatalf("expected Turn 1 foo.txt mutation, got %q err=%v", string(got), err)
	}

	parsePreview := func(raw json.RawMessage) (bool, []string, []any) {
		t.Helper()
		var out struct {
			CanRewind    bool     `json:"canRewind"`
			FilesChanged []string `json:"filesChanged"`
			Files        []any    `json:"files"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("unmarshal preview %s: %v", string(raw), err)
		}
		return out.CanRewind, out.FilesChanged, out.Files
	}

	sess.mu.Lock()
	var turn1UserID, turn1AgentID string
	for _, it := range sess.tr.Items {
		if it.Kind == "user" && turn1UserID == "" {
			turn1UserID = it.ID
		}
		if it.Kind == "agent" {
			turn1AgentID = it.ID
		}
	}
	sess.mu.Unlock()

	// Final-turn assistant message & empty itemID MUST report 0 filesChanged (matching no-op Rewind).
	for _, idArg := range []string{turn1AgentID, ""} {
		raw, stepIdx, err := mgr.RevertPreview(ctx, sess.ID(), idArg)
		if err != nil {
			t.Fatalf("RevertPreview(%q): %v", idArg, err)
		}
		canRew, changed, files := parsePreview(raw)
		if !canRew || len(changed) != 0 || len(files) != 0 || stepIdx != 1 {
			t.Fatalf("RevertPreview(%q) expected empty filesChanged & stepIdx=1, got stepIdx=%d raw=%s", idArg, stepIdx, string(raw))
		}
	}
	if _, err := mgr.Rewind(ctx, sess.ID(), turn1AgentID, false); err != nil {
		t.Fatalf("no-op Rewind(turn1AgentID): %v", err)
	}
	if got, _ := os.ReadFile(fooPath); string(got) != "v1-modified-in-turn1\n" {
		t.Fatalf("no-op Rewind unexpectedly altered foo.txt: %q", string(got))
	}

	// Turn 2 makes NO file modifications -> no checkpoint with TurnIndex >= 2.
	if _, err := sess.Prompt(PromptInput{Text: "REMEMBER:TURN2-READONLY"}); err != nil {
		t.Fatalf("Prompt Turn 2: %v", err)
	}
	waitIdleTurns(sess, 2)

	sess.mu.Lock()
	var turn2UserID, turn2AgentID string
	userSeen := 0
	for _, it := range sess.tr.Items {
		if it.Kind == "user" {
			userSeen++
			if userSeen == 2 {
				turn2UserID = it.ID
			}
		}
		if it.Kind == "agent" {
			turn2AgentID = it.ID
		}
	}
	sess.mu.Unlock()

	// Previewing Turn 2 assistant item (final) AND Turn 2 user item (checkpointless) MUST report 0 filesChanged.
	rawFinal2, stepFinal2, err := mgr.RevertPreview(ctx, sess.ID(), turn2AgentID)
	if err != nil {
		t.Fatalf("RevertPreview(turn2AgentID): %v", err)
	}
	canRew, changed, files := parsePreview(rawFinal2)
	if !canRew || len(changed) != 0 || len(files) != 0 || stepFinal2 != 2 {
		t.Fatalf("RevertPreview(turn2AgentID) expected empty & stepIdx=2, got stepIdx=%d raw=%s", stepFinal2, string(rawFinal2))
	}

	rawTurn2User, stepTurn2, err := mgr.RevertPreview(ctx, sess.ID(), turn2UserID)
	if err != nil {
		t.Fatalf("RevertPreview(turn2UserID): %v", err)
	}
	canRew, changed, files = parsePreview(rawTurn2User)
	if !canRew || len(changed) != 0 || len(files) != 0 || stepTurn2 != 1 {
		t.Fatalf("RevertPreview(turn2UserID) expected empty & stepIdx=1, got stepIdx=%d raw=%s", stepTurn2, string(rawTurn2User))
	}

	// Previewing Turn 1 user item MUST still report foo.txt.
	rawTurn1User, stepTurn1, err := mgr.RevertPreview(ctx, sess.ID(), turn1UserID)
	if err != nil {
		t.Fatalf("RevertPreview(turn1UserID): %v", err)
	}
	canRew, changed, files = parsePreview(rawTurn1User)
	if !canRew || len(changed) != 1 || changed[0] != "foo.txt" || len(files) != 1 || stepTurn1 != 0 {
		t.Fatalf("RevertPreview(turn1UserID) expected [foo.txt] & stepIdx=0, got stepIdx=%d raw=%s", stepTurn1, string(rawTurn1User))
	}

	// Rewinding Turn 2 leaves foo.txt at v1, and rewinding Turn 1 restores v0-initial.
	if _, err := mgr.Rewind(ctx, sess.ID(), turn2UserID, false); err != nil {
		t.Fatalf("Rewind(turn2UserID): %v", err)
	}
	if got, _ := os.ReadFile(fooPath); string(got) != "v1-modified-in-turn1\n" {
		t.Fatalf("Rewind(turn2UserID) unexpectedly modified foo.txt: %q", string(got))
	}
	if _, err := mgr.Rewind(ctx, sess.ID(), turn1UserID, false); err != nil {
		t.Fatalf("Rewind(turn1UserID): %v", err)
	}
	if got, _ := os.ReadFile(fooPath); string(got) != "v0-initial\n" {
		t.Fatalf("Rewind(turn1UserID) failed to restore foo.txt: %q", string(got))
	}
}

func TestCodexPreTurn1SetModelUnlistedSlugStartsFreshThread(t *testing.T) {
	root := nonTmpTestDir(t, "pre-turn1-setmodel")
	codexBin, logPath := writeCoreFakeCodex(t, root)
	t.Setenv("FAKE_CODEX_CORE_LOG", logPath)
	t.Setenv("FAKE_CODEX_REAL_UNPERSISTED", "1")
	t.Setenv("HOME", root)

	stateDir := filepath.Join(root, "state")
	cfgDir := filepath.Join(root, "config")
	workDir := filepath.Join(root, "work")
	for _, d := range []string{stateDir, cfgDir, workDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	mgr := NewManager(Config{
		AgyPath:          "nonexistent-agy",
		ClaudePath:       "nonexistent-claude",
		CodexPath:        codexBin,
		OpenRouterAPIKey: "sk-or-v1-preturn1-unit-test",
		StateDir:         stateDir,
		ConfigDir:        cfgDir,
		WorktreeDir:      filepath.Join(root, "worktrees"),
		HomeDir:          root,
	}, NewHub())
	defer mgr.Shutdown()

	ctx := context.Background()
	s, err := mgr.Create(ctx, CreateParams{
		Agent: "codex",
		Cwd:   workDir,
		Flags: Flags{Model: "google/gemini-3-flash-preview"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	origTID := s.codexThreadID()
	if origTID == "" {
		t.Fatalf("expected non-empty initial CodexThreadID")
	}

	unlisted := "vendor-future/brand-new-model-v9"
	if err := s.SetModel(ctx, unlisted); err != nil {
		t.Fatalf("SetModel(%q) before Turn 1: %v", unlisted, err)
	}
	newTID := s.codexThreadID()
	if newTID == "" || newTID == origTID {
		t.Fatalf("expected CodexThreadID to update from unpersisted %q to fresh thread, got %q", origTID, newTID)
	}
	if gotModel := s.Summary().Model; gotModel != unlisted {
		t.Fatalf("Summary().Model = %q, want %q", gotModel, unlisted)
	}

	if _, err := s.Prompt(PromptInput{Text: "Run Turn 1 on unlisted slug"}); err != nil {
		t.Fatalf("Prompt Turn 1: %v", err)
	}
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		sum := s.Summary()
		if sum.State == "idle" && (sum.Turns >= 1 || sum.Error != "") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	sum := s.Summary()
	if sum.Turns != 1 || sum.Error != "" {
		t.Fatalf("Turn 1 after pre-Turn-1 SetModel failed: turns=%d lastError=%q", sum.Turns, sum.Error)
	}
}
