package e2e_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestTier3_01_CustomModelVariant_CatalogRefresh_CreateAndBridgeAuth(t *testing.T) {
	secret := "sk-or-v1-tier3-01-preserved-key"
	suiteMockOR.ResetOverrides()
	suiteMockOR.ClearRequests()

	td := StartIsolatedDaemon(t, DaemonOpts{})
	cli := td.PairAndConnect(t, "T3-01", "linux")
	ctx := context.Background()

	if err := cli.Call(ctx, "providers.upsert", map[string]any{
		"id":      "codex",
		"name":    "Codex",
		"driver":  "codex",
		"enabled": true,
		"env":     map[string]string{"OPENROUTER_API_KEY": secret},
	}, nil); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	customSlug := "google/gemini-2.5-flash:thinking"
	if err := cli.Call(ctx, "providers.upsert", map[string]any{
		"id":      "codex",
		"name":    "Codex",
		"driver":  "codex",
		"enabled": true,
		"env":     map[string]string{"OPENROUTER_API_KEY": "***"},
		"customModels": []map[string]string{
			{"slug": customSlug, "name": "Gemini 2.5 Flash Thinking"},
		},
	}, nil); err != nil {
		t.Fatalf("second upsert with masked secret + customModels: %v", err)
	}

	var cat struct {
		OpenRouterConfigured bool `json:"openRouterConfigured"`
		CustomModels         []struct {
			Slug string `json:"slug"`
		} `json:"customModels"`
	}
	if err := cli.Call(ctx, "providers.models", map[string]any{"provider": "codex", "refresh": true}, &cat); err != nil {
		t.Fatalf("providers.models(refresh=true): %v", err)
	}
	if !cat.OpenRouterConfigured {
		t.Fatalf("expected openRouterConfigured=true")
	}
	var sawCustom bool
	for _, cm := range cat.CustomModels {
		if cm.Slug == customSlug {
			sawCustom = true
		}
	}
	if !sawCustom {
		t.Fatalf("missing %q in customModels: %+v", customSlug, cat.CustomModels)
	}

	repo := newTempGitRepo(t, nil)
	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(ctx, "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": customSlug,
	}, &created); err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sid := created.Session.ID
	_ = cli.Call(ctx, "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	_ = cli.Call(ctx, "session.prompt", map[string]any{
		"id":   sid,
		"text": "T3-01 check [bridge-probe] [inspect]",
	}, nil)
	_, items, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second)
	if err != nil {
		t.Fatalf("WaitForTurnIdle: %v", err)
	}

	insp, ok := extractInspectJSON(items)
	if !ok || insp["catalogHasModel"] != true {
		t.Fatalf("expected custom variant slug in model_catalog_json, got %+v", insp)
	}
	var sawBridge bool
	for _, r := range suiteMockOR.ResponsesRequests() {
		if strings.Contains(r.PromptText, "T3-01 check") {
			if r.AuthHeader != "Bearer "+secret || r.MaxOutputTokens != 8192 {
				t.Fatalf("unexpected bridge auth/cap: auth=%q maxOut=%d", r.AuthHeader, r.MaxOutputTokens)
			}
			sawBridge = true
		}
	}
	if !sawBridge {
		t.Fatalf("missing bridge request for T3-01")
	}
}

