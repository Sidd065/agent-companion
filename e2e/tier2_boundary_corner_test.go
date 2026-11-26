package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// ============================================================================
// F1 Boundary & Negative Tests (5 tests)
// ============================================================================

func TestTier2_F1_01_MissingCodexBinaryReportsUnavailableReason(t *testing.T) {
	td := StartIsolatedDaemon(t, DaemonOpts{
		OmitCodexFromPath:  true,
		OmitAgentcCodexEnv: true,
		ExtraArgs:          []string{"-codex-path", "/nonexistent/codex-missing-e2e"},
		Env:                map[string]string{"OPENROUTER_API_KEY": "sk-or-v1-t2-f1-01"},
	})
	cli := td.PairAndConnect(t, "T2-F1-01", "linux")

	var info struct {
		Agents []struct {
			ID        string `json:"id"`
			Available bool   `json:"available"`
			Reason    string `json:"reason"`
		} `json:"agents"`
	}
	if err := cli.Call(context.Background(), "daemon.info", map[string]any{}, &info); err != nil {
		t.Fatalf("daemon.info: %v", err)
	}
	var found bool
	for _, a := range info.Agents {
		if a.ID == "codex" {
			found = true
			if a.Available {
				t.Fatalf("expected codex.available=false when -codex-path points to missing binary")
			}
			if strings.TrimSpace(a.Reason) == "" {
				t.Fatalf("expected non-empty reason when codex binary is missing")
			}
		}
	}
	if !found {
		t.Fatalf("codex missing from agents list")
	}

	repo := newTempGitRepo(t, nil)
	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	err := cli.Call(context.Background(), "session.create", map[string]any{
		"agent":  "codex",
		"cwd":    repo,
		"prompt": map[string]any{"text": "should fail cleanly"},
	}, &created)
	if err == nil {
		// If session.create defers spawn until prompt, wait briefly for error state
		_ = cli.Call(context.Background(), "session.subscribe", map[string]any{"id": created.Session.ID, "since": 0}, nil)
		sum, _, wErr := cli.WaitForSession(created.Session.ID, 6*time.Second, func(s SessionSummaryWire, _ *SessionDetailWire, _ []TranscriptItemWire) bool {
			return s.Error != "" || s.State == "idle"
		})
		if wErr != nil || sum.Error == "" {
			t.Fatalf("expected missing codex binary to fail session.create or prompt cleanly; sum=%+v wErr=%v", sum, wErr)
		}
	}
}

func TestTier2_F1_02_DisabledCodexProviderOverrideBlocksSessionCreate(t *testing.T) {
	td := StartIsolatedDaemon(t, DaemonOpts{
		Env: map[string]string{"OPENROUTER_API_KEY": "sk-or-v1-t2-f1-02"},
	})
	cli := td.PairAndConnect(t, "T2-F1-02", "linux")

	if err := cli.Call(context.Background(), "providers.upsert", map[string]any{
		"id":      "codex",
		"name":    "Codex",
		"driver":  "codex",
		"enabled": false,
	}, nil); err != nil {
		t.Fatalf("providers.upsert(enabled=false): %v", err)
	}

	var info struct {
		Agents []struct {
			ID        string `json:"id"`
			Available bool   `json:"available"`
			Reason    string `json:"reason"`
		} `json:"agents"`
	}
	if err := cli.Call(context.Background(), "daemon.info", map[string]any{}, &info); err != nil {
		t.Fatalf("daemon.info: %v", err)
	}
	for _, a := range info.Agents {
		if a.ID == "codex" && a.Available {
			t.Fatalf("expected codex available=false immediately after disabling provider (cache invalidated)")
		}
	}

	repo := newTempGitRepo(t, nil)
	if err := cli.Call(context.Background(), "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
	}, nil); err == nil {
		t.Fatalf("expected session.create(agent='codex') to be rejected while provider is disabled")
	}
}

func TestTier2_F1_03_TildeExpansionInCodexProviderCommandPath(t *testing.T) {
	td := StartIsolatedDaemon(t, DaemonOpts{
		OmitCodexFromPath:  true,
		OmitAgentcCodexEnv: true,
		Env:                map[string]string{"OPENROUTER_API_KEY": "sk-or-v1-t2-f1-03"},
	})
	homeBinDir := filepath.Join(td.HomeDir, "bin")
	if err := os.MkdirAll(homeBinDir, 0o755); err != nil {
		t.Fatalf("mkdir ~/bin: %v", err)
	}
	expectedPath := filepath.Join(homeBinDir, "fakecodex-tilde")
	if err := os.Symlink(fakecodexBin, expectedPath); err != nil {
		t.Fatalf("symlink ~/bin/fakecodex-tilde: %v", err)
	}

	cli := td.PairAndConnect(t, "T2-F1-03", "linux")
	if err := cli.Call(context.Background(), "providers.upsert", map[string]any{
		"id":      "codex",
		"name":    "Codex",
		"driver":  "codex",
		"enabled": true,
		"command": "~/bin/fakecodex-tilde",
	}, nil); err != nil {
		t.Fatalf("providers.upsert tilde command: %v", err)
	}

	var health []struct {
		ID        string `json:"id"`
		Available bool   `json:"available"`
		Command   string `json:"command"`
	}
	if err := cli.Call(context.Background(), "providers.health", map[string]any{"id": "codex"}, &health); err != nil {
		t.Fatalf("providers.health: %v", err)
	}
	if len(health) == 0 || !health[0].Available || health[0].Command != expectedPath {
		t.Fatalf("expected tilde expansion to %q (available=true), got %+v", expectedPath, health)
	}
}

func TestTier2_F1_04_CodexBinaryFailingVersionFlagStillAvailable(t *testing.T) {
	td := StartIsolatedDaemon(t, DaemonOpts{
		Env: map[string]string{
			"OPENROUTER_API_KEY":     "sk-or-v1-t2-f1-04",
			"FAKECODEX_FAIL_VERSION": "1",
		},
	})
	cli := td.PairAndConnect(t, "T2-F1-04", "linux")

	var health []struct {
		ID        string `json:"id"`
		Available bool   `json:"available"`
	}
	if err := cli.Call(context.Background(), "providers.health", map[string]any{"id": "codex"}, &health); err != nil {
		t.Fatalf("providers.health: %v", err)
	}
	if len(health) == 0 || !health[0].Available {
		t.Fatalf("expected codex binary to remain available=true even if --version exits non-zero; got %+v", health)
	}
}

func TestTier2_F1_05_ProvidersUpsertInvalidIDAndUnsupportedDriverRejected(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T2-F1-05", "linux")

	if err := cli.Call(context.Background(), "providers.upsert", map[string]any{
		"id":      "Bad ID With Spaces!",
		"name":    "Bad",
		"driver":  "acp",
		"enabled": true,
	}, nil); err == nil {
		t.Fatalf("expected providers.upsert with invalid ID to return an error")
	}

	if err := cli.Call(context.Background(), "providers.upsert", map[string]any{
		"id":      "valid-id-bad-drv",
		"name":    "Bad Driver",
		"driver":  "nonexistent_driver_xyz",
		"enabled": true,
	}, nil); err == nil {
		t.Fatalf("expected providers.upsert with unsupported driver to return an error")
	}
}

// ============================================================================
// F2 Boundary & Negative Tests (5 tests)
// ============================================================================

func TestTier2_F2_01_KeyFileWithSurroundingWhitespaceAndNewlinesTrimmed(t *testing.T) {
	dir := newTempPlainDir(t)
	cleanKey := "sk-or-v1-trimmed-key-98765"
	keyFile := filepath.Join(dir, "padded.key")
	if err := os.WriteFile(keyFile, []byte("  \t\r\n "+cleanKey+" \r\n\t  "), 0o600); err != nil {
		t.Fatalf("write padded key: %v", err)
	}

	suiteMockOR.ClearRequests()
	td := StartIsolatedDaemon(t, DaemonOpts{
		ExtraArgs: []string{"-openrouter-api-key-file", keyFile},
	})
	cli := td.PairAndConnect(t, "T2-F2-01", "linux")
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
	_ = cli.Call(context.Background(), "session.prompt", map[string]any{"id": sid, "text": "Trim test [bridge-probe]"}, nil)
	if _, _, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second); err != nil {
		t.Fatalf("WaitForTurnIdle: %v", err)
	}

	var matched bool
	for _, r := range suiteMockOR.ResponsesRequests() {
		if strings.Contains(r.PromptText, "Trim test [bridge-probe]") {
			if r.AuthHeader != "Bearer "+cleanKey {
				t.Fatalf("expected exact trimmed Bearer header %q, got %q", "Bearer "+cleanKey, r.AuthHeader)
			}
			matched = true
		}
	}
	if !matched {
		t.Fatalf("did not observe /responses request for trimmed key test")
	}
}

