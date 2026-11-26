package e2e_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// ============================================================================
// F1: Codex Harness Discovery, Capabilities & Health (5 tests)
// ============================================================================

func TestTier1_F1_01_DaemonInfoCodexProfileAndFeatures(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T1-F1-01", "linux")

	var info struct {
		ProtocolVersion int `json:"protocolVersion"`
		Agents          []struct {
			ID        string   `json:"id"`
			Name      string   `json:"name"`
			Driver    string   `json:"driver"`
			Command   string   `json:"command"`
			Version   string   `json:"version"`
			Available bool     `json:"available"`
			Features  []string `json:"features"`
		} `json:"agents"`
	}
	if err := cli.Call(context.Background(), "daemon.info", map[string]any{}, &info); err != nil {
		t.Fatalf("daemon.info failed: %v", err)
	}
	if info.ProtocolVersion != 2 {
		t.Fatalf("expected protocolVersion 2, got %d", info.ProtocolVersion)
	}

	var found bool
	requiredFeatures := []string{
		"openrouter", "customModels", "thinking", "diffs",
		"permissions", "questions", "plans", "fork",
		"rewind", "steer", "resume", "history",
	}
	for _, a := range info.Agents {
		if a.ID == "codex" {
			found = true
			if a.Driver != "codex" {
				t.Fatalf("expected codex driver 'codex', got %q", a.Driver)
			}
			if !a.Available {
				t.Fatalf("expected codex available=true, got false")
			}
			if !strings.Contains(a.Version, "0.172.0") && a.Version == "" {
				t.Fatalf("expected non-empty codex version, got %q", a.Version)
			}
			for _, rf := range requiredFeatures {
				if !slices.Contains(a.Features, rf) {
					t.Fatalf("codex profile missing required feature %q in %v", rf, a.Features)
				}
			}
			break
		}
	}
	if !found {
		t.Fatalf("codex agent profile not found in daemon.info agents")
	}
}

func TestTier1_F1_02_HelloReplyCapabilitiesAdvertiseCodexAndOpenRouter(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T1-F1-02", "linux")

	hr := cli.HelloReply()
	if hr.ProtocolVersion != 2 {
		t.Fatalf("HelloReply protocolVersion=%d, want 2", hr.ProtocolVersion)
	}
	for _, capName := range []string{"providers.codex", "providers.openrouter", "customModels"} {
		if !slices.Contains(hr.Capabilities, capName) {
			t.Fatalf("HelloReply capabilities missing %q; got %v", capName, hr.Capabilities)
		}
	}

	var helloRes struct {
		ProtocolVersion int      `json:"protocolVersion"`
		Capabilities    []string `json:"capabilities"`
	}
	if err := cli.Call(context.Background(), "hello", map[string]any{"device": "T1-F1-02", "app": "0.5.0-e2e"}, &helloRes); err != nil {
		t.Fatalf("hello RPC failed: %v", err)
	}
	for _, capName := range []string{"providers.codex", "providers.openrouter", "customModels"} {
		if !slices.Contains(helloRes.Capabilities, capName) {
			t.Fatalf("hello RPC capabilities missing %q; got %v", capName, helloRes.Capabilities)
		}
	}
}

func TestTier1_F1_03_ProvidersHealthCodexWithOpenRouterKey(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T1-F1-03", "linux")

	var health []struct {
		ID         string `json:"id"`
		Available  bool   `json:"available"`
		Command    string `json:"command"`
		Version    string `json:"version"`
		AuthStatus string `json:"authStatus"`
		Message    string `json:"message"`
	}
	if err := cli.Call(context.Background(), "providers.health", map[string]any{"id": "codex"}, &health); err != nil {
		t.Fatalf("providers.health failed: %v", err)
	}
	if len(health) == 0 {
		t.Fatalf("providers.health returned empty list for codex")
	}
	h := health[0]
	if h.ID != "codex" || !h.Available {
		t.Fatalf("expected codex available=true, got %+v", h)
	}
	if h.Command == "" {
		t.Fatalf("expected resolved command in providers.health, got empty")
	}
	if !strings.Contains(h.Version, "0.172.0") {
		t.Fatalf("expected version containing 0.172.0, got %q", h.Version)
	}
	if h.AuthStatus != "ok" {
		t.Fatalf("expected authStatus 'ok' when OpenRouter key is configured, got %q (msg=%q)", h.AuthStatus, h.Message)
	}
}

func TestTier1_F1_04_CodexPathCLIFlagOverride(t *testing.T) {
	requireSuiteReady(t)
	customDir := newTempPlainDir(t)
	customBin := filepath.Join(customDir, "my-custom-codex-flag")
	if err := os.Symlink(fakecodexBin, customBin); err != nil {
		t.Fatalf("symlink custom codex: %v", err)
	}

	td := StartIsolatedDaemon(t, DaemonOpts{
		OmitCodexFromPath:  true,
		OmitAgentcCodexEnv: true,
		ExtraArgs:          []string{"-codex-path", customBin},
		Env: map[string]string{
			"OPENROUTER_API_KEY": "sk-or-v1-f1-04-key",
		},
	})
	cli := td.PairAndConnect(t, "T1-F1-04", "linux")

	var health []struct {
		ID        string `json:"id"`
		Available bool   `json:"available"`
		Command   string `json:"command"`
	}
	if err := cli.Call(context.Background(), "providers.health", map[string]any{"id": "codex"}, &health); err != nil {
		t.Fatalf("providers.health failed: %v", err)
	}
	if len(health) == 0 || !health[0].Available || health[0].Command != customBin {
		t.Fatalf("expected codex command=%q available=true, got %+v", customBin, health)
	}
}

func TestTier1_F1_05_AgentcCodexPathEnvAndProviderCommandOverride(t *testing.T) {
	requireSuiteReady(t)
	dir := newTempPlainDir(t)
	envBin := filepath.Join(dir, "env-codex")
	upsertBin := filepath.Join(dir, "upsert-codex")
	if err := os.Symlink(fakecodexBin, envBin); err != nil {
		t.Fatalf("symlink envBin: %v", err)
	}
	if err := os.Symlink(fakecodexBin, upsertBin); err != nil {
		t.Fatalf("symlink upsertBin: %v", err)
	}

	td := StartIsolatedDaemon(t, DaemonOpts{
		OmitCodexFromPath:  true,
		OmitAgentcCodexEnv: true,
		Env: map[string]string{
			"AGENTC_CODEX_PATH":  envBin,
			"OPENROUTER_API_KEY": "sk-or-v1-f1-05-key",
		},
	})
	cli := td.PairAndConnect(t, "T1-F1-05", "linux")

	var health []struct {
		ID      string `json:"id"`
		Command string `json:"command"`
	}
	if err := cli.Call(context.Background(), "providers.health", map[string]any{"id": "codex"}, &health); err != nil {
		t.Fatalf("providers.health failed: %v", err)
	}
	if len(health) == 0 || health[0].Command != envBin {
		t.Fatalf("expected AGENTC_CODEX_PATH %q, got %+v", envBin, health)
	}

	marker := cli.EventMarker()
	if err := cli.Call(context.Background(), "providers.upsert", map[string]any{
		"id":      "codex",
		"name":    "Codex",
		"driver":  "codex",
		"enabled": true,
		"command": upsertBin,
	}, nil); err != nil {
		t.Fatalf("providers.upsert failed: %v", err)
	}
	if _, err := cli.WaitForEvent(marker, 5*time.Second, func(ev PushEvent) bool {
		return ev.Event == "providers.changed"
	}); err != nil {
		t.Fatalf("expected providers.changed broadcast: %v", err)
	}

	if err := cli.Call(context.Background(), "providers.health", map[string]any{"id": "codex"}, &health); err != nil {
		t.Fatalf("providers.health after upsert failed: %v", err)
	}
	if len(health) == 0 || health[0].Command != upsertBin {
		t.Fatalf("expected provider command override %q, got %+v", upsertBin, health)
	}
}

// ============================================================================
// F2: OpenRouter CLI/Env/Config Key Setup & Strict Secret Redaction (5 tests)
// ============================================================================

func TestTier1_F2_01_OpenRouterAPIKeyCLIFlagConfiguresDaemon(t *testing.T) {
	secret := "sk-or-v1-cli-flag-secret-alpha99"
	td := StartIsolatedDaemon(t, DaemonOpts{
		ExtraArgs: []string{"-openrouter-api-key", secret},
	})
	cli := td.PairAndConnect(t, "T1-F2-01", "linux")

	statusMap, statusBytes, err := td.Unix.StatusRaw(context.Background())
	if err != nil {
		t.Fatalf("Unix.StatusRaw failed: %v", err)
	}
	if cfg, _ := statusMap["openRouterConfigured"].(bool); !cfg {
		t.Fatalf("expected openRouterConfigured=true in /local/status, got %v", statusMap["openRouterConfigured"])
	}
	assertZeroSecretSubstring(t, secret, "/local/status", statusBytes)

	rawInfo, err := cli.CallRaw(context.Background(), "daemon.info", map[string]any{})
	if err != nil {
		t.Fatalf("daemon.info failed: %v", err)
	}
	var info map[string]any
	_ = json.Unmarshal(rawInfo, &info)
	if cfg, _ := info["openRouterConfigured"].(bool); !cfg {
		t.Fatalf("expected openRouterConfigured=true in daemon.info, got %v", info["openRouterConfigured"])
	}
	assertZeroSecretSubstring(t, secret, "daemon.info", rawInfo)
}

func TestTier1_F2_02_OpenRouterAPIKeyFileCLIFlagLoadsKey(t *testing.T) {
	dir := newTempPlainDir(t)
	secret := "sk-or-v1-file-flag-secret-bravo88"
	keyFile := filepath.Join(dir, "or_key.txt")
	if err := os.WriteFile(keyFile, []byte(secret+"\n"), 0o600); err != nil {
		t.Fatalf("write key file: %v", err)
	}

	suiteMockOR.ClearRequests()
	td := StartIsolatedDaemon(t, DaemonOpts{
		ExtraArgs: []string{"-openrouter-api-key-file", keyFile},
	})
	cli := td.PairAndConnect(t, "T1-F2-02", "linux")
	repo := newTempGitRepo(t, nil)

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(context.Background(), "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "google/gemini-3-flash-preview",
	}, &created); err != nil {
		t.Fatalf("session.create failed: %v", err)
	}
	sid := created.Session.ID
	if err := cli.Call(context.Background(), "session.subscribe", map[string]any{"id": sid, "since": 0}, nil); err != nil {
		t.Fatalf("session.subscribe failed: %v", err)
	}
	if err := cli.Call(context.Background(), "session.prompt", map[string]any{
		"id":   sid,
		"text": "Run [bridge-probe]",
	}, nil); err != nil {
		t.Fatalf("session.prompt failed: %v", err)
	}
	if _, _, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second); err != nil {
		t.Fatalf("WaitForTurnIdle failed: %v", err)
	}

	reqs := suiteMockOR.ResponsesRequests()
	var matched bool
	for _, r := range reqs {
		if r.AuthHeader == "Bearer "+secret {
			matched = true
			break
		}
	}
	if !matched {
		t.Fatalf("expected bridge to send Authorization Bearer from -openrouter-api-key-file; recorded=%d", len(reqs))
	}
}