func TestTier3_02_SupervisedMode_PatchApproval_TurnDiff_CheckpointRevert(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T3-02", "linux")
	repo := newTempGitRepo(t, nil)
	ctx := context.Background()

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(ctx, "session.create", map[string]any{
		"agent":           "codex",
		"cwd":             repo,
		"model":           "google/gemini-3-flash-preview",
		"mode":            "default",
		"skipPermissions": false,
	}, &created); err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sid := created.Session.ID
	_ = cli.Call(ctx, "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	_ = cli.Call(ctx, "session.prompt", map[string]any{
		"id":   sid,
		"text": `Supervised patch [approve-patch:app.ts:export const v = 2;\n]`,
	}, nil)

	permItem, err := cli.WaitForPendingPerm(sid, 10*time.Second)
	if err != nil {
		t.Fatalf("WaitForPendingPerm: %v", err)
	}
	if err := cli.Call(ctx, "session.permission", map[string]any{
		"id":       sid,
		"reqId":    permItem.Perm.ReqID,
		"optionId": PickPermOptionID(permItem.Perm, "allow_once"),
	}, nil); err != nil {
		t.Fatalf("session.permission: %v", err)
	}
	if _, _, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second); err != nil {
		t.Fatalf("WaitForTurnIdle: %v", err)
	}

	var diffRes struct {
		TotalAdd int `json:"totalAdd"`
		Files    []struct {
			Path string `json:"path"`
		} `json:"files"`
	}
	if err := cli.Call(ctx, "session.turnDiff", map[string]any{"id": sid, "scope": "latest"}, &diffRes); err != nil {
		t.Fatalf("session.turnDiff: %v", err)
	}
	if diffRes.TotalAdd <= 0 || len(diffRes.Files) == 0 || diffRes.Files[0].Path != "app.ts" {
		t.Fatalf("unexpected turnDiff: %+v", diffRes)
	}

	if err := cli.Call(ctx, "session.checkpointRevert", map[string]any{
		"id":        sid,
		"turnIndex": 1,
		"toPre":     true,
	}, nil); err != nil {
		t.Fatalf("session.checkpointRevert: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "app.ts")); err == nil {
		t.Fatalf("expected app.ts reverted and removed by checkpointRevert")
	}
}

func TestTier3_03_PlanMode_DesktopThoughtAlias_PlanStream_SwitchToAutoEdit(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T3-03", "linux")
	repo := newTempGitRepo(t, nil)
	ctx := context.Background()

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(ctx, "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "google/gemini-2.5-flash",
		"mode":  "plan",
	}, &created); err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sid := created.Session.ID
	_ = cli.Call(ctx, "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)

	if err := cli.Call(ctx, "session.setConfig", map[string]any{
		"id":       sid,
		"configId": "thought",
		"value":    "high",
	}, nil); err != nil {
		t.Fatalf("session.setConfig(thought=high): %v", err)
	}
	_ = cli.Call(ctx, "session.prompt", map[string]any{"id": sid, "text": "Draft plan [plan] [inspect]"}, nil)
	_, items1, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second)
	if err != nil {
		t.Fatalf("Turn 1: %v", err)
	}
	var sawPlan bool
	for _, it := range items1 {
		if it.Kind == "plan" {
			sawPlan = true
		}
	}
	if !sawPlan {
		t.Fatalf("expected plan item in Turn 1")
	}

	if err := cli.Call(ctx, "session.setMode", map[string]any{"id": sid, "mode": "auto"}, nil); err != nil {
		t.Fatalf("session.setMode(auto): %v", err)
	}
	if err := cli.Call(ctx, "session.setModel", map[string]any{"id": sid, "model": "google/gemini-3-flash-preview"}, nil); err != nil {
		t.Fatalf("session.setModel: %v", err)
	}
	_ = cli.Call(ctx, "session.prompt", map[string]any{
		"id":   sid,
		"text": `Execute plan [edit:impl.ts:export const ready = true;\n]`,
	}, nil)
	sum2, _, err := cli.WaitForTurnIdle(sid, 2, 12*time.Second)
	if err != nil {
		t.Fatalf("Turn 2: %v", err)
	}
	if sum2.Turns != 2 || sum2.Model != "google/gemini-3-flash-preview" {
		t.Fatalf("unexpected Turn 2 summary: %+v", sum2)
	}
	if _, err := os.Stat(filepath.Join(repo, "impl.ts")); err != nil {
		t.Fatalf("impl.ts not written in Turn 2: %v", err)
	}
}

