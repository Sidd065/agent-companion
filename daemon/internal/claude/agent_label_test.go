package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentc/internal/acp"
)

func TestDefaultModelLabel(t *testing.T) {
	cases := []struct{ name, desc, wantName, wantDesc string }{
		{"Default (recommended)", "Opus 5.5 · Best for everyday, complex tasks", "Opus 5.5 (default)", "Best for everyday, complex tasks"},
		{"Default (recommended)", "Use the default model", "Default (recommended)", "Use the default model"},
		{"Default (recommended)", "", "Default (recommended)", ""},
		// Already relabelled: a second pass must not eat the next part of the description.
		{"Opus 5.5 (default)", "Most capable · 1M context", "Opus 5.5 (default)", "Most capable · 1M context"},
	}
	for _, c := range cases {
		n, d := defaultModelLabel(c.name, c.desc)
		if n != c.wantName || d != c.wantDesc {
			t.Errorf("defaultModelLabel(%q, %q) = %q, %q", c.name, c.desc, n, d)
		}
	}
}

func TestRelabelDefaultModel(t *testing.T) {
	ms := &acp.Models{CurrentModelID: "default", AvailableModels: []acp.Model{
		{ModelID: "default", Name: "Default (recommended)", Description: "Opus 5.5 · Best for everyday, complex tasks"},
		{ModelID: "haiku", Name: "Haiku", Description: "Haiku 5.5 · Fastest"},
	}}
	RelabelDefaultModel(ms)
	RelabelDefaultModel(ms) // idempotent
	if got := ms.AvailableModels[0].Name; got != "Opus 5.5 (default)" {
		t.Errorf("default entry = %q", got)
	}
	if got := ms.AvailableModels[0].Description; got != "Best for everyday, complex tasks" {
		t.Errorf("default description = %q", got)
	}
	if got := ms.AvailableModels[1].Name; got != "Haiku" {
		t.Errorf("other entries must stay: %q", got)
	}
	RelabelDefaultModel(nil)
}

func TestDeleteSessionClearsRewound(t *testing.T) {
	dir := t.TempDir()
	p := dir + "/0b2a1c3d-0000-4000-8000-000000000001.jsonl"
	for _, f := range []string{p + ".rewound-1", p + ".rewound-2"} {
		if err := os.WriteFile(f, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.MkdirAll(strings.TrimSuffix(p, ".jsonl")+"/subagents", 0o700)
	if err := DeleteSession(p); err != nil {
		t.Fatalf("missing transcript: %v", err)
	}
	if left, _ := filepath.Glob(dir + "/*"); len(left) != 0 {
		t.Fatalf("left behind: %v", left)
	}
}
