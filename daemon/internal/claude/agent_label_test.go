package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultModelLabel(t *testing.T) {
	cases := []struct{ name, desc, wantName, wantDesc string }{
		{"Default (recommended)", "Opus 5.5 · Best for everyday, complex tasks", "Opus 5.5 (default)", "Best for everyday, complex tasks"},
		{"Default (recommended)", "Use the default model", "Default (recommended)", "Use the default model"},
		{"Default (recommended)", "", "Default (recommended)", ""},
	}
	for _, c := range cases {
		n, d := defaultModelLabel(c.name, c.desc)
		if n != c.wantName || d != c.wantDesc {
			t.Errorf("defaultModelLabel(%q, %q) = %q, %q", c.name, c.desc, n, d)
		}
	}
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