func TestTier3_04_ActiveTurnSteer_QueuedFollowUpAutoDrain_AndCancelGuard(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T3-04", "linux")
	repo := newTempGitRepo(t, nil)
	ctx := context.Background()

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(ctx, "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "google/gemini-3-flash-preview",
	}, &created); err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sid := created.Session.ID
	_ = cli.Call(ctx, "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	_ = cli.Call(ctx, "session.prompt", map[string]any{"id": sid, "text": "Turn 1 [slow:1600]"}, nil)

	if _, _, err := cli.WaitForSession(sid, 5*time.Second, func(s SessionSummaryWire, _ *SessionDetailWire, items []TranscriptItemWire) bool {
		return s.State == "running" && strings.Contains(collectAgentText(items), "chunk-1")
	}); err != nil {
		t.Fatalf("wait running + chunk-1: %v", err)
	}

	if err := cli.Call(ctx, "session.prompt", map[string]any{
		"id":       sid,
		"text":     "steer_now_alpha",
		"delivery": "steer",
	}, nil); err != nil {
		t.Fatalf("steer prompt failed: %v", err)
	}

	var qRes struct {
		Queued bool `json:"queued"`
	}
	if err := cli.Call(ctx, "session.prompt", map[string]any{
		"id":       sid,
		"text":     "queued_followup_beta",
		"delivery": "queue",
	}, &qRes); err != nil {
		t.Fatalf("queue prompt failed: %v", err)
	}

	sum, items, err := cli.WaitForTurnIdle(sid, 2, 15*time.Second)
	if err != nil {
		t.Fatalf("WaitForTurnIdle(2): %v", err)
	}
	txt := collectAgentText(items)
	if !strings.Contains(txt, "steer_now_alpha") || !strings.Contains(txt, "queued_followup_beta") {
		t.Fatalf("expected both steered and queued prompts in transcript; turns=%d txt=%q", sum.Turns, txt)
	}

	// Now verify cancel guard stops active turn cleanly once Turn 3 is streaming on fakecodex
	beforeChunk1Count := strings.Count(txt, "chunk-1")
	_ = cli.Call(ctx, "session.prompt", map[string]any{"id": sid, "text": "Turn 3 [slow:3000]"}, nil)
	if _, _, err := cli.WaitForSession(sid, 5*time.Second, func(s SessionSummaryWire, _ *SessionDetailWire, its []TranscriptItemWire) bool {
		return s.State == "running" && strings.Count(collectAgentText(its), "chunk-1") > beforeChunk1Count
	}); err != nil {
		t.Fatalf("wait Turn 3 running + streaming chunk-1: %v", err)
	}
	_ = cli.Call(ctx, "session.prompt", map[string]any{
		"id":       sid,
		"text":     "queued_should_not_auto_run_on_cancel",
		"delivery": "queue",
	}, nil)
	if err := cli.Call(ctx, "session.cancel", map[string]any{"id": sid}, nil); err != nil {
		t.Fatalf("session.cancel: %v", err)
	}
	if _, _, err := cli.WaitForSession(sid, 8*time.Second, func(s SessionSummaryWire, _ *SessionDetailWire, _ []TranscriptItemWire) bool {
		return s.State == "idle" && s.Turns >= 3
	}); err != nil {
		t.Fatalf("wait idle after cancel: %v", err)
	}
	time.Sleep(250 * time.Millisecond)
	_, _, _, latestItems := cli.SessionSnapshotState(sid)
	if strings.Contains(collectAgentText(latestItems), "Completed codex turn for: queued_should_not_auto_run_on_cancel") {
		t.Fatalf("queued prompt should not auto-fire immediately after explicit session.cancel")
	}
}

