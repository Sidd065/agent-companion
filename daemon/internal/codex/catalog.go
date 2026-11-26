package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	DefaultCatalogTTL       = 10 * time.Minute
	DefaultOpenRouterModel  = "google/gemini-3-flash-preview"
	DefaultNativeCodexModel = "gpt-5.3-codex"
	codexBaseInstructions   = "You are Codex, an expert software engineering agent. Inspect code carefully, edit files with apply_patch, and verify changes with exec_command.\n" +
		"When calling apply_patch, pass a strict Codex V4A patch wrapped in '*** Begin Patch' and '*** End Patch': " +
		"use '*** Add File: <path>' (every line prefixed with '+'), '*** Delete File: <path>', or '*** Update File: <path>' (optional '*** Move to: <new_path>') " +
		"with hunks starting with bare '@@' and every hunk line prefixed with ' ' (unchanged context), '-' (removed), or '+' (added). " +
		"Never emit 'diff --git', '---', '+++', or unified-diff '@@ -old +new @@' line numbers."
)

// CatalogModel represents one normalized model entry for providers.models & ACP setup.
type CatalogModel struct {
	ModelID           string `json:"modelId"`
	Name              string `json:"name"`
	Description       string `json:"description"`
	Provider          string `json:"provider"` // "openrouter" | "openai"
	ContextWindow     int    `json:"contextWindow"`
	SupportsReasoning bool   `json:"supportsReasoning"`
	SupportsTools     bool   `json:"supportsTools"`
	SupportsImages    bool   `json:"supportsImages"`
	IsCustom          bool   `json:"isCustom"`
}

// CustomModelInput is a user-saved custom model slug & optional display name.
type CustomModelInput struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

type codexReasoningLevel struct {
	Effort      string `json:"effort"`
	Description string `json:"description"`
}

type codexTruncationPolicy struct {
	Mode  string `json:"mode"`
	Limit int    `json:"limit"`
}

// codexModelInfo matches the empirically verified Codex 0.172.0 ModelInfo schema.
type codexModelInfo struct {
	Slug                          string                `json:"slug"`
	DisplayName                   string                `json:"display_name"`
	Description                   string                `json:"description"`
	DefaultReasoningLevel         string                `json:"default_reasoning_level"`
	SupportedReasoningLevels      []codexReasoningLevel `json:"supported_reasoning_levels"`
	ShellType                     string                `json:"shell_type"`
	Visibility                    string                `json:"visibility"`
	SupportedInAPI                bool                  `json:"supported_in_api"`
	Priority                      int                   `json:"priority"`
	AvailabilityNux               any                   `json:"availability_nux"`
	Upgrade                       any                   `json:"upgrade"`
	BaseInstructions              string                `json:"base_instructions"`
	ModelMessages                 any                   `json:"model_messages"`
	SupportsReasoningSummaries    bool                  `json:"supports_reasoning_summaries"`
	DefaultReasoningSummary       string                `json:"default_reasoning_summary"`
	SupportVerbosity              bool                  `json:"support_verbosity"`
	DefaultVerbosity              any                   `json:"default_verbosity"`
	ApplyPatchToolType            string                `json:"apply_patch_tool_type"`
	TruncationPolicy              codexTruncationPolicy `json:"truncation_policy"`
	SupportsParallelToolCalls     bool                  `json:"supports_parallel_tool_calls"`
	SupportsImageDetailOriginal   bool                  `json:"supports_image_detail_original"`
	ContextWindow                 int64                 `json:"context_window"`
	AutoCompactTokenLimit         any                   `json:"auto_compact_token_limit"`
	EffectiveContextWindowPercent int                   `json:"effective_context_window_percent"`
	ExperimentalSupportedTools    []string              `json:"experimental_supported_tools"`
	InputModalities               []string              `json:"input_modalities"`
	PreferWebsockets              bool                  `json:"prefer_websockets"`
	UseResponsesLite              bool                  `json:"use_responses_lite"`
	SupportsSearchTool            bool                  `json:"supports_search_tool"`
	ToolMode                      any                   `json:"tool_mode"`
}

type codexCatalogFile struct {
	Models []codexModelInfo `json:"models"`
}