func TestTier1_F2_03_OpenRouterEnvVarsConfigureDaemon(t *testing.T) {
	secretEnv := "sk-or-v1-env-secret-charlie77"
	suiteMockOR.ClearRequests()
	td1 := StartIsolatedDaemon(t, DaemonOpts{
		Env: map[string]string{"OPENROUTER_API_KEY": secretEnv},
	})
	cli1 := td1.PairAndConnect(t, "T1-F2-03a", "linux")
	var catRes map[string]any
	if err := cli1.Call(context.Background(), "providers.models", map[string]any{"provider": "codex", "refresh": true}, &catRes); err != nil {
		t.Fatalf("providers.models failed: %v", err)
	}
	if cfg, _ := catRes["openRouterConfigured"].(bool); !cfg {
		t.Fatalf("expected openRouterConfigured=true via OPENROUTER_API_KEY env")
	}

	dir := newTempPlainDir(t)
	secretFile := "sk-or-v1-envfile-secret-delta66"
	kf := filepath.Join(dir, "env_or.key")
	_ = os.WriteFile(kf, []byte(secretFile), 0o600)
	td2 := StartIsolatedDaemon(t, DaemonOpts{
		Env: map[string]string{"OPENROUTER_API_KEY_FILE": kf},
	})
	cli2 := td2.PairAndConnect(t, "T1-F2-03b", "linux")
	var info map[string]any
	if err := cli2.Call(context.Background(), "daemon.info", map[string]any{}, &info); err != nil {
		t.Fatalf("daemon.info failed: %v", err)
	}
	if cfg, _ := info["openRouterConfigured"].(bool); !cfg {
		t.Fatalf("expected openRouterConfigured=true via OPENROUTER_API_KEY_FILE env")
	}
}

func TestTier1_F2_04_ProvidersUpsertEnvSecretRedactedInProvidersList(t *testing.T) {
	secret := "sk-or-v1-upsert-secret-echo55"
	td := StartIsolatedDaemon(t, DaemonOpts{})
	cli := td.PairAndConnect(t, "T1-F2-04", "linux")

	var saved struct {
		Env map[string]string `json:"env"`
	}
	if err := cli.Call(context.Background(), "providers.upsert", map[string]any{
		"id":      "codex",
		"name":    "Codex",
		"driver":  "codex",
		"enabled": true,
		"env": map[string]string{
			"OPENROUTER_API_KEY": secret,
		},
	}, &saved); err != nil {
		t.Fatalf("providers.upsert failed: %v", err)
	}
	if saved.Env["OPENROUTER_API_KEY"] != "***" {
		t.Fatalf("expected providers.upsert response to redact OPENROUTER_API_KEY to '***', got %q", saved.Env["OPENROUTER_API_KEY"])
	}

	rawList, err := cli.CallRaw(context.Background(), "providers.list", map[string]any{})
	if err != nil {
		t.Fatalf("providers.list failed: %v", err)
	}
	assertZeroSecretSubstring(t, secret, "providers.list", rawList)
	if !strings.Contains(string(rawList), `"***"`) {
		t.Fatalf("expected providers.list to show '***' placeholder, got %s", string(rawList))
	}

	rawInfo, _ := cli.CallRaw(context.Background(), "daemon.info", map[string]any{})
	rawHealth, _ := cli.CallRaw(context.Background(), "providers.health", map[string]any{"id": "codex"})
	assertZeroSecretSubstring(t, secret, "daemon.info", rawInfo)
	assertZeroSecretSubstring(t, secret, "providers.health", rawHealth)
}

func TestTier1_F2_05_AgentcConfigSetOpenRouterKeyAndCodexPath(t *testing.T) {
	secret := "sk-or-v1-config-set-foxtrot44"
	td := StartIsolatedDaemon(t, DaemonOpts{})

	stdout, stderr, code := td.RunCLI("config", "set", "openrouter_api_key", secret)
	if code != 0 {
		t.Fatalf("agentc config set openrouter_api_key exited %d: stdout=%s stderr=%s", code, stdout, stderr)
	}
	stdout2, stderr2, code2 := td.RunCLI("config", "set", "codex_path", fakecodexBin)
	if code2 != 0 {
		t.Fatalf("agentc config set codex_path exited %d: stdout=%s stderr=%s", code2, stdout2, stderr2)
	}

	cfgPath := filepath.Join(td.ConfigHome, "agentc", "config.json")
	st, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatalf("stat config.json: %v", err)
	}
	if st.Mode().Perm()&0o077 != 0 {
		t.Fatalf("expected config.json permissions 0600, got %o", st.Mode().Perm())
	}

	td.Restart(t, DaemonOpts{OmitAgentcCodexEnv: true})
	cli := td.PairAndConnect(t, "T1-F2-05", "linux")
	rawInfo, err := cli.CallRaw(context.Background(), "daemon.info", map[string]any{})
	if err != nil {
		t.Fatalf("daemon.info after restart failed: %v", err)
	}
	assertZeroSecretSubstring(t, secret, "daemon.info", rawInfo)
	var info map[string]any
	_ = json.Unmarshal(rawInfo, &info)
	if cfg, _ := info["openRouterConfigured"].(bool); !cfg {
		t.Fatalf("expected openRouterConfigured=true after config set + restart")
	}
}

// ============================================================================
// F3: Dynamic OpenRouter Model Catalog & Custom Model Persistence (5 tests)
// ============================================================================

func TestTier1_F3_01_ProvidersModelsReturnsDynamicOpenRouterCatalog(t *testing.T) {
	suiteMockOR.ResetOverrides()
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T1-F3-01", "linux")

	var res struct {
		Provider             string          `json:"provider"`
		OpenRouterConfigured bool            `json:"openRouterConfigured"`
		Models               []ModelInfoWire `json:"models"`
	}
	if err := cli.Call(context.Background(), "providers.models", map[string]any{
		"provider": "codex",
		"refresh":  true,
	}, &res); err != nil {
		t.Fatalf("providers.models failed: %v", err)
	}
	if res.Provider != "codex" || !res.OpenRouterConfigured {
		t.Fatalf("unexpected providers.models header: provider=%q openRouterConfigured=%v", res.Provider, res.OpenRouterConfigured)
	}

	var foundGem3, foundGem25 bool
	for _, m := range res.Models {
		if m.ModelID == "google/gemini-3-flash-preview" {
			foundGem3 = true
			if m.Provider != "openrouter" {
				t.Fatalf("expected provider='openrouter', got %q", m.Provider)
			}
			if m.ContextWindow < 1000000 {
				t.Fatalf("expected contextWindow >= 1000000, got %d", m.ContextWindow)
			}
			if !m.SupportsReasoning || !m.SupportsTools || !m.SupportsImages {
				t.Fatalf("expected reasoning/tools/images capabilities true on gemini-3-flash-preview, got %+v", m)
			}
		}
		if m.ModelID == "google/gemini-2.5-flash" {
			foundGem25 = true
		}
	}
	if !foundGem3 || !foundGem25 {
		t.Fatalf("expected google/gemini-3-flash-preview and google/gemini-2.5-flash in catalog, got %d models", len(res.Models))
	}
}

func TestTier1_F3_02_ProvidersModelsExposesModesAndReasoningEfforts(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T1-F3-02", "linux")

	var res struct {
		AvailableModes   []ModeInfoWire `json:"availableModes"`
		ReasoningEfforts []string       `json:"reasoningEfforts"`
	}
	if err := cli.Call(context.Background(), "providers.models", map[string]any{"provider": "codex"}, &res); err != nil {
		t.Fatalf("providers.models failed: %v", err)
	}
	for _, eff := range []string{"low", "medium", "high", "xhigh"} {
		if !slices.Contains(res.ReasoningEfforts, eff) {
			t.Fatalf("missing reasoning effort %q in %v", eff, res.ReasoningEfforts)
		}
	}
	var modeIDs []string
	for _, m := range res.AvailableModes {
		modeIDs = append(modeIDs, m.ID)
	}
	for _, reqMode := range []string{"plan", "auto", "full-access"} {
		if !slices.Contains(modeIDs, reqMode) {
			t.Fatalf("missing mode %q in availableModes %v", reqMode, modeIDs)
		}
	}
}

func TestTier1_F3_03_ProvidersUpsertCustomModelsPersistsAndSyncs(t *testing.T) {
	td := StartIsolatedDaemon(t, DaemonOpts{
		Env: map[string]string{"OPENROUTER_API_KEY": "sk-or-v1-f3-03"},
	})
	cliA := td.PairAndConnect(t, "T1-F3-03-Desktop", "linux")
	cliB := td.PairAndConnect(t, "T1-F3-03-Android", "android")

	if err := cliA.Call(context.Background(), "providers.upsert", map[string]any{
		"id":      "codex",
		"name":    "Codex",
		"driver":  "codex",
		"enabled": true,
		"customModels": []map[string]string{
			{"slug": "qwen/qwen3-coder", "name": "Qwen 3 Coder"},
		},
	}, nil); err != nil {
		t.Fatalf("providers.upsert customModels failed: %v", err)
	}

	provBytes, err := os.ReadFile(filepath.Join(td.ConfigHome, "agentc", "providers.json"))
	if err != nil {
		t.Fatalf("read providers.json: %v", err)
	}
	if !strings.Contains(string(provBytes), "qwen/qwen3-coder") {
		t.Fatalf("providers.json missing custom model slug: %s", string(provBytes))
	}

	var catB struct {
		CustomModels []struct {
			Slug string `json:"slug"`
			Name string `json:"name"`
		} `json:"customModels"`
		Models []ModelInfoWire `json:"models"`
	}
	if err := cliB.Call(context.Background(), "providers.models", map[string]any{"provider": "codex"}, &catB); err != nil {
		t.Fatalf("cliB providers.models failed: %v", err)
	}
	var inCustom bool
	for _, cm := range catB.CustomModels {
		if cm.Slug == "qwen/qwen3-coder" {
			inCustom = true
			break
		}
	}
	if !inCustom {
		t.Fatalf("expected qwen/qwen3-coder in cliB customModels, got %+v", catB.CustomModels)
	}
}

func TestTier1_F3_04_ProvidersUpsertDefaultModelReflectedInCatalog(t *testing.T) {
	td := StartIsolatedDaemon(t, DaemonOpts{
		Env: map[string]string{"OPENROUTER_API_KEY": "sk-or-v1-f3-04"},
	})
	cli := td.PairAndConnect(t, "T1-F3-04", "linux")

	if err := cli.Call(context.Background(), "providers.upsert", map[string]any{
		"id":           "codex",
		"name":         "Codex",
		"driver":       "codex",
		"enabled":      true,
		"defaultModel": "google/gemini-3-flash-preview",
	}, nil); err != nil {
		t.Fatalf("providers.upsert defaultModel failed: %v", err)
	}

	var cat struct {
		DefaultModelID string `json:"defaultModelId"`
	}
	if err := cli.Call(context.Background(), "providers.models", map[string]any{"provider": "codex"}, &cat); err != nil {
		t.Fatalf("providers.models failed: %v", err)
	}
	if cat.DefaultModelID != "google/gemini-3-flash-preview" {
		t.Fatalf("expected defaultModelId='google/gemini-3-flash-preview', got %q", cat.DefaultModelID)
	}

	repo := newTempGitRepo(t, nil)
	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(context.Background(), "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
	}, &created); err != nil {
		t.Fatalf("session.create failed: %v", err)
	}
	if created.Session.Model != "google/gemini-3-flash-preview" {
		t.Fatalf("expected new codex session to inherit defaultModel 'google/gemini-3-flash-preview', got %q", created.Session.Model)
	}
}

func TestTier1_F3_05_GeneratedModelCatalogJSONPassedToCodex(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T1-F3-05", "linux")
	repo := newTempGitRepo(t, nil)

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(context.Background(), "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "google/gemini-3-flash-preview",
	}, &created); err != nil {
		t.Fatalf("session.create failed: %v", err)
	}
	sid := created.Session.ID
	if err := cli.Call(context.Background(), "session.subscribe", map[string]any{"id": sid, "since": 0}, nil); err != nil {
		t.Fatalf("session.subscribe failed: %v", err)
	}
	if err := cli.Call(context.Background(), "session.prompt", map[string]any{
		"id":   sid,
		"text": "Check catalog [inspect]",
	}, nil); err != nil {
		t.Fatalf("session.prompt failed: %v", err)
	}
	_, items, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second)
	if err != nil {
		t.Fatalf("WaitForTurnIdle failed: %v", err)
	}
	insp, ok := extractInspectJSON(items)
	if !ok {
		t.Fatalf("missing INSPECT_JSON in assistant reply: %s", collectAgentText(items))
	}
	catPath, _ := insp["modelCatalogPath"].(string)
	if catPath == "" {
		t.Fatalf("expected non-empty modelCatalogPath in inspect payload: %+v", insp)
	}
	if strings.HasPrefix(catPath, "/tmp/") {
		t.Fatalf("model_catalog_json must not be placed in /tmp; got %q", catPath)
	}
	if hasMod, _ := insp["catalogHasModel"].(bool); !hasMod {
		t.Fatalf("expected catalogHasModel=true for google/gemini-3-flash-preview, got %+v", insp)
	}
}

