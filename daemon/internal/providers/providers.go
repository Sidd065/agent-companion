// Package providers manages configured coding-agent providers (built-in Claude,
// agy, Codex, plus user-configured generic ACP agents) and health checks.
package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RedactedSecret masks non-empty secret environment values over RPC and CLI output.
const RedactedSecret = "***"

// CustomModel is a user-saved custom model slug (e.g. an OpenRouter vendor/model ID).
type CustomModel struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// ProviderConfig is a user-configurable provider definition persisted in providers.json.
type ProviderConfig struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Driver       string            `json:"driver"` // claude | agy | codex | acp
	Enabled      bool              `json:"enabled"`
	Command      string            `json:"command,omitempty"`
	Args         []string          `json:"args,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
	DefaultModel string            `json:"defaultModel,omitempty"`
	CustomModels []CustomModel     `json:"customModels,omitempty"`
	Icon         string            `json:"icon,omitempty"`
	Builtin      bool              `json:"builtin,omitempty"`
	Features     []string          `json:"features,omitempty"`
}

// ProviderModelsParams is the request payload for `providers.models`.
type ProviderModelsParams struct {
	Provider string `json:"provider"`
	Refresh  bool   `json:"refresh,omitempty"`
}

// ProviderModelInfo describes one model in `providers.models`.
type ProviderModelInfo struct {
	ModelID           string `json:"modelId"`
	Name              string `json:"name"`
	Description       string `json:"description"`
	Provider          string `json:"provider"`
	ContextWindow     int    `json:"contextWindow"`
	SupportsReasoning bool   `json:"supportsReasoning"`
	SupportsTools     bool   `json:"supportsTools"`
	SupportsImages    bool   `json:"supportsImages"`
	IsCustom          bool   `json:"isCustom"`
}

// ProviderModeInfo describes an available execution/permission mode for a provider.
type ProviderModeInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ProviderModelsResult is the response payload of `providers.models`.
type ProviderModelsResult struct {
	Provider             string              `json:"provider"`
	DefaultModelID       string              `json:"defaultModelId"`
	OpenRouterConfigured bool                `json:"openRouterConfigured"`
	NativeAuthConfigured bool                `json:"nativeAuthConfigured"`
	Models               []ProviderModelInfo `json:"models"`
	CustomModels         []CustomModel       `json:"customModels"`
	AvailableModes       []ProviderModeInfo  `json:"availableModes"`
	ReasoningEfforts     []string            `json:"reasoningEfforts"`
}

// HealthResult reports live diagnostics for a provider.
type HealthResult struct {
	ID         string `json:"id"`
	Available  bool   `json:"available"`
	Command    string `json:"command"`
	Version    string `json:"version,omitempty"`
	AuthStatus string `json:"authStatus,omitempty"` // ok | unauthenticated | unknown
	Message    string `json:"message,omitempty"`
	LatencyMs  int64  `json:"latencyMs"`
	CheckedAt  int64  `json:"checkedAt"`
}

// IsSecretEnvKey reports whether an environment variable name holds a secret credential.
func IsSecretEnvKey(k string) bool {
	u := strings.ToUpper(strings.TrimSpace(k))
	if u == "" {
		return false
	}
	switch u {
	case "OPENROUTER_API_KEY", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "XAI_API_KEY", "CURSOR_API_KEY":
		return true
	}
	for _, suf := range []string{"_API_KEY", "_TOKEN", "_SECRET", "_PASSWORD", "_PRIVATE_KEY", "_ACCESS_KEY"} {
		if strings.HasSuffix(u, suf) {
			return true
		}
	}
	for _, sub := range []string{"SECRET_KEY", "API_KEY", "AUTH_TOKEN", "BEARER_TOKEN"} {
		if strings.Contains(u, sub) {
			return true
		}
	}
	return false
}

// Clone returns a deep copy of p safe against concurrent map/slice mutation.
func (p ProviderConfig) Clone() ProviderConfig {
	out := p
	if p.Args != nil {
		out.Args = append([]string(nil), p.Args...)
	}
	if p.Features != nil {
		out.Features = append([]string(nil), p.Features...)
	}
	if p.CustomModels != nil {
		out.CustomModels = append([]CustomModel(nil), p.CustomModels...)
	}
	if p.Env != nil {
		out.Env = make(map[string]string, len(p.Env))
		for k, v := range p.Env {
			out.Env[k] = v
		}
	}
	return out
}

// Redacted returns a deep copy of p with all non-empty secret env vars masked to "***".
func (p ProviderConfig) Redacted() ProviderConfig {
	out := p.Clone()
	for k, v := range out.Env {
		if v != "" && IsSecretEnvKey(k) {
			out.Env[k] = RedactedSecret
		}
	}
	return out
}

func normalizeCustomModels(in []CustomModel) []CustomModel {
	if len(in) == 0 {
		return nil
	}
	var out []CustomModel
	seen := map[string]bool{}
	for _, cm := range in {
		slug := strings.TrimSpace(cm.Slug)
		if slug == "" || seen[slug] {
			continue
		}
		seen[slug] = true
		name := strings.TrimSpace(cm.Name)
		if name == "" {
			name = slug
		}
		out = append(out, CustomModel{Slug: slug, Name: name})
	}
	return out
}

// Store persists custom provider configurations and overrides atomically.
type Store struct {
	path      string
	mu        sync.RWMutex
	providers map[string]ProviderConfig
}

var validIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,47}$`)