// Catalog fetches/caches OpenRouter models (10-min TTL + refresh bypass),
// merges native OpenAI Codex models & user customModels, and maintains
// <codexStateDir>/codex-openrouter-catalog.json outside /tmp.
type Catalog struct {
	mu          sync.Mutex
	catalogPath string
	upstreamURL string
	ttl         time.Duration
	client      *http.Client

	fetchedAt        time.Time
	fetchedKeyDigest string
	openRouterModels []CatalogModel
	customModels     []CustomModelInput
	ensuredSlugs     map[string]CatalogModel
	written          bool
}

// NewCatalog creates a Catalog writing to catalogPath (must be outside /tmp).
func NewCatalog(catalogPath, upstreamURL string) *Catalog {
	if strings.TrimSpace(upstreamURL) == "" {
		upstreamURL = DefaultOpenRouterBaseURL
	}
	c := &Catalog{
		catalogPath:  catalogPath,
		upstreamURL:  strings.TrimRight(strings.TrimSpace(upstreamURL), "/"),
		ttl:          DefaultCatalogTTL,
		client:       &http.Client{Timeout: 15 * time.Second},
		ensuredSlugs: map[string]CatalogModel{},
	}
	if strings.TrimSpace(catalogPath) != "" {
		if st, err := os.Stat(catalogPath); err != nil || st.Size() == 0 {
			c.mu.Lock()
			_, _ = c.writeCatalogFileLocked(CuratedOpenRouterFallback(), nil)
			c.mu.Unlock()
		}
	}
	return c
}

// SetCustomModels updates persisted custom models included when writing codex-openrouter-catalog.json.
func (c *Catalog) SetCustomModels(custom []CustomModelInput) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(custom) != len(c.customModels) {
		c.written = false
	} else {
		for i := range custom {
			if custom[i] != c.customModels[i] {
				c.written = false
				break
			}
		}
	}
	c.customModels = append([]CustomModelInput(nil), custom...)
}

// CatalogPath returns the path to codex-openrouter-catalog.json.
func (c *Catalog) CatalogPath() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.catalogPath
}

// SetUpstreamURL overrides the OpenRouter API base URL (used in hermetic tests).
func (c *Catalog) SetUpstreamURL(u string) {
	c.mu.Lock()
	c.upstreamURL = strings.TrimRight(strings.TrimSpace(u), "/")
	c.fetchedAt = time.Time{}
	c.mu.Unlock()
}

// SetTTL overrides the cache TTL (used in unit tests).
func (c *Catalog) SetTTL(d time.Duration) {
	c.mu.Lock()
	c.ttl = d
	c.mu.Unlock()
}

// Invalidate clears the in-memory OpenRouter fetch timestamp so the next read refreshes.
func (c *Catalog) Invalidate() {
	c.mu.Lock()
	c.fetchedAt = time.Time{}
	c.mu.Unlock()
}

// NormalizeOpenRouterSlug strips an optional leading "openrouter/" provider prefix on
// 2+-slash slugs ("openrouter/<vendor>/<model>" -> "<vendor>/<model>") case-insensitively,
// while preserving first-party single-slash OpenRouter router slugs ("openrouter/free",
// "openrouter/auto", normalizing "OpenRouter/free" -> "openrouter/free").
func NormalizeOpenRouterSlug(raw string) string {
	s := strings.TrimSpace(raw)
	const prefix = "openrouter/"
	if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
		rest := strings.TrimSpace(s[len(prefix):])
		if rest == "" {
			return ""
		}
		if strings.Contains(rest, "/") {
			return rest
		}
		return prefix + rest
	}
	return s
}

// DefaultModelID returns the default model slug according to precedence:
// ProviderConfig.DefaultModel -> AGENTC_CODEX_DEFAULT_MODEL -> "google/gemini-3-flash-preview" (OpenRouter) -> "gpt-5.3-codex" (native).
func DefaultModelID(configuredDefault string, hasOpenRouter, hasNativeAuth bool) string {
	if v := strings.TrimSpace(configuredDefault); v != "" && NormalizeOpenRouterSlug(v) != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("AGENTC_CODEX_DEFAULT_MODEL")); v != "" && NormalizeOpenRouterSlug(v) != "" {
		return v
	}
	if hasOpenRouter {
		return DefaultOpenRouterModel
	}
	if hasNativeAuth {
		return DefaultNativeCodexModel
	}
	return DefaultOpenRouterModel
}