// ============================================================================
// F4: Arbitrary & Future OpenRouter Model Slug Selection (5 tests)
// ============================================================================

func TestTier1_F4_01_SessionCreateWithCatalogedOpenRouterSlug(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T1-F4-01", "linux")
	repo := newTempGitRepo(t, nil)

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(context.Background(), "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "google/gemini-3-flash-preview",
	}, &created); err != nil {
		t.Fatalf("session.create failed: %v", err)
	}
	if created.Session.Model != "google/gemini-3-flash-preview" {
		t.Fatalf("Summary.Model=%q, want 'google/gemini-3-flash-preview'", created.Session.Model)
	}
	sid := created.Session.ID
	_ = cli.Call(context.Background(), "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	if err := cli.Call(context.Background(), "session.prompt", map[string]any{"id": sid, "text": "Verify [inspect]"}, nil); err != nil {
		t.Fatalf("session.prompt: %v", err)
	}
	_, items, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second)
	if err != nil {
		t.Fatalf("WaitForTurnIdle: %v", err)
	}
	insp, ok := extractInspectJSON(items)
	if !ok {
		t.Fatalf("expected INSPECT_JSON")
	}
	if mp, _ := insp["modelProvider"].(string); mp != "openrouter" {
		t.Fatalf("expected modelProvider='openrouter', got %q", mp)
	}
}

func TestTier1_F4_02_SessionCreateWithUnlistedFutureOpenRouterSlug(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T1-F4-02", "linux")
	repo := newTempGitRepo(t, nil)

	futureSlug := "x-ai/grok-9-future-2027"
	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(context.Background(), "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": futureSlug,
	}, &created); err != nil {
		t.Fatalf("session.create with unlisted future slug failed: %v", err)
	}
	if created.Session.Model != futureSlug {
		t.Fatalf("expected Summary.Model=%q, got %q", futureSlug, created.Session.Model)
	}

	sid := created.Session.ID
	if err := cli.Call(context.Background(), "session.subscribe", map[string]any{"id": sid, "since": 0}, nil); err != nil {
		t.Fatalf("session.subscribe: %v", err)
	}
	if err := cli.Call(context.Background(), "session.prompt", map[string]any{"id": sid, "text": "Future model [inspect]"}, nil); err != nil {
		t.Fatalf("session.prompt: %v", err)
	}
	sum, items, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second)
	if err != nil {
		t.Fatalf("WaitForTurnIdle: %v", err)
	}
	if sum.Model != futureSlug {
		t.Fatalf("after turn Summary.Model=%q, want %q", sum.Model, futureSlug)
	}
	insp, ok := extractInspectJSON(items)
	if !ok {
		t.Fatalf("missing INSPECT_JSON")
	}
	if hasMod, _ := insp["catalogHasModel"].(bool); !hasMod {
		t.Fatalf("expected unlisted future slug %q to be dynamically injected into model_catalog_json; inspect=%+v", futureSlug, insp)
	}
}

func TestTier1_F4_03_SessionCreateWithOpenRouterPrefixedSlugNormalized(t *testing.T) {
	suiteMockOR.ClearRequests()
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T1-F4-03", "linux")
	repo := newTempGitRepo(t, nil)

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(context.Background(), "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "openrouter/google/gemini-2.5-flash",
	}, &created); err != nil {
		t.Fatalf("session.create failed: %v", err)
	}
	sid := created.Session.ID
	_ = cli.Call(context.Background(), "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	if err := cli.Call(context.Background(), "session.prompt", map[string]any{
		"id":   sid,
		"text": "Prefix normalization check [bridge-probe]",
	}, nil); err != nil {
		t.Fatalf("session.prompt: %v", err)
	}
	if _, _, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second); err != nil {
		t.Fatalf("WaitForTurnIdle: %v", err)
	}

	reqs := suiteMockOR.ResponsesRequests()
	var sawNormalized bool
	for _, r := range reqs {
		if strings.Contains(r.PromptText, "Prefix normalization check") {
			if r.Model != "google/gemini-2.5-flash" {
				t.Fatalf("expected OpenRouter wire model normalized to 'google/gemini-2.5-flash', got %q", r.Model)
			}
			sawNormalized = true
		}
	}
	if !sawNormalized {
		t.Fatalf("did not observe outbound /responses request for openrouter/ prefixed slug")
	}
}

func TestTier1_F4_04_SessionSetModelLiveSessionUpdatesArbitrarySlug(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T1-F4-04", "linux")
	repo := newTempGitRepo(t, nil)

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(context.Background(), "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "google/gemini-3-flash-preview",
	}, &created); err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sid := created.Session.ID
	_ = cli.Call(context.Background(), "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	_ = cli.Call(context.Background(), "session.prompt", map[string]any{"id": sid, "text": "Turn 1"}, nil)
	if _, _, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second); err != nil {
		t.Fatalf("Turn 1 failed: %v", err)
	}

	newSlug := "deepseek/deepseek-r2-preview"
	var updated SessionSummaryWire
	if err := cli.Call(context.Background(), "session.setModel", map[string]any{
		"id":    sid,
		"model": newSlug,
	}, &updated); err != nil {
		t.Fatalf("session.setModel failed: %v", err)
	}
	if updated.Model != newSlug {
		t.Fatalf("session.setModel returned model=%q, want %q", updated.Model, newSlug)
	}

	_ = cli.Call(context.Background(), "session.prompt", map[string]any{"id": sid, "text": "Turn 2 [inspect]"}, nil)
	_, items, err := cli.WaitForTurnIdle(sid, 2, 12*time.Second)
	if err != nil {
		t.Fatalf("Turn 2 failed: %v", err)
	}
	insp, ok := extractInspectJSON(items)
	if !ok {
		t.Fatalf("missing INSPECT_JSON on Turn 2")
	}
	if gotMod, _ := insp["model"].(string); !strings.Contains(gotMod, "deepseek-r2-preview") {
		t.Fatalf("expected codex Turn 2 model to be %q, got %q", newSlug, gotMod)
	}
}

func TestTier1_F4_05_SessionSetModelSleepingSessionPersistsAcrossWake(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T1-F4-05", "linux")
	repo := newTempGitRepo(t, nil)

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(context.Background(), "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "google/gemini-3-flash-preview",
	}, &created); err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sid := created.Session.ID
	_ = cli.Call(context.Background(), "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	_ = cli.Call(context.Background(), "session.prompt", map[string]any{"id": sid, "text": "Before sleep"}, nil)
	if _, _, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second); err != nil {
		t.Fatalf("Turn 1: %v", err)
	}

	if err := cli.Call(context.Background(), "session.stop", map[string]any{"id": sid}, nil); err != nil {
		t.Fatalf("session.stop: %v", err)
	}
	if _, _, err := cli.WaitForSession(sid, 8*time.Second, func(sum SessionSummaryWire, _ *SessionDetailWire, _ []TranscriptItemWire) bool {
		return !sum.Live || sum.State == "sleeping"
	}); err != nil {
		t.Fatalf("wait sleeping: %v", err)
	}

	sleepSlug := "mistralai/codestral-2027"
	var updated SessionSummaryWire
	if err := cli.Call(context.Background(), "session.setModel", map[string]any{
		"id":    sid,
		"model": sleepSlug,
	}, &updated); err != nil {
		t.Fatalf("session.setModel while sleeping: %v", err)
	}
	if updated.Model != sleepSlug {
		t.Fatalf("sleeping session.setModel returned model=%q, want %q", updated.Model, sleepSlug)
	}

	_ = cli.Call(context.Background(), "session.prompt", map[string]any{"id": sid, "text": "Wake up [inspect]"}, nil)
	sum, items, err := cli.WaitForTurnIdle(sid, 2, 12*time.Second)
	if err != nil {
		t.Fatalf("Wake Turn 2: %v", err)
	}
	if sum.Model != sleepSlug {
		t.Fatalf("after wake Summary.Model=%q, want %q", sum.Model, sleepSlug)
	}
	insp, ok := extractInspectJSON(items)
	if !ok {
		t.Fatalf("missing INSPECT_JSON after wake")
	}
	if gotMod, _ := insp["model"].(string); !strings.Contains(gotMod, "codestral-2027") {
		t.Fatalf("woken codex received model=%q, want %q", gotMod, sleepSlug)
	}
}

// ============================================================================
// F5: Streaming, Reasoning Effort, Modes, Telemetry & Bridge Cap (6 tests)
// ============================================================================

func TestTier1_F5_01_StreamingAgentMessageDeltasCoalescedToTranscript(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T1-F5-01", "linux")
	repo := newTempGitRepo(t, nil)

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(context.Background(), "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "google/gemini-3-flash-preview",
	}, &created); err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sid := created.Session.ID
	marker := cli.EventMarker()
	_ = cli.Call(context.Background(), "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	_ = cli.Call(context.Background(), "session.prompt", map[string]any{
		"id":   sid,
		"text": "Stream chunks [slow:240]",
	}, nil)

	sum, items, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second)
	if err != nil {
		t.Fatalf("WaitForTurnIdle: %v", err)
	}
	agentTxt := collectAgentText(items)
	for _, want := range []string{"chunk-1", "chunk-8", "Completed codex turn for: Stream chunks [slow:240]"} {
		if !strings.Contains(agentTxt, want) {
			t.Fatalf("assembled agent transcript missing %q; got %q", want, agentTxt)
		}
	}
	if sum.LastText == "" {
		t.Fatalf("expected non-empty Summary.LastText after streaming turn")
	}
	evs := cli.EventsSince(marker)
	var opsCount int
	for _, ev := range evs {
		if ev.Event == "session.ops" {
			opsCount++
		}
	}
	if opsCount < 2 {
		t.Fatalf("expected multiple incremental session.ops events during streaming, got %d", opsCount)
	}
}

func TestTier1_F5_02_StreamingReasoningDeltasProduceThoughtItem(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T1-F5-02", "linux")
	repo := newTempGitRepo(t, nil)

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(context.Background(), "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "google/gemini-3-flash-preview",
	}, &created); err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sid := created.Session.ID
	_ = cli.Call(context.Background(), "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	_ = cli.Call(context.Background(), "session.prompt", map[string]any{"id": sid, "text": "Think carefully"}, nil)
	_, items, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second)
	if err != nil {
		t.Fatalf("WaitForTurnIdle: %v", err)
	}

	thoughtIdx, agentIdx := -1, -1
	for i, it := range items {
		if it.Kind == "thought" && thoughtIdx < 0 {
			thoughtIdx = i
		}
		if it.Kind == "agent" && agentIdx < 0 {
			agentIdx = i
		}
	}
	if thoughtIdx < 0 {
		t.Fatalf("expected kind='thought' transcript item from reasoning deltas; items=%+v", items)
	}
	if agentIdx < 0 || thoughtIdx > agentIdx {
		t.Fatalf("expected thought item (idx=%d) before agent item (idx=%d)", thoughtIdx, agentIdx)
	}
	if thoughtTxt := collectThoughtText(items); !strings.Contains(thoughtTxt, "Analyzing request") {
		t.Fatalf("unexpected thought text %q", thoughtTxt)
	}
}

