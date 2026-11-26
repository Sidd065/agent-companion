package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCodexModeNormalizationLiveAndSleeping(t *testing.T) {
	root := nonTmpTestDir(t, "codex-mode-norm")
	stateDir := filepath.Join(root, "state")
	cfgDir := filepath.Join(root, "cfg")
	homeDir := filepath.Join(root, "home")
	workDir := filepath.Join(root, "work")
	for _, d := range []string{stateDir, cfgDir, homeDir, workDir} {
		if err := mkdir0700(d); err != nil {
			t.Fatal(err)
		}
	}

	fakeBin, _ := writeCoreFakeCodex(t, root)
	mgr := NewManager(Config{
		AgyPath:          "nonexistent-agy",
		ClaudePath:       "nonexistent-claude",
		CodexPath:        fakeBin,
		OpenRouterAPIKey: "sk-or-v1-mode-test",
		StateDir:         stateDir,
		ConfigDir:        cfgDir,
		HomeDir:          homeDir,
	}, NewHub())
	defer mgr.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	s, err := mgr.Create(ctx, CreateParams{
		Agent: "codex",
		Cwd:   workDir,
		Flags: Flags{
			Model: "google/gemini-3-flash-preview",
			Mode:  "accept-edits",
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	assertMode := func(step, want string) {
		t.Helper()
		sum := s.Summary()
		s.mu.Lock()
		det := s.detailLocked()
		wm := s.meta.WantMode
		s.mu.Unlock()
		if sum.Mode != want {
			t.Fatalf("%s: Summary().Mode = %q, want %q", step, sum.Mode, want)
		}
		if det.Modes == nil || det.Modes.CurrentModeID != want {
			t.Fatalf("%s: Detail().Modes = %+v, want CurrentModeID=%q", step, det.Modes, want)
		}
		if wm != want {
			t.Fatalf("%s: s.meta.WantMode = %q, want %q", step, wm, want)
		}
	}

	assertMode("create(accept-edits)", "acceptEdits")

	// Live SetMode with aliases
	if err := s.SetMode(ctx, "supervised"); err != nil {
		t.Fatalf("live SetMode(supervised): %v", err)
	}
	assertMode("live SetMode(supervised)", "default")

	if err := s.SetMode(ctx, "fullAccess"); err != nil {
		t.Fatalf("live SetMode(fullAccess): %v", err)
	}
	assertMode("live SetMode(fullAccess)", "full-access")

	// Live SetConfig("mode", ...) must normalize AND update WantMode
	if err := s.SetConfig(ctx, "mode", "accept-edits"); err != nil {
		t.Fatalf("live SetConfig(mode, accept-edits): %v", err)
	}
	assertMode("live SetConfig(accept-edits)", "acceptEdits")

	// Hibernate and verify sleeping SetMode + SetConfig("mode", ...)
	s.Hibernate()
	if err := s.SetMode(ctx, "supervised"); err != nil {
		t.Fatalf("sleeping SetMode(supervised): %v", err)
	}
	assertMode("sleeping SetMode(supervised)", "default")

	if err := s.SetConfig(ctx, "mode", "fullAccess"); err != nil {
		t.Fatalf("sleeping SetConfig(fullAccess): %v", err)
	}
	assertMode("sleeping SetConfig(fullAccess)", "full-access")

	if err := s.SetMode(ctx, "accept-edits"); err != nil {
		t.Fatalf("sleeping SetMode(accept-edits): %v", err)
	}
	assertMode("sleeping SetMode(accept-edits)", "acceptEdits")

	// R2-D05: SetConfig(ctx, "model", ...) updates model on both sleeping and live codex sessions!
	assertModel := func(step, want string) {
		t.Helper()
		sum := s.Summary()
		s.mu.Lock()
		det := s.detailLocked()
		wm := s.meta.WantModel
		s.mu.Unlock()
		if sum.Model != want {
			t.Fatalf("%s: Summary().Model = %q, want %q", step, sum.Model, want)
		}
		if det.Models == nil || det.Models.CurrentModelID != want {
			t.Fatalf("%s: Detail().Models = %+v, want CurrentModelID=%q", step, det.Models, want)
		}
		if wm != want {
			t.Fatalf("%s: s.meta.WantModel = %q, want %q", step, wm, want)
		}
	}

	if err := s.SetConfig(ctx, "model", "x-ai/grok-sleep-99"); err != nil {
		t.Fatalf("sleeping SetConfig(model): %v", err)
	}
	assertModel("sleeping SetConfig(model)", "x-ai/grok-sleep-99")

	if err := mgr.Wake(ctx, s.ID()); err != nil {
		t.Fatalf("Wake after sleeping SetConfig(model): %v", err)
	}
	assertModel("after Wake", "x-ai/grok-sleep-99")

	if err := s.SetConfig(ctx, "model", "x-ai/grok-live-100"); err != nil {
		t.Fatalf("live SetConfig(model): %v", err)
	}
	assertModel("live SetConfig(model)", "x-ai/grok-live-100")
}

func mkdir0700(p string) error {
	return os.MkdirAll(p, 0o700)
}
