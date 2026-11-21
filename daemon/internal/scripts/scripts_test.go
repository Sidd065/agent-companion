package scripts

import (
	"os"
	"path/filepath"
	"testing"

	"agentc/internal/projects"
)

func TestDiscoverPriorityAndSources(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "t3.json"), []byte(`{"scripts":[{"id":"setup","name":"Setup","command":"npm ci","runOnWorktreeCreate":true}]}`), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"scripts":{"dev":"vite","test":"vitest"}}`), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "Makefile"), []byte("build:\n\tgo build .\n"), 0o644)

	list := Discover(dir, []projects.ScriptDef{{ID: "custom-lint", Name: "Lint", Command: "eslint ."}})
	if len(list) != 5 {
		t.Fatalf("expected 5 scripts, got %d: %+v", len(list), list)
	}
	if list[0].Source != "custom" || list[1].Source != "t3.json" || !list[1].RunOnWorktreeCreate {
		t.Fatalf("unexpected script priority: %+v", list)
	}
}