func TestTier1_F5_03_SetConfigThoughtLevelAndThoughtAlias(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T1-F5-03", "linux")
	repo := newTempGitRepo(t, nil)

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(context.Background(), "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "google/gemini-3-flash-preview",
	}, &created); err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sid := created.Session.ID
	_ = cli.Call(context.Background(), "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)

	var sum1 SessionSummaryWire
	if err := cli.Call(context.Background(), "session.setConfig", map[string]any{
		"id":       sid,
		"configId": "thought_level",
		"value":    "high",
	}, &sum1); err != nil {
		t.Fatalf("session.setConfig(thought_level=high) failed: %v", err)
	}
	if sum1.Thought != "high" {
		t.Fatalf("Summary.Thought=%q, want 'high'", sum1.Thought)
	}
	_ = cli.Call(context.Background(), "session.prompt", map[string]any{"id": sid, "text": "Check high [inspect]"}, nil)
	_, items1, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second)
	if err != nil {
		t.Fatalf("Turn 1: %v", err)
	}
	if insp, ok := extractInspectJSON(items1); !ok || insp["effort"] != "high" {
		t.Fatalf("expected effort='high' in codex turn/start, got %+v", insp)
	}

	var sum2 SessionSummaryWire
	if err := cli.Call(context.Background(), "session.setConfig", map[string]any{
		"id":       sid,
		"configId": "thought",
		"value":    "xhigh",
	}, &sum2); err != nil {
		t.Fatalf("session.setConfig(thought=xhigh) alias failed: %v", err)
	}
	if sum2.Thought != "xhigh" {
		t.Fatalf("Summary.Thought=%q, want 'xhigh'", sum2.Thought)
	}
	_ = cli.Call(context.Background(), "session.prompt", map[string]any{"id": sid, "text": "Check xhigh [inspect]"}, nil)
	_, items2, err := cli.WaitForTurnIdle(sid, 2, 12*time.Second)
	if err != nil {
		t.Fatalf("Turn 2: %v", err)
	}
	if insp, ok := extractInspectJSON(items2); !ok || insp["effort"] != "xhigh" {
		t.Fatalf("expected effort='xhigh' via 'thought' alias, got %+v", insp)
	}
}

func TestTier1_F5_04_SetModeBuildPlanAutoFullAccessMapping(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T1-F5-04", "linux")
	repo := newTempGitRepo(t, nil)

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(context.Background(), "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "google/gemini-3-flash-preview",
	}, &created); err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sid := created.Session.ID
	_ = cli.Call(context.Background(), "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)

	modes := []string{"plan", "acceptEdits", "auto", "full-access", "default"}
	for i, mode := range modes {
		var updated SessionSummaryWire
		if err := cli.Call(context.Background(), "session.setMode", map[string]any{
			"id":   sid,
			"mode": mode,
		}, &updated); err != nil {
			t.Fatalf("session.setMode(%q) failed: %v", mode, err)
		}
		if updated.Mode == "" {
			t.Fatalf("expected non-empty Summary.Mode after setMode(%q)", mode)
		}
		if mode == "plan" || mode == "full-access" {
			turnNum := 1
			if mode == "full-access" {
				turnNum = 2
			}
			_ = i
			_ = cli.Call(context.Background(), "session.prompt", map[string]any{
				"id":   sid,
				"text": "Mode check " + mode + " [inspect]",
			}, nil)
			_, items, err := cli.WaitForTurnIdle(sid, turnNum, 12*time.Second)
			if err != nil {
				t.Fatalf("turn in mode %q failed: %v", mode, err)
			}
			insp, ok := extractInspectJSON(items)
			if !ok {
				t.Fatalf("missing INSPECT_JSON in mode %q", mode)
			}
			if mode == "plan" {
				if cm, _ := insp["collaborationMode"].(string); cm != "plan" {
					t.Fatalf("expected collaborationMode='plan' in plan mode, got %+v", insp)
				}
			}
			if mode == "full-access" {
				sb, _ := insp["sandbox"].(string)
				if !strings.Contains(strings.ToLower(sb), "full") && !strings.Contains(strings.ToLower(sb), "danger") {
					t.Fatalf("expected danger-full-access sandbox in full-access mode, got %q", sb)
				}
			}
		}
	}
}

func TestTier1_F5_05_TokenUsageTelemetryAndUsageLedger(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T1-F5-05", "linux")
	repo := newTempGitRepo(t, nil)

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(context.Background(), "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "google/gemini-3-flash-preview",
	}, &created); err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sid := created.Session.ID
	marker := cli.EventMarker()
	_ = cli.Call(context.Background(), "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	_ = cli.Call(context.Background(), "session.prompt", map[string]any{"id": sid, "text": "Telemetry check"}, nil)

	sum, _, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second)
	if err != nil {
		t.Fatalf("WaitForTurnIdle: %v", err)
	}
	if sum.Usage.InputTokens < 1500 || sum.Usage.CachedReadTokens < 800 || sum.Usage.ThoughtTokens < 35 || sum.Usage.OutputTokens < 90 || sum.Usage.TotalTokens < 1590 {
		t.Fatalf("unexpected token usage telemetry in SessionSummary: %+v", sum.Usage)
	}

	var sawUsageEvent bool
	for _, ev := range cli.EventsSince(marker) {
		if ev.Event == "session.usage" {
			sawUsageEvent = true
			break
		}
	}
	if !sawUsageEvent && sum.ContextSize == 0 {
		t.Fatalf("expected session.usage event or ContextSize > 0")
	}

	var ledger struct {
		TotalTokens int64 `json:"totalTokens"`
		ByAgent     []struct {
			Key         string `json:"key"`
			TotalTokens int64  `json:"totalTokens"`
		} `json:"byAgent"`
	}
	if err := cli.Call(context.Background(), "usage.summary", map[string]any{"days": 7}, &ledger); err != nil {
		t.Fatalf("usage.summary: %v", err)
	}
	var foundCodex bool
	for _, b := range ledger.ByAgent {
		if b.Key == "codex" && b.TotalTokens > 0 {
			foundCodex = true
			break
		}
	}
	if !foundCodex {
		t.Fatalf("expected codex token bucket in usage.summary, got %+v", ledger)
	}
}

func TestTier1_F5_06_LoopbackBridgeCapsMaxOutputTokensAndInjectsBearer(t *testing.T) {
	suiteMockOR.ResetOverrides()
	suiteMockOR.ClearRequests()
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T1-F5-06", "linux")
	repo := newTempGitRepo(t, nil)

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(context.Background(), "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "google/gemini-3-flash-preview",
	}, &created); err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sid := created.Session.ID
	_ = cli.Call(context.Background(), "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	_ = cli.Call(context.Background(), "session.prompt", map[string]any{
		"id":   sid,
		"text": "Cap check [bridge-probe]",
	}, nil)
	if _, _, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second); err != nil {
		t.Fatalf("WaitForTurnIdle: %v", err)
	}

	reqs := suiteMockOR.ResponsesRequests()
	var verified bool
	for _, r := range reqs {
		if strings.Contains(r.PromptText, "Cap check [bridge-probe]") {
			if r.AuthHeader != "Bearer "+hermeticSharedSecret {
				t.Fatalf("expected Bearer injection on loopback bridge, got %q", r.AuthHeader)
			}
			if r.MaxOutputTokens != 8192 {
				t.Fatalf("expected max_output_tokens capped from 65536 to 8192, got %d", r.MaxOutputTokens)
			}
			verified = true
		}
	}
	if !verified {
		t.Fatalf("did not capture expected /responses request on MockOpenRouterServer")
	}
}

// ============================================================================
// F6: Tool Execution, apply_patch Bridge, File Edit Diffs & Checkpoints (6 tests)
// ============================================================================

func TestTier1_F6_01_CommandExecutionStreamingToolCallAndOutput(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T1-F6-01", "linux")
	repo := newTempGitRepo(t, nil)

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(context.Background(), "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "google/gemini-3-flash-preview",
	}, &created); err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sid := created.Session.ID
	_ = cli.Call(context.Background(), "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	_ = cli.Call(context.Background(), "session.prompt", map[string]any{
		"id":   sid,
		"text": "Run command [cmd:echo codex_exec_ok_77]",
	}, nil)

	_, items, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second)
	if err != nil {
		t.Fatalf("WaitForTurnIdle: %v", err)
	}
	var sawExecTool bool
	for _, it := range items {
		if it.Kind == "tool" && it.Tool != nil {
			rawTool, _ := json.Marshal(it.Tool)
			if it.Tool.Status == "completed" && strings.Contains(string(rawTool), "codex_exec_ok_77") {
				sawExecTool = true
				break
			}
		}
	}
	if !sawExecTool {
		t.Fatalf("expected completed tool call containing stdout 'codex_exec_ok_77'; items=%+v", items)
	}
}

func TestTier1_F6_02_ApplyPatchFunctionBridgeForNonOpenAIModels(t *testing.T) {
	suiteMockOR.ResetOverrides()
	suiteMockOR.ClearRequests()
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T1-F6-02", "linux")
	repo := newTempGitRepo(t, nil)

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(context.Background(), "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "google/gemini-2.5-flash",
	}, &created); err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sid := created.Session.ID
	_ = cli.Call(context.Background(), "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	_ = cli.Call(context.Background(), "session.prompt", map[string]any{
		"id":   sid,
		"text": "Apply patch via bridge [bridge-patch]",
	}, nil)

	_, items, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second)
	if err != nil {
		t.Fatalf("WaitForTurnIdle: %v", err)
	}

	reqs := suiteMockOR.ResponsesRequests()
	var sawFunctionRewrite bool
	for _, r := range reqs {
		if strings.Contains(r.PromptText, "[bridge-patch]") {
			for _, tl := range r.Tools {
				if tl["name"] == "apply_patch" && tl["type"] == "function" {
					sawFunctionRewrite = true
				}
			}
		}
	}
	if !sawFunctionRewrite {
		t.Fatalf("expected custom:apply_patch rewritten to function:apply_patch on non-OpenAI model google/gemini-2.5-flash")
	}

	agentTxt := collectAgentText(items)
	if !strings.Contains(agentTxt, "BRIDGED_TOOL_TYPE:custom_tool_call") {
		t.Fatalf("expected bridge SSE response to rewrite function_call -> custom_tool_call for codex; got %q", agentTxt)
	}
	patchedBytes, err := os.ReadFile(filepath.Join(repo, "bridged_patch.txt"))
	if err != nil || !strings.Contains(string(patchedBytes), "patched via openrouter bridge") {
		t.Fatalf("expected bridged_patch.txt written in workspace; err=%v content=%q", err, string(patchedBytes))
	}
}

func TestTier1_F6_03_FileChangeAndTurnDiffUpdatedAttachInlineDiff(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T1-F6-03", "linux")
	repo := newTempGitRepo(t, nil)

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(context.Background(), "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "google/gemini-3-flash-preview",
	}, &created); err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sid := created.Session.ID
	_ = cli.Call(context.Background(), "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	_ = cli.Call(context.Background(), "session.prompt", map[string]any{
		"id":   sid,
		"text": `Create hello.go [edit:hello.go:package main\nfunc main() {}\n]`,
	}, nil)

	_, items, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second)
	if err != nil {
		t.Fatalf("WaitForTurnIdle: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "hello.go")); err != nil {
		t.Fatalf("hello.go missing on disk: %v", err)
	}

	var sawEditTool bool
	for _, it := range items {
		if it.Kind == "tool" && it.Tool != nil && (it.Tool.Kind == "edit" || strings.Contains(string(it.Tool.Content), "hello.go")) {
			sawEditTool = true
			break
		}
	}
	if !sawEditTool {
		t.Fatalf("expected edit tool item in transcript; items=%+v", items)
	}

	var diffRes struct {
		Repo  bool `json:"repo"`
		Files []struct {
			Path string `json:"path"`
		} `json:"files"`
	}
	if err := cli.Call(context.Background(), "session.diff", map[string]any{"id": sid}, &diffRes); err != nil {
		t.Fatalf("session.diff: %v", err)
	}
	if !diffRes.Repo {
		t.Fatalf("expected session.diff repo=true")
	}
	var foundFile bool
	for _, f := range diffRes.Files {
		if f.Path == "hello.go" {
			foundFile = true
		}
	}
	if !foundFile {
		t.Fatalf("session.diff files missing hello.go: %+v", diffRes.Files)
	}
}