func TestTier3_05_GitWorktreeSession_ApplyPatchBridge_StageAndCommit(t *testing.T) {
	suiteMockOR.ResetOverrides()
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T3-05", "linux")
	parentDir := newTempPlainDir(t)
	ctx := context.Background()

	initTrue := true
	var proj struct {
		ID   string `json:"id"`
		Path string `json:"path"`
	}
	if err := cli.Call(ctx, "projects.init", map[string]any{
		"parentDir":     parentDir,
		"name":          "wt-codex-app",
		"initialCommit": &initTrue,
	}, &proj); err != nil {
		t.Fatalf("projects.init: %v", err)
	}

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(ctx, "session.create", map[string]any{
		"agent":       "codex",
		"cwd":         proj.Path,
		"projectId":   proj.ID,
		"useWorktree": true,
		"branchName":  "feat/cx-wt",
		"model":       "google/gemini-2.5-flash",
	}, &created); err != nil {
		t.Fatalf("session.create(useWorktree=true): %v", err)
	}
	wtPath := created.Session.WorktreePath
	if wtPath == "" || wtPath == proj.Path {
		t.Fatalf("expected isolated worktree path != %q, got %q", proj.Path, wtPath)
	}

	sid := created.Session.ID
	_ = cli.Call(ctx, "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	_ = cli.Call(ctx, "session.prompt", map[string]any{
		"id":   sid,
		"text": `Worktree bridge edit [bridge-patch] [edit:wt_only.txt:inside worktree\n]`,
	}, nil)
	if _, _, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second); err != nil {
		t.Fatalf("WaitForTurnIdle: %v", err)
	}

	if _, err := os.Stat(filepath.Join(wtPath, "wt_only.txt")); err != nil {
		t.Fatalf("wt_only.txt missing in worktree %s: %v", wtPath, err)
	}
	if _, err := os.Stat(filepath.Join(proj.Path, "wt_only.txt")); err == nil {
		t.Fatalf("wt_only.txt leaked into main checkout %s", proj.Path)
	}

	if err := cli.Call(ctx, "git.stage", map[string]any{"path": wtPath, "files": []string{"wt_only.txt", "bridged_patch.txt"}}, nil); err != nil {
		t.Fatalf("git.stage in worktree: %v", err)
	}
	if err := cli.Call(ctx, "git.commit", map[string]any{"path": wtPath, "message": "feat: worktree codex changes"}, nil); err != nil {
		t.Fatalf("git.commit in worktree: %v", err)
	}
	var logRes struct {
		Commits []map[string]any `json:"commits"`
	}
	if err := cli.Call(ctx, "git.log", map[string]any{"path": wtPath, "limit": 5}, &logRes); err != nil || len(logRes.Commits) < 2 {
		t.Fatalf("expected >= 2 git commits in worktree, err=%v entries=%d", err, len(logRes.Commits))
	}
}

func TestTier3_06_Hibernate_SleepingUnlistedSetModel_WakeResume_AndFork(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T3-06", "linux")
	repo := newTempGitRepo(t, nil)
	ctx := context.Background()

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(ctx, "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "google/gemini-3-flash-preview",
	}, &created); err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sid := created.Session.ID
	_ = cli.Call(ctx, "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	_ = cli.Call(ctx, "session.prompt", map[string]any{"id": sid, "text": "Turn 1 context"}, nil)
	if _, _, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second); err != nil {
		t.Fatalf("Turn 1: %v", err)
	}

	_ = cli.Call(ctx, "session.stop", map[string]any{"id": sid}, nil)
	if _, _, err := cli.WaitForSession(sid, 6*time.Second, func(s SessionSummaryWire, _ *SessionDetailWire, _ []TranscriptItemWire) bool {
		return !s.Live
	}); err != nil {
		t.Fatalf("WaitForSession(hibernate): %v", err)
	}

	newSlug := "qwen/qwen3-coder-next"
	if err := cli.Call(ctx, "session.setModel", map[string]any{"id": sid, "model": newSlug}, nil); err != nil {
		t.Fatalf("sleeping setModel: %v", err)
	}
	if err := cli.Call(ctx, "session.setConfig", map[string]any{"id": sid, "configId": "thought_level", "value": "xhigh"}, nil); err != nil {
		t.Fatalf("sleeping setConfig: %v", err)
	}

	_ = cli.Call(ctx, "session.prompt", map[string]any{"id": sid, "text": "Turn 2 wake [inspect]"}, nil)
	_, items2, err := cli.WaitForTurnIdle(sid, 2, 12*time.Second)
	if err != nil {
		t.Fatalf("Turn 2: %v", err)
	}
	insp, ok := extractInspectJSON(items2)
	if !ok {
		t.Fatalf("missing INSPECT_JSON")
	}
	if mod, _ := insp["model"].(string); !strings.Contains(mod, "qwen3-coder-next") {
		t.Fatalf("expected woken model %q, got %q", newSlug, mod)
	}
	if eff, _ := insp["effort"].(string); eff != "xhigh" {
		t.Fatalf("expected woken effort='xhigh', got %q", eff)
	}

	var forked struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(ctx, "session.fork", map[string]any{"id": sid}, &forked); err != nil {
		t.Fatalf("session.fork after wake: %v", err)
	}
	if forked.Session.ForkedFrom != sid {
		t.Fatalf("expected ForkedFrom=%q, got %q", sid, forked.Session.ForkedFrom)
	}
}