func TestTier2_F2_02_MissingOrEmptyKeyFileLeavesOpenRouterUnconfigured(t *testing.T) {
	requireSuiteReady(t)
	tdMissing, logsMissing, errMissing := TryStartDaemon(DaemonOpts{
		ExtraArgs: []string{"-openrouter-api-key-file", "/nonexistent/or_key_does_not_exist.txt"},
	})
	if errMissing != nil {
		if strings.Contains(logsMissing, "flag provided but not defined") {
			t.Fatalf("-openrouter-api-key-file CLI flag is not implemented: %s", logsMissing)
		}
		if strings.Contains(logsMissing, "panic:") {
			t.Fatalf("daemon panicked on missing key file: %s", logsMissing)
		}
	} else {
		defer tdMissing.Stop()
		cli := tdMissing.PairAndConnect(t, "T2-F2-02a", "linux")
		var info map[string]any
		if err := cli.Call(context.Background(), "daemon.info", map[string]any{}, &info); err != nil {
			t.Fatalf("daemon.info: %v", err)
		}
		if cfg, _ := info["openRouterConfigured"].(bool); cfg {
			t.Fatalf("expected openRouterConfigured=false when key file is missing")
		}
	}

	dir := newTempPlainDir(t)
	emptyKeyFile := filepath.Join(dir, "empty.key")
	_ = os.WriteFile(emptyKeyFile, []byte("   \n\t  "), 0o600)
	tdEmpty, logsEmpty, errEmpty := TryStartDaemon(DaemonOpts{
		ExtraArgs: []string{"-openrouter-api-key-file", emptyKeyFile},
	})
	if errEmpty != nil {
		if strings.Contains(logsEmpty, "flag provided but not defined") || strings.Contains(logsEmpty, "panic:") {
			t.Fatalf("unexpected failure on whitespace-only key file: %s", logsEmpty)
		}
	} else {
		defer tdEmpty.Stop()
		cli := tdEmpty.PairAndConnect(t, "T2-F2-02b", "linux")
		var info map[string]any
		if err := cli.Call(context.Background(), "daemon.info", map[string]any{}, &info); err != nil {
			t.Fatalf("daemon.info: %v", err)
		}
		if cfg, _ := info["openRouterConfigured"].(bool); cfg {
			t.Fatalf("expected openRouterConfigured=false for empty key file")
		}
	}
}

func TestTier2_F2_03_MaskedAsterisksSentinelPreservesStoredSecretOnUpsert(t *testing.T) {
	realSecret := "sk-or-v1-preserved-across-masked-upsert-333"
	suiteMockOR.ClearRequests()
	td := StartIsolatedDaemon(t, DaemonOpts{})
	cli := td.PairAndConnect(t, "T2-F2-03", "linux")

	if err := cli.Call(context.Background(), "providers.upsert", map[string]any{
		"id":      "codex",
		"name":    "Codex",
		"driver":  "codex",
		"enabled": true,
		"env":     map[string]string{"OPENROUTER_API_KEY": realSecret},
	}, nil); err != nil {
		t.Fatalf("first providers.upsert: %v", err)
	}

	// Second upsert sends masked "***" back alongside customModels
	if err := cli.Call(context.Background(), "providers.upsert", map[string]any{
		"id":      "codex",
		"name":    "Codex",
		"driver":  "codex",
		"enabled": true,
		"env":     map[string]string{"OPENROUTER_API_KEY": "***"},
		"customModels": []map[string]string{
			{"slug": "google/gemini-2.5-flash", "name": "Gemini 2.5 Flash"},
		},
	}, nil); err != nil {
		t.Fatalf("second providers.upsert with '***': %v", err)
	}

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
	_ = cli.Call(context.Background(), "session.prompt", map[string]any{"id": sid, "text": "Preserved secret [bridge-probe]"}, nil)
	if _, _, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second); err != nil {
		t.Fatalf("WaitForTurnIdle: %v", err)
	}

	var matched bool
	for _, r := range suiteMockOR.ResponsesRequests() {
		if strings.Contains(r.PromptText, "Preserved secret [bridge-probe]") {
			if r.AuthHeader != "Bearer "+realSecret {
				t.Fatalf("stored secret was overwritten instead of preserved! got AuthHeader=%q", r.AuthHeader)
			}
			matched = true
		}
	}
	if !matched {
		t.Fatalf("did not capture /responses request after masked upsert")
	}
}

func TestTier2_F2_04_KeyPrecedenceOrderCLIOverFileOverEnvOverConfig(t *testing.T) {
	cliKey := "sk-or-v1-cli-winner-111"
	cliFileKey := "sk-or-v1-clifile-runnerup-155"
	envKey := "sk-or-v1-env-loser-222"
	envFileKey := "sk-or-v1-envfile-loser-255"
	cfgKey := "sk-or-v1-cfg-loser-333"

	keyDir := newTempPlainDir(t)
	cliKeyFile := filepath.Join(keyDir, "cli-key.txt")
	envKeyFile := filepath.Join(keyDir, "env-key.txt")
	if err := os.WriteFile(cliKeyFile, []byte(cliFileKey+"\n"), 0o600); err != nil {
		t.Fatalf("write cliKeyFile: %v", err)
	}
	if err := os.WriteFile(envKeyFile, []byte(envFileKey+"\n"), 0o600); err != nil {
		t.Fatalf("write envKeyFile: %v", err)
	}

	// 1) All 5 inputs present: -openrouter-api-key beats -openrouter-api-key-file > OPENROUTER_API_KEY > OPENROUTER_API_KEY_FILE > config.json
	suiteMockOR.ClearRequests()
	td := StartIsolatedDaemon(t, DaemonOpts{
		ExtraArgs: []string{
			"-openrouter-api-key", cliKey,
			"-openrouter-api-key-file", cliKeyFile,
		},
		Env: map[string]string{
			"OPENROUTER_API_KEY":      envKey,
			"OPENROUTER_API_KEY_FILE": envKeyFile,
		},
		ConfigOverrides: map[string]any{
			"openRouterApiKey": cfgKey,
		},
	})
	cli := td.PairAndConnect(t, "T2-F2-04a", "linux")
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
	_ = cli.Call(context.Background(), "session.prompt", map[string]any{"id": sid, "text": "Precedence CLI test [bridge-probe]"}, nil)
	if _, _, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second); err != nil {
		t.Fatalf("WaitForTurnIdle: %v", err)
	}

	var sawWinner bool
	for _, r := range suiteMockOR.ResponsesRequests() {
		if strings.Contains(r.PromptText, "Precedence CLI test [bridge-probe]") {
			if r.AuthHeader != "Bearer "+cliKey {
				t.Fatalf("expected CLI flag key precedence %q, got %q", "Bearer "+cliKey, r.AuthHeader)
			}
			sawWinner = true
		}
	}
	if !sawWinner {
		t.Fatalf("did not capture /responses request for CLI precedence test")
	}

	// 2) Without -openrouter-api-key: -openrouter-api-key-file beats OPENROUTER_API_KEY > OPENROUTER_API_KEY_FILE > config.json
	suiteMockOR.ClearRequests()
	td2 := StartIsolatedDaemon(t, DaemonOpts{
		ExtraArgs: []string{"-openrouter-api-key-file", cliKeyFile},
		Env: map[string]string{
			"OPENROUTER_API_KEY":      envKey,
			"OPENROUTER_API_KEY_FILE": envKeyFile,
		},
		ConfigOverrides: map[string]any{
			"openRouterApiKey": cfgKey,
		},
	})
	cli2 := td2.PairAndConnect(t, "T2-F2-04b", "linux")
	var created2 struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli2.Call(context.Background(), "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "google/gemini-3-flash-preview",
	}, &created2); err != nil {
		t.Fatalf("session.create (file precedence): %v", err)
	}
	sid2 := created2.Session.ID
	_ = cli2.Call(context.Background(), "session.subscribe", map[string]any{"id": sid2, "since": 0}, nil)
	_ = cli2.Call(context.Background(), "session.prompt", map[string]any{"id": sid2, "text": "Precedence CLI-file test [bridge-probe]"}, nil)
	if _, _, err := cli2.WaitForTurnIdle(sid2, 1, 12*time.Second); err != nil {
		t.Fatalf("WaitForTurnIdle (file precedence): %v", err)
	}
	var sawFileWinner bool
	for _, r := range suiteMockOR.ResponsesRequests() {
		if strings.Contains(r.PromptText, "Precedence CLI-file test [bridge-probe]") {
			if r.AuthHeader != "Bearer "+cliFileKey {
				t.Fatalf("expected -openrouter-api-key-file precedence %q, got %q", "Bearer "+cliFileKey, r.AuthHeader)
			}
			sawFileWinner = true
		}
	}
	if !sawFileWinner {
		t.Fatalf("did not capture /responses request for CLI-file precedence test")
	}
}