// Open loads providers.json from path (missing file is fine).
func Open(path string) (*Store, error) {
	s := &Store{path: path, providers: map[string]ProviderConfig{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var list []ProviderConfig
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, fmt.Errorf("providers %s: %w", path, err)
	}
	for _, p := range list {
		if p.ID != "" {
			p.CustomModels = normalizeCustomModels(p.CustomModels)
			s.providers[p.ID] = p.Clone()
		}
	}
	return s, nil
}

// List returns deep-cloned provider configs sorted by ID (for internal daemon callers).
func (s *Store) List() []ProviderConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ProviderConfig, 0, len(s.providers))
	for _, p := range s.providers {
		out = append(out, p.Clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ListRedacted returns deep-cloned provider configs with secret env values masked to "***".
func (s *Store) ListRedacted() []ProviderConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ProviderConfig, 0, len(s.providers))
	for _, p := range s.providers {
		out = append(out, p.Redacted())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Get returns a deep-cloned provider config by ID.
func (s *Store) Get(id string) (ProviderConfig, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.providers[id]
	if !ok {
		return ProviderConfig{}, false
	}
	return p.Clone(), true
}

// Upsert validates and persists a provider configuration, preserving existing
// stored secrets when "***" is passed or when Env is nil.
func (s *Store) Upsert(p ProviderConfig) (ProviderConfig, error) {
	p.ID = strings.ToLower(strings.TrimSpace(p.ID))
	if !validIDRe.MatchString(p.ID) {
		return ProviderConfig{}, fmt.Errorf("invalid provider id %q (use lowercase letters, digits, - or _)", p.ID)
	}
	if strings.TrimSpace(p.Name) == "" {
		p.Name = p.ID
	}
	switch p.Driver {
	case "", "acp":
		p.Driver = "acp"
		if strings.TrimSpace(p.Command) == "" && !p.Builtin {
			return ProviderConfig{}, errors.New("command is required for ACP providers")
		}
	case "claude", "agy", "codex":
	default:
		return ProviderConfig{}, fmt.Errorf("unsupported provider driver %q", p.Driver)
	}
	if p.Driver == "acp" && len(p.Features) == 0 {
		p.Features = []string{"rename", "archive", "pin", "delete", "export", "changes", "terminal", "modes", "effort"}
	}
	p.DefaultModel = strings.TrimSpace(p.DefaultModel)
	p.CustomModels = normalizeCustomModels(p.CustomModels)

	s.mu.Lock()
	if prev, exists := s.providers[p.ID]; exists {
		if p.Env == nil && len(prev.Env) > 0 {
			p.Env = make(map[string]string, len(prev.Env))
			for k, v := range prev.Env {
				p.Env[k] = v
			}
		} else if p.Env != nil {
			cleaned := make(map[string]string, len(p.Env))
			for k, v := range p.Env {
				k = strings.TrimSpace(k)
				if k == "" {
					continue
				}
				if v == RedactedSecret && IsSecretEnvKey(k) {
					if old, ok := prev.Env[k]; ok && old != "" {
						cleaned[k] = old
					}
					continue
				}
				if v == "" {
					// Empty string explicitly clears the env var.
					continue
				}
				cleaned[k] = v
			}
			if len(cleaned) > 0 {
				p.Env = cleaned
			} else {
				p.Env = nil
			}
		}
	} else if p.Env != nil {
		cleaned := make(map[string]string, len(p.Env))
		for k, v := range p.Env {
			k = strings.TrimSpace(k)
			if k == "" || v == "" || (v == RedactedSecret && IsSecretEnvKey(k)) {
				continue
			}
			cleaned[k] = v
		}
		if len(cleaned) > 0 {
			p.Env = cleaned
		} else {
			p.Env = nil
		}
	}
	saved := p.Clone()
	s.providers[p.ID] = saved
	err := s.saveLocked()
	s.mu.Unlock()
	return saved.Clone(), err
}

// Remove deletes a custom provider configuration. Built-in providers cannot be deleted (disable them instead).
func (s *Store) Remove(id string) error {
	if id == "claude" || id == "agy" || id == "codex" {
		return fmt.Errorf("cannot delete built-in provider %q; disable it instead", id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.providers[id]; !ok {
		return errors.New("provider not found")
	}
	delete(s.providers, id)
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	list := make([]ProviderConfig, 0, len(s.providers))
	for _, p := range s.providers {
		list = append(list, p.Clone())
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), filepath.Base(s.path)+".tmp*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err = f.Write(b); err == nil {
		err = f.Close()
	} else {
		_ = f.Close()
	}
	if err == nil {
		_ = os.Chmod(tmp, 0o600)
		err = os.Rename(tmp, s.path)
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}

var semverHealthRe = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

func codexVersionSupported(ver string) bool {
	m := semverHealthRe.FindStringSubmatch(ver)
	if len(m) != 4 {
		return strings.TrimSpace(ver) != ""
	}
	maj, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	if maj > 0 {
		return true
	}
	return min >= 172
}

func envGet(env []string, key string) string {
	prefix := key + "="
	for i := len(env) - 1; i >= 0; i-- {
		if strings.HasPrefix(env[i], prefix) {
			return strings.TrimSpace(strings.TrimPrefix(env[i], prefix))
		}
	}
	return strings.TrimSpace(os.Getenv(key))
}

func expandCommandTilde(command, home string) string {
	c := strings.TrimSpace(command)
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	if c == "~" && home != "" {
		return home
	}
	if strings.HasPrefix(c, "~/") && home != "" {
		return filepath.Join(home, c[2:])
	}
	return c
}

// CheckHealth probes a provider binary for availability, version, and basic auth state.
func CheckHealth(ctx context.Context, id, driver, command string, env []string) HealthResult {
	start := time.Now()
	home := envGet(env, "HOME")
	expandedCmd := expandCommandTilde(command, home)
	res := HealthResult{ID: id, Command: expandedCmd, CheckedAt: start.UnixMilli(), AuthStatus: "unknown"}
	if expandedCmd == "" {
		res.Message = "no command configured"
		res.LatencyMs = time.Since(start).Milliseconds()
		return res
	}
	resolved, err := exec.LookPath(expandedCmd)
	if err != nil {
		res.Message = fmt.Sprintf("executable not found: %s", command)
		res.LatencyMs = time.Since(start).Milliseconds()
		return res
	}
	res.Command = resolved
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, resolved, "--version")
	if len(env) > 0 {
		cmd.Env = env
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		v := strings.TrimSpace(string(out))
		if i := strings.IndexByte(v, '\n'); i >= 0 {
			v = v[:i]
		}
		res.Available = true
		res.Version = v
	} else {
		// Some ACP binaries do not implement --version; executable existing is still available.
		res.Available = true
		res.Message = "binary present (no --version output)"
	}

	switch driver {
	case "claude":
		actx, acancel := context.WithTimeout(ctx, 8*time.Second)
		acmd := exec.CommandContext(actx, resolved, "auth", "status")
		if len(env) > 0 {
			acmd.Env = env
		}
		aout, aerr := acmd.CombinedOutput()
		acancel()
		s := strings.ToLower(string(aout))
		if aerr == nil && (strings.Contains(s, "logged") || strings.Contains(s, "authenticated") || strings.Contains(s, "true")) {
			res.AuthStatus = "ok"
		} else if strings.Contains(s, "not logged") || strings.Contains(s, "false") {
			res.AuthStatus = "unauthenticated"
			res.Message = "Run `claude auth login` on this host"
		}
	case "agy":
		actx, acancel := context.WithTimeout(ctx, 8*time.Second)
		acmd := exec.CommandContext(actx, resolved, "models")
		if len(env) > 0 {
			acmd.Env = env
		}
		aout, aerr := acmd.CombinedOutput()
		acancel()
		if aerr == nil && len(strings.TrimSpace(string(aout))) > 0 {
			res.AuthStatus = "ok"
		} else {
			res.AuthStatus = "unknown"
		}
	case "codex":
		if res.Version != "" && !codexVersionSupported(res.Version) {
			res.Available = false
			res.AuthStatus = "unknown"
			res.Message = fmt.Sprintf("codex >= 0.172.0 required (found %s)", res.Version)
			break
		}
		orKey := envGet(env, "OPENROUTER_API_KEY")
		hasOR := orKey != "" && orKey != RedactedSecret
		oaKey := envGet(env, "OPENAI_API_KEY")
		hasOA := oaKey != "" && oaKey != RedactedSecret
		if !hasOA {
			codexHome := envGet(env, "CODEX_HOME")
			for _, p := range []string{
				filepath.Join(codexHome, "auth.json"),
				filepath.Join(home, ".codex", "auth.json"),
			} {
				if codexHome == "" && strings.HasPrefix(p, "auth.json") {
					continue
				}
				if hasCodexAuthJSON(p) {
					hasOA = true
					break
				}
			}
		}
		switch {
		case hasOR && hasOA:
			res.AuthStatus = "ok"
			res.Message = "OpenRouter and OpenAI auth configured"
		case hasOR:
			res.AuthStatus = "ok"
			res.Message = "OpenRouter configured"
		case hasOA:
			res.AuthStatus = "ok"
			res.Message = "OpenAI auth configured"
		default:
			res.AuthStatus = "unauthenticated"
			res.Message = "Set OPENROUTER_API_KEY or run `codex login`"
		}
	default:
		res.AuthStatus = "ok"
	}

	res.LatencyMs = time.Since(start).Milliseconds()
	return res
}

func hasCodexAuthJSON(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
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

// MergeEnv appends custom env key-value pairs onto a base environment slice,
// skipping redacted placeholder values ("***") for secret keys.
func MergeEnv(base []string, extra map[string]string) []string {
	if len(extra) == 0 {
		return base
	}
	filtered := make(map[string]string, len(extra))
	for k, v := range extra {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if v == RedactedSecret && IsSecretEnvKey(k) {
			continue
		}
		filtered[k] = v
	}
	if len(filtered) == 0 {
		return base
	}
	out := make([]string, 0, len(base)+len(filtered))
	for _, kv := range base {
		k := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			k = kv[:i]
		}
		if _, override := filtered[k]; !override {
			out = append(out, kv)
		}
	}
	for k, v := range filtered {
		out = append(out, k+"="+v)
	}
	return out
}
