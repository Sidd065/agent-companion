package core

import (
	"os/exec"
	"strings"
	"sync"
	"time"
)

// AgentProfile describes a coding agent the daemon can drive. agy speaks ACP
// natively; Claude Code runs headless (stream-json) behind an in-process ACP
// adapter, so the session engine is shared and only spawn details and the
// history/fork/rewind backends differ per agent. Codex is planned.
type AgentProfile struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Command   string   `json:"command"`
	Version   string   `json:"version,omitempty"`
	Available bool     `json:"available"`
	Planned   bool     `json:"planned,omitempty"`
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

// Agents returns the known agent profiles (cached for a minute).
func (m *Manager) Agents() []AgentProfile {
	agentsMu.Lock()
	defer agentsMu.Unlock()
	if agentsList != nil && time.Since(agentsAt) < time.Minute {
		return agentsList
	}
	agy := AgentProfile{ID: "agy", Name: "Antigravity (agy)", Command: m.cfg.AgyPath,
		Features: []string{"history", "fork", "rewind", "rename", "archive", "pin", "delete", "export", "changes", "artifacts", "quota", "terminal", "manage", "asides", "autoTurns"}}
	if v := versionOf(m.cfg.AgyPath, "--version"); v != "" {
		agy.Available, agy.Version = true, v
	} else {
		agy.Reason = "agy not found at " + m.cfg.AgyPath
	}
	claude := AgentProfile{ID: "claude", Name: "Claude Code", Command: m.claudeBin(),
		Features: []string{"history", "fork", "rewind", "rename", "archive", "pin", "delete", "export", "changes", "artifacts", "terminal",
			"usage", "context", "sideQuestion", "asides", "mcp", "tasks", "subagents", "plans", "questions", "effort", "modes", "steer"}}
	codex := AgentProfile{ID: "codex", Name: "Codex", Command: "codex", Planned: true, Reason: "coming soon", Features: []string{"terminal"}}
	if v := versionOf(claude.Command, "--version"); v != "" {
		claude.Available, claude.Version = true, strings.TrimSuffix(v, " (Claude Code)")
	} else {
		claude.Reason = "claude not found (install Claude Code: https://claude.com/claude-code)"
	}
	if p, err := exec.LookPath("codex"); err == nil {
		codex.Command = p
		codex.Version = versionOf(p, "--version")
	}
	agentsList = []AgentProfile{agy, claude, codex}
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