// ResolveProviderAndModel determines whether a session/turn routes through the
// OpenRouter loopback bridge or native OpenAI auth, and returns the wire model slug.
func ResolveProviderAndModel(rawModel, configuredDefault, forcedProvider string, hasOpenRouter, hasNativeAuth bool) (useOpenRouter bool, wireModel, displayModel string) {
	fp := strings.ToLower(strings.TrimSpace(forcedProvider))
	raw := strings.TrimSpace(rawModel)
	if raw == "" || NormalizeOpenRouterSlug(raw) == "" {
		if fp == "openai" || fp == "native" {
			raw = DefaultModelID(configuredDefault, false, true)
		} else {
			raw = DefaultModelID(configuredDefault, hasOpenRouter, hasNativeAuth)
		}
	}
	displayModel = raw
	if fp == "openrouter" {
		w := NormalizeOpenRouterSlug(raw)
		if !strings.Contains(w, "/") && w != "" {
			w = "openai/" + w
		}
		return true, w, displayModel
	}
	if fp == "openai" || fp == "native" {
		w := NormalizeOpenRouterSlug(raw)
		if len(w) >= len("openai/") && strings.EqualFold(w[:len("openai/")], "openai/") {
			w = strings.TrimSpace(w[len("openai/"):])
		}
		if w == "" {
			w = NormalizeOpenRouterSlug(DefaultModelID(configuredDefault, false, true))
			if len(w) >= len("openai/") && strings.EqualFold(w[:len("openai/")], "openai/") {
				w = strings.TrimSpace(w[len("openai/"):])
			}
			if w == "" {
				w = DefaultNativeCodexModel
			}
		}
		return false, w, displayModel
	}
	if strings.HasPrefix(strings.ToLower(raw), "openrouter/") {
		return true, NormalizeOpenRouterSlug(raw), displayModel
	}
	if strings.Contains(raw, "/") {
		// Any vendor/model slug routes via OpenRouter when OpenRouter is available (or when native isn't).
		if hasOpenRouter || !hasNativeAuth {
			return true, raw, displayModel
		}
		if strings.HasPrefix(strings.ToLower(raw), "openai/") {
			return false, strings.TrimSpace(raw[len("openai/"):]), displayModel
		}
		return true, raw, displayModel
	}
	// Bare slug without "/" (e.g. gpt-5.3-codex, o3, o4-mini):
	if hasNativeAuth {
		return false, raw, displayModel
	}
	if hasOpenRouter {
		return true, "openai/" + raw, displayModel
	}
	return false, raw, displayModel
}

// HasNativeOpenAIAuth checks OPENAI_API_KEY and <codexHome>/auth.json or ~/.codex/auth.json.
func HasNativeOpenAIAuth(codexHome, userHome string, env []string) bool {
	if key := envLookup(env, "OPENAI_API_KEY"); key != "" && key != "***" {
		return true
	}
	if env == nil {
		if key := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")); key != "" && key != "***" {
			return true
		}
	}
	var paths []string
	if codexHome != "" {
		paths = append(paths, filepath.Join(codexHome, "auth.json"))
	}
	if userHome != "" {
		paths = append(paths, filepath.Join(userHome, ".codex", "auth.json"))
	}
	for _, p := range paths {
		if authFileHasCredentials(p) {
			return true
		}
	}
	return false
}

func authFileHasCredentials(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil || m == nil {
		return false
	}
	if k, ok := m["OPENAI_API_KEY"].(string); ok && strings.TrimSpace(k) != "" {
		return true
	}
	if tok, ok := m["tokens"].(map[string]any); ok {
		if s, _ := tok["access_token"].(string); strings.TrimSpace(s) != "" {
			return true
		}
		if s, _ := tok["id_token"].(string); strings.TrimSpace(s) != "" {
			return true
		}
	}
	return false
}

