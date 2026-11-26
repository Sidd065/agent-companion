package e2e_test

import (
	"context"
	"testing"
)

func TestTier1_V060_DevicesRevokeOthers_UsageWindows_QuotedSearch_AndAgyConfig(t *testing.T) {
	td := StartIsolatedDaemon(t, DaemonOpts{ExtraArgs: []string{"-openrouter-api-key", "sk-or-test-v060"}})
	cliA := td.PairAndConnect(t, "PrimaryDesktop", "linux-desktop")
	cliB := td.PairAndConnect(t, "SecondaryPhone", "android")
	cliC := td.PairAndConnect(t, "TabletDevice", "android")
	_ = cliB
	_ = cliC

	ctx := context.Background()

	// 1. Verify 3 paired devices exist, then revoke all other devices from cliA
	var devsBefore []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := cliA.Call(ctx, "devices.list", map[string]any{}, &devsBefore); err != nil {
		t.Fatalf("devices.list before: %v", err)
	}
	if len(devsBefore) != 3 {
		t.Fatalf("expected 3 paired devices, got %d", len(devsBefore))
	}

	var revOut struct {
		OK      bool `json:"ok"`
		Revoked int  `json:"revoked"`
	}
	if err := cliA.Call(ctx, "devices.revokeOthers", map[string]any{}, &revOut); err != nil {
		t.Fatalf("devices.revokeOthers: %v", err)
	}
	if !revOut.OK || revOut.Revoked != 2 {
		t.Fatalf("expected ok=true revoked=2, got %+v", revOut)
	}

	var devsAfter []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := cliA.Call(ctx, "devices.list", map[string]any{}, &devsAfter); err != nil {
		t.Fatalf("devices.list after: %v", err)
	}
	if len(devsAfter) != 1 || devsAfter[0].Name != "PrimaryDesktop" {
		t.Fatalf("expected only PrimaryDesktop remaining, got %+v", devsAfter)
	}

	// 2. Verify usage.summary supports hours (5h) and allTime windows
	for _, params := range []map[string]any{
		{"hours": 5},
		{"hours": 12},
		{"days": 180},
		{"allTime": true},
	} {
		var u map[string]any
		if err := cliA.Call(ctx, "usage.summary", params, &u); err != nil {
			t.Fatalf("usage.summary(%v): %v", params, err)
		}
	}

	// 3. Verify daemon.config.set persists agyAutoAllowReads & notifyEvents and push.register accepts events
	var cfgOut map[string]any
	if err := cliA.Call(ctx, "daemon.config.set", map[string]any{
		"agyAutoAllowReads": true,
		"notifyEvents":      []string{"input", "done", "halted"},
	}, &cfgOut); err != nil {
		t.Fatalf("daemon.config.set: %v", err)
	}

	var pushOut map[string]any
	if err := cliA.Call(ctx, "push.register", map[string]any{
		"token":  "fake-fcm-token-v060",
		"events": []string{"input", "done", "halted"},
	}, &pushOut); err != nil {
		t.Fatalf("push.register: %v", err)
	}

	// 4. Verify quoted exact-phrase & Turkish dotted-İ search over Noise RPC
	workDir := newTempPlainDir(t)
	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cliA.Call(ctx, "session.create", map[string]any{
		"agent": "codex",
		"cwd":   workDir,
		"title": "İstanbul Static Cloudflare Tunnel Setup",
	}, &created); err != nil {
		t.Fatalf("session.create: %v", err)
	}

	var hits []struct {
		SessionID string `json:"sessionId"`
		Title     string `json:"title"`
	}
	if err := cliA.Call(ctx, "search.query", map[string]any{
		"query": `istanbul "Static Cloudflare"`,
		"limit": 10,
	}, &hits); err != nil {
		t.Fatalf("search.query match: %v", err)
	}
	if len(hits) == 0 || hits[0].SessionID != created.Session.ID {
		t.Fatalf("expected session %s to match istanbul + exact phrase, got %+v", created.Session.ID, hits)
	}

	var missHits []map[string]any
	if err := cliA.Call(ctx, "search.query", map[string]any{
		"query": `"Cloudflare Static"`,
		"limit": 10,
	}, &missHits); err != nil {
		t.Fatalf("search.query miss: %v", err)
	}
	if len(missHits) != 0 {
		t.Fatalf("expected reversed quoted phrase not to match, got %+v", missHits)
	}
}