func TestTier3_07_ConcurrentDesktopAndAndroidClients_QuestionSyncAndDrafts(t *testing.T) {
	td := sharedDaemon(t)
	deskCli := td.PairAndConnect(t, "T3-07-Desktop", "linux")
	andCli := td.PairAndConnect(t, "T3-07-Android", "android")
	repo := newTempGitRepo(t, nil)
	ctx := context.Background()

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := deskCli.Call(ctx, "session.create", map[string]any{
		"agent":           "codex",
		"cwd":             repo,
		"model":           "google/gemini-3-flash-preview",
		"skipPermissions": false,
	}, &created); err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sid := created.Session.ID
	_ = deskCli.Call(ctx, "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	_ = andCli.Call(ctx, "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)

	_ = deskCli.Call(ctx, "session.prompt", map[string]any{"id": sid, "text": "Multi-client question [question]"}, nil)

	// Android observes pending question and answers it
	permOnAndroid, err := andCli.WaitForPendingPerm(sid, 10*time.Second)
	if err != nil {
		t.Fatalf("Android WaitForPendingPerm: %v", err)
	}
	if err := andCli.Call(ctx, "session.permission", map[string]any{
		"id":       sid,
		"reqId":    permOnAndroid.Perm.ReqID,
		"optionId": PickPermOptionID(permOnAndroid.Perm, "allow_once"),
		"answers":  map[string]string{"q_env": "Prod"},
	}, nil); err != nil {
		t.Fatalf("Android session.permission: %v", err)
	}

	// Desktop sees resolved permission AND completed turn with "Prod"
	_, deskItems, err := deskCli.WaitForTurnIdle(sid, 1, 12*time.Second)
	if err != nil {
		t.Fatalf("Desktop WaitForTurnIdle: %v", err)
	}
	if !strings.Contains(collectAgentText(deskItems), "Prod") {
		t.Fatalf("Desktop missing Android's question answer 'Prod': %q", collectAgentText(deskItems))
	}

	// Draft & settle sync from Android -> Desktop
	if err := andCli.Call(ctx, "session.draft.set", map[string]any{"id": sid, "draft": "shared draft text"}, nil); err != nil {
		t.Fatalf("session.draft.set: %v", err)
	}
	if err := andCli.Call(ctx, "session.settle", map[string]any{"id": sid, "settled": true}, nil); err != nil {
		t.Fatalf("session.settle: %v", err)
	}
	if _, _, err := deskCli.WaitForSession(sid, 6*time.Second, func(s SessionSummaryWire, _ *SessionDetailWire, _ []TranscriptItemWire) bool {
		return s.Draft == "shared draft text" && s.Settled
	}); err != nil {
		t.Fatalf("Desktop did not receive synced draft/settled state: %v", err)
	}
}