func TestTier2_F2_05_ExhaustiveRPCRedactionScanZeroSecretSubstrings(t *testing.T) {
	canarySecret := "sk-or-v1-canary-ultra-secret-do-not-leak-778899"
	td := StartIsolatedDaemon(t, DaemonOpts{
		ExtraArgs: []string{"-openrouter-api-key", canarySecret},
	})
	cli := td.PairAndConnect(t, "T2-F2-05", "linux")
	ctx := context.Background()

	_ = cli.Call(ctx, "providers.upsert", map[string]any{
		"id":      "codex",
		"name":    "Codex",
		"driver":  "codex",
		"enabled": true,
		"env":     map[string]string{"OPENROUTER_API_KEY": canarySecret},
	}, nil)

	repo := newTempGitRepo(t, nil)
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
	_ = cli.Call(ctx, "session.subscribe", map[string]any{"id": created.Session.ID, "since": 0}, nil)
	_ = cli.Call(ctx, "session.prompt", map[string]any{"id": created.Session.ID, "text": "Redaction scan [inspect]"}, nil)
	if _, _, err := cli.WaitForTurnIdle(created.Session.ID, 1, 12*time.Second); err != nil {
		t.Fatalf("WaitForTurnIdle: %v", err)
	}

	rpcChecks := []struct {
		method string
		params map[string]any
	}{
		{"daemon.info", map[string]any{}},
		{"hello", map[string]any{"device": "scan"}},
		{"providers.list", map[string]any{}},
		{"providers.health", map[string]any{}},
		{"providers.models", map[string]any{"provider": "codex"}},
		{"sessions.list", map[string]any{}},
	}
	for _, rc := range rpcChecks {
		raw, err := cli.CallRaw(ctx, rc.method, rc.params)
		if err != nil {
			t.Fatalf("%s failed: %v", rc.method, err)
		}
		assertZeroSecretSubstring(t, canarySecret, rc.method, raw)
	}
	_, statusBytes, err := td.Unix.StatusRaw(ctx)
	if err != nil {
		t.Fatalf("Unix.StatusRaw: %v", err)
	}
	assertZeroSecretSubstring(t, canarySecret, "/local/status", statusBytes)
	assertZeroSecretSubstring(t, canarySecret, "agentc.log", td.Logs())
}

// ============================================================================
// F3 Boundary & Negative Tests (5 tests)
// ============================================================================

func TestTier2_F3_01_OpenRouterUserModelsEndpointErrorFallsBackToPublicModels(t *testing.T) {
	requireSuiteReady(t)
	mor, err := NewMockOpenRouterServer(filepath.Join(newTempPlainDir(t), "ca.pem"))
	if err != nil {
		t.Fatalf("NewMockOpenRouterServer: %v", err)
	}
	defer mor.Close()
	mor.SetModelsStatusOverrides(500, 200)

	td := StartIsolatedDaemon(t, DaemonOpts{
		MockOR: mor,
		Env:    map[string]string{"OPENROUTER_API_KEY": "sk-or-v1-t2-f3-01"},
	})
	cli := td.PairAndConnect(t, "T2-F3-01", "linux")

	var res struct {
		Models []ModelInfoWire `json:"models"`
	}
	if err := cli.Call(context.Background(), "providers.models", map[string]any{
		"provider": "codex",
		"refresh":  true,
	}, &res); err != nil {
		t.Fatalf("providers.models failed when /models/user returned 500: %v", err)
	}
	if len(res.Models) == 0 {
		t.Fatalf("expected fallback to public /api/v1/models to populate models")
	}
}

func TestTier2_F3_02_ProvidersModelsCacheHitVsExplicitRefreshBypass(t *testing.T) {
	requireSuiteReady(t)
	mor, err := NewMockOpenRouterServer(filepath.Join(newTempPlainDir(t), "ca.pem"))
	if err != nil {
		t.Fatalf("NewMockOpenRouterServer: %v", err)
	}
	defer mor.Close()

	td := StartIsolatedDaemon(t, DaemonOpts{
		MockOR: mor,
		Env:    map[string]string{"OPENROUTER_API_KEY": "sk-or-v1-t2-f3-02"},
	})
	cli := td.PairAndConnect(t, "T2-F3-02", "linux")

	var res1 struct {
		Models []ModelInfoWire `json:"models"`
	}
	if err := cli.Call(context.Background(), "providers.models", map[string]any{"provider": "codex", "refresh": true}, &res1); err != nil {
		t.Fatalf("initial providers.models: %v", err)
	}

	mor.AddDynamicModel(OpenRouterModelFixture{
		ID:                  "new-vendor/model-v2-live",
		Name:                "New Vendor Model v2",
		ContextLength:       262144,
		SupportedParameters: []string{"tools", "reasoning"},
	})

	// Cached call (refresh: false) should not require upstream refetch
	var resCached struct {
		Models []ModelInfoWire `json:"models"`
	}
	if err := cli.Call(context.Background(), "providers.models", map[string]any{"provider": "codex", "refresh": false}, &resCached); err != nil {
		t.Fatalf("cached providers.models: %v", err)
	}

	// Explicit refresh: true MUST pick up the newly added model immediately
	var resRefreshed struct {
		Models []ModelInfoWire `json:"models"`
	}
	if err := cli.Call(context.Background(), "providers.models", map[string]any{"provider": "codex", "refresh": true}, &resRefreshed); err != nil {
		t.Fatalf("refreshed providers.models: %v", err)
	}
	var foundNew bool
	for _, m := range resRefreshed.Models {
		if m.ModelID == "new-vendor/model-v2-live" {
			foundNew = true
			break
		}
	}
	if !foundNew {
		t.Fatalf("expected providers.models(refresh=true) to return newly added 'new-vendor/model-v2-live'")
	}
}

func TestTier2_F3_03_CustomModelsPreserveLatestAndColonVariantSuffixes(t *testing.T) {
	td := StartIsolatedDaemon(t, DaemonOpts{
		Env: map[string]string{"OPENROUTER_API_KEY": "sk-or-v1-t2-f3-03"},
	})
	cli := td.PairAndConnect(t, "T2-F3-03", "linux")

	wantSlugs := []string{
		"openai/gpt-5-chat-latest",
		"google/gemini-2.5-flash:thinking",
		"qwen/qwen3:free",
	}
	var customIn []map[string]string
	for _, s := range wantSlugs {
		customIn = append(customIn, map[string]string{"slug": s, "name": s})
	}
	if err := cli.Call(context.Background(), "providers.upsert", map[string]any{
		"id":           "codex",
		"name":         "Codex",
		"driver":       "codex",
		"enabled":      true,
		"customModels": customIn,
	}, nil); err != nil {
		t.Fatalf("providers.upsert: %v", err)
	}

	var cat struct {
		CustomModels []struct {
			Slug string `json:"slug"`
		} `json:"customModels"`
	}
	if err := cli.Call(context.Background(), "providers.models", map[string]any{"provider": "codex"}, &cat); err != nil {
		t.Fatalf("providers.models: %v", err)
	}
	var gotSlugs []string
	for _, cm := range cat.CustomModels {
		gotSlugs = append(gotSlugs, cm.Slug)
	}
	for _, ws := range wantSlugs {
		if !slices.Contains(gotSlugs, ws) {
			t.Fatalf("custom model suffix corrupted: missing %q in %v", ws, gotSlugs)
		}
	}
}

func TestTier2_F3_04_CustomModelsDeduplicateAndIgnoreBlankSlugs(t *testing.T) {
	td := StartIsolatedDaemon(t, DaemonOpts{
		Env: map[string]string{"OPENROUTER_API_KEY": "sk-or-v1-t2-f3-04"},
	})
	cli := td.PairAndConnect(t, "T2-F3-04", "linux")

	if err := cli.Call(context.Background(), "providers.upsert", map[string]any{
		"id":      "codex",
		"name":    "Codex",
		"driver":  "codex",
		"enabled": true,
		"customModels": []map[string]string{
			{"slug": "  ", "name": "Blank"},
			{"slug": "", "name": "Empty"},
			{"slug": "google/gemini-2.5-flash", "name": "First"},
			{"slug": "google/gemini-2.5-flash", "name": "Duplicate"},
		},
	}, nil); err != nil {
		t.Fatalf("providers.upsert: %v", err)
	}

	var cat struct {
		CustomModels []struct {
			Slug string `json:"slug"`
		} `json:"customModels"`
	}
	if err := cli.Call(context.Background(), "providers.models", map[string]any{"provider": "codex"}, &cat); err != nil {
		t.Fatalf("providers.models: %v", err)
	}
	count := 0
	for _, cm := range cat.CustomModels {
		if strings.TrimSpace(cm.Slug) == "" {
			t.Fatalf("blank slug was not sanitized from customModels: %+v", cat.CustomModels)
		}
		if cm.Slug == "google/gemini-2.5-flash" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected deduplicated count=1 for google/gemini-2.5-flash, got %d (%+v)", count, cat.CustomModels)
	}
}

