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
	"strings"
	"sync"
	"time"
)

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
	Icon         string            `json:"icon,omitempty"`
	Builtin      bool              `json:"builtin,omitempty"`
	Features     []string          `json:"features,omitempty"`
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
			s.providers[p.ID] = p
		}
	}
	return s, nil
}

// List returns all custom/override provider configs sorted by ID.
func (s *Store) List() []ProviderConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ProviderConfig, 0, len(s.providers))
	for _, p := range s.providers {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Get returns a provider config by ID.
func (s *Store) Get(id string) (ProviderConfig, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.providers[id]
	return p, ok
}

// Upsert validates and persists a provider configuration.
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
	s.mu.Lock()
	s.providers[p.ID] = p
	err := s.saveLocked()
	s.mu.Unlock()
	return p, err
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
		list = append(list, p)
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

// CheckHealth probes a provider binary for availability, version, and basic auth state.
func CheckHealth(ctx context.Context, id, driver, command string, env []string) HealthResult {
	start := time.Now()
	res := HealthResult{ID: id, Command: command, CheckedAt: start.UnixMilli(), AuthStatus: "unknown"}
	if strings.TrimSpace(command) == "" {
		res.Message = "no command configured"
		res.LatencyMs = time.Since(start).Milliseconds()
		return res
	}
	resolved, err := exec.LookPath(command)
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
		if os.Getenv("OPENAI_API_KEY") != "" {
			res.AuthStatus = "ok"
		} else if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".codex", "auth.json")); err == nil {
			res.AuthStatus = "ok"
		}
	default:
		res.AuthStatus = "ok"
	}

	res.LatencyMs = time.Since(start).Milliseconds()
	return res
}

// MergeEnv appends custom env key-value pairs onto a base environment slice.
func MergeEnv(base []string, extra map[string]string) []string {
	if len(extra) == 0 {
		return base
	}
	out := make([]string, 0, len(base)+len(extra))
	for _, kv := range base {
		k := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			k = kv[:i]
		}
		if _, override := extra[k]; !override {
			out = append(out, kv)
		}
	}
	for k, v := range extra {
		if strings.TrimSpace(k) != "" {
			out = append(out, k+"="+v)
		}
	}
	return out
}