func envLookup(env []string, key string) string {
	prefix := key + "="
	for i := len(env) - 1; i >= 0; i-- {
		if strings.HasPrefix(env[i], prefix) {
			return strings.TrimSpace(strings.TrimPrefix(env[i], prefix))
		}
	}
	return ""
}

// CuratedOpenRouterFallback provides offline/bootstrap models before live fetch.
func CuratedOpenRouterFallback() []CatalogModel {
	return []CatalogModel{
		{
			ModelID:           "google/gemini-3-flash-preview",
			Name:              "Google: Gemini 3 Flash Preview",
			Description:       "Fast multimodal reasoning & coding model via OpenRouter",
			Provider:          "openrouter",
			ContextWindow:     1048576,
			SupportsReasoning: true,
			SupportsTools:     true,
			SupportsImages:    true,
		},
		{
			ModelID:           "google/gemini-2.5-flash",
			Name:              "Google: Gemini 2.5 Flash",
			Description:       "High-throughput hybrid reasoning model via OpenRouter",
			Provider:          "openrouter",
			ContextWindow:     1048576,
			SupportsReasoning: true,
			SupportsTools:     true,
			SupportsImages:    true,
		},
		{
			ModelID:           "google/gemini-2.5-pro",
			Name:              "Google: Gemini 2.5 Pro",
			Description:       "Frontier reasoning and coding model via OpenRouter",
			Provider:          "openrouter",
			ContextWindow:     1048576,
			SupportsReasoning: true,
			SupportsTools:     true,
			SupportsImages:    true,
		},
		{
			ModelID:           "deepseek/deepseek-v4.1-flash",
			Name:              "DeepSeek: V4.1 Flash",
			Description:       "Fast reasoning & software engineering model via OpenRouter",
			Provider:          "openrouter",
			ContextWindow:     131072,
			SupportsReasoning: true,
			SupportsTools:     true,
			SupportsImages:    false,
		},
		{
			ModelID:           "openai/gpt-5.3-codex",
			Name:              "OpenAI: GPT-5.3 Codex",
			Description:       "Agentic software engineering model via OpenRouter",
			Provider:          "openrouter",
			ContextWindow:     400000,
			SupportsReasoning: true,
			SupportsTools:     true,
			SupportsImages:    true,
		},
		{
			ModelID:           "openai/gpt-4.1-mini",
			Name:              "OpenAI: GPT-4.1 Mini",
			Description:       "Fast tool-calling coding model via OpenRouter",
			Provider:          "openrouter",
			ContextWindow:     1048576,
			SupportsReasoning: false,
			SupportsTools:     true,
			SupportsImages:    true,
		},
		{
			ModelID:           "openrouter/auto",
			Name:              "OpenRouter: Auto Router",
			Description:       "Automatically routes prompts across enabled OpenRouter models",
			Provider:          "openrouter",
			ContextWindow:     2000000,
			SupportsReasoning: true,
			SupportsTools:     true,
			SupportsImages:    true,
		},
	}
}

// CuratedNativeCodexModels provides built-in OpenAI Codex models when native auth is present.
func CuratedNativeCodexModels() []CatalogModel {
	return []CatalogModel{
		{
			ModelID:           "gpt-5.3-codex",
			Name:              "GPT-5.3 Codex",
			Description:       "Latest flagship agentic coding model (OpenAI)",
			Provider:          "openai",
			ContextWindow:     400000,
			SupportsReasoning: true,
			SupportsTools:     true,
			SupportsImages:    true,
		},
		{
			ModelID:           "gpt-5.3-codex-mini",
			Name:              "GPT-5.3 Codex Mini",
			Description:       "Faster, lighter Codex model (OpenAI)",
			Provider:          "openai",
			ContextWindow:     200000,
			SupportsReasoning: true,
			SupportsTools:     true,
			SupportsImages:    true,
		},
	}
}

