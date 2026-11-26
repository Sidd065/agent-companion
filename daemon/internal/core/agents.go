package core

import (
	"os"
	"os/exec"
	"strings"
	"time"

	"agentc/internal/codex"
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

func versionOf(cmd string, args ...string) string {
	b, err := exec.Command(cmd, args...).Output()
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(b))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return s
}

// Agents lists the coding agents this host can run (cached briefly per Manager).
func (m *Manager) Agents() []AgentProfile {
	m.agentsMu.Lock()
	defer m.agentsMu.Unlock()
	if m.agentsList != nil && time.Since(m.agentsAt) < 60*time.Second {
		out := make([]AgentProfile, len(m.agentsList))
		copy(out, m.agentsList)
		return out
	}

	agyBin := firstNonEmpty(m.cfg.AgyPath, "agy")
	claudeBin := m.claudeBin()
	codexBin := m.codexBin()
	if m.Providers != nil {
		if ov, ok := m.Providers.Get("agy"); ok && ov.Command != "" {
			agyBin = expandHome(ov.Command, m.cfg.HomeDir)
		}
		if ov, ok := m.Providers.Get("claude"); ok && ov.Command != "" {
			claudeBin = expandHome(ov.Command, m.cfg.HomeDir)
		}
		if ov, ok := m.Providers.Get("codex"); ok && ov.Command != "" {
			codexBin = expandHome(ov.Command, m.cfg.HomeDir)
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

	codexProf := AgentProfile{
		ID: "codex", Name: "Codex", Driver: "codex", Command: codexBin, Builtin: true,
		Features: []string{
			"openrouter", "customModels", "thinking", "diffs", "permissions", "questions", "plans",
			"fork", "rewind", "steer", "resume", "history", "rename", "archive", "pin", "delete",
			"export", "changes", "terminal", "usage", "context", "sideQuestion", "asides",
			"subagents", "effort", "modes", "checkpoints",
		},
	}
	if v := versionOf(codexBin, "--version"); v != "" {
		codexProf.Version = v
		if codex.IsVersionSupported(v) {
			codexProf.Available = true
		} else {
			codexProf.Reason = "codex >= 0.172.0 required (found " + v + ")"
		}
	} else {
		codexProf.Reason = "codex CLI not found on PATH (install @openai/codex >= 0.172.0)"
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
			codexProf.Available, codexProf.Reason = false, "disabled in provider settings"
		}
	}

	list := []AgentProfile{agy, claude, codexProf}
	seen := map[string]bool{"agy": true, "claude": true, "codex": true}

	// Add user-configured generic ACP providers.
	if m.Providers != nil {
		for _, pc := range m.Providers.List() {
			if seen[pc.ID] {
				continue
			}
			seen[pc.ID] = true
			cmdPath := expandHome(pc.Command, m.cfg.HomeDir)
			prof := AgentProfile{
				ID: pc.ID, Name: pc.Name, Driver: "acp", Command: cmdPath, Features: pc.Features,
			}
			if len(prof.Features) == 0 {
				prof.Features = []string{"rename", "archive", "pin", "delete", "export", "changes", "terminal", "modes", "effort", "checkpoints", "asides"}
			}
			if !pc.Enabled {
				prof.Reason = "disabled in provider settings"
			} else if resolved, err := exec.LookPath(cmdPath); err == nil {
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

	m.agentsList = list
	m.agentsAt = time.Now()
	out := make([]AgentProfile, len(list))
	copy(out, list)
	return out
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