func TestTier3_08_DaemonRestart_PersistenceOfDevicesCustomModelsAndCodexResume(t *testing.T) {
	dir := newTempPlainDir(t)
	keyFile := filepath.Join(dir, "persist_or.key")
	_ = os.WriteFile(keyFile, []byte("sk-or-v1-t3-08-restart-key\n"), 0o600)

	td := StartIsolatedDaemon(t, DaemonOpts{
		ExtraArgs: []string{"-openrouter-api-key-file", keyFile},
	})
	ctx := context.Background()

	_, link, err := td.Unix.Pair(ctx, td.HTTPBase, 300)
	if err != nil {
		t.Fatalf("Unix.Pair: %v", err)
	}
	devKey, _ := GenerateDeviceKeypair()
	if _, err := PairDeviceOverNoiseWS(ctx, link, devKey, "PersistentClient", "linux", nil); err != nil {
		t.Fatalf("PairDeviceOverNoiseWS: %v", err)
	}
	cli1, err := ConnectNoiseRPCClient(ctx, td.WSBase, link.DaemonPub, devKey, "PersistentClient", "linux", nil)
	if err != nil {
		t.Fatalf("ConnectNoiseRPCClient #1: %v", err)
	}

	if err := cli1.Call(ctx, "providers.upsert", map[string]any{
		"id":      "codex",
		"name":    "Codex",
		"driver":  "codex",
		"enabled": true,
		"customModels": []map[string]string{
			{"slug": "deepseek/deepseek-r1:free", "name": "DeepSeek R1 Free"},
		},
	}, nil); err != nil {
		t.Fatalf("providers.upsert: %v", err)
	}

	repo := newTempGitRepo(t, nil)
	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli1.Call(ctx, "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "deepseek/deepseek-r1:free",
	}, &created); err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sid := created.Session.ID
	_ = cli1.Call(ctx, "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	_ = cli1.Call(ctx, "session.prompt", map[string]any{"id": sid, "text": "persisted_turn_one_token"}, nil)
	if _, _, err := cli1.WaitForTurnIdle(sid, 1, 12*time.Second); err != nil {
		t.Fatalf("Turn 1: %v", err)
	}
	_ = cli1.Close()

	// Restart agentc serve on the exact same XDG state/config directories!
	td.Restart(t, DaemonOpts{
		ExtraArgs: []string{"-openrouter-api-key-file", keyFile},
	})

	// Reconnect using existing paired device keypair WITHOUT re-pairing!
	cli2, err := ConnectNoiseRPCClient(ctx, td.WSBase, link.DaemonPub, devKey, "PersistentClient", "linux", nil)
	if err != nil {
		t.Fatalf("ConnectNoiseRPCClient after daemon restart failed (identity/devices not persisted): %v", err)
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
	if len(cat.CustomModels) == 0 || cat.CustomModels[0].Slug != "deepseek/deepseek-r1:free" {
		t.Fatalf("customModels lost across restart: %+v", cat.CustomModels)
	}

	_ = cli2.Call(ctx, "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	_ = cli2.Call(ctx, "session.prompt", map[string]any{"id": sid, "text": "Turn 2 after daemon restart [inspect]"}, nil)
	sum2, items2, err := cli2.WaitForTurnIdle(sid, 2, 12*time.Second)
	if err != nil {
		t.Fatalf("Turn 2 after restart: %v", err)
	}
	if sum2.Turns != 2 || !strings.Contains(collectAgentText(items2), "persisted_turn_one_token") {
		t.Fatalf("expected Turns=2 and Turn 1 transcript preserved; sum=%+v", sum2)
	}
}

func TestTier3_09_CodexTurn_AsideBtw_AndPortableHandoffToFakeACP(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T3-09", "linux")
	repo := newTempGitRepo(t, nil)
	ctx := context.Background()

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(ctx, "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "google/gemini-3-flash-preview",
	}, &created); err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sid := created.Session.ID
	_ = cli.Call(ctx, "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	_ = cli.Call(ctx, "session.prompt", map[string]any{
		"id":   sid,
		"text": `Create module [edit:handoff_mod.go:package handoff\n] [edit:aside_canary.txt:turn1\n]`,
	}, nil)
	if _, _, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second); err != nil {
		t.Fatalf("Turn 1: %v", err)
	}
	_ = os.Remove(filepath.Join(repo, "aside_canary.txt"))

	_ = callAndWaitAsideDone(t, cli, sid, "Which Go package did we create?", 12*time.Second)
	if _, err := os.Stat(filepath.Join(repo, "aside_canary.txt")); err == nil {
		t.Fatalf("ephemeral read-only /btw aside MUST NOT re-execute historical [edit:aside_canary.txt:...] directive")
	}

	var handoffRes struct {
		Handoff string             `json:"handoff"`
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(ctx, "session.handoff", map[string]any{
		"id":          sid,
		"targetAgent": "fakeacp",
		"prompt":      "Continue from codex handoff",
	}, &handoffRes); err != nil {
		t.Fatalf("session.handoff(codex -> fakeacp): %v", err)
	}
	if handoffRes.Handoff == "" || handoffRes.Session.Agent != "fakeacp" {
		t.Fatalf("unexpected handoff response: %+v", handoffRes)
	}
}

