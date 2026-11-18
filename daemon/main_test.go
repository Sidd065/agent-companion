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