func TestTier2_F3_05_ProvidersModelsWhenOpenRouterUpstreamUnreachable(t *testing.T) {
	requireSuiteReady(t)
	mor, err := NewMockOpenRouterServer(filepath.Join(newTempPlainDir(t), "ca.pem"))
	if err != nil {
		t.Fatalf("NewMockOpenRouterServer: %v", err)
	}
	defer mor.Close()
	mor.SetModelsStatusOverrides(503, 503)

	td := StartIsolatedDaemon(t, DaemonOpts{
		MockOR: mor,
		Env:    map[string]string{"OPENROUTER_API_KEY": "sk-or-v1-t2-f3-05"},
	})
	cli := td.PairAndConnect(t, "T2-F3-05", "linux")

	if err := cli.Call(context.Background(), "providers.upsert", map[string]any{
		"id":      "codex",
		"name":    "Codex",
		"driver":  "codex",
		"enabled": true,
		"customModels": []map[string]string{
			{"slug": "offline/pinned-model", "name": "Offline Pinned"},
		},
	}, nil); err != nil {
		t.Fatalf("providers.upsert failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var res struct {
		Models           []ModelInfoWire `json:"models"`
		CustomModels     []any           `json:"customModels"`
		ReasoningEfforts []string        `json:"reasoningEfforts"`
	}
	err = cli.Call(ctx, "providers.models", map[string]any{"provider": "codex", "refresh": true}, &res)
	if errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
		t.Fatalf("providers.models hung/timed out on upstream 503: %v", err)
	}
	if err != nil {
		low := strings.ToLower(err.Error())
		if strings.Contains(low, "unknown method") || !strings.Contains(low, "503") {
			t.Fatalf("unexpected providers.models error on upstream 503: %v", err)
		}
	} else {
		if len(res.CustomModels) == 0 || len(res.Models) == 0 {
			t.Fatalf("expected both customModels and fallback models when OpenRouter returns 503, got custom=%d models=%d", len(res.CustomModels), len(res.Models))
		}
	}
}

// ============================================================================
// F4 Boundary & Negative Tests (5 tests)
// ============================================================================

func TestTier2_F4_01_LiveSetModelUnlistedSlugNotOverwrittenBySyncFromConfig(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T2-F4-01", "linux")
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
	_ = cli.Call(context.Background(), "session.prompt", map[string]any{"id": sid, "text": "Warm live proc"}, nil)
	if _, _, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second); err != nil {
		t.Fatalf("Turn 1: %v", err)
	}

	unlisted := "vendor-x/unlisted-model-99"
	var afterModel SessionSummaryWire
	if err := cli.Call(context.Background(), "session.setModel", map[string]any{
		"id":    sid,
		"model": unlisted,
	}, &afterModel); err != nil {
		t.Fatalf("session.setModel: %v", err)
	}
	if afterModel.Model != unlisted {
		t.Fatalf("after setModel got %q, want %q", afterModel.Model, unlisted)
	}

	// Trigger syncFromConfigLocked via session.setConfig! Must NOT clobber unlisted model!
	var afterCfg SessionSummaryWire
	if err := cli.Call(context.Background(), "session.setConfig", map[string]any{
		"id":       sid,
		"configId": "thought_level",
		"value":    "high",
	}, &afterCfg); err != nil {
		t.Fatalf("session.setConfig: %v", err)
	}
	if afterCfg.Model != unlisted {
		t.Fatalf("Daemon Bug #1 regression: syncFromConfigLocked overwrote unlisted model %q back to %q", unlisted, afterCfg.Model)
	}
}

func TestTier2_F4_02_SleepingSetModelUpdatesSummaryImmediatelyBeforeWake(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T2-F4-02", "linux")
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
		t.Fatalf("Turn 1: %v", err)
	}
	_ = cli.Call(context.Background(), "session.stop", map[string]any{"id": sid}, nil)
	_, _, _ = cli.WaitForSession(sid, 6*time.Second, func(s SessionSummaryWire, _ *SessionDetailWire, _ []TranscriptItemWire) bool {
		return !s.Live
	})

	unlistedSleep := "vendor-y/sleeping-unlisted-88"
	if err := cli.Call(context.Background(), "session.setModel", map[string]any{
		"id":    sid,
		"model": unlistedSleep,
	}, nil); err != nil {
		t.Fatalf("session.setModel while sleeping: %v", err)
	}

	var opened SessionSummaryWire
	if err := cli.Call(context.Background(), "session.open", map[string]any{"id": sid}, &opened); err != nil {
		t.Fatalf("session.open: %v", err)
	}
	if opened.Model != unlistedSleep {
		t.Fatalf("Daemon Bug #1 regression: session.open while sleeping returned model=%q, want %q", opened.Model, unlistedSleep)
	}

	var list []SessionSummaryWire
	if err := cli.Call(context.Background(), "sessions.list", map[string]any{}, &list); err != nil {
		t.Fatalf("sessions.list: %v", err)
	}
	for _, s := range list {
		if s.ID == sid && s.Model != unlistedSleep {
			t.Fatalf("sessions.list while sleeping returned model=%q, want %q", s.Model, unlistedSleep)
		}
	}
}

func TestTier2_F4_03_RapidSequentialModelSwitchesAcrossVendors(t *testing.T) {
	suiteMockOR.ClearRequests()
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T2-F4-03", "linux")
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

	seqModels := []struct {
		setSlug  string
		wantWire string
	}{
		{"google/gemini-2.5-flash", "google/gemini-2.5-flash"},
		{"openrouter/deepseek/deepseek-v4.1-flash", "deepseek/deepseek-v4.1-flash"},
		{"openai/gpt-4.1-mini", "openai/gpt-4.1-mini"},
	}

	for idx, sm := range seqModels {
		if idx > 0 {
			if err := cli.Call(context.Background(), "session.setModel", map[string]any{"id": sid, "model": sm.setSlug}, nil); err != nil {
				t.Fatalf("setModel(%s): %v", sm.setSlug, err)
			}
		}
		tag := "rapid-switch-" + itoaInt(idx+1)
		_ = cli.Call(context.Background(), "session.prompt", map[string]any{
			"id":   sid,
			"text": tag + " [bridge-probe]",
		}, nil)
		if _, _, err := cli.WaitForTurnIdle(sid, idx+1, 12*time.Second); err != nil {
			t.Fatalf("turn %d (%s): %v", idx+1, sm.setSlug, err)
		}
		var matched bool
		for _, r := range suiteMockOR.ResponsesRequests() {
			if strings.Contains(r.PromptText, tag) && r.Model == sm.wantWire {
				matched = true
			}
		}
		if !matched {
			t.Fatalf("turn %d did not route wire model %q", idx+1, sm.wantWire)
		}
	}
}

func TestTier2_F4_04_ModelSlugWithDotsHyphensColonsAndSlashes(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T2-F4-04", "linux")
	repo := newTempGitRepo(t, nil)

	complexSlug := "meta-llama/llama-4.1-405b-instruct:nitro"
	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(context.Background(), "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": complexSlug,
	}, &created); err != nil {
		t.Fatalf("session.create with complex slug: %v", err)
	}
	if created.Session.Model != complexSlug {
		t.Fatalf("Summary.Model=%q, want %q", created.Session.Model, complexSlug)
	}
	sid := created.Session.ID
	_ = cli.Call(context.Background(), "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	_ = cli.Call(context.Background(), "session.prompt", map[string]any{"id": sid, "text": "Complex slug turn"}, nil)
	sum, _, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second)
	if err != nil {
		t.Fatalf("WaitForTurnIdle: %v", err)
	}
	if sum.Model != complexSlug {
		t.Fatalf("after turn Summary.Model=%q, want %q", sum.Model, complexSlug)
	}
}

func TestTier2_F4_05_EmptyModelStringKeepsCurrentOrDefaultModel(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T2-F4-05", "linux")
	repo := newTempGitRepo(t, nil)

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(context.Background(), "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "",
	}, &created); err != nil {
		t.Fatalf("session.create with empty model: %v", err)
	}
	sid := created.Session.ID
	_ = cli.Call(context.Background(), "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	_ = cli.Call(context.Background(), "session.prompt", map[string]any{"id": sid, "text": "Default model check [inspect]"}, nil)
	_, items, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second)
	if err != nil {
		t.Fatalf("WaitForTurnIdle: %v", err)
	}
	insp, ok := extractInspectJSON(items)
	if !ok {
		t.Fatalf("missing INSPECT_JSON")
	}
	if mod, _ := insp["model"].(string); strings.TrimSpace(mod) == "" {
		t.Fatalf("expected non-blank fallback/default model on turn/start, got empty string")
	}
}

// ============================================================================
// F5 Boundary & Negative Tests (6 tests)
// ============================================================================

