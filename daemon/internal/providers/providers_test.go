package providers

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func nonTmpProvidersDir(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil || home == "" || strings.HasPrefix(home, "/tmp") {
		home = "/var/tmp"
	}
	base := filepath.Join(home, ".cache", "agentc-gotest")
	_ = os.MkdirAll(base, 0o700)
	dir, err := os.MkdirTemp(base, "providers-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestStoreUpsertReloadAndHealth(t *testing.T) {
	dir := nonTmpProvidersDir(t)
	path := filepath.Join(dir, "providers.json")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.Upsert(ProviderConfig{ID: "my-acp", Name: "My ACP", Driver: "acp", Enabled: true, Command: "sh", Args: []string{"-c", "echo ok"}})
	if err != nil || p.ID != "my-acp" {
		t.Fatalf("Upsert: %v %+v", err, p)
	}
	if err := st.Remove("claude"); err == nil {
		t.Fatal("expected error deleting built-in provider")
	}
	if err := st.Remove("codex"); err == nil {
		t.Fatal("expected error deleting built-in codex provider")
	}
	st2, err := Open(path)
	if err != nil || len(st2.List()) != 1 {
		t.Fatalf("reload: %v %+v", err, st2.List())
	}
	hr := CheckHealth(context.Background(), "my-acp", "acp", "sh", nil)
	if !hr.Available {
		t.Fatalf("expected sh available: %+v", hr)
	}
	if err := st2.Remove("my-acp"); err != nil {
		t.Fatal(err)
	}
}

func TestStoreSecretRedactionPreservationCustomModelsAndRaceSafety(t *testing.T) {
	dir := nonTmpProvidersDir(t)
	path := filepath.Join(dir, "providers.json")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Save real secret + custom models.
	_, err = st.Upsert(ProviderConfig{
		ID:           "codex",
		Name:         "Codex",
		Driver:       "codex",
		Enabled:      true,
		Builtin:      true,
		DefaultModel: "google/gemini-3-flash-preview",
		Env: map[string]string{
			"OPENROUTER_API_KEY": "sk-or-v1-real-secret-value",
			"OPENAI_API_KEY":     "sk-proj-real-openai",
			"CUSTOM_SECRET":      "s3cr3t",
			"NON_SECRET_FLAG":    "1",
		},
		CustomModels: []CustomModel{
			{Slug: " google/gemini-2.5-flash ", Name: ""},
			{Slug: "google/gemini-2.5-flash", Name: "Dup"},
			{Slug: "deepseek/deepseek-v4.1-flash", Name: "DeepSeek V4.1"},
		},
	})
	if err != nil {
		t.Fatalf("Upsert initial: %v", err)
	}

	// 2. ListRedacted masks all secrets to "***" while keeping non-secret env vars.
	redList := st.ListRedacted()
	if len(redList) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(redList))
	}
	rc := redList[0]
	if rc.Env["OPENROUTER_API_KEY"] != RedactedSecret || rc.Env["OPENAI_API_KEY"] != RedactedSecret || rc.Env["CUSTOM_SECRET"] != RedactedSecret {
		t.Fatalf("secrets were not redacted: %+v", rc.Env)
	}
	if rc.Env["NON_SECRET_FLAG"] != "1" {
		t.Fatalf("non-secret env var corrupted: %+v", rc.Env)
	}
	if len(rc.CustomModels) != 2 || rc.CustomModels[0].Name != "google/gemini-2.5-flash" {
		t.Fatalf("custom models normalization failed: %+v", rc.CustomModels)
	}

	// 3. Upsert with "***" preserves stored secret values!
	_, err = st.Upsert(ProviderConfig{
		ID:           "codex",
		Name:         "Codex",
		Driver:       "codex",
		Enabled:      true,
		Builtin:      true,
		DefaultModel: "google/gemini-2.5-flash",
		Env: map[string]string{
			"OPENROUTER_API_KEY": RedactedSecret,
			"OPENAI_API_KEY":     RedactedSecret,
		},
	})
	if err != nil {
		t.Fatalf("Upsert with ***: %v", err)
	}
	got, ok := st.Get("codex")
	if !ok || got.Env["OPENROUTER_API_KEY"] != "sk-or-v1-real-secret-value" || got.Env["OPENAI_API_KEY"] != "sk-proj-real-openai" {
		t.Fatalf("stored secrets lost after '***' upsert: %+v", got.Env)
	}

	// 4. Upsert with Env == nil (e.g. customModels-only update from UI) ALSO preserves stored secrets!
	_, err = st.Upsert(ProviderConfig{
		ID:      "codex",
		Name:    "Codex",
		Driver:  "codex",
		Enabled: true,
		Builtin: true,
		CustomModels: []CustomModel{
			{Slug: "openrouter/auto", Name: "Auto"},
		},
	})
	if err != nil {
		t.Fatalf("Upsert with nil Env: %v", err)
	}
	got2, _ := st.Get("codex")
	if got2.Env["OPENROUTER_API_KEY"] != "sk-or-v1-real-secret-value" || len(got2.CustomModels) != 1 {
		t.Fatalf("stored secrets lost after nil-Env upsert: env=%+v models=%+v", got2.Env, got2.CustomModels)
	}

	// 5. Concurrent Get/List/ListRedacted/Upsert mutation race test.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				c, ok := st.Get("codex")
				if ok && c.Env != nil {
					c.Env["MUTATED_BY_CALLER"] = "boom"
				}
				for _, item := range st.ListRedacted() {
					if item.Env != nil {
						item.Env["MUTATED"] = "x"
					}
				}
			}
		}()
	}
	wg.Wait()
	after, _ := st.Get("codex")
	if _, leaked := after.Env["MUTATED_BY_CALLER"]; leaked {
		t.Fatal("deep-clone failed: caller map mutation leaked into Store")
	}
}

