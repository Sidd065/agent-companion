package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestAgyrArgs(t *testing.T) {
	skipDefault := []string{"--dangerously-skip-permissions"}
	cases := []struct {
		defaults, args, want []string
		skip                 bool
	}{
		{skipDefault, nil, nil, true},
		{skipDefault, []string{"--safe"}, nil, false},
		{skipDefault, []string{"-c", "--model", "x"}, []string{"-c", "--model", "x"}, true},
		{[]string{"--model", "gemini-4-argon", "--dangerously-skip-permissions", "--sandbox"}, []string{"--model", "y"},
			[]string{"--model", "gemini-4-argon", "--sandbox", "--model", "y"}, true},
		{[]string{}, []string{"--conversation", "abc"}, []string{"--conversation", "abc"}, false},
	}
	for i, c := range cases {
		got, skip := agyrArgs(c.defaults, c.args)
		if !reflect.DeepEqual(got, c.want) || skip != c.skip {
			t.Errorf("case %d: got %q skip=%v, want %q skip=%v", i, got, skip, c.want, c.skip)
		}
	}
}

func TestInstallFindsCloudflaredOnServicePath(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	// A shell without ~/.local/bin on PATH (e.g. `ssh host agentc install`)
	// must not report cloudflared missing when the service will find it.
	t.Setenv("PATH", "/nonexistent")
	if _, err := lookPathIn("cloudflared-test", servicePath(home)); err == nil {
		t.Fatal("found a binary that doesn't exist")
	}
	f := filepath.Join(bin, "cloudflared-test")
	if err := os.WriteFile(f, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := lookPathIn("cloudflared-test", servicePath(home)); err == nil {
		t.Fatal("a non-executable file counted as installed")
	}
	if err := os.Chmod(f, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := lookPathIn("cloudflared-test", servicePath(home)); err != nil || got != f {
		t.Fatalf("lookPathIn = %q, %v; want %q", got, err, f)
	}
	if onPath(bin) {
		t.Error("onPath reported a directory missing from PATH")
	}
	t.Setenv("PATH", "/usr/bin:"+bin+"/")
	if !onPath(bin) {
		t.Error("onPath missed a directory on PATH")
	}
	// The unit gives the daemon the same PATH the check uses.
	unit := fmt.Sprintf(unitTmpl, "/x/agentc")
	if !strings.Contains(unit, "Environment=PATH=%h/.local/bin:"+unitSystemPath+"\n") {
		t.Errorf("unit PATH line differs from servicePath:\n%s", unit)
	}
}

func TestTunnelSettled(t *testing.T) {
	quick := func(url, status string) map[string]any {
		return map[string]any{"publicUrl": url, "tunnel": map[string]any{"mode": "cloudflare-quick", "status": status}}
	}
	cases := []struct {
		info map[string]any
		want bool
	}{
		{nil, false},
		{quick("", ""), false},
		{quick("", "restarting"), false},
		{quick("https://a.trycloudflare.com", "up"), true},
		{quick("", "error: exec: \"cloudflared\": executable file not found in $PATH"), true},
		{map[string]any{"publicUrl": "", "tunnel": map[string]any{"mode": "none", "url": ""}}, true},
	}
	for i, c := range cases {
		if got := tunnelSettled(c.info); got != c.want {
			t.Errorf("case %d: tunnelSettled(%v) = %v, want %v", i, c.info, got, c.want)
		}
	}
}

func nonTmpDirMain(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil || home == "" || strings.HasPrefix(home, "/tmp") {
		home = "/var/tmp"
	}
	base := filepath.Join(home, ".cache", "agentc-gotest")
	_ = os.MkdirAll(base, 0o700)
	dir, err := os.MkdirTemp(base, "main-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestParseServeArgsAndOpenRouterKeyPrecedence(t *testing.T) {
	dir := nonTmpDirMain(t)
	keyFile := filepath.Join(dir, "or.key")
	if err := os.WriteFile(keyFile, []byte("# comment\nOPENROUTER_API_KEY=\"sk-or-from-file\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgKeyFile := filepath.Join(dir, "cfg.key")
	if err := os.WriteFile(cfgKeyFile, []byte("sk-or-from-cfg-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := Config{
		Port:                 9473,
		OpenRouterAPIKey:     "sk-or-from-cfg",
		OpenRouterAPIKeyFile: cfgKeyFile,
	}
	opts, err := parseServeArgs(cfg, "/x/app.apk", []string{
		"-listen", "127.0.0.1:19473",
		"-openrouter-api-key", "sk-or-from-flag",
		"-openrouter-api-key-file", keyFile,
		"-codex-path", "/custom/bin/codex",
	})
	if err != nil {
		t.Fatalf("parseServeArgs: %v", err)
	}
	if opts.listen != "127.0.0.1:19473" || opts.codexPath != "/custom/bin/codex" {
		t.Fatalf("unexpected opts: %+v", opts)
	}

	// 1. Explicit -openrouter-api-key wins.
	t.Setenv("OPENROUTER_API_KEY", "sk-or-from-env")
	if got, err := resolveOpenRouterKey(opts.openRouterKey, opts.openRouterKeyFile, cfg, dir); err != nil || got != "sk-or-from-flag" {
		t.Fatalf("flag precedence: got %q, %v", got, err)
	}
	// 2. -openrouter-api-key-file wins over env.
	if got, err := resolveOpenRouterKey("", opts.openRouterKeyFile, cfg, dir); err != nil || got != "sk-or-from-file" {
		t.Fatalf("flag file precedence: got %q, %v", got, err)
	}
	// 3. Missing explicit -openrouter-api-key-file returns error.
	if _, err := resolveOpenRouterKey("", filepath.Join(dir, "missing.key"), cfg, dir); err == nil {
		t.Fatal("expected error for missing explicit -openrouter-api-key-file")
	}
	// 4. Env OPENROUTER_API_KEY wins over config.
	if got, err := resolveOpenRouterKey("", "", cfg, dir); err != nil || got != "sk-or-from-env" {
		t.Fatalf("env precedence: got %q, %v", got, err)
	}
	// 5. Config OpenRouterAPIKey wins over Config.OpenRouterAPIKeyFile.
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("OPENROUTER_API_KEY_FILE", "")
	if got, err := resolveOpenRouterKey("", "", cfg, dir); err != nil || got != "sk-or-from-cfg" {
		t.Fatalf("cfg key precedence: got %q, %v", got, err)
	}
	// 6. Config OpenRouterAPIKeyFile fallback.
	cfg.OpenRouterAPIKey = ""
	if got, err := resolveOpenRouterKey("", "", cfg, dir); err != nil || got != "sk-or-from-cfg-file" {
		t.Fatalf("cfg file precedence: got %q, %v", got, err)
	}
}

func TestConfigSubcommandAndSecretRedaction(t *testing.T) {
	dir := nonTmpDirMain(t)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(dir, "share"))

	if code := configCmd([]string{"set", "openrouter_api_key", "sk-secret-test-value-999"}); code != 0 {
		t.Fatalf("config set openrouter_api_key exited %d", code)
	}
	if code := configCmd([]string{"set", "codex_path", "/opt/codex/bin/codex"}); code != 0 {
		t.Fatalf("config set codex_path exited %d", code)
	}
	if code := configCmd([]string{"set", "env.OPENAI_API_KEY", "sk-openai-secret-123"}); code != 0 {
		t.Fatalf("config set env.OPENAI_API_KEY exited %d", code)
	}
	if code := configCmd([]string{"set", "env.LOG_LEVEL", "debug"}); code != 0 {
		t.Fatalf("config set env.LOG_LEVEL exited %d", code)
	}

	loaded := loadConfig(getPaths())
	if loaded.OpenRouterAPIKey != "sk-secret-test-value-999" {
		t.Fatalf("stored OpenRouterAPIKey mismatch")
	}
	if loaded.CodexPath != "/opt/codex/bin/codex" {
		t.Fatalf("stored CodexPath = %q", loaded.CodexPath)
	}

	// getConfigField and redactedConfigMap must mask secrets to "***".
	if got, ok := getConfigField(loaded, "openrouter_api_key"); !ok || got != "***" {
		t.Fatalf("expected redacted openrouter_api_key '***', got %q (ok=%v)", got, ok)
	}
	if got, ok := getConfigField(loaded, "env.OPENAI_API_KEY"); !ok || got != "***" {
		t.Fatalf("expected redacted env.OPENAI_API_KEY '***', got %q (ok=%v)", got, ok)
	}
	if got, ok := getConfigField(loaded, "env.LOG_LEVEL"); !ok || got != "debug" {
		t.Fatalf("expected non-secret env.LOG_LEVEL 'debug', got %q", got)
	}
	red := redactedConfigMap(loaded)
	if red["openRouterApiKey"] != "***" {
		t.Fatalf("redactedConfigMap leaked openRouterApiKey: %v", red["openRouterApiKey"])
	}
	envMap, _ := red["env"].(map[string]string)
	if envMap["OPENAI_API_KEY"] != "***" || envMap["LOG_LEVEL"] != "debug" {
		t.Fatalf("redactedConfigMap env mismatch: %+v", envMap)
	}
}