// SynthesizeCatalogModel creates a CatalogModel for any arbitrary user-entered slug.
func SynthesizeCatalogModel(slug, displayName string, isCustom bool) CatalogModel {
	s := strings.TrimSpace(slug)
	if displayName == "" {
		displayName = s
	}
	prov := "openrouter"
	if !strings.Contains(s, "/") {
		prov = "openai"
	}
	return CatalogModel{
		ModelID:           s,
		Name:              displayName,
		Description:       "Custom model slug (" + s + ")",
		Provider:          prov,
		ContextWindow:     200000,
		SupportsReasoning: true,
		SupportsTools:     true,
		SupportsImages:    true,
		IsCustom:          isCustom,
	}
}

// FetchModels returns the merged catalog of customModels + OpenRouter models + native models,
// and ensures codex-openrouter-catalog.json is written on disk outside /tmp.
func (c *Catalog) FetchModels(ctx context.Context, apiKey string, refresh bool, custom []CustomModelInput, nativeModels []CatalogModel, hasNativeAuth bool) ([]CatalogModel, error) {
	c.mu.Lock()
	ttl := c.ttl
	upstream := c.upstreamURL
	needFetch := refresh || c.fetchedAt.IsZero() || time.Since(c.fetchedAt) >= ttl || (apiKey != "" && c.fetchedKeyDigest != keyFingerprint(apiKey))
	cachedOR := append([]CatalogModel(nil), c.openRouterModels...)
	c.mu.Unlock()

	var fetchErr error
	if needFetch && strings.TrimSpace(apiKey) != "" {
		fetched, err := fetchOpenRouterModelsHTTP(ctx, c.client, upstream, apiKey)
		if err == nil && len(fetched) > 0 {
			c.mu.Lock()
			c.openRouterModels = fetched
			c.fetchedAt = time.Now()
			c.fetchedKeyDigest = keyFingerprint(apiKey)
			cachedOR = append([]CatalogModel(nil), fetched...)
			c.mu.Unlock()
		} else {
			fetchErr = err
		}
	}

	if len(cachedOR) == 0 && strings.TrimSpace(apiKey) != "" {
		cachedOR = CuratedOpenRouterFallback()
	}

	c.mu.Lock()
	if len(custom) > 0 {
		c.customModels = append([]CustomModelInput(nil), custom...)
	}
	effCustom := custom
	if len(effCustom) == 0 && len(c.customModels) > 0 {
		effCustom = c.customModels
	}
	merged := c.mergeLocked(cachedOR, custom, nativeModels, hasNativeAuth)
	_, writeErr := c.writeCatalogFileLocked(cachedOR, effCustom)
	c.mu.Unlock()
	if fetchErr != nil && len(merged) == 0 {
		return merged, fetchErr
	}
	if writeErr != nil && fetchErr == nil {
		fetchErr = writeErr
	}
	return merged, fetchErr
}