func TestTier2_F5_01_Http402CanOnlyAffordAutoRetriesWithReducedMaxTokens(t *testing.T) {
	suiteMockOR.ResetOverrides()
	suiteMockOR.ClearRequests()
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T2-F5-01", "linux")
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
		"text": "Trigger 402 auto-retry [bridge-402]",
	}, nil)

	sum, items, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second)
	if err != nil {
		t.Fatalf("WaitForTurnIdle: %v", err)
	}
	if sum.Error != "" {
		t.Fatalf("expected HTTP 402 auto-retry to succeed without session error, got %q", sum.Error)
	}
	if !strings.Contains(collectAgentText(items), "BRIDGE_TEXT:") {
		t.Fatalf("expected successful bridged SSE text after 402 retry; got %q", collectAgentText(items))
	}

	var attempts []RecordedResponsesRequest
	for _, r := range suiteMockOR.ResponsesRequests() {
		if strings.Contains(r.PromptText, "Trigger 402 auto-retry [bridge-402]") {
			attempts = append(attempts, r)
		}
	}
	if len(attempts) != 2 {
		t.Fatalf("expected exactly 2 /responses attempts (initial + retry), got %d", len(attempts))
	}
	if attempts[0].MaxOutputTokens != 8192 {
		t.Fatalf("attempt #1 MaxOutputTokens=%d, want 8192", attempts[0].MaxOutputTokens)
	}
	if attempts[1].MaxOutputTokens > 1436 || attempts[1].MaxOutputTokens < 256 {
		t.Fatalf("attempt #2 MaxOutputTokens=%d, want <= 1436 (1500-64)", attempts[1].MaxOutputTokens)
	}
}

func TestTier2_F5_02_Http402BelowMinimumFloor256DoesNotInfiniteRetry(t *testing.T) {
	suiteMockOR.ResetOverrides()
	suiteMockOR.ClearRequests()
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T2-F5-02", "linux")
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
		"text": "Trigger unaffordable 402 [bridge-402-unaffordable]",
	}, nil)

	sum, items, err := cli.WaitForTurnSettled(sid, 1, 12*time.Second)
	if err != nil {
		t.Fatalf("session did not settle to idle on unaffordable 402: %v", err)
	}
	var sawErrNotice bool
	for _, it := range items {
		if it.Kind == "notice" && it.Level == "error" {
			sawErrNotice = true
		}
	}
	if sum.Error == "" && !sawErrNotice {
		t.Fatalf("expected unaffordable 402 to surface turn error or error notice, got sum=%+v items=%+v", sum, items)
	}

	var attempts int
	for _, r := range suiteMockOR.ResponsesRequests() {
		if strings.Contains(r.PromptText, "Trigger unaffordable 402 [bridge-402-unaffordable]") {
			attempts++
		}
	}
	if attempts != 1 {
		t.Fatalf("expected exactly 1 upstream attempt when afford=120 (<256 floor), got %d", attempts)
	}
}

func TestTier2_F5_03_CustomEnvMaxOutputTokensOverrideRespected(t *testing.T) {
	suiteMockOR.ClearRequests()
	td := StartIsolatedDaemon(t, DaemonOpts{
		Env: map[string]string{
			"OPENROUTER_API_KEY":                 "sk-or-v1-t2-f5-03",
			"CODEX_OPENROUTER_MAX_OUTPUT_TOKENS": "2048",
		},
	})
	cli := td.PairAndConnect(t, "T2-F5-03", "linux")
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
		"text": "Custom cap 2048 [bridge-probe]",
	}, nil)
	if _, _, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second); err != nil {
		t.Fatalf("WaitForTurnIdle: %v", err)
	}

	var matched bool
	for _, r := range suiteMockOR.ResponsesRequests() {
		if strings.Contains(r.PromptText, "Custom cap 2048 [bridge-probe]") {
			if r.MaxOutputTokens != 2048 {
				t.Fatalf("expected CODEX_OPENROUTER_MAX_OUTPUT_TOKENS=2048 cap, got %d", r.MaxOutputTokens)
			}
			matched = true
		}
	}
	if !matched {
		t.Fatalf("did not capture /responses request for custom max_output_tokens")
	}
}

func TestTier2_F5_04_UpstreamOpenRouter401UnauthorizedSurfacedCleanly(t *testing.T) {
	suiteMockOR.ResetOverrides()
	suiteMockOR.ClearRequests()
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T2-F5-04", "linux")
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
		"text": "Simulate 401 [bridge-401]",
	}, nil)

	sum, items, err := cli.WaitForTurnSettled(sid, 1, 12*time.Second)
	if err != nil {
		t.Fatalf("expected 401 unauthorized to settle to idle: %v (sum=%+v items=%+v)", err, sum, items)
	}
	var saw401 bool
	if strings.Contains(strings.ToLower(sum.Error), "401") || strings.Contains(strings.ToLower(sum.Error), "invalid openrouter api key") {
		saw401 = true
	}
	for _, it := range items {
		if it.Kind == "notice" && (strings.Contains(strings.ToLower(it.Text), "401") || strings.Contains(strings.ToLower(it.Text), "invalid openrouter api key")) {
			saw401 = true
		}
	}
	if !saw401 {
		t.Fatalf("expected upstream 401 error/notice in session, got sum.Error=%q items=%+v", sum.Error, items)
	}

	var upstreamHits int
	for _, r := range suiteMockOR.ResponsesRequests() {
		if strings.Contains(r.PromptText, "[bridge-401]") {
			upstreamHits++
		}
	}
	if upstreamHits < 1 {
		t.Fatalf("expected >= 1 upstream MockOpenRouterServer /responses request for [bridge-401], got %d", upstreamHits)
	}

	// Verify daemon remains responsive for subsequent RPC calls
	var info map[string]any
	if err := cli.Call(context.Background(), "daemon.info", map[string]any{}, &info); err != nil {
		t.Fatalf("daemon unhealthy after 401 turn error: %v", err)
	}
}

func TestTier2_F5_05_FitThoughtClampsMaxToXHighForCodex(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T2-F5-05", "linux")
	repo := newTempGitRepo(t, nil)

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(context.Background(), "session.create", map[string]any{
		"agent":   "codex",
		"cwd":     repo,
		"model":   "google/gemini-3-flash-preview",
		"thought": "max",
	}, &created); err != nil {
		t.Fatalf("session.create(thought=max): %v", err)
	}
	sid := created.Session.ID
	_ = cli.Call(context.Background(), "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	_ = cli.Call(context.Background(), "session.prompt", map[string]any{"id": sid, "text": "Clamp thought [inspect]"}, nil)
	sum, items, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second)
	if err != nil {
		t.Fatalf("WaitForTurnIdle: %v", err)
	}
	if sum.Thought != "xhigh" && sum.Thought != "max" {
		t.Fatalf("unexpected Summary.Thought=%q", sum.Thought)
	}
	insp, ok := extractInspectJSON(items)
	if !ok {
		t.Fatalf("missing INSPECT_JSON")
	}
	if eff, _ := insp["effort"].(string); eff != "xhigh" {
		t.Fatalf("expected thought='max' clamped to effort='xhigh' for codex, got %q", eff)
	}
}

func TestTier2_F5_06_UnsupportedModeNameFallsBackWithoutBreakingSession(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T2-F5-06", "linux")
	repo := newTempGitRepo(t, nil)

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(context.Background(), "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "google/gemini-3-flash-preview",
		"mode":  "nonexistent-bogus-mode",
	}, &created); err != nil {
		t.Fatalf("session.create with unknown mode should fall back cleanly, got error: %v", err)
	}
	sid := created.Session.ID
	_ = cli.Call(context.Background(), "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	_ = cli.Call(context.Background(), "session.prompt", map[string]any{"id": sid, "text": "Fallback mode prompt"}, nil)
	if _, _, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second); err != nil {
		t.Fatalf("turn after unknown mode fallback failed: %v", err)
	}
}

// ============================================================================
// F6 Boundary & Negative Tests (6 tests)
// ============================================================================

func TestTier2_F6_01_OpenAIModelPreservesNativeCustomApplyPatchTool(t *testing.T) {
	suiteMockOR.ResetOverrides()
	suiteMockOR.ClearRequests()
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T2-F6-01", "linux")
	repo := newTempGitRepo(t, nil)

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(context.Background(), "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "openai/gpt-4.1-mini",
	}, &created); err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sid := created.Session.ID
	_ = cli.Call(context.Background(), "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	_ = cli.Call(context.Background(), "session.prompt", map[string]any{
		"id":   sid,
		"text": "OpenAI native custom tool [bridge-patch]",
	}, nil)
	if _, _, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second); err != nil {
		t.Fatalf("WaitForTurnIdle: %v", err)
	}

	var sawNativeCustom bool
	for _, r := range suiteMockOR.ResponsesRequests() {
		if strings.Contains(r.PromptText, "OpenAI native custom tool [bridge-patch]") {
			for _, tl := range r.Tools {
				if tl["name"] == "apply_patch" {
					if tl["type"] != "custom" {
						t.Fatalf("openai/* model must preserve type='custom' for apply_patch, got %q", tl["type"])
					}
					sawNativeCustom = true
				}
			}
		}
	}
	if !sawNativeCustom {
		t.Fatalf("did not capture apply_patch tool on openai/gpt-4.1-mini request")
	}
}