func TestTier3_10_CodexUsageTelemetry_GlobalSearch_AndOpenInTerminalTUI(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T3-10", "linux")
	repo := newTempGitRepo(t, nil)
	ctx := context.Background()

	uniquePhrase := "quantum_pangolin_42_search_needle"
	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(ctx, "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "google/gemini-3-flash-preview",
	}, &created); err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sid := created.Session.ID
	_ = cli.Call(ctx, "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	_ = cli.Call(ctx, "session.prompt", map[string]any{"id": sid, "text": "Include needle " + uniquePhrase}, nil)
	sum, _, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second)
	if err != nil {
		t.Fatalf("Turn 1: %v", err)
	}
	if sum.Usage.TotalTokens <= 0 {
		t.Fatalf("expected sum.Usage.TotalTokens > 0 after codex turn, got %+v", sum.Usage)
	}
	var usageSum struct {
		Turns       int   `json:"turns"`
		TotalTokens int64 `json:"totalTokens"`
		ByAgent     []struct {
			Key         string `json:"key"`
			Turns       int    `json:"turns"`
			TotalTokens int64  `json:"totalTokens"`
		} `json:"byAgent"`
	}
	if err := cli.Call(ctx, "usage.summary", map[string]any{"days": 7}, &usageSum); err != nil {
		t.Fatalf("usage.summary: %v", err)
	}
	if usageSum.Turns < 1 || usageSum.TotalTokens <= 0 {
		t.Fatalf("expected usage.summary Turns>=1 and TotalTokens>0, got %+v", usageSum)
	}
	foundCodexUsage := false
	for _, b := range usageSum.ByAgent {
		if b.Key == "codex" && b.Turns >= 1 && b.TotalTokens > 0 {
			foundCodexUsage = true
			break
		}
	}
	if !foundCodexUsage {
		t.Fatalf("expected codex bucket in usage.summary.byAgent, got %+v", usageSum.ByAgent)
	}

	var hits []struct {
		SessionID string `json:"sessionId"`
		Snippet   string `json:"snippet"`
	}
	if err := cli.Call(ctx, "search.query", map[string]any{"query": uniquePhrase, "limit": 20}, &hits); err != nil {
		t.Fatalf("search.query: %v", err)
	}
	var foundHit bool
	for _, h := range hits {
		if h.SessionID == sid {
			foundHit = true
		}
	}
	if !foundHit {
		t.Fatalf("expected search.query to find codex session %s for %q; hits=%+v", sid, uniquePhrase, hits)
	}

	var termInfo struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
	}
	if err := cli.Call(ctx, "session.openInTerminal", map[string]any{"id": sid, "cols": 120, "rows": 40}, &termInfo); err != nil {
		t.Fatalf("session.openInTerminal: %v", err)
	}
	defer func() {
		_ = cli.Call(ctx, "term.kill", map[string]any{"id": termInfo.ID}, nil)
	}()
	if termInfo.Kind != "codex" {
		t.Fatalf("expected term Kind='codex', got %q", termInfo.Kind)
	}
	waitForTmuxCapture(t, cli, termInfo.ID, "FAKEX_TUI_READY")
	if err := cli.Call(ctx, "term.sendKeys", map[string]any{"id": termInfo.ID, "text": "ping_tui_99", "enter": true}, nil); err != nil {
		t.Fatalf("term.sendKeys: %v", err)
	}
	waitForTmuxCapture(t, cli, termInfo.ID, "FAKEX_TUI_ECHO:ping_tui_99")
}