func TestTier1_F6_04_PerTurnGitCheckpointsAndSessionTurnDiff(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T1-F6-04", "linux")
	repo := newTempGitRepo(t, nil)

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(context.Background(), "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "google/gemini-3-flash-preview",
	}, &created); err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sid := created.Session.ID
	_ = cli.Call(context.Background(), "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	_ = cli.Call(context.Background(), "session.prompt", map[string]any{
		"id":   sid,
		"text": `Write checkpoint file [edit:cp_test.go:package cp\nconst X = 1\n]`,
	}, nil)

	sum, _, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second)
	if err != nil {
		t.Fatalf("WaitForTurnIdle: %v", err)
	}
	if sum.LastCheckpoint == nil || sum.LastCheckpoint.TurnIndex != 1 || sum.LastCheckpoint.TotalAdd <= 0 {
		t.Fatalf("expected TurnCheckpoint(turnIndex=1, totalAdd>0), got %+v", sum.LastCheckpoint)
	}

	var tdRes struct {
		TotalAdd int `json:"totalAdd"`
		Files    []struct {
			Path string `json:"path"`
		} `json:"files"`
	}
	if err := cli.Call(context.Background(), "session.turnDiff", map[string]any{
		"id":    sid,
		"scope": "latest",
	}, &tdRes); err != nil {
		t.Fatalf("session.turnDiff: %v", err)
	}
	if tdRes.TotalAdd <= 0 || len(tdRes.Files) == 0 {
		t.Fatalf("expected cp_test.go in session.turnDiff(latest), got %+v", tdRes)
	}

	var chRes struct {
		Diffs []struct {
			Path string `json:"path"`
		} `json:"diffs"`
	}
	if err := cli.Call(context.Background(), "session.changes", map[string]any{"id": sid}, &chRes); err != nil {
		t.Fatalf("session.changes: %v", err)
	}
	if len(chRes.Diffs) == 0 {
		t.Fatalf("expected non-empty diffs in session.changes")
	}
}

func TestTier1_F6_05_GitScopedDiffAcrossUnstagedStagedAndBranch(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T1-F6-05", "linux")
	repo := newTempGitRepo(t, map[string]string{"tracked.txt": "line1\n"})

	cmd := exec.Command("git", "checkout", "-b", "feat/scoped")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git checkout -b: %v %s", err, string(out))
	}

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(context.Background(), "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "google/gemini-3-flash-preview",
	}, &created); err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sid := created.Session.ID
	_ = cli.Call(context.Background(), "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	_ = cli.Call(context.Background(), "session.prompt", map[string]any{
		"id":   sid,
		"text": `Edit tracked.txt [edit:tracked.txt:line1\nline2\n]`,
	}, nil)
	if _, _, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second); err != nil {
		t.Fatalf("WaitForTurnIdle: %v", err)
	}

	var unstaged struct {
		TotalAdd int `json:"totalAdd"`
	}
	if err := cli.Call(context.Background(), "git.scopedDiff", map[string]any{"path": repo, "scope": "unstaged"}, &unstaged); err != nil {
		t.Fatalf("git.scopedDiff(unstaged): %v", err)
	}
	if unstaged.TotalAdd <= 0 {
		t.Fatalf("expected unstaged additions > 0, got %+v", unstaged)
	}

	if err := cli.Call(context.Background(), "git.stage", map[string]any{"path": repo, "files": []string{"tracked.txt"}}, nil); err != nil {
		t.Fatalf("git.stage: %v", err)
	}
	var staged struct {
		TotalAdd int `json:"totalAdd"`
	}
	if err := cli.Call(context.Background(), "git.scopedDiff", map[string]any{"path": repo, "scope": "staged"}, &staged); err != nil {
		t.Fatalf("git.scopedDiff(staged): %v", err)
	}
	if staged.TotalAdd <= 0 {
		t.Fatalf("expected staged additions > 0, got %+v", staged)
	}

	if err := cli.Call(context.Background(), "git.commit", map[string]any{"path": repo, "message": "feat: add line2"}, nil); err != nil {
		t.Fatalf("git.commit: %v", err)
	}
	var branchDiff struct {
		TotalAdd int `json:"totalAdd"`
	}
	if err := cli.Call(context.Background(), "git.scopedDiff", map[string]any{"path": repo, "scope": "branch", "baseRef": "main"}, &branchDiff); err != nil {
		t.Fatalf("git.scopedDiff(branch): %v", err)
	}
	if branchDiff.TotalAdd <= 0 {
		t.Fatalf("expected branch additions > 0 vs main, got %+v", branchDiff)
	}
}

func TestTier1_F6_06_CodexHomePlacedOutsideTmpDirectory(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T1-F6-06", "linux")
	repo := newTempGitRepo(t, nil)

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(context.Background(), "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "google/gemini-3-flash-preview",
	}, &created); err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sid := created.Session.ID
	_ = cli.Call(context.Background(), "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	_ = cli.Call(context.Background(), "session.prompt", map[string]any{"id": sid, "text": "Inspect CODEX_HOME [inspect]"}, nil)
	_, items, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second)
	if err != nil {
		t.Fatalf("WaitForTurnIdle: %v", err)
	}
	insp, ok := extractInspectJSON(items)
	if !ok {
		t.Fatalf("missing INSPECT_JSON")
	}
	ch, _ := insp["codexHome"].(string)
	if ch == "" || strings.HasPrefix(ch, "/tmp") {
		t.Fatalf("CODEX_HOME must be non-empty and outside /tmp, got %q", ch)
	}
}

// ============================================================================
// F7: Interactive Approvals & Structured User Questions (5 tests)
// ============================================================================

func TestTier1_F7_01_CommandExecutionApprovalAcceptOnce(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T1-F7-01", "linux")
	repo := newTempGitRepo(t, nil)

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(context.Background(), "session.create", map[string]any{
		"agent":           "codex",
		"cwd":             repo,
		"model":           "google/gemini-3-flash-preview",
		"mode":            "default",
		"skipPermissions": false,
	}, &created); err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sid := created.Session.ID
	marker := cli.EventMarker()
	_ = cli.Call(context.Background(), "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	_ = cli.Call(context.Background(), "session.prompt", map[string]any{
		"id":   sid,
		"text": "Need approval [approve-cmd:echo approved_once > cmd_approved.txt]",
	}, nil)

	permItem, err := cli.WaitForPendingPerm(sid, 10*time.Second)
	if err != nil {
		t.Fatalf("WaitForPendingPerm: %v", err)
	}
	optID := PickPermOptionID(permItem.Perm, "allow_once")
	if err := cli.Call(context.Background(), "session.permission", map[string]any{
		"id":       sid,
		"reqId":    permItem.Perm.ReqID,
		"optionId": optID,
	}, nil); err != nil {
		t.Fatalf("session.permission: %v", err)
	}

	_, items, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second)
	if err != nil {
		t.Fatalf("WaitForTurnIdle: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "cmd_approved.txt")); err != nil {
		t.Fatalf("approved command did not create cmd_approved.txt: %v", err)
	}
	var resolved bool
	for _, it := range items {
		if it.Kind == "perm" && it.Perm != nil && it.Perm.ReqID == permItem.Perm.ReqID && it.State == "resolved" {
			resolved = true
		}
	}
	if !resolved {
		t.Fatalf("expected Perm item state='resolved' after session.permission")
	}
	var sawNotify bool
	for _, ev := range cli.EventsSince(marker) {
		if ev.Event == "notify" && strings.Contains(string(ev.Data), "permission") {
			sawNotify = true
		}
	}
	if !sawNotify {
		t.Fatalf("expected notify(kind='permission') push event")
	}
}

func TestTier1_F7_02_CommandExecutionApprovalAcceptForSession(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T1-F7-02", "linux")
	repo := newTempGitRepo(t, nil)

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(context.Background(), "session.create", map[string]any{
		"agent":           "codex",
		"cwd":             repo,
		"model":           "google/gemini-3-flash-preview",
		"mode":            "default",
		"skipPermissions": false,
	}, &created); err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sid := created.Session.ID
	_ = cli.Call(context.Background(), "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	_ = cli.Call(context.Background(), "session.prompt", map[string]any{
		"id":   sid,
		"text": "Session approval [approve-cmd:echo always_ok]",
	}, nil)

	permItem, err := cli.WaitForPendingPerm(sid, 10*time.Second)
	if err != nil {
		t.Fatalf("WaitForPendingPerm: %v", err)
	}
	optID := PickPermOptionID(permItem.Perm, "allow_always")
	if err := cli.Call(context.Background(), "session.permission", map[string]any{
		"id":       sid,
		"reqId":    permItem.Perm.ReqID,
		"optionId": optID,
	}, nil); err != nil {
		t.Fatalf("session.permission: %v", err)
	}

	_, items, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second)
	if err != nil {
		t.Fatalf("WaitForTurnIdle: %v", err)
	}
	agentTxt := collectAgentText(items)
	if !strings.Contains(agentTxt, "APPROVAL_DECISION:acceptForSession") {
		t.Fatalf("expected decision='acceptForSession' forwarded to codex app-server; got %q", agentTxt)
	}
}

func TestTier1_F7_03_FileChangeApprovalAcceptWritesFile(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T1-F7-03", "linux")
	repo := newTempGitRepo(t, nil)

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(context.Background(), "session.create", map[string]any{
		"agent":           "codex",
		"cwd":             repo,
		"model":           "google/gemini-3-flash-preview",
		"mode":            "default",
		"skipPermissions": false,
	}, &created); err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sid := created.Session.ID
	_ = cli.Call(context.Background(), "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	_ = cli.Call(context.Background(), "session.prompt", map[string]any{
		"id":   sid,
		"text": `Patch approval [approve-patch:patched.txt:approved line\n]`,
	}, nil)

	permItem, err := cli.WaitForPendingPerm(sid, 10*time.Second)
	if err != nil {
		t.Fatalf("WaitForPendingPerm: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "patched.txt")); err == nil {
		t.Fatalf("patched.txt should not exist before approval")
	}

	optID := PickPermOptionID(permItem.Perm, "allow_once")
	if err := cli.Call(context.Background(), "session.permission", map[string]any{
		"id":       sid,
		"reqId":    permItem.Perm.ReqID,
		"optionId": optID,
	}, nil); err != nil {
		t.Fatalf("session.permission: %v", err)
	}

	if _, _, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second); err != nil {
		t.Fatalf("WaitForTurnIdle: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(repo, "patched.txt"))
	if err != nil || !strings.Contains(string(b), "approved line") {
		t.Fatalf("expected patched.txt written after approval, err=%v content=%q", err, string(b))
	}
}

func TestTier1_F7_04_RequestUserInputStructuredQuestionRoundTrip(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T1-F7-04", "linux")
	repo := newTempGitRepo(t, nil)

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(context.Background(), "session.create", map[string]any{
		"agent":           "codex",
		"cwd":             repo,
		"model":           "google/gemini-3-flash-preview",
		"skipPermissions": false,
	}, &created); err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sid := created.Session.ID
	_ = cli.Call(context.Background(), "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	_ = cli.Call(context.Background(), "session.prompt", map[string]any{
		"id":   sid,
		"text": "Ask structured question [question]",
	}, nil)

	permItem, err := cli.WaitForPendingPerm(sid, 10*time.Second)
	if err != nil {
		t.Fatalf("WaitForPendingPerm: %v", err)
	}
	if permItem.Perm.Kind != "question" {
		t.Fatalf("expected Perm.Kind=='question', got %q", permItem.Perm.Kind)
	}

	optID := PickPermOptionID(permItem.Perm, "allow_once")
	if err := cli.Call(context.Background(), "session.permission", map[string]any{
		"id":       sid,
		"reqId":    permItem.Perm.ReqID,
		"optionId": optID,
		"answers": map[string]string{
			"q_env": "Staging",
		},
	}, nil); err != nil {
		t.Fatalf("session.permission(question): %v", err)
	}

	_, items, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second)
	if err != nil {
		t.Fatalf("WaitForTurnIdle: %v", err)
	}
	agentTxt := collectAgentText(items)
	if !strings.Contains(agentTxt, "QUESTION_ANSWER:") || !strings.Contains(agentTxt, "Staging") {
		t.Fatalf("expected structured answer 'Staging' echoed by codex, got %q", agentTxt)
	}
}

