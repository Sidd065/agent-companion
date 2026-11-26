package e2e_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func startLiveBYOKDaemon(t *testing.T, allowHostAgents bool, skipPerms bool) (*TestDaemon, string, string) {
	t.Helper()
	realCodex := requireRealCodexBinary(t)
	realKey := readLiveOpenRouterKeyForRedactionCheckOnly(t)

	td := StartIsolatedDaemon(t, DaemonOpts{
		UseRealCodex:           true,
		DisableMockOpenRouter:  true,
		AllowRealHostAgents:    allowHostAgents,
		DefaultSkipPermissions: &skipPerms,
		ExtraArgs: []string{
			"-openrouter-api-key-file", liveOpenRouterKeyPath,
			"-codex-path", realCodex,
		},
	})
	return td, realCodex, realKey
}

func TestTier4_01_LiveBYOK_Gemini3Flash_MultiTurnCodingAndShellVerify_OverNoiseWS(t *testing.T) {
	td, _, realKey := startLiveBYOKDaemon(t, false, true)

	statusMap, statusBytes, err := td.Unix.StatusRaw(context.Background())
	if err != nil {
		t.Fatalf("Unix.StatusRaw: %v", err)
	}
	if cfg, _ := statusMap["openRouterConfigured"].(bool); !cfg {
		t.Fatalf("expected openRouterConfigured=true on live BYOK daemon")
	}
	assertZeroSecretSubstring(t, realKey, "/local/status", statusBytes)

	cli := td.PairAndConnect(t, "T4-01-LiveClient", "linux")

	catCtx, catCancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer catCancel()
	var cat struct {
		OpenRouterConfigured bool            `json:"openRouterConfigured"`
		Models               []ModelInfoWire `json:"models"`
	}
	if err := cli.Call(catCtx, "providers.models", map[string]any{
		"provider": "codex",
		"refresh":  true,
	}, &cat); err != nil {
		t.Fatalf("live providers.models failed: %v", err)
	}
	if !cat.OpenRouterConfigured || len(cat.Models) < 50 {
		t.Fatalf("expected live OpenRouter catalog >= 50 models, got %d (openRouterConfigured=%v)", len(cat.Models), cat.OpenRouterConfigured)
	}
	var modelIDs []string
	for _, m := range cat.Models {
		modelIDs = append(modelIDs, m.ModelID)
	}
	for _, reqSlug := range []string{"google/gemini-3-flash-preview", "google/gemini-2.5-flash"} {
		if !slices.Contains(modelIDs, reqSlug) {
			t.Fatalf("live OpenRouter catalog missing %q", reqSlug)
		}
	}

	parentDir := newTempPlainDir(t)
	initTrue := true
	var proj struct {
		ID   string `json:"id"`
		Path string `json:"path"`
	}
	if err := cli.Call(context.Background(), "projects.init", map[string]any{
		"parentDir":     parentDir,
		"name":          "live-byok-math",
		"initialCommit": &initTrue,
	}, &proj); err != nil {
		t.Fatalf("projects.init: %v", err)
	}

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(context.Background(), "session.create", map[string]any{
		"agent":           "codex",
		"cwd":             proj.Path,
		"projectId":       proj.ID,
		"model":           "google/gemini-3-flash-preview",
		"mode":            "full-access",
		"thought":         "low",
		"skipPermissions": true,
	}, &created); err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sid := created.Session.ID
	_ = cli.Call(context.Background(), "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)

	prompt := "Create math_util.py with a function is_prime(n: int) -> bool that returns True only for primes >= 2, create test_math_util.py using Python's unittest module testing is_prime(2), is_prime(17), is_prime(1), and is_prime(15), and run 'python3 -m unittest -v' via a shell command to verify all tests pass."
	if err := cli.Call(context.Background(), "session.prompt", map[string]any{
		"id":   sid,
		"text": prompt,
	}, nil); err != nil {
		t.Fatalf("session.prompt: %v", err)
	}

	sum, items, err := cli.WaitForTurnIdle(sid, 1, 120*time.Second)
	if err != nil {
		t.Fatalf("live Turn 1 timed out: %v", err)
	}
	if sum.Error != "" {
		t.Fatalf("live Turn 1 ended with error: %s", sum.Error)
	}
	if sum.Usage.TotalTokens <= 0 {
		t.Fatalf("expected Usage.TotalTokens > 0 on live BYOK turn, got %+v", sum.Usage)
	}

	var sawTool, sawAgent bool
	for _, it := range items {
		if it.Kind == "tool" {
			sawTool = true
		}
		if it.Kind == "agent" && strings.TrimSpace(it.Text) != "" {
			sawAgent = true
		}
	}
	if !sawTool || !sawAgent {
		t.Fatalf("expected both tool and agent items in live transcript (sawTool=%v sawAgent=%v)", sawTool, sawAgent)
	}

	if _, err := os.Stat(filepath.Join(proj.Path, "math_util.py")); err != nil {
		t.Fatalf("math_util.py not created by live Codex turn: %v", err)
	}
	if _, err := os.Stat(filepath.Join(proj.Path, "test_math_util.py")); err != nil {
		t.Fatalf("test_math_util.py not created by live Codex turn: %v", err)
	}

	testCmd := exec.Command("python3", "-m", "unittest", "-v")
	testCmd.Dir = proj.Path
	if out, err := testCmd.CombinedOutput(); err != nil {
		t.Fatalf("generated unit tests failed: %v\n%s", err, string(out))
	}

	var tdRes struct {
		TotalAdd int `json:"totalAdd"`
	}
	if err := cli.Call(context.Background(), "session.turnDiff", map[string]any{"id": sid, "scope": "latest"}, &tdRes); err != nil {
		t.Fatalf("session.turnDiff: %v", err)
	}
	if tdRes.TotalAdd <= 0 {
		t.Fatalf("expected TotalAdd > 0 in session.turnDiff(latest)")
	}

	_ = cli.Call(context.Background(), "git.stage", map[string]any{
		"path":  proj.Path,
		"files": []string{"math_util.py", "test_math_util.py"},
	}, nil)
	var scoped struct {
		TotalAdd int `json:"totalAdd"`
	}
	if err := cli.Call(context.Background(), "git.scopedDiff", map[string]any{"path": proj.Path, "scope": "uncommitted"}, &scoped); err != nil {
		t.Fatalf("git.scopedDiff: %v", err)
	}
	if scoped.TotalAdd <= 0 {
		t.Fatalf("expected TotalAdd > 0 in git.scopedDiff(uncommitted)")
	}

	rawInfo, _ := cli.CallRaw(context.Background(), "daemon.info", map[string]any{})
	rawProv, _ := cli.CallRaw(context.Background(), "providers.list", map[string]any{})
	assertZeroSecretSubstring(t, realKey, "daemon.info", rawInfo)
	assertZeroSecretSubstring(t, realKey, "providers.list", rawProv)
	assertZeroSecretSubstring(t, realKey, "agentc.log", td.Logs())
}

func TestTier4_02_LiveBYOK_Gemini25Flash_ApplyPatchBridge_AndMidSessionModelSwitch(t *testing.T) {
	td, _, realKey := startLiveBYOKDaemon(t, false, true)
	cli := td.PairAndConnect(t, "T4-02-LiveSwitch", "linux")

	repo := newTempGitRepo(t, map[string]string{
		"greeter.py": "def greet(name: str) -> str:\n    return 'hi ' + name\n",
	})

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(context.Background(), "session.create", map[string]any{
		"agent":           "codex",
		"cwd":             repo,
		"model":           "google/gemini-2.5-flash",
		"mode":            "full-access",
		"thought":         "low",
		"skipPermissions": true,
	}, &created); err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sid := created.Session.ID
	_ = cli.Call(context.Background(), "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)

	if err := cli.Call(context.Background(), "session.prompt", map[string]any{
		"id":   sid,
		"text": "Modify greeter.py so that greet(name) returns f'Hello, {name}!' instead of 'hi ' + name. Do not create extra files.",
	}, nil); err != nil {
		t.Fatalf("Turn 1 prompt: %v", err)
	}
	sum1, _, err := cli.WaitForTurnIdle(sid, 1, 120*time.Second)
	if err != nil {
		t.Fatalf("Turn 1 (gemini-2.5-flash) timed out: %v", err)
	}
	if sum1.Error != "" {
		t.Fatalf("Turn 1 error: %s", sum1.Error)
	}

	verifyCmd := exec.Command("python3", "-c", "import greeter; assert greeter.greet('World') == 'Hello, World!', greeter.greet('World')")
	verifyCmd.Dir = repo
	if out, err := verifyCmd.CombinedOutput(); err != nil {
		t.Fatalf("greeter.py not patched properly in Turn 1: %v\n%s", err, string(out))
	}

	var tdRes struct {
		TotalAdd int `json:"totalAdd"`
		TotalDel int `json:"totalDel"`
	}
	if err := cli.Call(context.Background(), "session.turnDiff", map[string]any{"id": sid, "scope": "latest"}, &tdRes); err != nil {
		t.Fatalf("session.turnDiff: %v", err)
	}
	if tdRes.TotalAdd <= 0 || tdRes.TotalDel <= 0 {
		t.Fatalf("expected +add/-del diff on greeter.py, got %+v", tdRes)
	}

	// Switch model mid-session to openrouter/ prefixed Gemini 3 Flash
	var switched SessionSummaryWire
	if err := cli.Call(context.Background(), "session.setModel", map[string]any{
		"id":    sid,
		"model": "openrouter/google/gemini-3-flash-preview",
	}, &switched); err != nil {
		t.Fatalf("session.setModel mid-session failed: %v", err)
	}
	if !strings.Contains(switched.Model, "gemini-3-flash-preview") {
		t.Fatalf("unexpected Summary.Model after switch: %q", switched.Model)
	}

	if err := cli.Call(context.Background(), "session.prompt", map[string]any{
		"id":   sid,
		"text": "Run python3 -c \"import greeter; print(greeter.greet('AgentC'))\" and state the exact Python filename you edited in Turn 1.",
	}, nil); err != nil {
		t.Fatalf("Turn 2 prompt: %v", err)
	}
	sum2, items2, err := cli.WaitForTurnIdle(sid, 2, 120*time.Second)
	if err != nil {
		t.Fatalf("Turn 2 timed out: %v", err)
	}
	if sum2.Error != "" {
		t.Fatalf("Turn 2 error: %s", sum2.Error)
	}
	if !strings.Contains(collectAgentText(items2), "greeter.py") {
		t.Fatalf("expected Turn 2 assistant reply to recall 'greeter.py' from Turn 1 context; got %q", collectAgentText(items2))
	}
	assertZeroSecretSubstring(t, realKey, "agentc.log", td.Logs())
}

func TestTier4_03_LiveBYOK_CustomModelPersistence_Fork_DaemonRestartResume_AndRevert(t *testing.T) {
	td, realCodex, realKey := startLiveBYOKDaemon(t, false, true)
	ctx := context.Background()

	_, link, err := td.Unix.Pair(ctx, td.HTTPBase, 300)
	if err != nil {
		t.Fatalf("Unix.Pair: %v", err)
	}
	devKey, _ := GenerateDeviceKeypair()
	if _, err := PairDeviceOverNoiseWS(ctx, link, devKey, "T4-03-Client", "linux", nil); err != nil {
		t.Fatalf("PairDeviceOverNoiseWS: %v", err)
	}
	cli1, err := ConnectNoiseRPCClient(ctx, td.WSBase, link.DaemonPub, devKey, "T4-03-Client", "linux", nil)
	if err != nil {
		t.Fatalf("ConnectNoiseRPCClient #1: %v", err)
	}

	if err := cli1.Call(ctx, "providers.upsert", map[string]any{
		"id":      "codex",
		"name":    "Codex",
		"driver":  "codex",
		"enabled": true,
		"customModels": []map[string]string{
			{"slug": "google/gemini-2.5-flash", "name": "Pinned Gemini 2.5 Flash"},
		},
	}, nil); err != nil {
		t.Fatalf("providers.upsert: %v", err)
	}

	repo := newTempGitRepo(t, nil)
	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli1.Call(ctx, "session.create", map[string]any{
		"agent":           "codex",
		"cwd":             repo,
		"model":           "google/gemini-2.5-flash",
		"mode":            "full-access",
		"thought":         "low",
		"skipPermissions": true,
	}, &created); err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sid := created.Session.ID
	_ = cli1.Call(ctx, "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	_ = cli1.Call(ctx, "session.prompt", map[string]any{
		"id":   sid,
		"text": "Create changelog.md with '# v0.5.0 Changelog' and one bullet '- Initial Codex integration'.",
	}, nil)
	sum1, _, err := cli1.WaitForTurnIdle(sid, 1, 120*time.Second)
	if err != nil || sum1.Error != "" {
		t.Fatalf("Turn 1 failed: err=%v sessionErr=%q", err, sum1.Error)
	}
	if _, err := os.Stat(filepath.Join(repo, "changelog.md")); err != nil {
		t.Fatalf("changelog.md missing after Turn 1: %v", err)
	}

	var forked struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli1.Call(ctx, "session.fork", map[string]any{"id": sid}, &forked); err != nil {
		t.Fatalf("live session.fork failed (must not call agy LS): %v", err)
	}
	if forked.Session.ForkedFrom != sid {
		t.Fatalf("expected ForkedFrom=%q, got %q", sid, forked.Session.ForkedFrom)
	}
	_ = cli1.Close()

	// Restart daemon on same state/config dirs and resume live Codex thread!
	skipTrue := true
	td.Restart(t, DaemonOpts{
		UseRealCodex:           true,
		DisableMockOpenRouter:  true,
		AllowRealHostAgents:    false,
		DefaultSkipPermissions: &skipTrue,
		ExtraArgs: []string{
			"-openrouter-api-key-file", liveOpenRouterKeyPath,
			"-codex-path", realCodex,
		},
	})

	cli2, err := ConnectNoiseRPCClient(ctx, td.WSBase, link.DaemonPub, devKey, "T4-03-Client", "linux", nil)
	if err != nil {
		t.Fatalf("ConnectNoiseRPCClient #2 after restart: %v", err)
	}
	defer cli2.Close()

	var cat struct {
		CustomModels []struct {
			Slug string `json:"slug"`
		} `json:"customModels"`
	}
	if err := cli2.Call(ctx, "providers.models", map[string]any{"provider": "codex"}, &cat); err != nil {
		t.Fatalf("providers.models after restart: %v", err)
	}
	var foundPinned bool
	for _, cm := range cat.CustomModels {
		if cm.Slug == "google/gemini-2.5-flash" {
			foundPinned = true
		}
	}
	if !foundPinned {
		t.Fatalf("pinned customModel lost across daemon restart")
	}

	_ = cli2.Call(ctx, "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	_ = cli2.Call(ctx, "session.prompt", map[string]any{
		"id":   sid,
		"text": "Append a second bullet '- Live daemon restart resume verified' to changelog.md.",
	}, nil)
	sum2, _, err := cli2.WaitForTurnIdle(sid, 2, 120*time.Second)
	if err != nil || sum2.Error != "" {
		t.Fatalf("Turn 2 after restart failed: err=%v sessionErr=%q", err, sum2.Error)
	}

	if err := cli2.Call(ctx, "session.checkpointRevert", map[string]any{
		"id":        sid,
		"turnIndex": 1,
		"toPre":     true,
	}, nil); err != nil {
		t.Fatalf("session.checkpointRevert: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "changelog.md")); err == nil {
		t.Fatalf("expected changelog.md removed after reverting to pre-Turn-1 checkpoint")
	}
	assertZeroSecretSubstring(t, realKey, "agentc.log", td.Logs())
}

func TestTier4_04_LiveBYOK_SupervisedMultiClientApproval_AndAsideWorkflow(t *testing.T) {
	td, _, realKey := startLiveBYOKDaemon(t, false, false)
	deskCli := td.PairAndConnect(t, "T4-04-Desktop", "linux")
	andCli := td.PairAndConnect(t, "T4-04-Android", "android")
	repo := newTempGitRepo(t, nil)
	ctx := context.Background()

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := deskCli.Call(ctx, "session.create", map[string]any{
		"agent":           "codex",
		"cwd":             repo,
		"model":           "google/gemini-3-flash-preview",
		"mode":            "default",
		"thought":         "low",
		"skipPermissions": false,
		"flags": map[string]any{
			"approvalPolicy":  "untrusted",
			"sandbox":         "workspace-write",
			"skipPermissions": false,
		},
	}, &created); err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sid := created.Session.ID
	_ = deskCli.Call(ctx, "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	_ = andCli.Call(ctx, "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)

	if err := deskCli.Call(ctx, "session.prompt", map[string]any{
		"id":   sid,
		"text": "Run the exact shell command: python3 -c \"open('approved_live.txt','w').write('ok')\" to create approved_live.txt in the current directory.",
	}, nil); err != nil {
		t.Fatalf("session.prompt: %v", err)
	}

	// Android approves any pending command/patch permission requests while Desktop observes sync!
	approvedReqs := map[string]bool{}
	deadline := time.Now().Add(120 * time.Second)
	var finished bool
	for time.Now().Before(deadline) {
		sum, _, _, items := andCli.SessionSnapshotState(sid)
		for _, it := range items {
			if it.Kind == "perm" && it.State == "pending" && it.Perm != nil && !approvedReqs[it.Perm.ReqID] {
				approvedReqs[it.Perm.ReqID] = true
				optID := PickPermOptionID(it.Perm, "allow_once")
				if err := andCli.Call(ctx, "session.permission", map[string]any{
					"id":       sid,
					"reqId":    it.Perm.ReqID,
					"optionId": optID,
				}, nil); err != nil {
					t.Fatalf("Android session.permission(%s): %v", it.Perm.ReqID, err)
				}
			}
		}
		if sum.State == "idle" && sum.Turns >= 1 {
			if sum.Error != "" {
				t.Fatalf("live supervised turn ended with error: %s", sum.Error)
			}
			finished = true
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if !finished {
		t.Fatalf("timed out waiting for supervised live turn to finish (approved=%d)", len(approvedReqs))
	}
	if len(approvedReqs) == 0 {
		t.Fatalf("expected supervised untrusted/workspace-write mode to trigger at least 1 interactive approval request")
	}

	_, deskItems, err := deskCli.WaitForTurnIdle(sid, 1, 10*time.Second)
	if err != nil {
		t.Fatalf("Desktop WaitForTurnIdle: %v", err)
	}
	var sawResolvedOnDesk bool
	for _, it := range deskItems {
		if it.Kind == "perm" && it.State == "resolved" {
			sawResolvedOnDesk = true
		}
	}
	if !sawResolvedOnDesk {
		t.Fatalf("Desktop did not observe resolved Perm item synced from Android")
	}
	if _, err := os.Stat(filepath.Join(repo, "approved_live.txt")); err != nil {
		t.Fatalf("approved_live.txt missing after Android approved command: %v", err)
	}

	_ = callAndWaitAsideDone(t, deskCli, sid, "What file did we just create? Reply in under 10 words.", 60*time.Second)
	assertZeroSecretSubstring(t, realKey, "agentc.log", td.Logs())
}

func TestTier4_05_MultiHarnessCoexistence_LiveCodex_FakeACP_CodexTerminal_AndSafetyAudit(t *testing.T) {
	assertProtectedCloudflaredUntouched(t)
	t.Cleanup(func() {
		assertProtectedCloudflaredUntouched(t)
	})
	td, _, realKey := startLiveBYOKDaemon(t, true, true)
	cli := td.PairAndConnect(t, "T4-05-Coexist", "linux")
	ctx := context.Background()

	if err := cli.Call(ctx, "providers.upsert", map[string]any{
		"id":      "coexist-acp",
		"name":    "Coexist FakeACP",
		"driver":  "acp",
		"enabled": true,
		"command": fakeacpBin,
		"env":     map[string]string{"FAKEACP_STATE_DIR": td.FakeACPDir},
	}, nil); err != nil {
		t.Fatalf("providers.upsert(coexist-acp): %v", err)
	}

	codexRepo := newTempGitRepo(t, nil)
	acpRepo := newTempGitRepo(t, nil)

	var cxCreated, acpCreated struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(ctx, "session.create", map[string]any{
		"agent":           "codex",
		"cwd":             codexRepo,
		"model":           "google/gemini-2.5-flash",
		"mode":            "full-access",
		"thought":         "low",
		"skipPermissions": true,
	}, &cxCreated); err != nil {
		t.Fatalf("create codex session: %v", err)
	}
	if err := cli.Call(ctx, "session.create", map[string]any{
		"agent":           "coexist-acp",
		"cwd":             acpRepo,
		"skipPermissions": true,
	}, &acpCreated); err != nil {
		t.Fatalf("create coexist-acp session: %v", err)
	}

	cxSID := cxCreated.Session.ID
	acpSID := acpCreated.Session.ID
	_ = cli.Call(ctx, "session.subscribe", map[string]any{"id": cxSID, "since": 0}, nil)
	_ = cli.Call(ctx, "session.subscribe", map[string]any{"id": acpSID, "since": 0}, nil)

	var wg sync.WaitGroup
	var cxErr, acpErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		_ = cli.Call(ctx, "session.prompt", map[string]any{
			"id":   cxSID,
			"text": "Create live_coexist.txt containing 'codex_live_ok'.",
		}, nil)
		sum, _, err := cli.WaitForTurnIdle(cxSID, 1, 120*time.Second)
		if err != nil {
			cxErr = err
		} else if sum.Error != "" {
			cxErr = &RPCError{Message: sum.Error}
		}
	}()
	go func() {
		defer wg.Done()
		_ = cli.Call(ctx, "session.prompt", map[string]any{
			"id":   acpSID,
			"text": "[edit:acp_coexist.txt:acp_ok]",
		}, nil)
		_, _, acpErr = cli.WaitForTurnIdle(acpSID, 1, 15*time.Second)
	}()
	wg.Wait()

	if cxErr != nil {
		t.Fatalf("concurrent live codex turn failed: %v", cxErr)
	}
	if acpErr != nil {
		t.Fatalf("concurrent fakeacp turn failed: %v", acpErr)
	}
	if _, err := os.Stat(filepath.Join(codexRepo, "live_coexist.txt")); err != nil {
		t.Fatalf("live_coexist.txt missing in codexRepo: %v", err)
	}
	if _, err := os.Stat(filepath.Join(acpRepo, "acp_coexist.txt")); err != nil {
		t.Fatalf("acp_coexist.txt missing in acpRepo: %v", err)
	}

	// Verify built-in health discovery across codex, claude, agy
	var healthList []struct {
		ID string `json:"id"`
	}
	if err := cli.Call(ctx, "providers.health", map[string]any{}, &healthList); err != nil {
		t.Fatalf("providers.health: %v", err)
	}
	var seenHealth []string
	for _, h := range healthList {
		seenHealth = append(seenHealth, h.ID)
	}
	for _, wantID := range []string{"codex", "claude", "agy"} {
		if !slices.Contains(seenHealth, wantID) {
			t.Fatalf("providers.health missing %q in %v", wantID, seenHealth)
		}
	}

	// Exercise terminal kind="codex" lifecycle
	var termRes struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
	}
	if err := cli.Call(ctx, "term.create", map[string]any{
		"kind": "codex",
		"cwd":  codexRepo,
		"cols": 120,
		"rows": 40,
	}, &termRes); err != nil {
		t.Fatalf("term.create(kind=codex): %v", err)
	}
	if termRes.Kind != "codex" {
		t.Fatalf("expected term Kind='codex', got %q", termRes.Kind)
	}
	var termList []struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
	}
	if err := cli.Call(ctx, "term.list", map[string]any{}, &termList); err != nil {
		t.Fatalf("term.list: %v", err)
	}
	var inList bool
	for _, ti := range termList {
		if ti.ID == termRes.ID && ti.Kind == "codex" {
			inList = true
		}
	}
	if !inList {
		t.Fatalf("term.list missing created codex terminal %s", termRes.ID)
	}
	var capRes struct {
		Text string `json:"text"`
	}
	_ = cli.Call(ctx, "term.capture", map[string]any{"id": termRes.ID, "lines": 40}, &capRes)
	if err := cli.Call(ctx, "term.kill", map[string]any{"id": termRes.ID}, nil); err != nil {
		t.Fatalf("term.kill: %v", err)
	}

	// Final operational & git hygiene audit
	if err := exec.Command("git", "-C", repoRootOnDevBox, "check-ignore", "-q", ".open_router_api_key").Run(); err != nil {
		t.Fatalf(".open_router_api_key not git-ignored: %v", err)
	}
	assertProtectedCloudflaredUntouched(t)
	svcCmd := exec.Command("systemctl", "--user", "is-active", "agentc.service")
	svcCmd.Env = append(os.Environ(), "XDG_RUNTIME_DIR=/run/user/"+uidString())
	if out, err := svcCmd.CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != "active" {
		t.Fatalf("agentc.service not active: %q (%v)", strings.TrimSpace(string(out)), err)
	}
	assertZeroSecretSubstring(t, realKey, "agentc.log", td.Logs())
}