func TestTier3_11_ScheduledCodexTaskWithCustomOpenRouterSlug_RunNow(t *testing.T) {
	td := StartIsolatedDaemon(t, DaemonOpts{
		Env: map[string]string{"OPENROUTER_API_KEY": "sk-or-v1-t3-11"},
	})
	cli := td.PairAndConnect(t, "T3-11", "linux")
	repo := newTempGitRepo(t, nil)
	ctx := context.Background()

	var task struct {
		ID string `json:"id"`
	}
	if err := cli.Call(ctx, "schedules.upsert", map[string]any{
		"name":    "Nightly Codex OpenRouter Audit",
		"enabled": true,
		"kind":    "cron",
		"cron":    "0 3 * * *",
		"cwd":     repo,
		"agent":   "codex",
		"model":   "google/gemini-2.5-flash",
		"prompt":  "Run scheduled codex check",
	}, &task); err != nil {
		t.Fatalf("schedules.upsert: %v", err)
	}
	if task.ID == "" {
		t.Fatalf("empty task.ID from schedules.upsert")
	}

	if err := cli.Call(ctx, "schedules.runNow", map[string]any{"id": task.ID}, nil); err != nil {
		t.Fatalf("schedules.runNow: %v", err)
	}

	deadline := time.Now().Add(12 * time.Second)
	var completed bool
	for time.Now().Before(deadline) {
		var sessions []SessionSummaryWire
		if err := cli.Call(ctx, "sessions.list", map[string]any{}, &sessions); err == nil {
			for _, s := range sessions {
				if s.Agent == "codex" && s.Turns >= 1 && s.State == "idle" {
					if s.Model != "google/gemini-2.5-flash" {
						t.Fatalf("scheduled codex session used model %q, want 'google/gemini-2.5-flash'", s.Model)
					}
					completed = true
					break
				}
			}
		}
		if completed {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !completed {
		t.Fatalf("scheduled codex task did not spawn and complete Turn 1 within 12s")
	}
}

func TestTier3_12_CombinedHttp402RetryAndNonOpenAIApplyPatchBridge(t *testing.T) {
	suiteMockOR.ResetOverrides()
	suiteMockOR.ClearRequests()
	td := StartIsolatedDaemon(t, DaemonOpts{
		EnableFakeACP: true,
		Env: map[string]string{
			"OPENROUTER_API_KEY": hermeticSharedSecret,
		},
	})
	cli := td.PairAndConnect(t, "T3-12", "linux")
	repo := newTempGitRepo(t, nil)
	ctx := context.Background()

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(ctx, "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "google/gemini-2.5-flash",
	}, &created); err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sid := created.Session.ID
	_ = cli.Call(ctx, "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	_ = cli.Call(ctx, "session.prompt", map[string]any{
		"id":   sid,
		"text": "Combined 402 + patch [bridge-402] [afford:1800] [bridge-patch]",
	}, nil)

	sum, items, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second)
	if err != nil {
		t.Fatalf("WaitForTurnIdle: %v", err)
	}
	if sum.Error != "" {
		t.Fatalf("unexpected turn error: %q", sum.Error)
	}

	var attempts []RecordedResponsesRequest
	for _, r := range suiteMockOR.ResponsesRequests() {
		if strings.Contains(r.PromptText, "Combined 402 + patch") {
			attempts = append(attempts, r)
		}
	}
	if len(attempts) != 2 {
		t.Fatalf("expected 2 attempts (402 + retry), got %d", len(attempts))
	}
	if attempts[1].MaxOutputTokens > 1736 || attempts[1].MaxOutputTokens < 256 {
		t.Fatalf("expected retry MaxOutputTokens <= 1736, got %d", attempts[1].MaxOutputTokens)
	}
	// Verify function:apply_patch rewrite was preserved on the 402 retry request too!
	var retryHadFunctionTool bool
	for _, tl := range attempts[1].Tools {
		if tl["name"] == "apply_patch" && tl["type"] == "function" {
			retryHadFunctionTool = true
		}
	}
	if !retryHadFunctionTool {
		t.Fatalf("retry request lost function:apply_patch tool rewrite: %+v", attempts[1].Tools)
	}
	if !slices.Contains(strings.Split(collectAgentText(items), "\n"), "BRIDGED_TOOL_TYPE:custom_tool_call") && !strings.Contains(collectAgentText(items), "BRIDGED_TOOL_TYPE:custom_tool_call") {
		t.Fatalf("expected custom_tool_call SSE rewrite after 402 retry; got %q", collectAgentText(items))
	}
	if _, err := os.Stat(filepath.Join(repo, "bridged_patch.txt")); err != nil {
		t.Fatalf("bridged_patch.txt missing after combined 402 retry + patch: %v", err)
	}
}
