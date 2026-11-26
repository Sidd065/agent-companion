package codex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func nonTmpCodexTestDir(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil || home == "" || strings.HasPrefix(home, "/tmp") {
		home = "/var/tmp"
	}
	base := filepath.Join(home, ".cache", "agentc-gotest")
	_ = os.MkdirAll(base, 0o700)
	dir, err := os.MkdirTemp(base, "codex-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestCatalogFetchTTLRefreshAndEnsureModel(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models/user" {
			hits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"data": [
					{
						"id": "google/gemini-3-flash-preview",
						"name": "Google: Gemini 3 Flash Preview",
						"description": "Fast Gemini 3 model",
						"context_length": 1048576,
						"supported_parameters": ["tools", "reasoning"],
						"architecture": {"modality": "text+image->text", "input_modalities": ["text", "image"]}
					},
					{
						"id": "google/gemini-2.5-flash",
						"name": "Google: Gemini 2.5 Flash",
						"context_length": 1048576,
						"supported_parameters": ["tools", "reasoning"],
						"architecture": {"input_modalities": ["text", "image"]}
					}
				]
			}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	dir := nonTmpCodexTestDir(t)
	catPath := filepath.Join(dir, "codex-openrouter-catalog.json")
	if strings.HasPrefix(catPath, "/tmp/") {
		t.Fatalf("catalog path must not be under /tmp: %s", catPath)
	}

	cat := NewCatalog(catPath, srv.URL)
	ctx := context.Background()

	models1, err := cat.FetchModels(ctx, "sk-test", false, []CustomModelInput{{Slug: "my-org/custom-coder-v1", Name: "Custom Coder"}}, nil, false)
	if err != nil {
		t.Fatalf("FetchModels #1: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("expected 1 HTTP fetch, got %d", hits.Load())
	}
	if len(models1) < 3 || models1[0].ModelID != "my-org/custom-coder-v1" || !models1[0].IsCustom {
		t.Fatalf("expected custom model first, got %+v", models1)
	}

	// Cached read within 10-min TTL does not hit network.
	_, err = cat.FetchModels(ctx, "sk-test", false, nil, nil, false)
	if err != nil || hits.Load() != 1 {
		t.Fatalf("expected cached hit count 1, got %d (err=%v)", hits.Load(), err)
	}

	// refresh=true bypasses TTL.
	_, err = cat.FetchModels(ctx, "sk-test", true, nil, nil, false)
	if err != nil || hits.Load() != 2 {
		t.Fatalf("expected refresh hit count 2, got %d (err=%v)", hits.Load(), err)
	}

	// EnsureModel dynamically synthesizes an unlisted slug and updates codex-openrouter-catalog.json.
	added, writtenPath, err := cat.EnsureModel("openrouter/vendor/brand-new-2027")
	if err != nil || !added || writtenPath != catPath {
		t.Fatalf("EnsureModel first call: added=%v path=%q err=%v", added, writtenPath, err)
	}
	added2, _, err := cat.EnsureModel("vendor/brand-new-2027")
	if err != nil || added2 {
		t.Fatalf("EnsureModel second call should report added=false, got added=%v err=%v", added2, err)
	}

	rawFile, err := os.ReadFile(catPath)
	if err != nil {
		t.Fatal(err)
	}
	var parsed codexCatalogFile
	if err := json.Unmarshal(rawFile, &parsed); err != nil {
		t.Fatalf("parse catalog file: %v", err)
	}
	foundUnlisted := false
	for _, mi := range parsed.Models {
		if mi.ApplyPatchToolType != "freeform" {
			t.Fatalf("model %s has invalid apply_patch_tool_type %q (must be freeform)", mi.Slug, mi.ApplyPatchToolType)
		}
		if mi.PreferWebsockets || mi.UseResponsesLite || mi.SupportsSearchTool || mi.ToolMode != nil {
			t.Fatalf("model %s has invalid boolean/tool_mode flags: %+v", mi.Slug, mi)
		}
		if mi.Slug == "vendor/brand-new-2027" {
			foundUnlisted = true
		}
	}
	if !foundUnlisted {
		t.Fatalf("synthesized slug vendor/brand-new-2027 missing from catalog JSON")
	}

	// Verify DefaultModelID precedence.
	t.Setenv("AGENTC_CODEX_DEFAULT_MODEL", "")
	if got := DefaultModelID("", true, false); got != "google/gemini-3-flash-preview" {
		t.Fatalf("DefaultModelID default = %q", got)
	}
	t.Setenv("AGENTC_CODEX_DEFAULT_MODEL", "google/gemini-2.5-flash")
	if got := DefaultModelID("", true, false); got != "google/gemini-2.5-flash" {
		t.Fatalf("DefaultModelID env = %q", got)
	}
	if got := DefaultModelID("deepseek/deepseek-v4.1-flash", true, false); got != "deepseek/deepseek-v4.1-flash" {
		t.Fatalf("DefaultModelID configured = %q", got)
	}
}

func TestCatalogBootstrapWriteAndSetCustomModelsPreservation(t *testing.T) {
	dir := nonTmpCodexTestDir(t)
	catPath := filepath.Join(dir, "bootstrap-catalog.json")

	cat := NewCatalog(catPath, "http://127.0.0.1:9")
	st, err := os.Stat(catPath)
	if err != nil || st.Size() == 0 {
		t.Fatalf("expected NewCatalog to write bootstrap catalog immediately, err=%v st=%+v", err, st)
	}
	rawBoot, err := os.ReadFile(catPath)
	if err != nil {
		t.Fatal(err)
	}
	var bootParsed codexCatalogFile
	if err := json.Unmarshal(rawBoot, &bootParsed); err != nil || len(bootParsed.Models) == 0 {
		t.Fatalf("invalid bootstrap catalog JSON: err=%v models=%d", err, len(bootParsed.Models))
	}
	for _, wantRule := range []string{"*** Begin Patch", "*** Update File:", "@@"} {
		if !strings.Contains(bootParsed.Models[0].BaseInstructions, wantRule) {
			t.Fatalf("bootstrap BaseInstructions missing V4A rule %q: %s", wantRule, bootParsed.Models[0].BaseInstructions)
		}
	}

	// SetCustomModels followed by EnsureModel must preserve custom slugs on disk.
	cat.SetCustomModels([]CustomModelInput{{Slug: "custom-org/alpha-v2", Name: "Alpha V2"}})
	if _, _, err := cat.EnsureModel(DefaultOpenRouterModel); err != nil {
		t.Fatalf("EnsureModel after SetCustomModels: %v", err)
	}
	rawAfter, err := os.ReadFile(catPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rawAfter), `"slug": "custom-org/alpha-v2"`) {
		t.Fatalf("expected SetCustomModels slug preserved in written catalog:\n%s", string(rawAfter))
	}
}

func TestNormalizeOpenRouterSlugRouterAndMixedCase(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"openrouter/auto", "openrouter/auto"},
		{"openrouter/free", "openrouter/free"},
		{"OpenRouter/free", "openrouter/free"},
		{"OPENROUTER/auto", "openrouter/auto"},
		{"OpenRouter/auto:nitro", "openrouter/auto:nitro"},
		{"openrouter/google/gemini-3-flash-preview", "google/gemini-3-flash-preview"},
		{"OpenRouter/google/gemini-2.5-flash:thinking", "google/gemini-2.5-flash:thinking"},
		{"OPENROUTER/openai/gpt-4.1-mini", "openai/gpt-4.1-mini"},
		{"google/gemini-3-flash-preview", "google/gemini-3-flash-preview"},
	}
	for _, tc := range cases {
		if got := NormalizeOpenRouterSlug(tc.in); got != tc.want {
			t.Fatalf("NormalizeOpenRouterSlug(%q) = %q, want %q", tc.in, got, tc.want)
		}
		useOR, wire, disp := ResolveProviderAndModel(tc.in, "", "", true, true)
		if !useOR || wire != tc.want || disp != tc.in {
			t.Fatalf("ResolveProviderAndModel(%q) = (useOR=%v, wire=%q, disp=%q), want (true, %q, %q)", tc.in, useOR, wire, disp, tc.want, tc.in)
		}
	}

	// Trailing-slash bare prefixes normalize to "" and fall back to DefaultModelID.
	for _, emptySlug := range []string{"openrouter/", "OpenRouter/", "openrouter/   ", "  OPENROUTER/  "} {
		if got := NormalizeOpenRouterSlug(emptySlug); got != "" {
			t.Fatalf("NormalizeOpenRouterSlug(%q) = %q, want empty", emptySlug, got)
		}
		useOR, wire, disp := ResolveProviderAndModel(emptySlug, "deepseek/deepseek-v4.1-flash", "", true, true)
		if !useOR || wire != "deepseek/deepseek-v4.1-flash" || disp != "deepseek/deepseek-v4.1-flash" {
			t.Fatalf("ResolveProviderAndModel(%q) = (%v, %q, %q), want (true, deepseek/deepseek-v4.1-flash, deepseek/deepseek-v4.1-flash)", emptySlug, useOR, wire, disp)
		}
		useORN, wireN, dispN := ResolveProviderAndModel(emptySlug, "", "openai", true, true)
		if useORN || wireN != DefaultNativeCodexModel || dispN != DefaultNativeCodexModel {
			t.Fatalf("ResolveProviderAndModel(%q, openai) = (%v, %q, %q), want (false, %q, %q)", emptySlug, useORN, wireN, dispN, DefaultNativeCodexModel, DefaultNativeCodexModel)
		}
	}

	// Forced provider "openai" / "native" strips case-insensitive "openai/" (and "openrouter/openai/").
	for _, oaSlug := range []string{"openai/gpt-4.1-mini", "OpenAI/gpt-4.1-mini", "openrouter/openai/gpt-4.1-mini", "OpenRouter/OpenAI/gpt-4.1-mini"} {
		useOR, wire, disp := ResolveProviderAndModel(oaSlug, "", "openai", true, true)
		if useOR || wire != "gpt-4.1-mini" || disp != oaSlug {
			t.Fatalf("ResolveProviderAndModel(%q, forced=openai) = (%v, %q, %q), want (false, gpt-4.1-mini, %q)", oaSlug, useOR, wire, disp, oaSlug)
		}
	}

	// Agent.handleSetModel ensures wireModel ("openai/gpt-4.1-mini") in catalog when want is bare ("gpt-4.1-mini") on OpenRouter.
	dir := nonTmpCodexTestDir(t)
	catPath := filepath.Join(dir, "setmodel-wire-catalog.json")
	cat := NewCatalog(catPath, "http://127.0.0.1:9")
	if added, _, err := cat.EnsureModel("openrouter/"); err != nil || added {
		t.Fatalf("EnsureModel(openrouter/) must not insert invalid slug: added=%v err=%v", added, err)
	}
	rawCatBytes, err := os.ReadFile(catPath)
	if err != nil {
		t.Fatalf("read catPath: %v", err)
	}
	if strings.Contains(string(rawCatBytes), `"openrouter/"`) {
		t.Fatalf("catalog JSON must not contain bare openrouter/ slug: %s", string(rawCatBytes))
	}
	ag := Start(Options{
		Bin:           "nonexistent-codex",
		Cwd:           dir,
		CodexHome:     filepath.Join(dir, "codex-home"),
		Provider:      "openrouter",
		HasOpenRouter: true,
		Catalog:       cat,
	})
	defer ag.Close(time.Second)
	if _, err := ag.handleSetModel(context.Background(), "gpt-4.1-mini"); err != nil {
		t.Fatalf("handleSetModel(gpt-4.1-mini): %v", err)
	}
	if !cat.HasCatalogSlug("openai/gpt-4.1-mini") {
		t.Fatalf("expected handleSetModel to EnsureModel(wireModel=openai/gpt-4.1-mini) in catalog")
	}
}