func TestTier1_F7_05_TurnPlanUpdatedRendersPlanTranscriptItem(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T1-F7-05", "linux")
	repo := newTempGitRepo(t, nil)

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(context.Background(), "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "google/gemini-3-flash-preview",
	}, &created); err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sid := created.Session.ID
	_ = cli.Call(context.Background(), "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	_ = cli.Call(context.Background(), "session.prompt", map[string]any{
		"id":   sid,
		"text": "Emit execution plan [plan]",
	}, nil)

	_, items, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second)
	if err != nil {
		t.Fatalf("WaitForTurnIdle: %v", err)
	}
	var sawPlan bool
	for _, it := range items {
		if it.Kind == "plan" && (len(it.Plan) > 2 || it.Text != "") {
			sawPlan = true
			break
		}
	}
	if !sawPlan {
		t.Fatalf("expected kind='plan' transcript item from turn/plan/updated; items=%+v", items)
	}
}

// ============================================================================
// F8: Session Lifecycle, Fork, Rewind, Aside, Cancel & Terminal (5 tests)
// ============================================================================

func TestTier1_F8_01_SessionPersistenceHibernateAndWakeResume(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T1-F8-01", "linux")
	repo := newTempGitRepo(t, nil)

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(context.Background(), "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "google/gemini-3-flash-preview",
	}, &created); err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sid := created.Session.ID
	_ = cli.Call(context.Background(), "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	_ = cli.Call(context.Background(), "session.prompt", map[string]any{"id": sid, "text": "Turn 1 marker_persist_1"}, nil)
	if _, _, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second); err != nil {
		t.Fatalf("Turn 1: %v", err)
	}

	if err := cli.Call(context.Background(), "session.stop", map[string]any{"id": sid}, nil); err != nil {
		t.Fatalf("session.stop: %v", err)
	}
	if _, _, err := cli.WaitForSession(sid, 8*time.Second, func(sum SessionSummaryWire, _ *SessionDetailWire, _ []TranscriptItemWire) bool {
		return !sum.Live || sum.State == "sleeping"
	}); err != nil {
		t.Fatalf("wait sleeping: %v", err)
	}

	if err := cli.Call(context.Background(), "session.wake", map[string]any{"id": sid}, nil); err != nil {
		t.Fatalf("session.wake: %v", err)
	}
	_ = cli.Call(context.Background(), "session.prompt", map[string]any{"id": sid, "text": "Turn 2 after wake [inspect]"}, nil)
	sum, items, err := cli.WaitForTurnIdle(sid, 2, 12*time.Second)
	if err != nil {
		t.Fatalf("Turn 2: %v", err)
	}
	if sum.Turns != 2 {
		t.Fatalf("expected Turns=2 after wake, got %d", sum.Turns)
	}
	if !strings.Contains(collectAgentText(items), "marker_persist_1") {
		t.Fatalf("prior Turn 1 transcript missing after hibernate/wake")
	}
	if insp, ok := extractInspectJSON(items); !ok || insp["resumed"] != true {
		t.Fatalf("expected codex thread/resume (resumed=true) on wake, got %+v", insp)
	}
}

func TestTier1_F8_02_SessionForkNativeCodexWithoutAgyLS(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T1-F8-02", "linux")
	repo := newTempGitRepo(t, nil)

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(context.Background(), "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "google/gemini-3-flash-preview",
	}, &created); err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sid := created.Session.ID
	_ = cli.Call(context.Background(), "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	_ = cli.Call(context.Background(), "session.prompt", map[string]any{"id": sid, "text": "Original turn before fork"}, nil)
	if _, _, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second); err != nil {
		t.Fatalf("Turn 1: %v", err)
	}

	var forked struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(context.Background(), "session.fork", map[string]any{"id": sid}, &forked); err != nil {
		t.Fatalf("session.fork on codex failed (must not fall back to agy LS): %v", err)
	}
	if forked.Session.ID == "" || forked.Session.ID == sid {
		t.Fatalf("unexpected forked session ID %q", forked.Session.ID)
	}
	if forked.Session.Agent != "codex" || forked.Session.ForkedFrom != sid {
		t.Fatalf("expected agent='codex' forkedFrom=%q, got %+v", sid, forked.Session)
	}

	// Also verify mid-conversation fork with explicit itemId (beforeTurnId)
	_ = cli.Call(context.Background(), "session.prompt", map[string]any{"id": sid, "text": "Second turn for mid-fork"}, nil)
	_, items2, err := cli.WaitForTurnIdle(sid, 2, 12*time.Second)
	if err != nil {
		t.Fatalf("Turn 2: %v", err)
	}
	var turn2UserID string
	for _, it := range items2 {
		if it.Kind == "user" && strings.Contains(it.Text, "Second turn for mid-fork") {
			turn2UserID = it.ID
		}
	}
	if turn2UserID == "" {
		t.Fatalf("failed to find Turn 2 user item ID in transcript")
	}
	var midFork struct {
		Session SessionSummaryWire `json:"session"`
		Prefill string             `json:"prefill"`
	}
	if err := cli.Call(context.Background(), "session.fork", map[string]any{"id": sid, "itemId": turn2UserID}, &midFork); err != nil {
		t.Fatalf("mid-conversation session.fork(itemId=%s) failed: %v", turn2UserID, err)
	}
	if midFork.Session.ID == "" || midFork.Session.Turns != 1 || !strings.Contains(midFork.Prefill, "Second turn for mid-fork") {
		t.Fatalf("unexpected mid-conversation fork result: %+v", midFork)
	}
}

func TestTier1_F8_03_SessionRewindRevertPreviewAndCheckpointRevert(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T1-F8-03", "linux")
	repo := newTempGitRepo(t, nil)

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(context.Background(), "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "google/gemini-3-flash-preview",
	}, &created); err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sid := created.Session.ID
	_ = cli.Call(context.Background(), "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	_ = cli.Call(context.Background(), "session.prompt", map[string]any{
		"id":   sid,
		"text": `Turn 1 [edit:rewind_me.txt:turn1\n]`,
	}, nil)
	if _, _, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second); err != nil {
		t.Fatalf("Turn 1: %v", err)
	}
	_ = cli.Call(context.Background(), "session.prompt", map[string]any{
		"id":   sid,
		"text": `Turn 2 [edit:rewind_me.txt:turn2\n]`,
	}, nil)
	_, items2, err := cli.WaitForTurnIdle(sid, 2, 12*time.Second)
	if err != nil {
		t.Fatalf("Turn 2: %v", err)
	}
	bTurn2, err := os.ReadFile(filepath.Join(repo, "rewind_me.txt"))
	if err != nil || string(bTurn2) != "turn2\n" {
		t.Fatalf("expected rewind_me.txt == 'turn2\\n' after Turn 2, got %q (err=%v)", string(bTurn2), err)
	}

	var turn2UserID string
	for _, it := range items2 {
		if it.Kind == "user" && strings.Contains(it.Text, "Turn 2") {
			turn2UserID = it.ID
		}
	}
	if turn2UserID == "" {
		t.Fatalf("failed to locate Turn 2 user transcript item ID in %+v", items2)
	}

	var prev struct {
		Preview struct {
			CanRewind    bool     `json:"canRewind"`
			FilesChanged []string `json:"filesChanged"`
		} `json:"preview"`
		StepIndex int `json:"stepIndex"`
	}
	if err := cli.Call(context.Background(), "session.revertPreview", map[string]any{
		"id":     sid,
		"itemId": turn2UserID,
	}, &prev); err != nil {
		t.Fatalf("session.revertPreview on codex failed: %v", err)
	}
	if !prev.Preview.CanRewind || !slices.Contains(prev.Preview.FilesChanged, "rewind_me.txt") {
		t.Fatalf("expected revertPreview to report canRewind=true and filesChanged containing rewind_me.txt, got %+v", prev)
	}

	var rew struct {
		Prefill string `json:"prefill"`
	}
	if err := cli.Call(context.Background(), "session.rewind", map[string]any{
		"id":     sid,
		"itemId": turn2UserID,
	}, &rew); err != nil {
		t.Fatalf("session.rewind on codex failed: %v", err)
	}
	if !strings.Contains(rew.Prefill, "Turn 2") {
		t.Fatalf("expected session.rewind prefill to contain Turn 2 prompt, got %q", rew.Prefill)
	}

	bRewound, err := os.ReadFile(filepath.Join(repo, "rewind_me.txt"))
	if err != nil || string(bRewound) != "turn1\n" {
		t.Fatalf("expected session.rewind to restore rewind_me.txt from 'turn2\\n' back to 'turn1\\n', got %q (err=%v)", string(bRewound), err)
	}
	rewoundSum, _, err := cli.WaitForSession(sid, 5*time.Second, func(sum SessionSummaryWire, _ *SessionDetailWire, _ []TranscriptItemWire) bool {
		return sum.Turns == 1
	})
	if err != nil || rewoundSum.Turns != 1 {
		t.Fatalf("expected Turns == 1 after session.rewind, got %d (err=%v)", rewoundSum.Turns, err)
	}

	if err := cli.Call(context.Background(), "session.checkpointRevert", map[string]any{
		"id":        sid,
		"turnIndex": 1,
		"toPre":     true,
	}, nil); err != nil {
		t.Fatalf("session.checkpointRevert: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "rewind_me.txt")); err == nil {
		t.Fatalf("expected rewind_me.txt removed after checkpointRevert(turnIndex=1, toPre=true)")
	}
}

func TestTier1_F8_04_SessionAsideBtwAndMidStreamCancel(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T1-F8-04", "linux")
	repo := newTempGitRepo(t, nil)

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(context.Background(), "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "google/gemini-3-flash-preview",
	}, &created); err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sid := created.Session.ID
	_ = cli.Call(context.Background(), "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	_ = cli.Call(context.Background(), "session.prompt", map[string]any{"id": sid, "text": "Initial turn"}, nil)
	if _, _, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second); err != nil {
		t.Fatalf("Turn 1: %v", err)
	}

	_ = callAndWaitAsideDone(t, cli, sid, "Side question via /btw", 12*time.Second)

	_ = cli.Call(context.Background(), "session.prompt", map[string]any{
		"id":   sid,
		"text": "Long running turn [slow:4000]",
	}, nil)
	if _, _, err := cli.WaitForSession(sid, 5*time.Second, func(sum SessionSummaryWire, _ *SessionDetailWire, items []TranscriptItemWire) bool {
		return sum.State == "running" && strings.Contains(collectAgentText(items), "chunk-1")
	}); err != nil {
		t.Fatalf("wait running + streaming chunk-1: %v", err)
	}
	if err := cli.Call(context.Background(), "session.cancel", map[string]any{"id": sid}, nil); err != nil {
		t.Fatalf("session.cancel: %v", err)
	}
	_, cancelItems, err := cli.WaitForSession(sid, 8*time.Second, func(sum SessionSummaryWire, _ *SessionDetailWire, _ []TranscriptItemWire) bool {
		return sum.State == "idle" && sum.Turns >= 2
	})
	if err != nil {
		t.Fatalf("expected session to return to idle after cancel: %v", err)
	}
	if strings.Contains(collectAgentText(cancelItems), "Completed codex turn for: Long running turn [slow:4000]") {
		t.Fatalf("expected mid-stream cancel to interrupt turn before final completion message")
	}
	var sawCancelledTurn bool
	for _, it := range cancelItems {
		if it.Kind == "turn" && it.Turn != nil && it.Turn.StopReason == "cancelled" {
			sawCancelledTurn = true
		}
	}
	if !sawCancelledTurn {
		t.Fatalf("expected turn item with stopReason == 'cancelled', items=%+v", cancelItems)
	}
}

