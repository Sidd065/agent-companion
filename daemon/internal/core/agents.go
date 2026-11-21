package core

import (
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// AgentProfile describes a coding agent the daemon can drive. agy and custom
// ACP agents speak ACP natively; Claude Code and Codex run behind in-process
// ACP adapters so the session engine is shared across all providers.
type AgentProfile struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Driver    string   `json:"driver,omitempty"` // agy | claude | codex | acp
	Command   string   `json:"command"`
	Version   string   `json:"version,omitempty"`
	Available bool     `json:"available"`
	Planned   bool     `json:"planned,omitempty"`
	Builtin   bool     `json:"builtin,omitempty"`
	Reason    string   `json:"reason,omitempty"`
	Features  []string `json:"features"`
}

var (
	agentsMu   sync.Mutex
	agentsList []AgentProfile
	agentsAt   time.Time
)

func versionOf(cmd string, args ...string) string {
	b, err := exec.Command(cmd, args...).Output()
	if err != nil {
		return ""
	}
	v := strings.TrimSpace(string(b))
	if i := strings.IndexByte(v, '\n'); i >= 0 {
		v = v[:i]
	}
	return v
}

// Agents returns all built-in and configured ACP agent profiles (cached for a minute).
func (m *Manager) Agents() []AgentProfile {
	agentsMu.Lock()
	defer agentsMu.Unlock()
	if agentsList != nil && time.Since(agentsAt) < time.Minute {
		return agentsList
	}

	agyBin := m.cfg.AgyPath
	claudeBin := m.claudeBin()
	codexBin := "codex"
	if p, err := exec.LookPath("codex"); err == nil {
		codexBin = p
	}

	// Apply any user overrides for built-in providers.
	if m.Providers != nil {
		if ov, ok := m.Providers.Get("agy"); ok && ov.Command != "" {
			agyBin = ov.Command
		}
		if ov, ok := m.Providers.Get("claude"); ok && ov.Command != "" {
			claudeBin = ov.Command
		}
		if ov, ok := m.Providers.Get("codex"); ok && ov.Command != "" {
			codexBin = ov.Command
		}
	}

	agy := AgentProfile{
		ID: "agy", Name: "Antigravity (agy)", Driver: "agy", Command: agyBin, Builtin: true,
		Features: []string{"history", "fork", "rewind", "rename", "archive", "pin", "delete", "export", "changes", "artifacts", "quota", "terminal", "manage", "asides", "autoTurns", "checkpoints", "modes", "effort"},
	}
	if v := versionOf(agyBin, "--version"); v != "" {
		agy.Available, agy.Version = true, v
	} else {
		agy.Reason = "agy not found at " + agyBin
	}

	claude := AgentProfile{
		ID: "claude", Name: "Claude Code", Driver: "claude", Command: claudeBin, Builtin: true,
		Features: []string{"history", "fork", "rewind", "rename", "archive", "pin", "delete", "export", "changes", "artifacts", "terminal",
			"usage", "context", "sideQuestion", "asides", "mcp", "tasks", "subagents", "plans", "questions", "effort", "modes", "steer", "checkpoints"},
	}
	if v := versionOf(claudeBin, "--version"); v != "" {
		claude.Available, claude.Version = true, strings.TrimSuffix(v, " (Claude Code)")
	} else {
		claude.Reason = "claude not found (install Claude Code: https://claude.com/claude-code)"
	}

	codex := AgentProfile{
		ID: "codex", Name: "Codex", Driver: "codex", Command: codexBin, Builtin: true,
		Features: []string{"rename", "archive", "pin", "delete", "export", "changes", "terminal", "effort", "modes", "checkpoints"},
	}
	if v := versionOf(codexBin, "--version"); v != "" {
		codex.Available, codex.Version = true, v
	} else {
		codex.Reason = "codex CLI not found on PATH (install @openai/codex)"
	}

	// Respect enabled=false overrides on built-in providers.
	if m.Providers != nil {
		if ov, ok := m.Providers.Get("agy"); ok && !ov.Enabled {
			agy.Available, agy.Reason = false, "disabled in provider settings"
		}
		if ov, ok := m.Providers.Get("claude"); ok && !ov.Enabled {
			claude.Available, claude.Reason = false, "disabled in provider settings"
		}
		if ov, ok := m.Providers.Get("codex"); ok && !ov.Enabled {
			codex.Available, codex.Reason = false, "disabled in provider settings"
		}
	}

	list := []AgentProfile{agy, claude, codex}
	seen := map[string]bool{"agy": true, "claude": true, "codex": true}

	// Add user-configured generic ACP providers.
	if m.Providers != nil {
		for _, pc := range m.Providers.List() {
			if seen[pc.ID] {
				continue
			}
			seen[pc.ID] = true
			prof := AgentProfile{
				ID: pc.ID, Name: pc.Name, Driver: "acp", Command: pc.Command, Features: pc.Features,
			}
			if len(prof.Features) == 0 {
				prof.Features = []string{"rename", "archive", "pin", "delete", "export", "changes", "terminal", "modes", "effort", "checkpoints", "asides"}
			}
			if !pc.Enabled {
				prof.Reason = "disabled in provider settings"
			} else if resolved, err := exec.LookPath(pc.Command); err == nil {
				prof.Available = true
				prof.Command = resolved
				prof.Version = versionOf(resolved, "--version")
			} else {
				prof.Reason = "command not found: " + pc.Command
			}
			list = append(list, prof)
		}
	}

	// Hermetic test daemon support: auto-register fakeacp when AGENTC_FAKE_ACP is set.
	if bin := os.Getenv("AGENTC_FAKE_ACP"); bin != "" && !seen["fakeacp"] {
		prof := AgentProfile{
			ID: "fakeacp", Name: "Fake ACP", Driver: "acp", Command: bin,
			Features: []string{"rename", "archive", "pin", "delete", "export", "changes", "terminal", "modes", "effort", "plans", "questions", "asides", "checkpoints"},
		}
		if resolved, err := exec.LookPath(bin); err == nil {
			prof.Available = true
			prof.Command = resolved
			prof.Version = versionOf(resolved, "--version")
		} else {
			prof.Reason = "fakeacp binary not found at " + bin
		}
		list = append(list, prof)
	}

	agentsList = list
	agentsAt = time.Now()
	return agentsList
}

// Agent returns one profile.
func (m *Manager) Agent(id string) *AgentProfile {
	for _, a := range m.Agents() {
		if a.ID == id {
			a := a
			return &a
		}
	}
	return nil
}
