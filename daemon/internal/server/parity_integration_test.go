package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/flynn/noise"

	"agentc/internal/core"
	"agentc/internal/secure"
	"agentc/internal/term"
)

type testRPCClient struct {
	conn   *secure.Conn
	mu     sync.Mutex
	nextID int64
	pend   map[int64]chan map[string]any
	events chan map[string]any
}

func dialTestRPC(t *testing.T, baseURL string, daemonPub []byte, devKey noise.DHKey, mode, pairID string, psk []byte) *testRPCClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, err := secure.Dial(ctx, secure.ClientConfig{
		URL: baseURL, DaemonKey: daemonPub, DeviceKey: devKey,
		Mode: mode, PairID: pairID, PSK: psk,
		Hello: secure.Hello{V: 1, Purpose: "rpc", Name: "TestDesktop", Platform: "linux", App: "0.4.0-test"},
	})
	if err != nil {
		t.Fatalf("secure.Dial(%s): %v", mode, err)
	}
	c := &testRPCClient{conn: conn, pend: map[int64]chan map[string]any{}, events: make(chan map[string]any, 2000)}
	go func() {
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var m map[string]any
			if json.Unmarshal(data, &m) != nil {
				continue
			}
			if idv, ok := m["id"]; ok && m["event"] == nil {
				id := int64(idv.(float64))
				c.mu.Lock()
				ch := c.pend[id]
				delete(c.pend, id)
				c.mu.Unlock()
				if ch != nil {
					ch <- m
				}
			} else {
				select {
				case c.events <- m:
				default:
				}
			}
		}
	}()
	return c
}

func (c *testRPCClient) call(t *testing.T, method string, params any) any {
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
	case <-time.After(30 * time.Second):
		t.Fatalf("rpc %s timed out", method)
		return nil
	}
}

func (c *testRPCClient) waitForEvent(t *testing.T, timeout time.Duration, pred func(ev string, data map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case m := <-c.events:
			ev, _ := m["event"].(string)
			data, _ := m["data"].(map[string]any)
			if pred(ev, data) {
				return data
			}
		case <-deadline:
			t.Fatalf("timed out waiting for event")
			return nil
		}
	}
}