func TestTier1_F8_05_OpenInTerminalAndTermCreateKindCodex(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T1-F8-05", "linux")
	repo := newTempGitRepo(t, nil)

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(context.Background(), "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "google/gemini-3-flash-preview",
	}, &created); err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sid := created.Session.ID
	_ = cli.Call(context.Background(), "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	_ = cli.Call(context.Background(), "session.prompt", map[string]any{"id": sid, "text": "Prepare thread for terminal"}, nil)
	if _, _, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second); err != nil {
		t.Fatalf("Turn 1: %v", err)
	}

	var openTerm struct {
		ID      string `json:"id"`
		Kind    string `json:"kind"`
		Command string `json:"command"`
	}
	if err := cli.Call(context.Background(), "session.openInTerminal", map[string]any{
		"id":   sid,
		"cols": 120,
		"rows": 40,
	}, &openTerm); err != nil {
		t.Fatalf("session.openInTerminal failed: %v", err)
	}
	defer func() {
		_ = cli.Call(context.Background(), "term.kill", map[string]any{"id": openTerm.ID}, nil)
	}()
	if openTerm.Kind != "codex" {
		t.Fatalf("expected openInTerminal Kind='codex', got %q (cmd=%q)", openTerm.Kind, openTerm.Command)
	}

	waitForTmuxCapture(t, cli, openTerm.ID, "FAKEX_TUI_READY")

	var directTerm struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
	}
	if err := cli.Call(context.Background(), "term.create", map[string]any{
		"kind": "codex",
		"cwd":  repo,
		"cols": 120,
		"rows": 40,
	}, &directTerm); err != nil {
		t.Fatalf("term.create(kind=codex) failed: %v", err)
	}
	defer func() {
		_ = cli.Call(context.Background(), "term.kill", map[string]any{"id": directTerm.ID}, nil)
	}()
	if directTerm.Kind != "codex" {
		t.Fatalf("expected term.create Kind='codex', got %q", directTerm.Kind)
	}
	waitForTmuxCapture(t, cli, directTerm.ID, "FAKEX_TUI_READY")
}

func waitForTmuxCapture(t *testing.T, cli *NoiseRPCClient, termID, substr string) string {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		var capRes struct {
			Text string `json:"text"`
		}
		if err := cli.Call(context.Background(), "term.capture", map[string]any{"id": termID, "lines": 80}, &capRes); err == nil {
			last = capRes.Text
			if strings.Contains(capRes.Text, substr) {
				return capRes.Text
			}
		}
		time.Sleep(80 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q in term.capture(%s); last=%q", substr, termID, last)
	return ""
}

// ============================================================================
// F9: Multi-Surface Wire Contract & Non-Regression (5 tests)
// ============================================================================

func TestTier1_F9_01_LocalUnixSocketStatusPairAndDevicesLifecycle(t *testing.T) {
	td := StartIsolatedDaemon(t, DaemonOpts{
		Env: map[string]string{"OPENROUTER_API_KEY": "sk-or-v1-f9-01"},
	})
	ctx := context.Background()

	status, _, err := td.Unix.StatusRaw(ctx)
	if err != nil {
		t.Fatalf("Unix.StatusRaw: %v", err)
	}
	if status["name"] != "agentc" {
		t.Fatalf("expected name='agentc', got %v", status["name"])
	}

	_, link, err := td.Unix.Pair(ctx, td.HTTPBase, 300)
	if err != nil {
		t.Fatalf("Unix.Pair: %v", err)
	}
	devKey, _ := GenerateDeviceKeypair()
	expectedDevID := KeyIDFromPub(devKey.Public)
	reply, err := PairDeviceOverNoiseWS(ctx, link, devKey, "UnixLifecycleClient", "linux", nil)
	if err != nil {
		t.Fatalf("PairDeviceOverNoiseWS: %v", err)
	}
	if reply.DeviceID != expectedDevID {
		t.Fatalf("DeviceID=%q, want %q", reply.DeviceID, expectedDevID)
	}

	devs, err := td.Unix.Devices(ctx)
	if err != nil {
		t.Fatalf("Unix.Devices: %v", err)
	}
	if len(devs) != 1 || devs[0].ID != expectedDevID {
		t.Fatalf("expected 1 paired device %q, got %+v", expectedDevID, devs)
	}

	removed, err := td.Unix.RevokeDevice(ctx, expectedDevID)
	if err != nil || !removed {
		t.Fatalf("Unix.RevokeDevice: removed=%v err=%v", removed, err)
	}
	devsAfter, _ := td.Unix.Devices(ctx)
	if len(devsAfter) != 0 {
		t.Fatalf("expected 0 devices after revoke, got %+v", devsAfter)
	}
}

func TestTier1_F9_02_NoiseIKpsk1PairThenNoiseIKReconnectParity(t *testing.T) {
	td := sharedDaemon(t)
	ctx := context.Background()

	stdout, stderr, code := td.RunCLI("pair", "-no-qr", "-url", td.HTTPBase)
	if code != 0 {
		t.Fatalf("agentc pair -no-qr failed (%d): %s %s", code, stdout, stderr)
	}
	var pairURL string
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "http://") && strings.Contains(line, "/p/") {
			pairURL = line
			break
		}
	}
	if pairURL == "" {
		t.Fatalf("failed to extract pair URL from CLI stdout: %s", stdout)
	}
	link, err := ParsePairURL(pairURL)
	if err != nil {
		t.Fatalf("ParsePairURL: %v", err)
	}

	devKey, _ := GenerateDeviceKeypair()
	if _, err := PairDeviceOverNoiseWS(ctx, link, devKey, "ReconnectDevice", "android", nil); err != nil {
		t.Fatalf("PairDeviceOverNoiseWS: %v", err)
	}

	cli1, err := ConnectNoiseRPCClient(ctx, td.WSBase, link.DaemonPub, devKey, "ReconnectDevice", "android", nil)
	if err != nil {
		t.Fatalf("ConnectNoiseRPCClient #1: %v", err)
	}
	var info map[string]any
	if err := cli1.Call(ctx, "daemon.info", map[string]any{}, &info); err != nil {
		t.Fatalf("daemon.info #1: %v", err)
	}
	_ = cli1.Close()

	cli2, err := ConnectNoiseRPCClient(ctx, td.WSBase, link.DaemonPub, devKey, "ReconnectDevice", "android", nil)
	if err != nil {
		t.Fatalf("ConnectNoiseRPCClient #2: %v", err)
	}
	defer cli2.Close()

	repo := newTempGitRepo(t, nil)
	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli2.Call(ctx, "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "google/gemini-3-flash-preview",
	}, &created); err != nil {
		t.Fatalf("session.create on reconnected Noise_IK client: %v", err)
	}
	_ = cli2.Call(ctx, "session.subscribe", map[string]any{"id": created.Session.ID, "since": 0}, nil)
	_ = cli2.Call(ctx, "session.prompt", map[string]any{"id": created.Session.ID, "text": "Reconnect parity turn"}, nil)
	if _, _, err := cli2.WaitForTurnIdle(created.Session.ID, 1, 12*time.Second); err != nil {
		t.Fatalf("turn on reconnected client: %v", err)
	}
}

func TestTier1_F9_03_DesktopWireSchemaContractValidation(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T1-F9-03-Desktop", "linux")
	ctx := context.Background()

	rawModels, err := cli.CallRaw(ctx, "providers.models", map[string]any{"provider": "codex"})
	if err != nil {
		t.Fatalf("providers.models: %v", err)
	}
	var modelsMap map[string]any
	_ = json.Unmarshal(rawModels, &modelsMap)
	for _, k := range []string{"provider", "defaultModelId", "openRouterConfigured", "models", "customModels", "availableModes", "reasoningEfforts"} {
		if _, ok := modelsMap[k]; !ok {
			t.Fatalf("Desktop contract missing key %q in providers.models: %s", k, string(rawModels))
		}
	}

	repo := newTempGitRepo(t, nil)
	var created map[string]any
	if err := cli.Call(ctx, "session.create", map[string]any{
		"agent":           "codex",
		"cwd":             repo,
		"model":           "google/gemini-3-flash-preview",
		"mode":            "default",
		"thought":         "low",
		"skipPermissions": true,
		"prompt": map[string]any{
			"text": `Desktop contract edit [edit:desk.ts:export const x = 1;\n]`,
		},
	}, &created); err != nil {
		t.Fatalf("Desktop session.create failed: %v", err)
	}
	sessMap, _ := created["session"].(map[string]any)
	sid, _ := sessMap["id"].(string)
	if sid == "" {
		t.Fatalf("missing session.id in session.create result")
	}

	marker := cli.EventMarker()
	if err := cli.Call(ctx, "session.subscribe", map[string]any{"id": sid, "since": 0}, nil); err != nil {
		t.Fatalf("session.subscribe: %v", err)
	}
	snapEv, err := cli.WaitForEvent(marker, 6*time.Second, func(ev PushEvent) bool {
		return ev.Event == "session.snapshot"
	})
	if err != nil {
		t.Fatalf("missing session.snapshot event: %v", err)
	}
	var snapMap map[string]any
	_ = json.Unmarshal(snapEv.Data, &snapMap)
	for _, k := range []string{"id", "session", "seq", "items"} {
		if _, ok := snapMap[k]; !ok {
			t.Fatalf("session.snapshot missing key %q", k)
		}
	}
	if _, _, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second); err != nil {
		t.Fatalf("WaitForTurnIdle: %v", err)
	}

	rawTD, err := cli.CallRaw(ctx, "session.turnDiff", map[string]any{"id": sid, "scope": "latest"})
	if err != nil {
		t.Fatalf("session.turnDiff: %v", err)
	}
	var tdMap map[string]any
	_ = json.Unmarshal(rawTD, &tdMap)
	for _, k := range []string{"scope", "files", "totalAdd", "totalDel"} {
		if _, ok := tdMap[k]; !ok {
			t.Fatalf("DiffResult missing key %q: %s", k, string(rawTD))
		}
	}
}

func TestTier1_F9_04_AndroidWireSchemaAndCodexFlagsContract(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T1-F9-04-Android", "android")
	repo := newTempGitRepo(t, nil)

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(context.Background(), "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"flags": map[string]any{
			"model":           "google/gemini-2.5-flash",
			"effort":          "high",
			"mode":            "auto",
			"approvalPolicy":  "never",
			"sandbox":         false,
			"provider":        "openrouter",
			"skipPermissions": true,
			"addDirs":         []string{},
			"extraArgs":       []string{},
		},
	}, &created); err != nil {
		t.Fatalf("Android nested flags session.create failed: %v", err)
	}
	sid := created.Session.ID
	if created.Session.Model != "google/gemini-2.5-flash" {
		t.Fatalf("expected nested flags.model='google/gemini-2.5-flash', got %q", created.Session.Model)
	}

	// Subscribing with empty/default flags must NOT clobber the session's model!
	if err := cli.Call(context.Background(), "session.subscribe", map[string]any{
		"id":    sid,
		"since": 0,
		"flags": map[string]any{"skipPermissions": true},
	}, nil); err != nil {
		t.Fatalf("session.subscribe with Android flags failed: %v", err)
	}
	_ = cli.Call(context.Background(), "session.prompt", map[string]any{"id": sid, "text": "Android flags [inspect]"}, nil)
	sum, items, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second)
	if err != nil {
		t.Fatalf("WaitForTurnIdle: %v", err)
	}
	if sum.Model != "google/gemini-2.5-flash" {
		t.Fatalf("session.subscribe clobbered model to %q", sum.Model)
	}
	insp, ok := extractInspectJSON(items)
	if !ok {
		t.Fatalf("missing INSPECT_JSON")
	}
	if rawArgs, _ := json.Marshal(insp["cliArgs"]); strings.Contains(string(rawArgs), "include-default-customizations") {
		t.Fatalf("Android AgyFlags bled into codex CLI args: %s", string(rawArgs))
	}
}