func TestTier2_F6_02_FailedCommandExecutionNonZeroExitCodeMarksFailed(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T2-F6-02", "linux")
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
		"text": "Fail exit 7 [cmd:exit 7]",
	}, nil)

	_, items, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second)
	if err != nil {
		t.Fatalf("WaitForTurnIdle: %v", err)
	}
	var sawFailedTool bool
	for _, it := range items {
		if it.Kind == "tool" && it.Tool != nil && it.Tool.Status == "failed" {
			sawFailedTool = true
			break
		}
	}
	if !sawFailedTool {
		t.Fatalf("expected tool.status=='failed' on non-zero exit code 7; items=%+v", items)
	}
}

func TestTier2_F6_03_SessionInNonGitDirectorySkipsCheckpointsGracefully(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T2-F6-03", "linux")
	plainDir := newTempPlainDir(t)

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(context.Background(), "session.create", map[string]any{
		"agent": "codex",
		"cwd":   plainDir,
		"model": "google/gemini-3-flash-preview",
	}, &created); err != nil {
		t.Fatalf("session.create in plain dir: %v", err)
	}
	sid := created.Session.ID
	_ = cli.Call(context.Background(), "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	_ = cli.Call(context.Background(), "session.prompt", map[string]any{
		"id":   sid,
		"text": `Edit in non-git dir [edit:plain.txt:hello plain\n]`,
	}, nil)

	sum, _, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second)
	if err != nil {
		t.Fatalf("WaitForTurnIdle: %v", err)
	}
	if sum.Error != "" {
		t.Fatalf("unexpected error in non-git dir: %q", sum.Error)
	}
	if _, err := os.Stat(filepath.Join(plainDir, "plain.txt")); err != nil {
		t.Fatalf("plain.txt not written: %v", err)
	}
	if sum.LastCheckpoint != nil {
		t.Fatalf("expected nil LastCheckpoint in non-git directory, got %+v", sum.LastCheckpoint)
	}
}

func TestTier2_F6_04_CheckpointSkipsOversizedUntrackedFileAndNodeModules(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T2-F6-04", "linux")
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

	// Pre-create an 11 MiB untracked file + node_modules file during turn via [cmd:...] + [edit:...]
	_ = cli.Call(context.Background(), "session.prompt", map[string]any{
		"id":   sid,
		"text": `Large untracked [cmd:dd if=/dev/zero of=huge.bin bs=1048576 count=11 status=none && mkdir -p node_modules/pkg && echo x > node_modules/pkg/index.js] [edit:small.txt:ok\n]`,
	}, nil)

	sum, _, err := cli.WaitForTurnIdle(sid, 1, 15*time.Second)
	if err != nil {
		t.Fatalf("WaitForTurnIdle: %v", err)
	}
	cp := sum.LastCheckpoint
	if cp == nil {
		t.Fatalf("expected TurnCheckpoint")
	}
	if cp.SkippedLarge < 1 {
		t.Fatalf("expected SkippedLarge >= 1 for 11 MiB untracked file, got %+v", sum.LastCheckpoint)
	}
}

func TestTier2_F6_05_TurnDiffInvalidScopeAndMissingTurnIndexErrors(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T2-F6-05", "linux")
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

	if err := cli.Call(context.Background(), "session.turnDiff", map[string]any{
		"id":    sid,
		"scope": "bogus_invalid_scope",
	}, nil); err == nil {
		t.Fatalf("expected error for invalid session.turnDiff scope")
	}

	if err := cli.Call(context.Background(), "session.turnDiff", map[string]any{
		"id":        sid,
		"scope":     "turn",
		"turnIndex": 999,
	}, nil); err == nil {
		t.Fatalf("expected error for nonexistent turnIndex 999")
	}
}

func TestTier2_F6_06_EmptyPromptAndTraversalAttachmentRefRejected(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T2-F6-06", "linux")
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

	if err := cli.Call(context.Background(), "session.prompt", map[string]any{
		"id":   created.Session.ID,
		"text": "   \t\n  ",
	}, nil); err == nil {
		t.Fatalf("expected whitespace-only session.prompt to be rejected")
	}

	if err := cli.Call(context.Background(), "attachment.read", map[string]any{
		"ref":    "att:../../etc/passwd",
		"offset": 0,
		"length": 1024,
	}, nil); err == nil {
		t.Fatalf("expected path-traversal attachment.read reference to be rejected")
	}
}

// ============================================================================
// F7 Boundary & Negative Tests (5 tests)
// ============================================================================

func TestTier2_F7_01_DecliningCommandApprovalWithFeedbackSkipsExecution(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T2-F7-01", "linux")
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
		"text": "Try dangerous command [approve-cmd:touch forbidden_cmd.txt]",
	}, nil)

	permItem, err := cli.WaitForPendingPerm(sid, 10*time.Second)
	if err != nil {
		t.Fatalf("WaitForPendingPerm: %v", err)
	}
	optID := PickPermOptionID(permItem.Perm, "reject_once")
	if err := cli.Call(context.Background(), "session.permission", map[string]any{
		"id":       sid,
		"reqId":    permItem.Perm.ReqID,
		"optionId": optID,
		"message":  "Use safe path instead",
	}, nil); err != nil {
		t.Fatalf("session.permission(deny): %v", err)
	}

	if _, _, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second); err != nil {
		t.Fatalf("WaitForTurnIdle: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "forbidden_cmd.txt")); err == nil {
		t.Fatalf("declined command MUST NOT execute or create forbidden_cmd.txt")
	}
}

func TestTier2_F7_02_DecliningFileChangeApprovalLeavesWorkspaceUntouched(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T2-F7-02", "linux")
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
		"text": `Decline patch [approve-patch:secret_out.txt:do not write\n]`,
	}, nil)

	permItem, err := cli.WaitForPendingPerm(sid, 10*time.Second)
	if err != nil {
		t.Fatalf("WaitForPendingPerm: %v", err)
	}
	optID := PickPermOptionID(permItem.Perm, "reject_once")
	if err := cli.Call(context.Background(), "session.permission", map[string]any{
		"id":       sid,
		"reqId":    permItem.Perm.ReqID,
		"optionId": optID,
	}, nil); err != nil {
		t.Fatalf("session.permission(deny): %v", err)
	}

	if _, _, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second); err != nil {
		t.Fatalf("WaitForTurnIdle: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "secret_out.txt")); err == nil {
		t.Fatalf("declined file patch MUST NOT write secret_out.txt")
	}
}

func TestTier2_F7_03_DuplicateOrUnknownPermissionReqIDReturnsError(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T2-F7-03", "linux")
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

	if err := cli.Call(context.Background(), "session.permission", map[string]any{
		"id":       sid,
		"reqId":    "r999999",
		"optionId": "accept",
	}, nil); err == nil {
		t.Fatalf("expected error for unknown permission reqId r999999")
	}

	_ = cli.Call(context.Background(), "session.prompt", map[string]any{
		"id":   sid,
		"text": "[approve-cmd:echo once]",
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
		t.Fatalf("first session.permission: %v", err)
	}
	if _, _, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second); err != nil {
		t.Fatalf("WaitForTurnIdle after first permission: %v", err)
	}

	// Duplicate resolution on same reqId must return clean error
	if err := cli.Call(context.Background(), "session.permission", map[string]any{
		"id":       sid,
		"reqId":    permItem.Perm.ReqID,
		"optionId": optID,
	}, nil); err == nil {
		t.Fatalf("expected error on duplicate session.permission for already resolved reqId")
	}
}

func TestTier2_F7_04_CancelTurnWhileWaitingOnPermissionAutoCancelsPerm(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T2-F7-04", "linux")
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
		"text": "Block then cancel [approve-cmd:touch never_run.txt]",
	}, nil)

	permItem, err := cli.WaitForPendingPerm(sid, 10*time.Second)
	if err != nil {
		t.Fatalf("WaitForPendingPerm: %v", err)
	}
	if err := cli.Call(context.Background(), "session.cancel", map[string]any{"id": sid}, nil); err != nil {
		t.Fatalf("session.cancel while waiting on permission: %v", err)
	}

	_, items, err := cli.WaitForSession(sid, 10*time.Second, func(s SessionSummaryWire, _ *SessionDetailWire, _ []TranscriptItemWire) bool {
		return s.State == "idle" && s.PendingPermissions == 0
	})
	if err != nil {
		t.Fatalf("expected session to settle to idle with 0 pending permissions after cancel: %v", err)
	}
	for _, it := range items {
		if it.Kind == "perm" && it.Perm != nil && it.Perm.ReqID == permItem.Perm.ReqID && it.State == "pending" {
			t.Fatalf("expected Perm item to no longer be 'pending' after session.cancel")
		}
	}
	if _, err := os.Stat(filepath.Join(repo, "never_run.txt")); err == nil {
		t.Fatalf("cancelled approval command must not execute")
	}
}

