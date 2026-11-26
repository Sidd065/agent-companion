package term

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateCodexTerminalWithHookAndEnv(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" || strings.HasPrefix(home, "/tmp") {
		home = "/var/tmp"
	}
	base := filepath.Join(home, ".cache", "agentc-gotest")
	_ = os.MkdirAll(base, 0o700)
	dir, err := os.MkdirTemp(base, "term-codex-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	// Create a mock codex TUI script that prints CODEX_HOME, AGENTC_BRIDGE_TOKEN, and argv, then sleeps briefly.
	mockCodex := filepath.Join(dir, "mock-codex")
	script := "#!/bin/sh\necho \"HOME_IS=$CODEX_HOME TOK_IS=$AGENTC_BRIDGE_TOKEN ARGS=$*\"\nsleep 2\n"
	if err := os.WriteFile(mockCodex, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	tm := &Manager{
		Sock:        filepath.Join(dir, "tmux.sock"),
		Conf:        filepath.Join(dir, "tmux.conf"),
		CodexPath:   mockCodex,
		DefaultSkip: true,
		CodexLaunch: func(convID string, skip bool, extraArgs []string) (string, []string, map[string]string) {
			args := []string{"-c", `model_provider="openrouter"`}
			if skip {
				args = append(args, "--dangerously-bypass-approvals-and-sandbox")
			}
			if convID != "" {
				args = append(args, "resume", convID)
			}
			args = append(args, extraArgs...)
			return mockCodex, args, map[string]string{
				"CODEX_HOME":          filepath.Join(dir, "codex-home"),
				"AGENTC_BRIDGE_TOKEN": "bridge-tok-xyz",
			}
		},
	}
	ctx := context.Background()
	defer func() {
		_, _ = tm.tmux(ctx, "kill-server")
	}()

	skip := true
	trm, err := tm.Create(ctx, CreateOpts{
		Kind:         "codex",
		Cwd:          dir,
		ConvID:       "019c1234-5678-7000-8000-abcdef123456",
		Skip:         &skip,
		RemainOnExit: true,
	})
	if err != nil {
		t.Fatalf("Create(codex): %v", err)
	}
	if trm.Kind != "codex" || trm.ConvID != "019c1234-5678-7000-8000-abcdef123456" {
		t.Fatalf("unexpected term metadata: %+v", trm)
	}
	if !strings.Contains(trm.Command, "resume") || !strings.Contains(trm.Command, "019c1234-5678-7000-8000-abcdef123456") {
		t.Fatalf("expected `codex ... resume <convID>` in command, got %q", trm.Command)
	}
}