func TestTier1_F9_05_FakeACPCustomProviderAndBuiltinsNonRegression(t *testing.T) {
	td := StartIsolatedDaemon(t, DaemonOpts{
		AllowRealHostAgents: true,
		EnableFakeACP:       true,
		Env:                 map[string]string{"OPENROUTER_API_KEY": "sk-or-v1-f9-05"},
	})
	cli := td.PairAndConnect(t, "T1-F9-05", "linux")

	var info struct {
		Agents []struct {
			ID string `json:"id"`
		} `json:"agents"`
	}
	if err := cli.Call(context.Background(), "daemon.info", map[string]any{}, &info); err != nil {
		t.Fatalf("daemon.info: %v", err)
	}
	var agentIDs []string
	for _, a := range info.Agents {
		agentIDs = append(agentIDs, a.ID)
	}
	for _, builtin := range []string{"agy", "claude", "codex"} {
		if !slices.Contains(agentIDs, builtin) {
			t.Fatalf("missing built-in agent %q in daemon.info agents %v", builtin, agentIDs)
		}
	}

	if err := cli.Call(context.Background(), "providers.upsert", map[string]any{
		"id":      "custom-acp",
		"name":    "Custom FakeACP",
		"driver":  "acp",
		"enabled": true,
		"command": fakeacpBin,
		"env":     map[string]string{"FAKEACP_STATE_DIR": td.FakeACPDir},
	}, nil); err != nil {
		t.Fatalf("providers.upsert(custom-acp): %v", err)
	}

	repo := newTempGitRepo(t, nil)
	var acpCreated struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(context.Background(), "session.create", map[string]any{
		"agent":           "custom-acp",
		"cwd":             repo,
		"skipPermissions": false,
	}, &acpCreated); err != nil {
		t.Fatalf("session.create(custom-acp): %v", err)
	}
	acpSID := acpCreated.Session.ID
	_ = cli.Call(context.Background(), "session.subscribe", map[string]any{"id": acpSID, "since": 0}, nil)
	_ = cli.Call(context.Background(), "session.prompt", map[string]any{
		"id":   acpSID,
		"text": "[edit:acp_file.txt:acp_ok] [perm:echo hi]",
	}, nil)

	permItem, err := cli.WaitForPendingPerm(acpSID, 10*time.Second)
	if err != nil {
		t.Fatalf("fakeacp WaitForPendingPerm: %v", err)
	}
	_ = cli.Call(context.Background(), "session.permission", map[string]any{
		"id":       acpSID,
		"reqId":    permItem.Perm.ReqID,
		"optionId": PickPermOptionID(permItem.Perm, "allow_once"),
	}, nil)
	if _, _, err := cli.WaitForTurnIdle(acpSID, 1, 12*time.Second); err != nil {
		t.Fatalf("fakeacp WaitForTurnIdle: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "acp_file.txt")); err != nil {
		t.Fatalf("expected acp_file.txt created by custom-acp: %v", err)
	}
}

// ============================================================================
// F10: Git/Repo .gitignore, Untracked Evaluation Doc & Operational Safety (5 tests)
// ============================================================================

func TestTier1_F10_01_RootGitignoreCoversSecretsResearchAndBuildDirs(t *testing.T) {
	gitignorePath := filepath.Join(repoRootOnDevBox, ".gitignore")
	b, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatalf("read %s: %v", gitignorePath, err)
	}
	content := string(b)
	for _, entry := range []string{".open_router_api_key", ".research", "node_modules", "dist", "release"} {
		if !strings.Contains(content, entry) {
			t.Fatalf("root .gitignore missing required ignore pattern %q", entry)
		}
	}
}

func TestTier1_F10_02_HarnessEvaluationDocExistsAndIsUntracked(t *testing.T) {
	docPath := filepath.Join(repoRootOnDevBox, ".research", "harness-evaluation.md")
	b, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("expected untracked evaluation doc at %s: %v", docPath, err)
	}
	lower := strings.ToLower(string(b))
	if len(b) < 200 {
		t.Fatalf("harness-evaluation.md appears too short (%d bytes)", len(b))
	}
	for _, term := range []string{"t3", "codex", "openrouter", "opencode", "pi"} {
		if !strings.Contains(lower, term) {
			t.Fatalf("harness-evaluation.md missing comparison discussion of %q", term)
		}
	}

	cmd := exec.Command("git", "-C", repoRootOnDevBox, "ls-files", "--error-unmatch", ".research/harness-evaluation.md")
	if err := cmd.Run(); err == nil {
		t.Fatalf(".research/harness-evaluation.md MUST remain untracked, but is in git index")
	}
}

func TestTier1_F10_03_OpenRouterKeyFileExistsNonEmptyAndGitIgnored(t *testing.T) {
	st, err := os.Stat(liveOpenRouterKeyPath)
	if err != nil || st.Size() == 0 {
		t.Fatalf(".open_router_api_key missing or empty: %v", err)
	}
	cmd := exec.Command("git", "-C", repoRootOnDevBox, "check-ignore", "-q", ".open_router_api_key")
	if err := cmd.Run(); err != nil {
		t.Fatalf("git check-ignore .open_router_api_key failed (must be ignored by .gitignore): %v", err)
	}
}

func TestTier1_F10_04_CLIVersionHelpAndStatusSmoke(t *testing.T) {
	td := sharedDaemon(t)

	verOut, verErr, code := td.RunCLI("version")
	if code != 0 || !strings.Contains(verOut+verErr, "agentc") {
		t.Fatalf("agentc version failed (code=%d): out=%q err=%q", code, verOut, verErr)
	}
	stOut, stErr, stCode := td.RunCLI("status")
	if stCode != 0 || strings.TrimSpace(stOut) == "" {
		t.Fatalf("agentc status failed (code=%d): out=%q err=%q", stCode, stOut, stErr)
	}
	devOut, devErr, devCode := td.RunCLI("devices")
	if devCode != 0 {
		t.Fatalf("agentc devices failed (code=%d): out=%q err=%q", devCode, devOut, devErr)
	}
}

func TestTier1_F10_05_ProtectedDevBoxServicesActiveAndUntouched(t *testing.T) {
	assertProtectedCloudflaredUntouched(t)
	beforeSnap, err := snapshotProtectedCloudflared(defaultProcRoot)
	if err != nil || len(beforeSnap) == 0 {
		t.Fatalf("before-test cloudflared snapshot failed (len=%d): %v", len(beforeSnap), err)
	}
	t.Cleanup(func() {
		assertProtectedCloudflaredUntouched(t)
	})

	for _, badRaw := range []string{"", "   \n\t\n", "not-a-pid\n", "0\n", "-12\n", "123\nabc\n"} {
		if _, err := parsePgrepPIDs(badRaw); err == nil {
			t.Fatalf("expected parsePgrepPIDs(%q) to return an error", badRaw)
		}
	}
	if parsedPIDs, err := parsePgrepPIDs(" 402 \n 105 \n 402 \n"); err != nil || !slices.Equal(parsedPIDs, []int{105, 402}) {
		t.Fatalf("unexpected parsePgrepPIDs valid parse result: pids=%v err=%v", parsedPIDs, err)
	}
	for _, badStat := range []string{"", "99991 cloudflared S 1", "99991 (cloudflared) S 1 2 3", "99991 (cloudflared) S 1 99991 99991 0 -1 4194304 100 0 0 0 10 5 0 0 20 0 1 0 notticks 1000 500"} {
		if _, err := parseProcStatStartTime(badStat); err == nil {
			t.Fatalf("expected parseProcStatStartTime(%q) to return an error", badStat)
		}
	}

	synthProc := newTempPlainDir(t)
	const synthPID = 99991
	synthPIDDir := filepath.Join(synthProc, "99991")
	synthStatLine := func(ticks string) string {
		return "99991 (cloudflared) S 1 99991 99991 0 -1 4194304 100 0 0 0 10 5 0 0 20 0 1 0 " + ticks + " 1000000 500\n"
	}
	synthBaseline := []cloudflaredProcSnapshot{{
		PID:            synthPID,
		Comm:           expectedCloudflaredComm,
		StartTimeTicks: "4200",
	}}

	if err := verifyProtectedCloudflaredSnapshot(synthProc, synthBaseline, false); err == nil {
		t.Fatalf("expected missing synthetic PID dir in verifyProtectedCloudflaredSnapshot to fail")
	}
	if err := os.MkdirAll(synthPIDDir, 0o755); err != nil {
		t.Fatalf("mkdir synthPIDDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(synthPIDDir, "comm"), []byte("cloudflared\n"), 0o644); err != nil {
		t.Fatalf("write synth comm: %v", err)
	}
	if err := os.WriteFile(filepath.Join(synthPIDDir, "stat"), []byte(synthStatLine("4200")), 0o644); err != nil {
		t.Fatalf("write synth stat: %v", err)
	}
	if err := verifyProtectedCloudflaredSnapshot(synthProc, synthBaseline, false); err != nil {
		t.Fatalf("expected matching synthetic snapshot to pass, got: %v", err)
	}
	if err := os.WriteFile(filepath.Join(synthPIDDir, "comm"), []byte("bash\n"), 0o644); err != nil {
		t.Fatalf("write wrong synth comm: %v", err)
	}
	if err := verifyProtectedCloudflaredSnapshot(synthProc, synthBaseline, false); err == nil {
		t.Fatalf("expected wrong comm in verifyProtectedCloudflaredSnapshot to fail")
	}
	if err := os.WriteFile(filepath.Join(synthPIDDir, "comm"), []byte("cloudflared\n"), 0o644); err != nil {
		t.Fatalf("restore synth comm: %v", err)
	}
	if err := os.WriteFile(filepath.Join(synthPIDDir, "stat"), []byte(synthStatLine("4201")), 0o644); err != nil {
		t.Fatalf("write mutated synth stat: %v", err)
	}
	if err := verifyProtectedCloudflaredSnapshot(synthProc, synthBaseline, false); err == nil {
		t.Fatalf("expected mutated starttime ticks in verifyProtectedCloudflaredSnapshot to fail")
	}

	td := StartIsolatedDaemon(t, DaemonOpts{
		Env: map[string]string{"OPENROUTER_API_KEY": "sk-or-v1-f10-05"},
	})
	td.Stop()

	if err := verifyProtectedCloudflaredSnapshot(defaultProcRoot, beforeSnap, true); err != nil {
		t.Fatalf("after-test cloudflared snapshot verification failed: %v", err)
	}
	assertProtectedCloudflaredUntouched(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "systemctl", "--user", "is-active", "agentc.service")
	cmd.Env = append(os.Environ(), "XDG_RUNTIME_DIR=/run/user/"+uidString())
	out, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "active" {
		t.Fatalf("expected user agentc.service to remain active, got %q (err=%v)", strings.TrimSpace(string(out)), err)
	}
}

func uidString() string {
	return itoaInt(os.Getuid())
}

func itoaInt(n int) string {
	if n == 0 {
		return "0"
	}
	var digits [20]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	return string(digits[i:])
}