// EnsureModel guarantees that slug (and its stripped openrouter/ form) exists in
// codex-openrouter-catalog.json before spawning or resuming codex app-server.
// Returns wasAdded=true if slug was not previously present in the written catalog.
func (c *Catalog) EnsureModel(slug string) (wasAdded bool, catalogPath string, err error) {
	raw := strings.TrimSpace(slug)
	norm := NormalizeOpenRouterSlug(raw)
	if norm == "" {
		raw = ""
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	known := map[string]bool{}
	for _, m := range c.openRouterModels {
		known[m.ModelID] = true
		known[NormalizeOpenRouterSlug(m.ModelID)] = true
	}
	for _, m := range CuratedOpenRouterFallback() {
		known[m.ModelID] = true
		known[NormalizeOpenRouterSlug(m.ModelID)] = true
	}
	for _, cm := range c.customModels {
		s := strings.TrimSpace(cm.Slug)
		if s != "" {
			known[s] = true
			known[NormalizeOpenRouterSlug(s)] = true
		}
	}
	for k := range c.ensuredSlugs {
		known[k] = true
	}

	for _, candidate := range []string{norm, raw} {
		if candidate == "" {
			continue
		}
		if !known[candidate] {
			c.ensuredSlugs[candidate] = SynthesizeCatalogModel(candidate, candidate, true)
			known[candidate] = true
			wasAdded = true
		}
	}

	if !wasAdded && c.written && strings.TrimSpace(c.catalogPath) != "" {
		if st, stErr := os.Stat(c.catalogPath); stErr == nil && st.Size() > 0 {
			return false, c.catalogPath, nil
		}
	}

	base := c.openRouterModels
	if len(base) == 0 {
		base = CuratedOpenRouterFallback()
	}
	path, err := c.writeCatalogFileLocked(base, c.customModels)
	return wasAdded, path, err
}

// HasCatalogSlug reports whether slug is already present in the written catalog.
func (c *Catalog) HasCatalogSlug(slug string) bool {
	norm := NormalizeOpenRouterSlug(slug)
	if norm == "" {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.ensuredSlugs[norm]; ok {
		return true
	}
	if _, ok := c.ensuredSlugs[slug]; ok {
		return true
	}
	list := c.openRouterModels
	if len(list) == 0 {
		list = CuratedOpenRouterFallback()
	}
	for _, m := range list {
		if m.ModelID == norm || NormalizeOpenRouterSlug(m.ModelID) == norm || m.ModelID == slug {
			return true
		}
	}
	return false
}

func (c *Catalog) mergeLocked(orModels []CatalogModel, custom []CustomModelInput, nativeModels []CatalogModel, hasNativeAuth bool) []CatalogModel {
	out := make([]CatalogModel, 0, len(custom)+len(orModels)+len(nativeModels)+len(c.ensuredSlugs))
	seen := map[string]bool{}

	add := func(m CatalogModel) {
		id := strings.TrimSpace(m.ModelID)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		if m.Name == "" {
			m.Name = id
		}
		if m.ContextWindow <= 0 {
			m.ContextWindow = 128000
		}
		out = append(out, m)
	}

	// 1. User-persisted custom models appear first with IsCustom: true.
	for _, cm := range custom {
		slug := strings.TrimSpace(cm.Slug)
		if slug == "" {
			continue
		}
		name := strings.TrimSpace(cm.Name)
		if name == "" {
			name = slug
		}
		// Enrich metadata if OpenRouter already knows this slug.
		enriched := SynthesizeCatalogModel(slug, name, true)
		norm := NormalizeOpenRouterSlug(slug)
		for _, orm := range orModels {
			if orm.ModelID == slug || orm.ModelID == norm {
				enriched.ContextWindow = orm.ContextWindow
				enriched.SupportsReasoning = orm.SupportsReasoning
				enriched.SupportsTools = orm.SupportsTools
				enriched.SupportsImages = orm.SupportsImages
				if cm.Name == "" || cm.Name == cm.Slug {
					enriched.Name = orm.Name
				}
				if orm.Description != "" {
					enriched.Description = orm.Description
				}
				break
			}
		}
		add(enriched)
	}

	// 2. Dynamically ensured session slugs.
	for _, em := range c.ensuredSlugs {
		add(em)
	}

	// 3. OpenRouter models (preferring verified BYOK/popular coding families at top).
	prioritized := prioritizeOpenRouterModels(orModels)
	for _, m := range prioritized {
		add(m)
	}

	// 4. Native OpenAI Codex models when native auth exists (or when OpenRouter is unconfigured).
	if hasNativeAuth || len(orModels) == 0 {
		src := nativeModels
		if len(src) == 0 {
			src = CuratedNativeCodexModels()
		}
		for _, m := range src {
			add(m)
		}
	}

	return out
}

func prioritizeOpenRouterModels(in []CatalogModel) []CatalogModel {
	if len(in) == 0 {
		return nil
	}
	pinnedOrder := []string{
		"google/gemini-3-flash-preview",
		"google/gemini-2.5-flash",
		"google/gemini-2.5-pro",
		"google/gemini-2.5-flash-lite",
		"deepseek/deepseek-v4.1-flash",
		"openai/gpt-5.3-codex",
		"openai/gpt-4.1-mini",
		"openrouter/auto",
	}
	rank := map[string]int{}
	for i, id := range pinnedOrder {
		rank[id] = i + 1
	}
	top := make([]CatalogModel, 0, len(pinnedOrder))
	rest := make([]CatalogModel, 0, len(in))
	byPinned := map[int]CatalogModel{}
	for _, m := range in {
		if r := rank[m.ModelID]; r > 0 {
			byPinned[r] = m
		} else {
			rest = append(rest, m)
		}
	}
	for i := 1; i <= len(pinnedOrder); i++ {
		if m, ok := byPinned[i]; ok {
			top = append(top, m)
		}
	}
	return append(top, rest...)
}

func (c *Catalog) writeCatalogFileLocked(orModels []CatalogModel, custom []CustomModelInput) (string, error) {
	if strings.TrimSpace(c.catalogPath) == "" {
		return "", nil
	}
	entries := make([]codexModelInfo, 0, len(orModels)+len(custom)+len(c.ensuredSlugs)+8)
	seen := map[string]bool{}
	prio := 1

	appendModel := func(slug string, cm CatalogModel) {
		slug = strings.TrimSpace(slug)
		if slug == "" || seen[slug] {
			return
		}
		seen[slug] = true
		entries = append(entries, toCodexModelInfo(slug, cm, prio))
		prio++
	}

	for _, cm := range custom {
		s := strings.TrimSpace(cm.Slug)
		if s == "" {
			continue
		}
		m := SynthesizeCatalogModel(s, firstNonEmpty(cm.Name, s), true)
		appendModel(NormalizeOpenRouterSlug(s), m)
		if s != NormalizeOpenRouterSlug(s) {
			appendModel(s, m)
		}
	}
	for slug, m := range c.ensuredSlugs {
		appendModel(slug, m)
		norm := NormalizeOpenRouterSlug(slug)
		if norm != slug {
			appendModel(norm, m)
		}
	}
	for _, m := range prioritizeOpenRouterModels(orModels) {
		norm := NormalizeOpenRouterSlug(m.ModelID)
		appendModel(norm, m)
		if m.ModelID != norm {
			appendModel(m.ModelID, m)
		}
	}
	for _, m := range CuratedOpenRouterFallback() {
		appendModel(NormalizeOpenRouterSlug(m.ModelID), m)
	}

	payload, err := json.MarshalIndent(codexCatalogFile{Models: entries}, "", "  ")
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(c.catalogPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, "codex-catalog-*.tmp")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(append(payload, '\n')); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return "", err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return "", err
	}
	_ = os.Chmod(tmpName, 0o600)
	if err := os.Rename(tmpName, c.catalogPath); err != nil {
		_ = os.Remove(tmpName)
		return "", err
	}
	c.written = true
	return c.catalogPath, nil
}

func toCodexModelInfo(slug string, m CatalogModel, priority int) codexModelInfo {
	ctxWin := int64(m.ContextWindow)
	if ctxWin <= 0 {
		ctxWin = 128000
	}
	modalities := []string{"text"}
	if m.SupportsImages {
		modalities = []string{"text", "image"}
	}
	desc := strings.TrimSpace(m.Description)
	if desc == "" {
		desc = firstNonEmpty(m.Name, slug) + " via OpenRouter"
	}
	if len(desc) > 240 {
		desc = desc[:237] + "..."
	}
	return codexModelInfo{
		Slug:                  slug,
		DisplayName:           firstNonEmpty(m.Name, slug),
		Description:           desc,
		DefaultReasoningLevel: "medium",
		SupportedReasoningLevels: []codexReasoningLevel{
			{Effort: "low", Description: "Fast responses with lighter reasoning"},
			{Effort: "medium", Description: "Balanced speed and reasoning depth"},
			{Effort: "high", Description: "Greater reasoning depth for complex tasks"},
			{Effort: "xhigh", Description: "Maximum reasoning depth for hard problems"},
		},
		ShellType:                     "shell_command",
		Visibility:                    "list",
		SupportedInAPI:                true,
		Priority:                      priority,
		AvailabilityNux:               nil,
		Upgrade:                       nil,
		BaseInstructions:              codexBaseInstructions,
		ModelMessages:                 nil,
		SupportsReasoningSummaries:    true,
		DefaultReasoningSummary:       "auto",
		SupportVerbosity:              false,
		DefaultVerbosity:              nil,
		ApplyPatchToolType:            "freeform",
		TruncationPolicy:              codexTruncationPolicy{Mode: "bytes", Limit: 10000},
		SupportsParallelToolCalls:     true,
		SupportsImageDetailOriginal:   false,
		ContextWindow:                 ctxWin,
		AutoCompactTokenLimit:         nil,
		EffectiveContextWindowPercent: 95,
		ExperimentalSupportedTools:    []string{},
		InputModalities:               modalities,
		PreferWebsockets:              false,
		UseResponsesLite:              false,
		SupportsSearchTool:            false,
		ToolMode:                      nil,
	}
}

type openRouterModelsResponse struct {
	Data []struct {
		ID              string   `json:"id"`
		Name            string   `json:"name"`
		Description     string   `json:"description"`
		ContextLength   int      `json:"context_length"`
		SupportedParams []string `json:"supported_parameters"`
		Architecture    struct {
			Modality        string   `json:"modality"`
			InputModalities []string `json:"input_modalities"`
		} `json:"architecture"`
		TopProvider struct {
			ContextLength int `json:"context_length"`
		} `json:"top_provider"`
	} `json:"data"`
}

func fetchOpenRouterModelsHTTP(ctx context.Context, client *http.Client, upstream, apiKey string) ([]CatalogModel, error) {
	// Try /models/user first (user-scoped/BYOK enabled models), falling back to /models.
	endpoints := []string{upstream + "/models/user", upstream + "/models"}
	var lastErr error
	for _, ep := range endpoints {
		models, err := fetchOneModelsEndpoint(ctx, client, ep, apiKey)
		if err == nil && len(models) > 0 {
			return models, nil
		}
		if err != nil {
			lastErr = err
		}
	}
	return nil, lastErr
}

func fetchOneModelsEndpoint(ctx context.Context, client *http.Client, url, apiKey string) ([]CatalogModel, error) {
	cctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(apiKey) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(apiKey))
	}
	req.Header.Set("HTTP-Referer", "https://github.com/Sidd065/agent-companion")
	req.Header.Set("X-Title", "agentc")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("openrouter %s HTTP %d: %s", url, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	var parsed openRouterModelsResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	out := make([]CatalogModel, 0, len(parsed.Data))
	for _, d := range parsed.Data {
		id := strings.TrimSpace(d.ID)
		if id == "" {
			continue
		}
		ctxWin := d.ContextLength
		if ctxWin <= 0 {
			ctxWin = d.TopProvider.ContextLength
		}
		if ctxWin <= 0 {
			ctxWin = 128000
		}
		hasTools := false
		hasReasoning := false
		for _, p := range d.SupportedParams {
			switch strings.ToLower(p) {
			case "tools", "tool_choice":
				hasTools = true
			case "reasoning", "include_reasoning":
				hasReasoning = true
			}
		}
		// If supported_parameters was omitted, assume chat models support tools unless image-generation-only.
		if len(d.SupportedParams) == 0 {
			hasTools = true
		}
		hasImg := strings.Contains(strings.ToLower(d.Architecture.Modality), "image")
		for _, im := range d.Architecture.InputModalities {
			if strings.EqualFold(im, "image") {
				hasImg = true
			}
		}
		desc := strings.TrimSpace(d.Description)
		if i := strings.IndexByte(desc, '\n'); i >= 0 {
			desc = strings.TrimSpace(desc[:i])
		}
		if len(desc) > 220 {
			desc = desc[:217] + "..."
		}
		out = append(out, CatalogModel{
			ModelID:           id,
			Name:              firstNonEmpty(strings.TrimSpace(d.Name), id),
			Description:       desc,
			Provider:          "openrouter",
			ContextWindow:     ctxWin,
			SupportsReasoning: hasReasoning,
			SupportsTools:     hasTools,
			SupportsImages:    hasImg,
		})
	}
	return out, nil
}

func keyFingerprint(k string) string {
	k = strings.TrimSpace(k)
	if len(k) <= 8 {
		return fmt.Sprintf("len:%d", len(k))
	}
	// Non-reversible length + edge byte hash so raw key is never stored here either.
	var h uint64 = 14695981039346656037
	for i := 0; i < len(k); i++ {
		h ^= uint64(k[i])
		h *= 1099511628211
	}
	return fmt.Sprintf("%x", h)
}