func TestTier2_F7_05_QuestionAnsweredViaFreeTextMessageFallback(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T2-F7-05", "linux")
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
		"text": "Free text fallback [question]",
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
		"message":  "Prod",
	}, nil); err != nil {
		t.Fatalf("session.permission with message fallback: %v", err)
	}

	_, items, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second)
	if err != nil {
		t.Fatalf("WaitForTurnIdle: %v", err)
	}
	if !strings.Contains(collectAgentText(items), "Prod") {
		t.Fatalf("expected free-text message fallback 'Prod' delivered to codex question, got %q", collectAgentText(items))
	}
}

// ============================================================================
// F8 Boundary & Negative Tests (5 tests)
// ============================================================================

func TestTier2_F8_01_RewindRejectedWhileTurnIsActivelyRunning(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T2-F8-01", "linux")
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
		t.Fatalf("Turn 1: %v", err)
	}

	_ = cli.Call(context.Background(), "session.prompt", map[string]any{"id": sid, "text": "Busy [slow:3000]"}, nil)
	if _, _, err := cli.WaitForSession(sid, 5*time.Second, func(s SessionSummaryWire, _ *SessionDetailWire, items []TranscriptItemWire) bool {
		return s.State == "running" && strings.Contains(collectAgentText(items), "chunk-1")
	}); err != nil {
		t.Fatalf("wait running: %v", err)
	}

	err := cli.Call(context.Background(), "session.rewind", map[string]any{"id": sid}, nil)
	_ = cli.Call(context.Background(), "session.cancel", map[string]any{"id": sid}, nil)
	_, _, _ = cli.WaitForSession(sid, 5*time.Second, func(s SessionSummaryWire, _ *SessionDetailWire, _ []TranscriptItemWire) bool {
		return s.State == "idle"
	})
	if err == nil {
		t.Fatalf("expected session.rewind to be rejected while turn is running")
	}
}

func TestTier2_F8_02_OpenInTerminalRejectedWhileSessionRunningOrWaiting(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T2-F8-02", "linux")
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
	_ = cli.Call(context.Background(), "session.prompt", map[string]any{"id": sid, "text": "Running [slow:3000]"}, nil)
	if _, _, err := cli.WaitForSession(sid, 5*time.Second, func(s SessionSummaryWire, _ *SessionDetailWire, items []TranscriptItemWire) bool {
		return s.State == "running" && strings.Contains(collectAgentText(items), "chunk-1")
	}); err != nil {
		t.Fatalf("wait running: %v", err)
	}

	err := cli.Call(context.Background(), "session.openInTerminal", map[string]any{"id": sid}, nil)
	_ = cli.Call(context.Background(), "session.cancel", map[string]any{"id": sid}, nil)
	_, _, _ = cli.WaitForSession(sid, 5*time.Second, func(s SessionSummaryWire, _ *SessionDetailWire, _ []TranscriptItemWire) bool {
		return s.State == "idle"
	})
	if err == nil {
		t.Fatalf("expected session.openInTerminal to be rejected while session is busy")
	}
}

func TestTier2_F8_03_CodexProcessCrashMidTurnAppendsNoticeAndRecoversNextTurn(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T2-F8-03", "linux")
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
	_ = cli.Call(context.Background(), "session.prompt", map[string]any{"id": sid, "text": "Simulate crash [crash]"}, nil)

	// Wait until session exits running state after subprocess crash
	if _, _, err := cli.WaitForSession(sid, 10*time.Second, func(s SessionSummaryWire, _ *SessionDetailWire, _ []TranscriptItemWire) bool {
		return s.State != "running" && (s.State == "idle" || s.State == "sleeping" || !s.Live || s.Error != "")
	}); err != nil {
		t.Fatalf("session remained stuck in running after codex subprocess exit(2): %v", err)
	}

	// Next prompt must spawn a fresh codex app-server and complete cleanly!
	if err := cli.Call(context.Background(), "session.prompt", map[string]any{
		"id":   sid,
		"text": "Recovered turn after crash",
	}, nil); err != nil {
		t.Fatalf("recovery session.prompt failed: %v", err)
	}
	_, items, err := cli.WaitForSession(sid, 12*time.Second, func(s SessionSummaryWire, _ *SessionDetailWire, its []TranscriptItemWire) bool {
		return s.State == "idle" && strings.Contains(collectAgentText(its), "Recovered turn after crash")
	})
	if err != nil {
		t.Fatalf("failed to recover on next prompt after codex crash: %v (items=%+v)", err, items)
	}
}

func TestTier2_F8_04_ProvidersRemoveBuiltinCodexRejected(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T2-F8-04", "linux")

	err := cli.Call(context.Background(), "providers.remove", map[string]any{"id": "codex"}, nil)
	if err == nil {
		t.Fatalf("expected providers.remove({id:'codex'}) to reject deleting built-in provider")
	}
}

func TestTier2_F8_05_KillManagedProcessRejectsUnmanagedPIDs(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T2-F8-05", "linux")

	for _, badPID := range []int{1, -5, os.Getpid()} {
		if err := cli.Call(context.Background(), "daemon.killProcess", map[string]any{"pid": badPID}, nil); err == nil {
			t.Fatalf("expected daemon.killProcess(pid=%d) to be rejected", badPID)
		}
	}
}

// ============================================================================
// F9 Boundary & Negative Tests (5 tests)
// ============================================================================

