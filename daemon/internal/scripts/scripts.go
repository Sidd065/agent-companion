// Package scripts discovers runnable project scripts from user overrides,
// `.agentc.json`, `t3.json`, `package.json`, and `Makefile`.
package scripts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"agentc/internal/projects"
)

// Script is a runnable project action shown in the thread info card and command palette.
type Script struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	Command             string `json:"command"`
	Cwd                 string `json:"cwd,omitempty"`
	Source              string `json:"source"` // custom | agentc.json | t3.json | package.json | makefile
	Icon                string `json:"icon,omitempty"`
	RunOnWorktreeCreate bool   `json:"runOnWorktreeCreate,omitempty"`
	RunOnSettle         bool   `json:"runOnSettle,omitempty"`
	StopOnSettle        bool   `json:"stopOnSettle,omitempty"`
	Keybinding          string `json:"keybinding,omitempty"`
}

// Discover merges scripts from user overrides + config files + package.json + Makefile.
func Discover(dir string, custom []projects.ScriptDef) []Script {
	var out []Script
	seen := map[string]bool{}
	add := func(s Script) {
		if strings.TrimSpace(s.Command) == "" {
			return
		}
		if s.ID == "" {
			s.ID = slug(s.Name)
		}
		if seen[s.ID] {
			return
		}
		seen[s.ID] = true
		out = append(out, s)
	}

	// 1. User-defined project scripts (highest priority).
	for _, c := range custom {
		add(Script{
			ID: c.ID, Name: c.Name, Command: c.Command, Cwd: c.Cwd, Source: "custom",
			Icon: c.Icon, RunOnWorktreeCreate: c.RunOnWorktreeCreate,
			RunOnSettle: c.RunOnSettle, StopOnSettle: c.StopOnSettle, Keybinding: c.Keybinding,
		})
	}

	// 2. Checked-in `.agentc.json` or `t3.json` (compatible with T3 Code project config!).
	for _, fname := range []string{".agentc.json", "t3.json"} {
		b, err := os.ReadFile(filepath.Join(dir, fname))
		if err != nil {
			continue
		}
		var cfg struct {
			Scripts []struct {
				ID                  string `json:"id"`
				Name                string `json:"name"`
				Command             string `json:"command"`
				Cwd                 string `json:"cwd"`
				Icon                string `json:"icon"`
				RunOnWorktreeCreate bool   `json:"runOnWorktreeCreate"`
				RunOnSettle         bool   `json:"runOnSettle"`
				StopOnSettle        bool   `json:"stopOnSettle"`
				Keybinding          string `json:"keybinding"`
			} `json:"scripts"`
		}
		if json.Unmarshal(b, &cfg) == nil {
			for _, s := range cfg.Scripts {
				add(Script{
					ID: s.ID, Name: s.Name, Command: s.Command, Cwd: s.Cwd, Source: fname,
					Icon: s.Icon, RunOnWorktreeCreate: s.RunOnWorktreeCreate,
					RunOnSettle: s.RunOnSettle, StopOnSettle: s.StopOnSettle, Keybinding: s.Keybinding,
				})
			}
		}
	}

	// 3. package.json scripts.
	if b, err := os.ReadFile(filepath.Join(dir, "package.json")); err == nil {
		var pkg struct {
			Scripts map[string]string `json:"scripts"`
		}
		if json.Unmarshal(b, &pkg) == nil && len(pkg.Scripts) > 0 {
			runner := detectNodeRunner(dir)
			keys := make([]string, 0, len(pkg.Scripts))
			for k := range pkg.Scripts {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				add(Script{
					ID:      "pkg-" + slug(k),
					Name:    k,
					Command: runner + " run " + k,
					Source:  "package.json",
					Icon:    iconFor(k),
				})
			}
		}
	}

	// 4. Makefile targets.
	if b, err := os.ReadFile(filepath.Join(dir, "Makefile")); err == nil {
		for _, target := range parseMakeTargets(string(b)) {
			add(Script{
				ID:      "make-" + slug(target),
				Name:    "make " + target,
				Command: "make " + target,
				Source:  "makefile",
				Icon:    iconFor(target),
			})
		}
	}

	return out
}

func detectNodeRunner(dir string) string {
	if _, err := os.Stat(filepath.Join(dir, "pnpm-lock.yaml")); err == nil {
		return "pnpm"
	}
	if _, err := os.Stat(filepath.Join(dir, "bun.lockb")); err == nil {
		return "bun"
	}
	if _, err := os.Stat(filepath.Join(dir, "yarn.lock")); err == nil {
		return "yarn"
	}
	return "npm"
}

var makeTargetRe = regexp.MustCompile(`^([a-zA-Z0-9][a-zA-Z0-9._-]*)\s*:`)

func parseMakeTargets(content string) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "\t") || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ".") {
			continue
		}
		if mm := makeTargetRe.FindStringSubmatch(line); mm != nil {
			t := mm[1]
			if !seen[t] && len(out) < 25 {
				seen[t] = true
				out = append(out, t)
			}
		}
	}
	return out
}

func iconFor(name string) string {
	low := strings.ToLower(name)
	switch {
	case strings.Contains(low, "test") || strings.Contains(low, "spec"):
		return "test"
	case strings.Contains(low, "build") || strings.Contains(low, "compile"):
		return "build"
	case strings.Contains(low, "dev") || strings.Contains(low, "start") || strings.Contains(low, "serve"):
		return "play"
	case strings.Contains(low, "lint") || strings.Contains(low, "check") || strings.Contains(low, "fmt"):
		return "check"
	default:
		return "terminal"
	}
}

var slugRe = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

func slug(s string) string {
	r := strings.ToLower(strings.Trim(slugRe.ReplaceAllString(s, "-"), "-"))
	if r == "" {
		return "script"
	}
	return r
}