func TestDaemonParityEndToEnd(t *testing.T) {
	tmp := t.TempDir()
	fakeBin := filepath.Join(tmp, "fakeacp")
	build := exec.Command("go", "build", "-o", fakeBin, "../../cmd/fakeacp")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fakeacp: %v\n%s", err, string(out))
	}
	t.Setenv("AGENTC_FAKE_ACP", fakeBin)
	t.Setenv("FAKEACP_STATE_DIR", filepath.Join(tmp, "fakeacp-state"))

	stateDir := filepath.Join(tmp, "state")
	cfgDir := filepath.Join(tmp, "config")
	wtDir := filepath.Join(tmp, "worktrees")
	workRoot := filepath.Join(tmp, "repos")
	_ = os.MkdirAll(workRoot, 0o755)

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
		AgyPath:     "nonexistent-agy-for-test",
		ClaudePath:  "nonexistent-claude-for-test",
		StateDir:    stateDir,
		ConfigDir:   cfgDir,
		WorktreeDir: wtDir,
		HomeDir:     tmp,
		DefaultSkip: false,
	}, hub)
	defer mgr.Shutdown()

	terms := &term.Manager{Sock: filepath.Join(tmp, "tmux.sock"), Conf: filepath.Join(tmp, "tmux.conf")}
	srv := &Server{
		Mgr: mgr, Terms: terms, Identity: id, Devices: devs,
		Tokens: secure.NewTokens(), Started: time.Now(),
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	srv.PublicURL = func() string { return ts.URL }

	// 1. Pair via local handler -> Noise-IKpsk1 -> reconnect via Noise-IK.
	pairReq := httptest.NewRequest("POST", "/local/pair", strings.NewReader(`{"ttlSeconds":120}`))
	pairRec := httptest.NewRecorder()
	srv.LocalHandler().ServeHTTP(pairRec, pairReq)
	if pairRec.Code != http.StatusOK {
		t.Fatalf("local/pair failed: %d %s", pairRec.Code, pairRec.Body.String())
	}
	var pairResp struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(pairRec.Body.Bytes(), &pairResp)
	parsed, err := url.Parse(pairResp.URL)
	if err != nil {
		t.Fatal(err)
	}
	pairID := strings.TrimPrefix(parsed.Path, "/p/")
	frag, _ := url.ParseQuery(parsed.Fragment)
	daemonPub, _ := base64.RawURLEncoding.DecodeString(frag.Get("k"))
	psk, _ := base64.RawURLEncoding.DecodeString(frag.Get("s"))
	devKey, _ := noise.DH25519.GenerateKeypair(rand.Reader)

	pairClient := dialTestRPC(t, ts.URL, daemonPub, devKey, secure.ModePair, pairID, psk)
	if pv, _ := pairClient.conn.Reply["protocolVersion"].(float64); int(pv) != ProtocolVersion {
		t.Fatalf("expected protocolVersion %d, got %v", ProtocolVersion, pairClient.conn.Reply["protocolVersion"])
	}
	_ = pairClient.conn.Close()

	// Reconnect using Noise-IK (registered device).
	rpc := dialTestRPC(t, ts.URL, daemonPub, devKey, secure.ModeConnect, "", nil)
	defer rpc.conn.Close()

	helloRes := rpc.call(t, "hello", map[string]any{"device": "Linux Desktop", "app": "0.4.0"}).(map[string]any)
	if int(helloRes["protocolVersion"].(float64)) != ProtocolVersion {
		t.Fatalf("hello protocolVersion mismatch: %v", helloRes["protocolVersion"])
	}

	// 2. Init a real git project + verify projects.list & scripts.list.
	proj := rpc.call(t, "projects.init", map[string]any{"parentDir": workRoot, "name": "myapp", "initialCommit": true}).(map[string]any)
	projID := proj["id"].(string)
	projPath := proj["path"].(string)
	_ = os.WriteFile(filepath.Join(projPath, "package.json"), []byte(`{"scripts":{"test":"echo ok"}}`), 0o644)
	scriptList := rpc.call(t, "scripts.list", map[string]any{"projectId": projID}).([]any)
	if len(scriptList) < 1 {
		t.Fatalf("expected discovered scripts, got %v", scriptList)
	}

	// 3. Create session in a fresh git worktree using fakeacp!
	created := rpc.call(t, "session.create", map[string]any{
		"agent":       "fakeacp",
		"cwd":         projPath,
		"projectId":   projID,
		"useWorktree": true,
		"branchName":  "feat/e2e-wt",
	}).(map[string]any)
	sess := created["session"].(map[string]any)
	sid := sess["id"].(string)
	wtPath := sess["worktreePath"].(string)
	if wtPath == "" || sess["branch"].(string) != "feat/e2e-wt" {
		t.Fatalf("worktree not attached to session: %+v", sess)
	}

	// Subscribe to session transcript stream.
	rpc.call(t, "session.subscribe", map[string]any{"id": sid, "since": 0})
	rpc.waitForEvent(t, 5*time.Second, func(ev string, d map[string]any) bool { return ev == "session.snapshot" })

	// 4. Send edit prompt -> check real file edit, per-turn checkpoint & turnDiff!
	rpc.call(t, "session.prompt", map[string]any{"id": sid, "text": "[edit:greeting.txt:hello world\\n]"})
	rpc.waitForEvent(t, 15*time.Second, func(ev string, d map[string]any) bool {
		return ev == "session.changed" && d["id"] == sid && d["state"] == "idle" && int(d["turns"].(float64)) >= 1
	})
	if b, err := os.ReadFile(filepath.Join(wtPath, "greeting.txt")); err != nil || string(b) != "hello world\n" {
		t.Fatalf("greeting.txt not written in worktree: %q err=%v", string(b), err)
	}
	tdiff := rpc.call(t, "session.turnDiff", map[string]any{"id": sid, "scope": "latest"}).(map[string]any)
	diffFiles := tdiff["files"].([]any)
	if len(diffFiles) != 1 {
		t.Fatalf("expected 1 file in latest turn diff, got %+v", tdiff)
	}

	// 5. Permission flow: trigger [perm], approve via session.permission.
	rpc.call(t, "session.prompt", map[string]any{"id": sid, "text": "[perm:echo hi]"})
	permData := rpc.waitForEvent(t, 15*time.Second, func(ev string, d map[string]any) bool {
		return ev == "notify" && d["kind"] == "permission" && d["sessionId"] == sid
	})
	reqID := permData["reqId"].(string)
	rpc.call(t, "session.permission", map[string]any{"id": sid, "reqId": reqID, "optionId": "allow"})
	rpc.waitForEvent(t, 15*time.Second, func(ev string, d map[string]any) bool {
		return ev == "session.changed" && d["id"] == sid && d["state"] == "idle" && int(d["turns"].(float64)) >= 2
	})

	// 6. Question flow: trigger [question], answer via session.permission.
	rpc.call(t, "session.prompt", map[string]any{"id": sid, "text": "[question]"})
	qData := rpc.waitForEvent(t, 15*time.Second, func(ev string, d map[string]any) bool {
		return ev == "notify" && d["kind"] == "question" && d["sessionId"] == sid
	})
	rpc.call(t, "session.permission", map[string]any{
		"id": sid, "reqId": qData["reqId"].(string), "optionId": "allow",
		"answers": map[string]any{"Which test mode should we use?": "Full"},
	})
	rpc.waitForEvent(t, 15*time.Second, func(ev string, d map[string]any) bool {
		return ev == "session.changed" && d["id"] == sid && d["state"] == "idle" && int(d["turns"].(float64)) >= 3
	})

	// 7. Revert turn 1 checkpoint -> greeting.txt removed cleanly!
	rpc.call(t, "session.checkpointRevert", map[string]any{"id": sid, "turnIndex": 1, "toPre": true})
	if _, err := os.Stat(filepath.Join(wtPath, "greeting.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected greeting.txt removed after checkpoint revert")
	}

	// 8. Inline !command execution.
	execRes := rpc.call(t, "session.exec", map[string]any{"id": sid, "command": "!echo inline_parity_ok"}).(map[string]any)
	if !strings.Contains(execRes["output"].(string), "inline_parity_ok") {
		t.Fatalf("unexpected inline exec result: %+v", execRes)
	}

	// 9. Side question (/btw) on generic ACP.
	aside := rpc.call(t, "session.aside", map[string]any{"id": sid, "question": "What happened?"}).(map[string]any)
	if aside["id"] == nil {
		t.Fatalf("aside failed: %+v", aside)
	}

	// 10. Portable provider handoff.
	hoff := rpc.call(t, "session.handoff", map[string]any{"id": sid, "targetAgent": "fakeacp"}).(map[string]any)
	if !strings.Contains(hoff["handoff"].(string), "Handoff context") {
		t.Fatalf("unexpected handoff: %+v", hoff)
	}

	// 11. Settle, draft, search, usage, schedules, health, providers.
	rpc.call(t, "session.settle", map[string]any{"id": sid, "settled": true})
	rpc.call(t, "session.draft.set", map[string]any{"id": sid, "draft": "next thought"})
	hits := rpc.call(t, "search.query", map[string]any{"query": "Permission approved"}).(any)
	if len(hits.([]any)) < 1 {
		t.Fatalf("expected search hit, got %+v", hits)
	}
	usg := rpc.call(t, "usage.summary", map[string]any{"days": 7}).(map[string]any)
	if int(usg["turns"].(float64)) < 3 {
		t.Fatalf("expected >= 3 usage turns, got %+v", usg)
	}
	hlth := rpc.call(t, "daemon.health", map[string]any{}).(map[string]any)
	if hlth["memTotalKiB"].(float64) <= 0 {
		t.Fatalf("expected valid host health: %+v", hlth)
	}
	sched := rpc.call(t, "schedules.upsert", map[string]any{
		"name": "Smoke cron", "enabled": true, "kind": "cron", "cron": "0 9 * * *",
		"cwd": projPath, "agent": "fakeacp", "prompt": "daily check",
	}).(map[string]any)
	if sched["id"] == nil {
		t.Fatalf("schedule upsert failed: %+v", sched)
	}
}