func TestTier2_F9_01_UnpairedNoiseIKClientRejectedWithUnpairedClose(t *testing.T) {
	td := sharedDaemon(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	_, link, err := td.Unix.Pair(ctx, td.HTTPBase, 300)
	if err != nil {
		t.Fatalf("Unix.Pair: %v", err)
	}
	unpairedKey, _ := GenerateDeviceKeypair()
	hdrs := http.Header{"Cf-Connecting-Ip": []string{nextDistinctTestIP()}}

	cli, err := ConnectNoiseRPCClient(ctx, td.WSBase, link.DaemonPub, unpairedKey, "RogueDevice", "linux", hdrs)
	if err == nil {
		_ = cli.Close()
		t.Fatalf("expected unpaired X25519 key to be rejected on /v1/ws?m=conn")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "unpaired") && !strings.Contains(err.Error(), "1008") && !strings.Contains(strings.ToLower(err.Error()), "close") {
		t.Fatalf("expected WebSocket policy violation / unpaired close error, got %v", err)
	}
}

func TestTier2_F9_02_SingleUsePairTokenCannotBeReplayedOrUsedAfterRevoke(t *testing.T) {
	td := StartIsolatedDaemon(t, DaemonOpts{
		Env: map[string]string{"OPENROUTER_API_KEY": "sk-or-v1-t2-f9-02"},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, link, err := td.Unix.Pair(ctx, td.HTTPBase, 300)
	if err != nil {
		t.Fatalf("Unix.Pair: %v", err)
	}
	devKey1, _ := GenerateDeviceKeypair()
	if _, err := PairDeviceOverNoiseWS(ctx, link, devKey1, "Dev1", "linux", http.Header{"Cf-Connecting-Ip": []string{nextDistinctTestIP()}}); err != nil {
		t.Fatalf("first pair failed: %v", err)
	}

	// Replaying the exact same single-use pairId must fail!
	devKey2, _ := GenerateDeviceKeypair()
	if _, err := PairDeviceOverNoiseWS(ctx, link, devKey2, "ReplayAttacker", "linux", http.Header{"Cf-Connecting-Ip": []string{nextDistinctTestIP()}}); err == nil {
		t.Fatalf("single-use pair token replay MUST be rejected")
	}

	// Connect Dev1, then revoke Dev1 via RPC and verify active WebSocket closes immediately
	cli1, err := ConnectNoiseRPCClient(ctx, td.WSBase, link.DaemonPub, devKey1, "Dev1", "linux", nil)
	if err != nil {
		t.Fatalf("ConnectNoiseRPCClient Dev1: %v", err)
	}
	defer cli1.Close()

	adminCli := td.PairAndConnect(t, "AdminClient", "linux")
	dev1ID := KeyIDFromPub(devKey1.Public)
	if err := adminCli.Call(ctx, "devices.revoke", map[string]any{"id": dev1ID}, nil); err != nil {
		t.Fatalf("devices.revoke: %v", err)
	}

	select {
	case <-cli1.ClosedChan():
	case <-time.After(5 * time.Second):
		t.Fatalf("expected revoked device's active WebSocket to be closed immediately")
	}
}

func TestTier2_F9_03_UnknownRPCMethodAndMalformedParamsReturnCleanErrors(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T2-F9-03", "linux")
	ctx := context.Background()

	if err := cli.Call(ctx, "nonexistent.bogus.method", map[string]any{}, nil); err == nil {
		t.Fatalf("expected error on unknown RPC method")
	}

	// Malformed param types on same connection
	if err := cli.Call(ctx, "session.create", "not-an-object", nil); err == nil {
		t.Fatalf("expected error on malformed session.create params")
	}

	// Connection must remain healthy for valid subsequent RPCs
	var info map[string]any
	if err := cli.Call(ctx, "daemon.info", map[string]any{}, &info); err != nil {
		t.Fatalf("Noise_IK connection broke after malformed RPC calls: %v", err)
	}
}

func TestTier2_F9_04_SessionSubscribeDeltaCatchupFromSeqVsFullSnapshot(t *testing.T) {
	td := sharedDaemon(t)
	cli1 := td.PairAndConnect(t, "T2-F9-04-A", "linux")
	repo := newTempGitRepo(t, nil)

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli1.Call(context.Background(), "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "google/gemini-3-flash-preview",
	}, &created); err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sid := created.Session.ID
	_ = cli1.Call(context.Background(), "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	_ = cli1.Call(context.Background(), "session.prompt", map[string]any{"id": sid, "text": "Turn 1"}, nil)
	if _, _, err := cli1.WaitForTurnIdle(sid, 1, 12*time.Second); err != nil {
		t.Fatalf("Turn 1: %v", err)
	}
	_, _, midSeq, _ := cli1.SessionSnapshotState(sid)
	if midSeq <= 0 {
		t.Fatalf("expected midSeq > 0 after Turn 1, got %d", midSeq)
	}

	_ = cli1.Call(context.Background(), "session.prompt", map[string]any{"id": sid, "text": "Turn 2"}, nil)
	if _, _, err := cli1.WaitForTurnIdle(sid, 2, 12*time.Second); err != nil {
		t.Fatalf("Turn 2: %v", err)
	}

	cli2 := td.PairAndConnect(t, "T2-F9-04-B", "android")
	marker := cli2.EventMarker()
	if err := cli2.Call(context.Background(), "session.subscribe", map[string]any{"id": sid, "since": midSeq}, nil); err != nil {
		t.Fatalf("cli2 subscribe(since=%d): %v", midSeq, err)
	}
	deltaEv, err := cli2.WaitForEvent(marker, 6*time.Second, func(ev PushEvent) bool {
		return ev.Event == "session.delta"
	})
	if err != nil {
		t.Fatalf("expected session.delta when subscribing with since=%d: %v", midSeq, err)
	}
	var deltaPayload struct {
		Ops []TranscriptOpWire `json:"ops"`
	}
	_ = json.Unmarshal(deltaEv.Data, &deltaPayload)
	if len(deltaPayload.Ops) == 0 {
		t.Fatalf("expected non-empty delta ops > midSeq")
	}
	for _, op := range deltaPayload.Ops {
		if op.Seq <= midSeq {
			t.Fatalf("delta included op.Seq=%d <= midSeq=%d", op.Seq, midSeq)
		}
	}
}

func TestTier2_F9_05_LargeMessageFragmentationOverNoiseWebSocket(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T2-F9-05", "linux")
	dir := newTempPlainDir(t)

	// 210 KiB payload (> 3 * 65518 maxFragment) exercises multi-frame Noise fragmentation both ways!
	pattern := "0123456789abcdefABCDEFGHIJKLMNOPQRSTUVWXYZ_fragment_test\n"
	repeats := (210 * 1024 / len(pattern)) + 1
	largeText := strings.Repeat(pattern, repeats)
	targetFile := filepath.Join(dir, "large_210k.txt")

	if err := cli.Call(context.Background(), "fs.write", map[string]any{
		"path":       targetFile,
		"text":       largeText,
		"createDirs": true,
	}, nil); err != nil {
		t.Fatalf("fs.write 210 KiB over fragmented Noise_IK failed: %v", err)
	}

	var readRes struct {
		Size int64  `json:"size"`
		Text string `json:"text"`
	}
	if err := cli.Call(context.Background(), "fs.read", map[string]any{
		"path": targetFile,
		"max":  512 * 1024,
	}, &readRes); err != nil {
		t.Fatalf("fs.read 210 KiB over fragmented Noise_IK failed: %v", err)
	}
	if readRes.Text != largeText {
		t.Fatalf("210 KiB round-trip mismatch: got len=%d, want len=%d", len(readRes.Text), len(largeText))
	}
}

// ============================================================================
// F10 Boundary & Negative Tests (5 tests)
// ============================================================================

func TestTier2_F10_01_GitCheckIgnoreBlocksAllForbiddenArtifactPaths(t *testing.T) {
	forbiddenPaths := []string{
		".open_router_api_key",
		".research/harness-evaluation.md",
		"node_modules/some-pkg/index.js",
		"dist/bundle.js",
		"release/agentc-linux-amd64",
	}
	for _, p := range forbiddenPaths {
		cmd := exec.Command("git", "-C", repoRootOnDevBox, "check-ignore", "-q", p)
		if err := cmd.Run(); err != nil {
			t.Fatalf("git check-ignore failed for %q (must be ignored by .gitignore): %v", p, err)
		}
	}
}

func TestTier2_F10_02_ZeroSecretPatternsInTrackedRepoAndRecentGitLog(t *testing.T) {
	realKey := readLiveOpenRouterKeyForRedactionCheckOnly(t)

	lsOut, err := exec.Command("git", "-C", repoRootOnDevBox, "ls-files").Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	if strings.Contains(string(lsOut), ".open_router_api_key") {
		t.Fatalf("SECURITY VIOLATION: .open_router_api_key is tracked in git index!")
	}

	// Scan tracked files outside e2e/ for actual key bytes or real sk-or-v1-[0-9a-f]{32,}
	grepCmd := exec.Command("git", "-C", repoRootOnDevBox, "grep", "-I", "-E", "sk-or-v1-[0-9a-f]{32,}")
	if out, err := grepCmd.Output(); err == nil && len(bytes.TrimSpace(out)) > 0 {
		t.Fatalf("SECURITY VIOLATION: found real-looking OpenRouter key pattern in tracked files")
	}

	logOut, err := exec.Command("git", "-C", repoRootOnDevBox, "log", "-n", "5", "-p").Output()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	assertZeroSecretSubstring(t, realKey, "git log -n 5 -p", logOut)
}

func TestTier2_F10_03_ResearchDocContainsZeroKeyFragments(t *testing.T) {
	realKey := readLiveOpenRouterKeyForRedactionCheckOnly(t)
	docPath := filepath.Join(repoRootOnDevBox, ".research", "harness-evaluation.md")
	b, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read %s: %v", docPath, err)
	}
	assertZeroSecretSubstring(t, realKey, ".research/harness-evaluation.md", b)
	if strings.Contains(string(b), "sk-or-v1-") {
		t.Fatalf(".research/harness-evaluation.md must not contain sk-or-v1- key strings")
	}
}

func TestTier2_F10_04_IsolatedTestDaemonsNeverTouchRealUserAgentcDirs(t *testing.T) {
	realCfgFile := filepath.Join(realHostHome, ".config", "agentc", "config.json")
	realDevFile := filepath.Join(realHostHome, ".local", "state", "agentc", "devices.json")

	statMtime := func(p string) int64 {
		st, err := os.Stat(p)
		if err != nil {
			return -1
		}
		return st.ModTime().UnixNano()
	}
	beforeCfg := statMtime(realCfgFile)
	beforeDev := statMtime(realDevFile)

	td := StartIsolatedDaemon(t, DaemonOpts{
		Env: map[string]string{"OPENROUTER_API_KEY": "sk-or-v1-t2-f10-04"},
	})
	cli := td.PairAndConnect(t, "T2-F10-04", "linux")
	_ = cli.Call(context.Background(), "providers.upsert", map[string]any{
		"id":      "codex",
		"name":    "Codex",
		"driver":  "codex",
		"enabled": true,
	}, nil)

	afterCfg := statMtime(realCfgFile)
	afterDev := statMtime(realDevFile)
	if beforeCfg != afterCfg || beforeDev != afterDev {
		t.Fatalf("isolated test daemon modified real user agentc files! cfg(%d->%d) dev(%d->%d)", beforeCfg, afterCfg, beforeDev, afterDev)
	}
}

func TestTier2_F10_05_NoTmpCodexArtifactsCreatedDuringSuite(t *testing.T) {
	if strings.HasPrefix(suiteRoot, "/tmp") {
		t.Fatalf("E2E suiteRoot %q must not be under /tmp", suiteRoot)
	}
	entries, err := os.ReadDir("/tmp")
	if err != nil {
		t.Fatalf("ReadDir /tmp: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "agce2e") || strings.HasPrefix(name, "agentc-e2e") || strings.HasPrefix(name, "codex-openrouter-catalog") {
			t.Fatalf("found forbidden E2E/codex artifact in /tmp: %q", name)
		}
	}
}
