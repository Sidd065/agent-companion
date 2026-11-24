package agyproc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func envVal(env []string, key string) (string, bool) {
	prefix := key + "="
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			return strings.TrimPrefix(kv, prefix), true
		}
	}
	return "", false
}

func TestBuildEnvForcesFileTokenStorageWhenFileExists(t *testing.T) {
	home := t.TempDir()
	tokDir := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(tokDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tokDir, "antigravity-oauth-token"), []byte(`{"token":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	got := BuildEnv([]string{"HOME=" + home, "PATH=/usr/bin"})
	v, ok := envVal(got, forceFileTokenStorageEnv)
	if !ok || v != "1" {
		t.Fatalf("expected %s=1 when token file exists, got ok=%v val=%q", forceFileTokenStorageEnv, ok, v)
	}
}

func TestBuildEnvLeavesKeyringDefaultWhenFileMissing(t *testing.T) {
	home := t.TempDir()
	got := BuildEnv([]string{"HOME=" + home, "PATH=/usr/bin"})
	if v, ok := envVal(got, forceFileTokenStorageEnv); ok {
		t.Fatalf("expected %s to stay unset when token file missing, got %q", forceFileTokenStorageEnv, v)
	}
}

func TestBuildEnvRespectsExplicitOverride(t *testing.T) {
	home := t.TempDir()
	tokDir := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(tokDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tokDir, "antigravity-oauth-token"), []byte(`{"token":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	got := BuildEnv([]string{"HOME=" + home, forceFileTokenStorageEnv + "=0"})
	v, ok := envVal(got, forceFileTokenStorageEnv)
	if !ok || v != "0" {
		t.Fatalf("expected explicit override %s=0 to be preserved, got ok=%v val=%q", forceFileTokenStorageEnv, ok, v)
	}
}