func TestCheckHealthCodexTildeVersionAndAuth(t *testing.T) {
	dir := nonTmpProvidersDir(t)
	localBin := filepath.Join(dir, ".local", "bin")
	if err := os.MkdirAll(localBin, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeCodex := filepath.Join(localBin, "codex")
	if err := os.WriteFile(fakeCodex, []byte("#!/bin/sh\necho 'codex-cli 0.172.0'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Unauthenticated when neither OPENROUTER_API_KEY nor OPENAI_API_KEY/auth.json exists.
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	envNoAuth := []string{"HOME=" + dir, "CODEX_HOME=" + filepath.Join(dir, "codex-home"), "PATH=/usr/bin:/bin"}
	hr1 := CheckHealth(context.Background(), "codex", "codex", "~/.local/bin/codex", envNoAuth)
	if !hr1.Available || hr1.AuthStatus != "unauthenticated" {
		t.Fatalf("expected available + unauthenticated, got %+v", hr1)
	}

	// Authenticated when OPENROUTER_API_KEY is present in env slice.
	envOR := append(append([]string(nil), envNoAuth...), "OPENROUTER_API_KEY=sk-or-health-test")
	hr2 := CheckHealth(context.Background(), "codex", "codex", "~/.local/bin/codex", envOR)
	if !hr2.Available || hr2.AuthStatus != "ok" || !strings.Contains(hr2.Message, "OpenRouter") {
		t.Fatalf("expected AuthStatus=ok with OpenRouter, got %+v", hr2)
	}
	if strings.Contains(hr2.Message, "sk-or-health-test") {
		t.Fatalf("CheckHealth leaked raw API key in Message: %+v", hr2)
	}

	// Old version (< 0.172.0) rejected cleanly.
	oldCodex := filepath.Join(localBin, "codex-old")
	if err := os.WriteFile(oldCodex, []byte("#!/bin/sh\necho 'codex-cli 0.118.0'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	hrOld := CheckHealth(context.Background(), "codex", "codex", oldCodex, envOR)
	if hrOld.Available || !strings.Contains(hrOld.Message, "0.172.0") {
		t.Fatalf("expected old codex 0.118.0 marked unavailable, got %+v", hrOld)
	}
}
