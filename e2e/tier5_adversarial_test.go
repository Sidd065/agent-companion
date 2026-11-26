package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestTier5_01_GlobalSearchUTF8CaseExpansionAndEmojiSnippets(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T5-01-SearchUTF8", "linux")
	repo := newTempGitRepo(t, nil)
	ctx := context.Background()

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(ctx, "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "google/gemini-3-flash-preview",
	}, &created); err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sid := created.Session.ID
	if err := cli.Call(ctx, "session.subscribe", map[string]any{"id": sid, "since": 0}, nil); err != nil {
		t.Fatalf("session.subscribe: %v", err)
	}

	prompts := []struct {
		text  string
		query string
	}{
		{
			text:  strings.Repeat("\u023a", 100) + " PANICNEEDLE_T501 " + strings.Repeat("\u023a", 30),
			query: "panicneedle_t501",
		},
		{
			text:  strings.Repeat("\u0130", 100) + " TURKISHNEEDLE_T501 " + strings.Repeat("\u0130", 30),
			query: "turkishneedle_t501",
		},
		{
			text:  strings.Repeat("\U0001F680\u65E5\u672C\u8A9E", 25) + " EMOJINEEDLE_T501 " + strings.Repeat("\U0001F525\u4E2D\u6587", 25),
			query: "emojineedle_t501",
		},
	}

	for idx, p := range prompts {
		if err := cli.Call(ctx, "session.prompt", map[string]any{"id": sid, "text": p.text}, nil); err != nil {
			t.Fatalf("session.prompt %d: %v", idx+1, err)
		}
		if _, _, err := cli.WaitForTurnIdle(sid, idx+1, 12*time.Second); err != nil {
			t.Fatalf("WaitForTurnIdle %d: %v", idx+1, err)
		}

		var hits []struct {
			SessionID string `json:"sessionId"`
			Snippet   string `json:"snippet"`
		}
		if err := cli.Call(ctx, "search.query", map[string]any{"query": p.query, "limit": 20}, &hits); err != nil {
			t.Fatalf("search.query(%q) failed: %v", p.query, err)
		}
		var matched *struct {
			SessionID string `json:"sessionId"`
			Snippet   string `json:"snippet"`
		}
		for i := range hits {
			if hits[i].SessionID == sid {
				matched = &hits[i]
				break
			}
		}
		if matched == nil {
			t.Fatalf("search.query(%q) missed session %s: %+v", p.query, sid, hits)
		}
		if !utf8.ValidString(matched.Snippet) {
			t.Fatalf("search.query(%q) returned invalid UTF-8 snippet: %q", p.query, matched.Snippet)
		}
		if strings.ContainsRune(matched.Snippet, utf8.RuneError) {
			t.Fatalf("search.query(%q) returned U+FFFD mid-rune corruption: %q", p.query, matched.Snippet)
		}
		if !strings.Contains(strings.ToLower(matched.Snippet), p.query) {
			t.Fatalf("search.query(%q) snippet %q does not contain matched query", p.query, matched.Snippet)
		}
	}
}

func TestTier5_02_ExternalCodexHomeOpenResumeForkRewindAndOpenInTerminal(t *testing.T) {
	td := StartIsolatedDaemon(t, DaemonOpts{
		Env: map[string]string{"OPENROUTER_API_KEY": "sk-or-v1-t5-02-must-not-hijack-native"},
	})
	cli1 := td.PairAndConnect(t, "T5-02-Desktop", "linux")
	cli2 := td.PairAndConnect(t, "T5-02-Android", "android")
	repo := newTempGitRepo(t, nil)
	ctx := context.Background()

	extCodexHome := filepath.Join(td.HomeDir, ".codex")
	extID := "01970000-0000-7000-8000-000000000502"
	rolloutDir := filepath.Join(extCodexHome, "sessions", "2026", "11", "25")
	threadsDir := filepath.Join(extCodexHome, "threads")
	for _, d := range []string{rolloutDir, threadsDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	if err := os.WriteFile(filepath.Join(extCodexHome, "auth.json"), []byte(`{"OPENAI_API_KEY":"sk-native-t5-02"}`), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	rolloutPath := filepath.Join(rolloutDir, "rollout-2026-11-25T10-00-00-"+extID+".jsonl")
	rolloutJSONL := strings.Join([]string{
		`{"timestamp":"2026-11-25T10:00:00Z","type":"session_meta","payload":{"id":"` + extID + `","timestamp":"2026-11-25T10:00:00Z","cwd":"` + repo + `","cli_version":"0.172.0","source":"cli","model_provider":"openai","model":"gpt-5.3-codex"}}`,
		`{"timestamp":"2026-11-25T10:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"Seeded external turn 1"}}`,
		`{"timestamp":"2026-11-25T10:00:02Z","type":"event_msg","payload":{"type":"user_message","message":"Seeded external turn 2"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(rolloutPath, []byte(rolloutJSONL), 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}

	seededThread := map[string]any{
		"id":            extID,
		"sessionId":     extID,
		"cliVersion":    "0.172.0",
		"createdAt":     1764064800,
		"updatedAt":     1764064805,
		"cwd":           repo,
		"model":         "gpt-5.3-codex",
		"modelProvider": "openai",
		"name":          "Seeded External Native Thread",
		"preview":       "Seeded external turn 2",
		"source":        "cli",
		"turns": []map[string]any{
			{
				"id":        "turn-ext-1",
				"status":    "completed",
				"createdAt": 1764064801,
				"items": []map[string]any{
					{"id": "u-ext-1", "type": "userMessage", "content": []map[string]any{{"type": "text", "text": "Seeded external turn 1"}}},
					{"id": "a-ext-1", "type": "agentMessage", "text": "Seeded external reply 1"},
				},
			},
			{
				"id":        "turn-ext-2",
				"status":    "completed",
				"createdAt": 1764064803,
				"items": []map[string]any{
					{"id": "u-ext-2", "type": "userMessage", "content": []map[string]any{{"type": "text", "text": "Seeded external turn 2"}}},
					{"id": "a-ext-2", "type": "agentMessage", "text": "Seeded external reply 2"},
				},
			},
		},
	}
	seedBytes, _ := json.MarshalIndent(seededThread, "", "  ")
	if err := os.WriteFile(filepath.Join(threadsDir, extID+".json"), seedBytes, 0o600); err != nil {
		t.Fatalf("write seeded thread JSON: %v", err)
	}

	// 1. sessions.list discovers standalone ~/.codex rollout.
	var listed []SessionSummaryWire
	if err := cli1.Call(ctx, "sessions.list", map[string]any{"includeHistory": true}, &listed); err != nil {
		t.Fatalf("sessions.list: %v", err)
	}
	foundInList := false
	for _, row := range listed {
		if row.ID == extID {
			foundInList = true
			break
		}
	}
	if !foundInList {
		t.Fatalf("sessions.list did not discover ~/.codex session %s: %+v", extID, listed)
	}

	// 2. session.open + session.subscribe hydrates turns from ~/.codex!
	var openSum SessionSummaryWire
	if err := cli1.Call(ctx, "session.open", map[string]any{"id": extID}, &openSum); err != nil {
		t.Fatalf("session.open: %v", err)
	}
	if err := cli1.Call(ctx, "session.subscribe", map[string]any{"id": extID, "since": 0}, nil); err != nil {
		t.Fatalf("session.subscribe: %v", err)
	}
	_, items, err := cli1.WaitForSession(extID, 8*time.Second, func(_ SessionSummaryWire, _ *SessionDetailWire, it []TranscriptItemWire) bool {
		return len(it) >= 4
	})
	if err != nil {
		t.Fatalf("expected >=4 hydrated items from ~/.codex rollout, got err=%v", err)
	}

	// 3. session.rename updates title in ~/.codex.
	if err := cli1.Call(ctx, "session.rename", map[string]any{"id": extID, "title": "Renamed External Thread"}, nil); err != nil {
		t.Fatalf("session.rename: %v", err)
	}

	// Find 2nd user item for fork & rewind.
	var secondUserID string
	uCount := 0
	for _, it := range items {
		if it.Kind == "user" {
			uCount++
			if uCount == 2 {
				secondUserID = it.ID
				break
			}
		}
	}
	if secondUserID == "" {
		t.Fatalf("could not find 2nd user item in hydrated items: %+v", items)
	}

	// 4. session.fork resolves ~/.codex source thread cleanly.
	var forked struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli1.Call(ctx, "session.fork", map[string]any{"id": extID, "itemId": secondUserID}, &forked); err != nil {
		t.Fatalf("session.fork: %v", err)
	}
	if forked.Session.ForkedFrom != extID || !strings.HasPrefix(forked.Session.ID, "thr-fork-") {
		t.Fatalf("expected real fakecodex fork from ~/.codex thread %s, got %+v", extID, forked.Session)
	}

	// 5. session.rewind rewinds sleeping ~/.codex thread cleanly.
	if err := cli1.Call(ctx, "session.rewind", map[string]any{
		"id":               extID,
		"itemId":           secondUserID,
		"conversationOnly": true,
	}, nil); err != nil {
		t.Fatalf("session.rewind: %v", err)
	}

	// 6. session.prompt resumes existing ~/.codex thread (NOT fresh!) and preserves native "openai" provider!
	if err := cli1.Call(ctx, "session.prompt", map[string]any{
		"id":   extID,
		"text": "Resume external native thread [inspect]",
	}, nil); err != nil {
		t.Fatalf("session.prompt: %v", err)
	}
	_, itemsAfterPrompt, err := cli1.WaitForSession(extID, 12*time.Second, func(s SessionSummaryWire, _ *SessionDetailWire, it []TranscriptItemWire) bool {
		_, ok := extractInspectJSON(it)
		return s.State == "idle" && ok
	})
	if err != nil {
		t.Fatalf("WaitForSession after resume prompt: %v", err)
	}
	inspect, ok := extractInspectJSON(itemsAfterPrompt)
	if !ok {
		t.Fatalf("missing INSPECT_JSON after resume prompt")
	}
	if gotHome, _ := inspect["codexHome"].(string); filepath.Clean(gotHome) != filepath.Clean(extCodexHome) {
		t.Fatalf("resume ran in wrong CODEX_HOME=%q, want %q", gotHome, extCodexHome)
	}
	if resumed, _ := inspect["resumed"].(bool); !resumed {
		t.Fatalf("expected resumed=true on ~/.codex thread, got %+v", inspect)
	}
	if gotTID, _ := inspect["threadId"].(string); gotTID != extID {
		t.Fatalf("expected resumed threadId=%q, got %q", extID, gotTID)
	}
	if gotProv, _ := inspect["modelProvider"].(string); gotProv != "openai" {
		t.Fatalf("expected native modelProvider='openai' preserved (no OpenRouter hijack), got %q (%+v)", gotProv, inspect)
	}

	// 7. session.openInTerminal sets CODEX_HOME=~/.codex, preserves native provider, AND broadcasts terms.changed to secondary client cli2!
	marker := cli2.EventMarker()
	var openTerm struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
	}
	if err := cli1.Call(ctx, "session.openInTerminal", map[string]any{
		"id":   extID,
		"cols": 160,
		"rows": 40,
	}, &openTerm); err != nil {
		t.Fatalf("session.openInTerminal: %v", err)
	}
	defer func() {
		_ = cli1.Call(ctx, "term.kill", map[string]any{"id": openTerm.ID}, nil)
	}()

	ev, err := cli2.WaitForEvent(marker, 6*time.Second, func(e PushEvent) bool {
		return e.Event == "terms.changed"
	})
	if err != nil {
		t.Fatalf("secondary client cli2 did not receive terms.changed on session.openInTerminal: %v", err)
	}
	var pushedTerms []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(ev.Data, &pushedTerms); err != nil {
		t.Fatalf("terms.changed payload must be TerminalInfo[] JSON array, got %s: %v", string(ev.Data), err)
	}
	foundPushedTerm := false
	for _, pt := range pushedTerms {
		if pt.ID == openTerm.ID {
			foundPushedTerm = true
			break
		}
	}
	if !foundPushedTerm {
		t.Fatalf("terms.changed array %s missing new terminal %s", string(ev.Data), openTerm.ID)
	}

	readyCapture := waitForTmuxCapture(t, cli1, openTerm.ID, "FAKEX_TUI_READY")
	if strings.Contains(readyCapture, `model_provider="openrouter"`) {
		t.Fatalf("session.openInTerminal hijacked native openai session onto OpenRouter: %s", readyCapture)
	}
	if !strings.Contains(readyCapture, "resume "+extID) {
		t.Fatalf("session.openInTerminal missing resume %s: %s", extID, readyCapture)
	}
	if err := cli1.Call(ctx, "term.sendKeys", map[string]any{"id": openTerm.ID, "text": "env:CODEX_HOME", "enter": true}, nil); err != nil {
		t.Fatalf("term.sendKeys CODEX_HOME: %v", err)
	}
	waitForTmuxCapture(t, cli1, openTerm.ID, "FAKEX_TUI_ENV:CODEX_HOME="+extCodexHome)
	if err := cli1.Call(ctx, "term.sendKeys", map[string]any{"id": openTerm.ID, "text": "env:AGENTC_BRIDGE_TOKEN", "enter": true}, nil); err != nil {
		t.Fatalf("term.sendKeys AGENTC_BRIDGE_TOKEN: %v", err)
	}
	envCap := waitForTmuxCapture(t, cli1, openTerm.ID, "FAKEX_TUI_ENV:AGENTC_BRIDGE_TOKEN=")
	if !strings.Contains(envCap, "FAKEX_TUI_ENV:AGENTC_BRIDGE_TOKEN=\n") && !strings.HasSuffix(strings.TrimSpace(envCap), "FAKEX_TUI_ENV:AGENTC_BRIDGE_TOKEN=") {
		t.Fatalf("expected empty AGENTC_BRIDGE_TOKEN in native openai TUI, got:\n%s", envCap)
	}

	// 8. R2-D04: term.create(kind: "codex") & session.openInTerminal propagate DefaultModel / sessModel via -c model=...,
	// prepend filepath.Dir(bin) to PATH, merge non-secret ProviderConfig Args/Env, omit resume on pre-Turn-1 unmaterialized
	// sessions, and resume materialized sessions after Turn 1!
	if err := cli1.Call(ctx, "providers.upsert", map[string]any{
		"id":           "codex",
		"name":         "Codex",
		"enabled":      true,
		"command":      fakecodexBin,
		"defaultModel": "google/gemini-2.5-pro",
		"args":         []string{"-c", "custom_tui_flag=1"},
		"env": map[string]string{
			"CUSTOM_TUI_ENV":          "tui-ok",
			"OPENROUTER_API_KEY":      "***",
			"OPENROUTER_API_KEY_FILE": "/secret/do-not-expose",
		},
	}, nil); err != nil {
		t.Fatalf("providers.upsert(codex): %v", err)
	}

	var defTerm struct {
		ID string `json:"id"`
	}
	if err := cli1.Call(ctx, "term.create", map[string]any{
		"kind": "codex",
		"cwd":  repo,
		"cols": 500,
		"rows": 40,
	}, &defTerm); err != nil {
		t.Fatalf("term.create(kind=codex): %v", err)
	}
	defer func() {
		_ = cli1.Call(ctx, "term.kill", map[string]any{"id": defTerm.ID}, nil)
	}()
	defReady := waitForTmuxCaptureUnwrapped(t, cli1, defTerm.ID, "cwd="+repo)
	if !strings.Contains(defReady, `-c model="google/gemini-2.5-pro"`) {
		t.Fatalf("expected DefaultModel -c model=\"google/gemini-2.5-pro\" in codex TUI args, got: %s", defReady)
	}
	if !strings.Contains(defReady, "-c custom_tui_flag=1") {
		t.Fatalf("expected ProviderConfig.Args (-c custom_tui_flag=1) in codex TUI args, got: %s", defReady)
	}
	_ = cli1.Call(ctx, "term.sendKeys", map[string]any{"id": defTerm.ID, "text": "env:PATH", "enter": true}, nil)
	waitForTmuxCaptureUnwrapped(t, cli1, defTerm.ID, "FAKEX_TUI_ENV:PATH="+filepath.Dir(fakecodexBin))
	_ = cli1.Call(ctx, "term.sendKeys", map[string]any{"id": defTerm.ID, "text": "env:CUSTOM_TUI_ENV", "enter": true}, nil)
	waitForTmuxCaptureUnwrapped(t, cli1, defTerm.ID, "FAKEX_TUI_ENV:CUSTOM_TUI_ENV=tui-ok")
	_ = cli1.Call(ctx, "term.sendKeys", map[string]any{"id": defTerm.ID, "text": "env:OPENROUTER_API_KEY_FILE", "enter": true}, nil)
	keyFileCap := waitForTmuxCaptureUnwrapped(t, cli1, defTerm.ID, "FAKEX_TUI_ENV:OPENROUTER_API_KEY_FILE=")
	if strings.Contains(keyFileCap, "/secret/do-not-expose") {
		t.Fatalf("OPENROUTER_API_KEY_FILE leaked into tmux env: %s", keyFileCap)
	}

	var unmatCreated struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli1.Call(ctx, "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "deepseek/deepseek-v4.1-flash",
	}, &unmatCreated); err != nil {
		t.Fatalf("session.create(unmaterialized): %v", err)
	}
	unmatID := unmatCreated.Session.ID
	var unmatTerm struct {
		ID string `json:"id"`
	}
	if err := cli1.Call(ctx, "session.openInTerminal", map[string]any{
		"id":   unmatID,
		"cols": 500,
		"rows": 40,
	}, &unmatTerm); err != nil {
		t.Fatalf("session.openInTerminal(unmaterialized): %v", err)
	}
	defer func() {
		_ = cli1.Call(ctx, "term.kill", map[string]any{"id": unmatTerm.ID}, nil)
	}()
	unmatReady := waitForTmuxCaptureUnwrapped(t, cli1, unmatTerm.ID, "cwd="+repo)
	if !strings.Contains(unmatReady, `-c model="deepseek/deepseek-v4.1-flash"`) {
		t.Fatalf("expected sessModel -c model=\"deepseek/deepseek-v4.1-flash\" on pre-Turn-1 openInTerminal, got: %s", unmatReady)
	}
	if strings.Contains(unmatReady, "resume ") {
		t.Fatalf("expected pre-Turn-1 unmaterialized session to omit resume, got: %s", unmatReady)
	}

	// Materialize Turn 1 on unmatID, then verify session.openInTerminal DOES pass resume <unmatID>!
	_ = cli1.Call(ctx, "session.subscribe", map[string]any{"id": unmatID, "since": 0}, nil)
	if err := cli1.Call(ctx, "session.prompt", map[string]any{
		"id":   unmatID,
		"text": "Materialize turn 1",
	}, nil); err != nil {
		t.Fatalf("session.prompt(materialize): %v", err)
	}
	if _, _, err := cli1.WaitForTurnIdle(unmatID, 1, 12*time.Second); err != nil {
		t.Fatalf("WaitForTurnIdle(materialize): %v", err)
	}
	var matTerm struct {
		ID string `json:"id"`
	}
	if err := cli1.Call(ctx, "session.openInTerminal", map[string]any{
		"id":   unmatID,
		"cols": 500,
		"rows": 40,
	}, &matTerm); err != nil {
		t.Fatalf("session.openInTerminal(materialized): %v", err)
	}
	defer func() {
		_ = cli1.Call(ctx, "term.kill", map[string]any{"id": matTerm.ID}, nil)
	}()
	matReady := waitForTmuxCaptureUnwrapped(t, cli1, matTerm.ID, "cwd="+repo)
	if !strings.Contains(matReady, "resume thr-") || strings.Contains(matReady, "resume "+unmatID) {
		t.Fatalf("expected materialized session to resume fresh persisted thread (not unpersisted %s), got: %s", unmatID, matReady)
	}
}

func TestTier5_03_OpenRouterFirstPartyRouterAndMixedCaseSlugs(t *testing.T) {
	suiteMockOR.ClearRequests()
	td := StartIsolatedDaemon(t, DaemonOpts{
		Env: map[string]string{"OPENROUTER_API_KEY": "sk-or-v1-t5-03-routers"},
	})
	cli := td.PairAndConnect(t, "T5-03", "linux")
	repo := newTempGitRepo(t, nil)
	ctx := context.Background()

	cases := []struct {
		inputSlug string
		wantWire  string
	}{
		{"openrouter/free", "openrouter/free"},
		{"openrouter/auto", "openrouter/auto"},
		{"OpenRouter/free", "openrouter/free"},
		{"OpenRouter/auto:nitro", "openrouter/auto:nitro"},
		{"OpenRouter/google/gemini-3-flash-preview:thinking", "google/gemini-3-flash-preview:thinking"},
		{"openrouter/", "google/gemini-3-flash-preview"},
		{"OpenRouter/   ", "google/gemini-3-flash-preview"},
	}

	for i, tc := range cases {
		var created struct {
			Session SessionSummaryWire `json:"session"`
		}
		if err := cli.Call(ctx, "session.create", map[string]any{
			"agent": "codex",
			"cwd":   repo,
			"model": tc.inputSlug,
		}, &created); err != nil {
			t.Fatalf("session.create(%s): %v", tc.inputSlug, err)
		}
		sid := created.Session.ID
		if err := cli.Call(ctx, "session.subscribe", map[string]any{"id": sid, "since": 0}, nil); err != nil {
			t.Fatalf("session.subscribe(%s): %v", tc.inputSlug, err)
		}
		beforeCount := len(suiteMockOR.ResponsesRequests())
		if err := cli.Call(ctx, "session.prompt", map[string]any{
			"id":   sid,
			"text": "Router wire check [bridge-probe]",
		}, nil); err != nil {
			t.Fatalf("session.prompt(%s): %v", tc.inputSlug, err)
		}
		if _, _, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second); err != nil {
			t.Fatalf("WaitForTurnIdle(%s, case %d): %v", tc.inputSlug, i, err)
		}
		reqs := suiteMockOR.ResponsesRequests()
		if len(reqs) <= beforeCount {
			t.Fatalf("expected OpenRouter /responses request for %s, got %d -> %d", tc.inputSlug, beforeCount, len(reqs))
		}
		gotWire := reqs[len(reqs)-1].Model
		if gotWire != tc.wantWire {
			t.Fatalf("slug %q sent upstream wire model %q, want %q", tc.inputSlug, gotWire, tc.wantWire)
		}
	}

	// Explicit provider="openai" strips case-insensitive "openai/" and "openrouter/openai/".
	for _, oaSlug := range []string{"openai/gpt-4.1-mini", "openrouter/openai/gpt-4.1-mini"} {
		var createdOA struct {
			Session SessionSummaryWire `json:"session"`
		}
		if err := cli.Call(ctx, "session.create", map[string]any{
			"agent": "codex",
			"cwd":   repo,
			"flags": map[string]any{
				"provider": "openai",
				"model":    oaSlug,
			},
		}, &createdOA); err != nil {
			t.Fatalf("session.create(openai, %s): %v", oaSlug, err)
		}
		sidOA := createdOA.Session.ID
		_ = cli.Call(ctx, "session.subscribe", map[string]any{"id": sidOA, "since": 0}, nil)
		if err := cli.Call(ctx, "session.prompt", map[string]any{
			"id":   sidOA,
			"text": "Native provider wire check [inspect]",
		}, nil); err != nil {
			t.Fatalf("session.prompt(openai, %s): %v", oaSlug, err)
		}
		_, itemsOA, err := cli.WaitForSession(sidOA, 12*time.Second, func(s SessionSummaryWire, _ *SessionDetailWire, it []TranscriptItemWire) bool {
			_, ok := extractInspectJSON(it)
			return s.State == "idle" && ok
		})
		if err != nil {
			t.Fatalf("WaitForSession(openai, %s): %v", oaSlug, err)
		}
		insOA, _ := extractInspectJSON(itemsOA)
		if gotProv, _ := insOA["modelProvider"].(string); gotProv != "openai" {
			t.Fatalf("expected modelProvider='openai' for %q, got %q", oaSlug, gotProv)
		}
		if gotModel, _ := insOA["model"].(string); gotModel != "gpt-4.1-mini" {
			t.Fatalf("expected stripped wire model 'gpt-4.1-mini' for %q on provider=openai, got %q", oaSlug, gotModel)
		}
	}

	// Live session.setModel with bare "gpt-4.1-mini" on provider="openrouter" ensures "openai/gpt-4.1-mini" in catalog & wire!
	var createdOR struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(ctx, "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"flags": map[string]any{
			"provider": "openrouter",
			"model":    "google/gemini-3-flash-preview",
		},
	}, &createdOR); err != nil {
		t.Fatalf("session.create(openrouter): %v", err)
	}
	sidOR := createdOR.Session.ID
	_ = cli.Call(ctx, "session.subscribe", map[string]any{"id": sidOR, "since": 0}, nil)
	if err := cli.Call(ctx, "session.setModel", map[string]any{"id": sidOR, "model": "gpt-4.1-mini"}, nil); err != nil {
		t.Fatalf("session.setModel(gpt-4.1-mini): %v", err)
	}
	beforeOR := len(suiteMockOR.ResponsesRequests())
	if err := cli.Call(ctx, "session.prompt", map[string]any{
		"id":   sidOR,
		"text": "Bare model on forced openrouter [inspect] [bridge-probe]",
	}, nil); err != nil {
		t.Fatalf("session.prompt(setModel bare): %v", err)
	}
	_, itemsOR, err := cli.WaitForSession(sidOR, 12*time.Second, func(s SessionSummaryWire, _ *SessionDetailWire, it []TranscriptItemWire) bool {
		_, ok := extractInspectJSON(it)
		return s.State == "idle" && ok
	})
	if err != nil {
		t.Fatalf("WaitForSession(setModel bare): %v", err)
	}
	insOR, _ := extractInspectJSON(itemsOR)
	if hasCat, _ := insOR["catalogHasModel"].(bool); !hasCat {
		t.Fatalf("expected catalogHasModel=true after handleSetModel(EnsureModel(wireModel)), got %+v", insOR)
	}
	reqsAfter := suiteMockOR.ResponsesRequests()
	if len(reqsAfter) <= beforeOR || reqsAfter[len(reqsAfter)-1].Model != "openai/gpt-4.1-mini" {
		t.Fatalf("expected wire model openai/gpt-4.1-mini, got %+v", reqsAfter)
	}
}

func TestTier5_04_OpenRouterAffordCapPersistsAcrossSessions(t *testing.T) {
	suiteMockOR.ClearRequests()
	td := StartIsolatedDaemon(t, DaemonOpts{
		Env: map[string]string{"OPENROUTER_API_KEY": "sk-or-v1-t5-04-same-key"},
	})
	cli := td.PairAndConnect(t, "T5-04", "linux")
	repo := newTempGitRepo(t, nil)
	ctx := context.Background()

	// Session 1 triggers HTTP 402 "can only afford 512" -> retries at 448 and caches affordCap=448.
	var s1 struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(ctx, "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "google/gemini-3-flash-preview",
	}, &s1); err != nil {
		t.Fatalf("session.create s1: %v", err)
	}
	_ = cli.Call(ctx, "session.subscribe", map[string]any{"id": s1.Session.ID, "since": 0}, nil)
	_ = cli.Call(ctx, "session.prompt", map[string]any{
		"id":   s1.Session.ID,
		"text": "Trigger 402 ceiling [bridge-402] [afford:512]",
	}, nil)
	if _, _, err := cli.WaitForTurnIdle(s1.Session.ID, 1, 12*time.Second); err != nil {
		t.Fatalf("s1 WaitForTurnIdle: %v", err)
	}

	// Session 2 invokes Manager.CodexBridge() -> SetAPIKey(sameKey) and must preserve affordCap=448!
	var s2 struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(ctx, "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "google/gemini-2.5-flash",
	}, &s2); err != nil {
		t.Fatalf("session.create s2: %v", err)
	}
	_ = cli.Call(ctx, "session.subscribe", map[string]any{"id": s2.Session.ID, "since": 0}, nil)
	beforeS2 := len(suiteMockOR.ResponsesRequests())
	_ = cli.Call(ctx, "session.prompt", map[string]any{
		"id":   s2.Session.ID,
		"text": "Verify cached cap on new session [bridge-probe]",
	}, nil)
	if _, _, err := cli.WaitForTurnIdle(s2.Session.ID, 1, 12*time.Second); err != nil {
		t.Fatalf("s2 WaitForTurnIdle: %v", err)
	}

	reqs := suiteMockOR.ResponsesRequests()
	if beforeS2 != 2 || len(reqs) != 3 {
		t.Fatalf("expected 2 requests for s1 (8192->448) + 1 request for s2 (448), got beforeS2=%d total=%d", beforeS2, len(reqs))
	}
	if reqs[0].MaxOutputTokens != 8192 || reqs[1].MaxOutputTokens != 448 || reqs[2].MaxOutputTokens != 448 {
		t.Fatalf("expected max_output_tokens [8192, 448, 448], got [%d, %d, %d]",
			reqs[0].MaxOutputTokens, reqs[1].MaxOutputTokens, reqs[2].MaxOutputTokens)
	}
}

func TestTier5_05_ApplyPatchAddFileWithLiteralLeadingPlusLines(t *testing.T) {
	suiteMockOR.ClearRequests()
	td := StartIsolatedDaemon(t, DaemonOpts{
		Env: map[string]string{"OPENROUTER_API_KEY": "sk-or-v1-t5-05-patch"},
	})
	cli := td.PairAndConnect(t, "T5-05", "linux")
	repo := newTempGitRepo(t, nil)
	ctx := context.Background()

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(ctx, "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "google/gemini-3-flash-preview",
	}, &created); err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sid := created.Session.ID
	_ = cli.Call(ctx, "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)
	_ = cli.Call(ctx, "session.prompt", map[string]any{
		"id":   sid,
		"text": "Create notes.md with literal leading plus line [bridge-patch] [literal-plus]",
	}, nil)
	if _, _, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second); err != nil {
		t.Fatalf("WaitForTurnIdle: %v", err)
	}

	gotBytes, err := os.ReadFile(filepath.Join(repo, "notes.md"))
	if err != nil {
		t.Fatalf("read notes.md: %v", err)
	}
	want := "# Heading\n\n+ literal bullet\nplain line\n"
	if string(gotBytes) != want {
		t.Fatalf("notes.md = %q, want %q", string(gotBytes), want)
	}

	gotMain, err := os.ReadFile(filepath.Join(repo, "cmd", "app", "main.go"))
	if err != nil {
		t.Fatalf("read cmd/app/main.go: %v", err)
	}
	wantMain := "package main\n\nfunc main() {\n\tprintln(\"ok\")\n}\n"
	if string(gotMain) != wantMain {
		t.Fatalf("cmd/app/main.go = %q, want %q", string(gotMain), wantMain)
	}
}

func TestTier5_06_CodexModeAliasNormalizationAcrossLiveSetModeSetConfigAndWake(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T5-06-ModeNorm", "linux")
	repo := newTempGitRepo(t, nil)
	ctx := context.Background()

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(ctx, "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "google/gemini-3-flash-preview",
		"mode":  "accept-edits",
	}, &created); err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sid := created.Session.ID
	if created.Session.Mode != "acceptEdits" {
		t.Fatalf("session.create(accept-edits) returned mode=%q, want acceptEdits", created.Session.Mode)
	}
	_ = cli.Call(ctx, "session.subscribe", map[string]any{"id": sid, "since": 0}, nil)

	assertCanonicalMode := func(step, want string) {
		t.Helper()
		var openSum SessionSummaryWire
		if err := cli.Call(ctx, "session.open", map[string]any{"id": sid}, &openSum); err != nil {
			t.Fatalf("%s session.open: %v", step, err)
		}
		if openSum.Mode != want {
			t.Fatalf("%s session.open mode=%q, want %q", step, openSum.Mode, want)
		}
		var rows []SessionSummaryWire
		if err := cli.Call(ctx, "sessions.list", map[string]any{}, &rows); err != nil {
			t.Fatalf("%s sessions.list: %v", step, err)
		}
		for _, r := range rows {
			if r.ID == sid && r.Mode != want {
				t.Fatalf("%s sessions.list mode=%q, want %q", step, r.Mode, want)
			}
		}
	}

	assertCanonicalMode("initial create", "acceptEdits")

	// Live session.setMode aliases
	for _, step := range []struct{ alias, want string }{
		{"supervised", "default"},
		{"fullAccess", "full-access"},
		{"accept-edits", "acceptEdits"},
	} {
		if err := cli.Call(ctx, "session.setMode", map[string]any{"id": sid, "mode": step.alias}, nil); err != nil {
			t.Fatalf("live setMode(%s): %v", step.alias, err)
		}
		assertCanonicalMode("live setMode("+step.alias+")", step.want)
	}

	// Live session.setConfig(configId: "mode")
	if err := cli.Call(ctx, "session.setConfig", map[string]any{
		"id":       sid,
		"configId": "mode",
		"value":    "supervised",
	}, nil); err != nil {
		t.Fatalf("live setConfig(mode=supervised): %v", err)
	}
	assertCanonicalMode("live setConfig(supervised)", "default")

	if err := cli.Call(ctx, "session.setConfig", map[string]any{
		"id":       sid,
		"configId": "mode",
		"value":    "accept-edits",
	}, nil); err != nil {
		t.Fatalf("live setConfig(mode=accept-edits): %v", err)
	}
	assertCanonicalMode("live setConfig(accept-edits)", "acceptEdits")

	// Stop (hibernate) -> sleeping setMode -> wake
	if err := cli.Call(ctx, "session.stop", map[string]any{"id": sid}, nil); err != nil {
		t.Fatalf("session.stop: %v", err)
	}
	if _, _, err := cli.WaitForSession(sid, 6*time.Second, func(s SessionSummaryWire, _ *SessionDetailWire, _ []TranscriptItemWire) bool {
		return !s.Live
	}); err != nil {
		t.Fatalf("WaitForSession(stop): %v", err)
	}
	assertCanonicalMode("after stop", "acceptEdits")

	if err := cli.Call(ctx, "session.setMode", map[string]any{"id": sid, "mode": "fullAccess"}, nil); err != nil {
		t.Fatalf("sleeping setMode(fullAccess): %v", err)
	}
	assertCanonicalMode("sleeping setMode(fullAccess)", "full-access")

	if err := cli.Call(ctx, "session.setMode", map[string]any{"id": sid, "mode": "accept-edits"}, nil); err != nil {
		t.Fatalf("sleeping setMode(accept-edits): %v", err)
	}
	assertCanonicalMode("sleeping setMode(accept-edits)", "acceptEdits")

	if err := cli.Call(ctx, "session.wake", map[string]any{"id": sid}, nil); err != nil {
		t.Fatalf("session.wake: %v", err)
	}
	if _, _, err := cli.WaitForSession(sid, 8*time.Second, func(s SessionSummaryWire, d *SessionDetailWire, _ []TranscriptItemWire) bool {
		return s.Live && d != nil && d.Modes != nil && d.Modes.CurrentModeID == "acceptEdits"
	}); err != nil {
		t.Fatalf("WaitForSession(wake with acceptEdits): %v", err)
	}
	assertCanonicalMode("after wake", "acceptEdits")

	// R2-D05: session.setConfig(configId: "model", value: "<unlisted-slug>") updates model live, sleeping, and across wake!
	assertModel := func(step, want string) {
		t.Helper()
		var openSum SessionSummaryWire
		if err := cli.Call(ctx, "session.open", map[string]any{"id": sid}, &openSum); err != nil {
			t.Fatalf("%s session.open: %v", step, err)
		}
		if openSum.Model != want {
			t.Fatalf("%s session.open model=%q, want %q", step, openSum.Model, want)
		}
	}

	if err := cli.Call(ctx, "session.setConfig", map[string]any{
		"id":       sid,
		"configId": "model",
		"value":    "x-ai/grok-live-cfg-99",
	}, nil); err != nil {
		t.Fatalf("live setConfig(model): %v", err)
	}
	assertModel("live setConfig(model)", "x-ai/grok-live-cfg-99")

	if err := cli.Call(ctx, "session.stop", map[string]any{"id": sid}, nil); err != nil {
		t.Fatalf("session.stop(2): %v", err)
	}
	if _, _, err := cli.WaitForSession(sid, 6*time.Second, func(s SessionSummaryWire, _ *SessionDetailWire, _ []TranscriptItemWire) bool {
		return !s.Live
	}); err != nil {
		t.Fatalf("WaitForSession(stop 2): %v", err)
	}
	if err := cli.Call(ctx, "session.setConfig", map[string]any{
		"id":       sid,
		"configId": "model",
		"value":    "x-ai/grok-sleep-cfg-100",
	}, nil); err != nil {
		t.Fatalf("sleeping setConfig(model): %v", err)
	}
	assertModel("sleeping setConfig(model)", "x-ai/grok-sleep-cfg-100")

	if err := cli.Call(ctx, "session.wake", map[string]any{"id": sid}, nil); err != nil {
		t.Fatalf("session.wake(2): %v", err)
	}
	if _, _, err := cli.WaitForSession(sid, 8*time.Second, func(s SessionSummaryWire, _ *SessionDetailWire, _ []TranscriptItemWire) bool {
		return s.Live && s.Model == "x-ai/grok-sleep-cfg-100"
	}); err != nil {
		t.Fatalf("WaitForSession(wake 2 with model): %v", err)
	}
	assertModel("after wake 2", "x-ai/grok-sleep-cfg-100")
}

func TestTier5_07_EmptyFileCreateReadAndEditRoundTrip(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T5-07-EmptyFile", "android")
	dir := newTempPlainDir(t)
	ctx := context.Background()

	emptyPath := filepath.Join(dir, "brand_new_empty.txt")
	if err := cli.Call(ctx, "fs.create", map[string]any{"path": emptyPath}, nil); err != nil {
		t.Fatalf("fs.create: %v", err)
	}

	rawRead, err := cli.CallRaw(ctx, "fs.read", map[string]any{"path": emptyPath, "max": 1024 * 1024})
	if err != nil {
		t.Fatalf("fs.read empty file: %v", err)
	}
	var readMap map[string]any
	if err := json.Unmarshal(rawRead, &readMap); err != nil {
		t.Fatalf("unmarshal fs.read: %v", err)
	}
	txtVal, hasText := readMap["text"]
	if !hasText || txtVal != "" {
		t.Fatalf("expected raw fs.read JSON to contain \"text\":\"\", got hasText=%v val=%#v raw=%s", hasText, txtVal, string(rawRead))
	}
	if binVal, _ := readMap["binary"].(bool); binVal {
		t.Fatalf("expected binary=false on 0-byte file, got %s", string(rawRead))
	}
	mtimeFloat, ok := readMap["mtime"].(float64)
	if !ok || int64(mtimeFloat) <= 0 {
		t.Fatalf("expected positive mtime in fs.read, got %s", string(rawRead))
	}

	wantBody := "first edit into newly created empty file\n"
	if err := cli.Call(ctx, "fs.write", map[string]any{
		"path":        emptyPath,
		"text":        wantBody,
		"expectMtime": int64(mtimeFloat),
	}, nil); err != nil {
		t.Fatalf("fs.write with expectMtime: %v", err)
	}

	var afterRead struct {
		Text   string `json:"text"`
		Size   int64  `json:"size"`
		Binary bool   `json:"binary"`
	}
	if err := cli.Call(ctx, "fs.read", map[string]any{"path": emptyPath, "max": 1024 * 1024}, &afterRead); err != nil {
		t.Fatalf("fs.read after write: %v", err)
	}
	if afterRead.Binary || afterRead.Text != wantBody || afterRead.Size != int64(len(wantBody)) {
		t.Fatalf("unexpected round-trip read: %+v", afterRead)
	}
}

func TestTier5_08_ScheduleTogglePreservesIntervalRunAtModelModeProject(t *testing.T) {
	td := StartIsolatedDaemon(t, DaemonOpts{
		Env: map[string]string{"OPENROUTER_API_KEY": "sk-or-v1-t5-08"},
	})
	cli := td.PairAndConnect(t, "T5-08-Schedules", "android")
	repo := newTempGitRepo(t, nil)
	ctx := context.Background()

	futureRunAt := time.Now().Add(24 * time.Hour).UnixMilli()

	type schedWire struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Enabled     bool   `json:"enabled"`
		Kind        string `json:"kind"`
		Cron        string `json:"cron"`
		IntervalSec int64  `json:"intervalSec"`
		RunAt       int64  `json:"runAt"`
		ProjectID   string `json:"projectId"`
		Cwd         string `json:"cwd"`
		Agent       string `json:"agent"`
		Model       string `json:"model"`
		Mode        string `json:"mode"`
		Prompt      string `json:"prompt"`
		UseWorktree bool   `json:"useWorktree"`
	}

	var cronTask, intervalTask, onceTask schedWire
	if err := cli.Call(ctx, "schedules.upsert", map[string]any{
		"name":        "Cron T5",
		"enabled":     true,
		"kind":        "cron",
		"cron":        "0 9 * * 1-5",
		"projectId":   "proj-cron-t5",
		"cwd":         repo,
		"agent":       "codex",
		"model":       "google/gemini-3-flash-preview",
		"mode":        "auto",
		"prompt":      "Cron prompt",
		"useWorktree": true,
	}, &cronTask); err != nil {
		t.Fatalf("upsert cron: %v", err)
	}

	if err := cli.Call(ctx, "schedules.upsert", map[string]any{
		"name":        "Interval T5",
		"enabled":     true,
		"kind":        "interval",
		"intervalSec": 120,
		"projectId":   "proj-int-t5",
		"cwd":         repo,
		"agent":       "codex",
		"model":       "openrouter/free",
		"mode":        "full-access",
		"prompt":      "Interval prompt",
		"useWorktree": false,
	}, &intervalTask); err != nil {
		t.Fatalf("upsert interval: %v", err)
	}

	if err := cli.Call(ctx, "schedules.upsert", map[string]any{
		"name":        "Once T5",
		"enabled":     true,
		"kind":        "once",
		"runAt":       futureRunAt,
		"projectId":   "proj-once-t5",
		"cwd":         repo,
		"agent":       "codex",
		"model":       "google/gemini-2.5-flash",
		"mode":        "plan",
		"prompt":      "Once prompt",
		"useWorktree": false,
	}, &onceTask); err != nil {
		t.Fatalf("upsert once: %v", err)
	}

	togglePayload := func(tw schedWire, enabled bool) map[string]any {
		m := map[string]any{
			"id":          tw.ID,
			"name":        tw.Name,
			"enabled":     enabled,
			"kind":        tw.Kind,
			"cwd":         tw.Cwd,
			"agent":       tw.Agent,
			"prompt":      tw.Prompt,
			"useWorktree": tw.UseWorktree,
		}
		if tw.Cron != "" {
			m["cron"] = tw.Cron
		}
		if tw.IntervalSec > 0 {
			m["intervalSec"] = tw.IntervalSec
		}
		if tw.RunAt > 0 {
			m["runAt"] = tw.RunAt
		}
		if tw.ProjectID != "" {
			m["projectId"] = tw.ProjectID
		}
		if tw.Model != "" {
			m["model"] = tw.Model
		}
		if tw.Mode != "" {
			m["mode"] = tw.Mode
		}
		return m
	}

	for _, targetEnabled := range []bool{false, true} {
		for _, cur := range []*schedWire{&cronTask, &intervalTask, &onceTask} {
			var updated schedWire
			if err := cli.Call(ctx, "schedules.upsert", togglePayload(*cur, targetEnabled), &updated); err != nil {
				t.Fatalf("toggle %s enabled=%v failed: %v", cur.Kind, targetEnabled, err)
			}
			*cur = updated
		}
	}

	var listed struct {
		Tasks []schedWire `json:"tasks"`
	}
	if err := cli.Call(ctx, "schedules.list", map[string]any{}, &listed); err != nil {
		t.Fatalf("schedules.list: %v", err)
	}
	byID := map[string]schedWire{}
	for _, tk := range listed.Tasks {
		byID[tk.ID] = tk
	}

	gotCron := byID[cronTask.ID]
	if !gotCron.Enabled || gotCron.Model != "google/gemini-3-flash-preview" || gotCron.Mode != "auto" || gotCron.ProjectID != "proj-cron-t5" || !gotCron.UseWorktree {
		t.Fatalf("cron task fields lost across toggle: %+v", gotCron)
	}
	gotInt := byID[intervalTask.ID]
	if !gotInt.Enabled || gotInt.IntervalSec != 120 || gotInt.Model != "openrouter/free" || gotInt.Mode != "full-access" || gotInt.ProjectID != "proj-int-t5" {
		t.Fatalf("interval task fields lost across toggle: %+v", gotInt)
	}
	gotOnce := byID[onceTask.ID]
	if !gotOnce.Enabled || gotOnce.RunAt != futureRunAt || gotOnce.Model != "google/gemini-2.5-flash" || gotOnce.Mode != "plan" || gotOnce.ProjectID != "proj-once-t5" {
		t.Fatalf("once task fields lost across toggle: %+v", gotOnce)
	}
}

func waitForTmuxCaptureUnwrapped(t *testing.T, cli *NoiseRPCClient, termID, substr string) string {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		var capRes struct {
			Text string `json:"text"`
		}
		if err := cli.Call(context.Background(), "term.capture", map[string]any{"id": termID, "lines": 80}, &capRes); err == nil {
			unwrapped := strings.ReplaceAll(capRes.Text, "\n", "")
			last = unwrapped
			if strings.Contains(unwrapped, substr) {
				return unwrapped
			}
		}
		time.Sleep(80 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for unwrapped %q in term.capture(%s); last=%q", substr, termID, last)
	return ""
}

func TestTier5_09_SessionOpsStrictSeqMonotonicityAndOpsBeforeIdleUnderStreaming(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T5-09-Primary", "linux")
	cliLate := td.PairAndConnect(t, "T5-09-LateSub", "android")
	repo := newTempGitRepo(t, nil)
	ctx := context.Background()

	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(ctx, "session.create", map[string]any{
		"agent": "fakeacp",
		"cwd":   repo,
	}, &created); err != nil {
		t.Fatalf("session.create(fakeacp): %v", err)
	}
	sid := created.Session.ID
	if err := cli.Call(ctx, "session.subscribe", map[string]any{"id": sid, "since": 0}, nil); err != nil {
		t.Fatalf("session.subscribe: %v", err)
	}

	// Simulate strict Desktop (transcript-reducer.ts) & Android (HostConnection.kt) monotonic-seq reducers
	// directly over raw Noise_IK PushEvents: any op with op.Seq <= seq is permanently dropped, and every
	// session.ops frame for a turn MUST arrive strictly before session.changed(state == "idle", turns == turnNum).
	reducedSeq := int64(0)
	reducedItems := map[string]TranscriptItemWire{}

	assertTurnWireStream := func(events []PushEvent, wantTurn int, wantSubstr string) {
		t.Helper()
		sawIdleForTurn := false
		sawOpsForTurn := false
		for idx, ev := range events {
			switch ev.Event {
			case "session.ops":
				var opsEv struct {
					ID  string             `json:"id"`
					Ops []TranscriptOpWire `json:"ops"`
				}
				if err := json.Unmarshal(ev.Data, &opsEv); err != nil {
					t.Fatalf("unmarshal session.ops: %v", err)
				}
				if opsEv.ID != sid {
					continue
				}
				sawOpsForTurn = true
				if sawIdleForTurn {
					t.Fatalf("turn %d frame %d: session.ops arrived AFTER session.changed(state=idle, turns=%d)", wantTurn, idx, wantTurn)
				}
				for _, op := range opsEv.Ops {
					if op.Seq > 0 && op.Seq <= reducedSeq {
						t.Fatalf("turn %d: strict Seq inversion/duplicate on session.ops: op.Seq=%d <= prevSeq=%d (op=%+v)", wantTurn, op.Seq, reducedSeq, op)
					}
					if op.Seq > reducedSeq {
						reducedSeq = op.Seq
					}
					switch op.Op {
					case "reset":
						reducedItems = map[string]TranscriptItemWire{}
						for _, it := range op.Items {
							reducedItems[it.ID] = it
						}
					case "add", "set":
						if op.Item != nil {
							reducedItems[op.Item.ID] = *op.Item
						}
					case "append":
						cur := reducedItems[op.ID]
						cur.Text += op.Text
						reducedItems[op.ID] = cur
					case "remove":
						delete(reducedItems, op.ID)
					}
				}
			case "session.changed":
				var sum SessionSummaryWire
				if err := json.Unmarshal(ev.Data, &sum); err == nil && sum.ID == sid {
					if sum.State == "idle" && sum.Turns == wantTurn {
						sawIdleForTurn = true
					}
				}
			}
		}
		if !sawOpsForTurn || !sawIdleForTurn {
			t.Fatalf("turn %d: expected both session.ops (%v) and session.changed(idle) (%v)", wantTurn, sawOpsForTurn, sawIdleForTurn)
		}
		var allAgentText strings.Builder
		turnItemSeen := false
		for _, it := range reducedItems {
			if it.Kind == "agent" {
				allAgentText.WriteString(it.Text)
				allAgentText.WriteByte('\n')
			}
			if it.Kind == "turn" && it.Turn != nil && it.Turn.TurnIndex == wantTurn {
				turnItemSeen = true
			}
		}
		if !strings.Contains(allAgentText.String(), wantSubstr) || !turnItemSeen {
			t.Fatalf("turn %d: strict client reducer missed %q or turn item (turnSeen=%v text=%q)", wantTurn, wantSubstr, turnItemSeen, allAgentText.String())
		}
	}

	// Execute 3 multi-chunk streaming turns crossing multiple 60ms flushTimer intervals,
	// plus mid-stream snapshot & delta subscriptions from a second client.
	for turnNum := 1; turnNum <= 3; turnNum++ {
		marker := cli.EventMarker()
		tag := fmt.Sprintf("STREAM_TAG_T509_%d", turnNum)
		if err := cli.Call(ctx, "session.prompt", map[string]any{
			"id":   sid,
			"text": fmt.Sprintf("[slow:180] %s", tag),
		}, nil); err != nil {
			t.Fatalf("session.prompt turn %d: %v", turnNum, err)
		}
		if turnNum == 2 {
			time.Sleep(65 * time.Millisecond)
			lateMarker := cliLate.EventMarker()
			if err := cliLate.Call(ctx, "session.subscribe", map[string]any{"id": sid, "since": 0}, nil); err != nil {
				t.Fatalf("cliLate subscribe(0): %v", err)
			}
			if _, _, err := cliLate.WaitForTurnIdle(sid, turnNum, 10*time.Second); err != nil {
				t.Fatalf("cliLate WaitForTurnIdle: %v", err)
			}
			lateSeq := int64(0)
			for _, ev := range cliLate.EventsSince(lateMarker) {
				switch ev.Event {
				case "session.snapshot":
					var snap struct {
						ID  string `json:"id"`
						Seq int64  `json:"seq"`
					}
					if json.Unmarshal(ev.Data, &snap) == nil && snap.ID == sid {
						lateSeq = snap.Seq
					}
				case "session.ops":
					var opsEv struct {
						ID  string             `json:"id"`
						Ops []TranscriptOpWire `json:"ops"`
					}
					if json.Unmarshal(ev.Data, &opsEv) == nil && opsEv.ID == sid {
						for _, op := range opsEv.Ops {
							if op.Seq > 0 && op.Seq <= lateSeq {
								t.Fatalf("cliLate Seq inversion/duplicate after mid-stream subscribe: op.Seq=%d <= lateSeq=%d", op.Seq, lateSeq)
							}
							lateSeq = op.Seq
						}
					}
				}
			}
		}
		if _, _, err := cli.WaitForTurnIdle(sid, turnNum, 10*time.Second); err != nil {
			t.Fatalf("WaitForTurnIdle turn %d: %v", turnNum, err)
		}
		// Give any stray post-idle frame a moment to arrive if broken, then verify strict ordering.
		time.Sleep(25 * time.Millisecond)
		assertTurnWireStream(cli.EventsSince(marker), turnNum, "Step 4/")
	}
}

func TestTier5_10_RevertPreviewFinalAndCheckpointlessContract_AndPromptlessHandoff(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T5-10", "linux")
	repo := newTempGitRepo(t, map[string]string{"alpha.txt": "initial-v0\n"})
	ctx := context.Background()

	// Part A (R3-D02): session.revertPreview boundary parity with session.rewind.
	var created struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(ctx, "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "google/gemini-3-flash-preview",
	}, &created); err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sid := created.Session.ID
	if err := cli.Call(ctx, "session.subscribe", map[string]any{"id": sid, "since": 0}, nil); err != nil {
		t.Fatalf("session.subscribe: %v", err)
	}

	// Turn 1 edits alpha.txt -> records Turn 1 git checkpoint.
	if err := cli.Call(ctx, "session.prompt", map[string]any{
		"id":   sid,
		"text": `Turn 1 edit [edit:alpha.txt:turn1-v1\n]`,
	}, nil); err != nil {
		t.Fatalf("Turn 1 prompt: %v", err)
	}
	if _, _, err := cli.WaitForTurnIdle(sid, 1, 12*time.Second); err != nil {
		t.Fatalf("Turn 1 idle: %v", err)
	}

	// Turn 2 adds distinct context token ZEPHYR-9000 WITHOUT editing any files (no Turn 2 checkpoint).
	if err := cli.Call(ctx, "session.prompt", map[string]any{
		"id":   sid,
		"text": "Turn 2 read-only architecture decision: token ZEPHYR-9000",
	}, nil); err != nil {
		t.Fatalf("Turn 2 prompt: %v", err)
	}
	_, items2, err := cli.WaitForTurnIdle(sid, 2, 12*time.Second)
	if err != nil {
		t.Fatalf("Turn 2 idle: %v", err)
	}

	var turn1UserID, turn2UserID, turn2AgentID string
	for _, it := range items2 {
		if it.Kind == "user" && strings.Contains(it.Text, "Turn 1 edit") {
			turn1UserID = it.ID
		}
		if it.Kind == "user" && strings.Contains(it.Text, "ZEPHYR-9000") {
			turn2UserID = it.ID
		}
		if it.Kind == "agent" {
			turn2AgentID = it.ID
		}
	}
	if turn1UserID == "" || turn2UserID == "" || turn2AgentID == "" {
		t.Fatalf("missing transcript item IDs: t1u=%q t2u=%q t2a=%q", turn1UserID, turn2UserID, turn2AgentID)
	}

	callPreview := func(itemID string) ([]string, int) {
		t.Helper()
		var out struct {
			Preview struct {
				CanRewind    bool     `json:"canRewind"`
				FilesChanged []string `json:"filesChanged"`
			} `json:"preview"`
			StepIndex int `json:"stepIndex"`
		}
		if err := cli.Call(ctx, "session.revertPreview", map[string]any{
			"id":     sid,
			"itemId": itemID,
		}, &out); err != nil {
			t.Fatalf("session.revertPreview(%q): %v", itemID, err)
		}
		if !out.Preview.CanRewind {
			t.Fatalf("expected canRewind=true for itemId=%q, got %+v", itemID, out)
		}
		return out.Preview.FilesChanged, out.StepIndex
	}

	// 1. Final-turn assistant message & empty itemId MUST return filesChanged: [] and stepIndex == 2.
	for _, idArg := range []string{turn2AgentID, ""} {
		changed, stepIdx := callPreview(idArg)
		if len(changed) != 0 || stepIdx != 2 {
			t.Fatalf("revertPreview(%q) expected empty filesChanged & stepIndex=2, got changed=%v stepIndex=%d", idArg, changed, stepIdx)
		}
	}
	// 2. Checkpointless Turn 2 user message MUST return filesChanged: [] and stepIndex == 1.
	if changed, stepIdx := callPreview(turn2UserID); len(changed) != 0 || stepIdx != 1 {
		t.Fatalf("revertPreview(turn2UserID) expected empty filesChanged & stepIndex=1, got changed=%v stepIndex=%d", changed, stepIdx)
	}
	// 3. Turn 1 user message MUST still report alpha.txt and stepIndex == 0.
	if changed, stepIdx := callPreview(turn1UserID); !slices.Contains(changed, "alpha.txt") || stepIdx != 0 {
		t.Fatalf("revertPreview(turn1UserID) expected [alpha.txt] & stepIndex=0, got changed=%v stepIndex=%d", changed, stepIdx)
	}

	// Part B (R3-D03): Promptless session.handoff delivers SeedHandoff on Turn 1 (and not Turn 2) across fakeacp & codex.
	var srcCreated struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(ctx, "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "google/gemini-3-flash-preview",
	}, &srcCreated); err != nil {
		t.Fatalf("session.create(handoffSrc): %v", err)
	}
	handoffSrcID := srcCreated.Session.ID
	_ = cli.Call(ctx, "session.subscribe", map[string]any{"id": handoffSrcID, "since": 0}, nil)
	if err := cli.Call(ctx, "session.prompt", map[string]any{
		"id":   handoffSrcID,
		"text": "Record architecture token ZEPHYR-9000 for portable handoff",
	}, nil); err != nil {
		t.Fatalf("handoffSrc prompt: %v", err)
	}
	if _, _, err := cli.WaitForTurnIdle(handoffSrcID, 1, 12*time.Second); err != nil {
		t.Fatalf("handoffSrc idle: %v", err)
	}

	var acpHandoff struct {
		Handoff string             `json:"handoff"`
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(ctx, "session.handoff", map[string]any{
		"id":          handoffSrcID,
		"targetAgent": "fakeacp",
		"prompt":      "",
	}, &acpHandoff); err != nil {
		t.Fatalf("promptless session.handoff(codex -> fakeacp): %v", err)
	}
	if !strings.Contains(acpHandoff.Handoff, "ZEPHYR-9000") {
		t.Fatalf("handoff summary missing ZEPHYR-9000: %q", acpHandoff.Handoff)
	}
	acpSID := acpHandoff.Session.ID
	_ = cli.Call(ctx, "session.subscribe", map[string]any{"id": acpSID, "since": 0}, nil)
	_ = cli.Call(ctx, "session.hibernate", map[string]any{"id": acpSID}, nil)

	if err := cli.Call(ctx, "session.prompt", map[string]any{
		"id":   acpSID,
		"text": "First turn on handed-off fakeacp",
	}, nil); err != nil {
		t.Fatalf("fakeacp Turn 1 prompt: %v", err)
	}
	_, acpItems1, err := cli.WaitForTurnIdle(acpSID, 1, 12*time.Second)
	if err != nil {
		t.Fatalf("fakeacp Turn 1 idle: %v", err)
	}
	if !strings.Contains(collectAgentText(acpItems1), "ZEPHYR-9000") {
		t.Fatalf("fakeacp Turn 1 reply did not receive SeedHandoff ZEPHYR-9000: %+v", acpItems1)
	}

	// Turn 2 on fakeacp MUST NOT re-inject SeedHandoff into the new reply.
	if err := cli.Call(ctx, "session.prompt", map[string]any{
		"id":   acpSID,
		"text": "Second turn clean prompt",
	}, nil); err != nil {
		t.Fatalf("fakeacp Turn 2 prompt: %v", err)
	}
	_, acpItems2, err := cli.WaitForTurnIdle(acpSID, 2, 12*time.Second)
	if err != nil {
		t.Fatalf("fakeacp Turn 2 idle: %v", err)
	}
	var lastAgentReply string
	for _, it := range acpItems2 {
		if it.Kind == "agent" {
			lastAgentReply = it.Text
		}
	}
	if strings.Contains(lastAgentReply, "ZEPHYR-9000") {
		t.Fatalf("fakeacp Turn 2 unexpectedly re-injected SeedHandoff ZEPHYR-9000: %q", lastAgentReply)
	}

	// Also verify promptless session.handoff -> codex (OpenRouter bridge sees ZEPHYR-9000 on Turn 1).
	var codexHandoff struct {
		Handoff string             `json:"handoff"`
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(ctx, "session.handoff", map[string]any{
		"id":          handoffSrcID,
		"targetAgent": "codex",
		"targetModel": "google/gemini-3-flash-preview",
	}, &codexHandoff); err != nil {
		t.Fatalf("promptless session.handoff(codex -> codex): %v", err)
	}
	codexSID := codexHandoff.Session.ID
	_ = cli.Call(ctx, "session.subscribe", map[string]any{"id": codexSID, "since": 0}, nil)
	suiteMockOR.ClearRequests()
	if err := cli.Call(ctx, "session.prompt", map[string]any{
		"id":   codexSID,
		"text": "[bridge-probe] Turn 1 on handed-off codex T510_CODEX_T1",
	}, nil); err != nil {
		t.Fatalf("codex handoff Turn 1 prompt: %v", err)
	}
	if _, _, err := cli.WaitForTurnIdle(codexSID, 1, 12*time.Second); err != nil {
		t.Fatalf("codex handoff Turn 1 idle: %v", err)
	}
	foundCodexT1 := false
	for _, req := range suiteMockOR.ResponsesRequests() {
		if strings.Contains(req.PromptText, "T510_CODEX_T1") {
			foundCodexT1 = true
			if !strings.Contains(req.PromptText, "ZEPHYR-9000") {
				t.Fatalf("codex handoff Turn 1 OpenRouter request missing ZEPHYR-9000: %q", req.PromptText)
			}
		}
	}
	if !foundCodexT1 {
		t.Fatalf("did not find OpenRouter request for T510_CODEX_T1")
	}

	suiteMockOR.ClearRequests()
	if err := cli.Call(ctx, "session.prompt", map[string]any{
		"id":   codexSID,
		"text": "[bridge-probe] Turn 2 on handed-off codex T510_CODEX_T2",
	}, nil); err != nil {
		t.Fatalf("codex handoff Turn 2 prompt: %v", err)
	}
	if _, _, err := cli.WaitForTurnIdle(codexSID, 2, 12*time.Second); err != nil {
		t.Fatalf("codex handoff Turn 2 idle: %v", err)
	}
	foundCodexT2 := false
	for _, req := range suiteMockOR.ResponsesRequests() {
		if strings.Contains(req.PromptText, "T510_CODEX_T2") {
			foundCodexT2 = true
			if strings.Contains(req.PromptText, "ZEPHYR-9000") {
				t.Fatalf("codex handoff Turn 2 unexpectedly re-injected SeedHandoff ZEPHYR-9000: %q", req.PromptText)
			}
		}
	}
	if !foundCodexT2 {
		t.Fatalf("did not find OpenRouter request for T510_CODEX_T2")
	}
}

func TestTier5_11_PreTurn1CodexUnlistedSetModel_PromptedHandoffRetry_AndGenericACPOps(t *testing.T) {
	td := sharedDaemon(t)
	cli := td.PairAndConnect(t, "T5-11", "linux")
	repo := newTempGitRepo(t, map[string]string{"main.go": "package main\n"})
	ctx := context.Background()

	// Part A (R4-D01): Pre-Turn-1 session.setModel & session.setConfig("model", ...) with unlisted OpenRouter slugs
	// when thread/start has NOT persisted <id>.json before Turn 1 (matching real codex 0.172.0).
	suiteMockOR.ClearRequests()
	var preSetCreated struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(ctx, "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "google/gemini-3-flash-preview",
	}, &preSetCreated); err != nil {
		t.Fatalf("session.create(preSet): %v", err)
	}
	preSetID := preSetCreated.Session.ID
	_ = cli.Call(ctx, "session.subscribe", map[string]any{"id": preSetID, "since": 0}, nil)

	unlistedSlug1 := "vendor-future/brand-new-model-v9"
	if err := cli.Call(ctx, "session.setModel", map[string]any{
		"id":    preSetID,
		"model": unlistedSlug1,
	}, nil); err != nil {
		t.Fatalf("pre-Turn-1 session.setModel(%q): %v", unlistedSlug1, err)
	}
	if err := cli.Call(ctx, "session.prompt", map[string]any{
		"id":   preSetID,
		"text": "[bridge-probe] Turn 1 after pre-Turn-1 unlisted setModel T511_PRETURN1_M1",
	}, nil); err != nil {
		t.Fatalf("session.prompt(preSet): %v", err)
	}
	preSum1, _, err := cli.WaitForTurnIdle(preSetID, 1, 15*time.Second)
	if err != nil {
		t.Fatalf("WaitForTurnIdle(preSet): %v", err)
	}
	if preSum1.Error != "" {
		t.Fatalf("unexpected LastError after pre-Turn-1 setModel: %q", preSum1.Error)
	}
	foundM1 := false
	for _, req := range suiteMockOR.ResponsesRequests() {
		if strings.Contains(req.PromptText, "T511_PRETURN1_M1") && req.Model == unlistedSlug1 {
			foundM1 = true
		}
	}
	if !foundM1 {
		t.Fatalf("OpenRouter bridge did not receive model=%q for T511_PRETURN1_M1", unlistedSlug1)
	}

	// Also verify pre-Turn-1 session.setConfig(configId: "model") on an unpersisted thread.
	var preCfgCreated struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(ctx, "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "google/gemini-3-flash-preview",
	}, &preCfgCreated); err != nil {
		t.Fatalf("session.create(preCfg): %v", err)
	}
	preCfgID := preCfgCreated.Session.ID
	_ = cli.Call(ctx, "session.subscribe", map[string]any{"id": preCfgID, "since": 0}, nil)
	unlistedSlug2 := "vendor-next/ultra-coder-v2"
	if err := cli.Call(ctx, "session.setConfig", map[string]any{
		"id":       preCfgID,
		"configId": "model",
		"value":    unlistedSlug2,
	}, nil); err != nil {
		t.Fatalf("pre-Turn-1 session.setConfig(model=%q): %v", unlistedSlug2, err)
	}
	if err := cli.Call(ctx, "session.prompt", map[string]any{
		"id":   preCfgID,
		"text": "[bridge-probe] Turn 1 after pre-Turn-1 unlisted setConfig T511_PRETURN1_M2",
	}, nil); err != nil {
		t.Fatalf("session.prompt(preCfg): %v", err)
	}
	if _, _, err := cli.WaitForTurnIdle(preCfgID, 1, 15*time.Second); err != nil {
		t.Fatalf("WaitForTurnIdle(preCfg): %v", err)
	}
	foundM2 := false
	for _, req := range suiteMockOR.ResponsesRequests() {
		if strings.Contains(req.PromptText, "T511_PRETURN1_M2") && req.Model == unlistedSlug2 {
			foundM2 = true
		}
	}
	if !foundM2 {
		t.Fatalf("OpenRouter bridge did not receive model=%q for T511_PRETURN1_M2", unlistedSlug2)
	}

	// Part B (R4-D03): Prompted session.handoff retains SeedHandoff across failed Turn 1, keeps user bubble clean,
	// orders [notice, user, notice], and delivers SeedHandoff on retry (without repeating on Turn 2).
	var hoffSrc struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(ctx, "session.create", map[string]any{
		"agent": "codex",
		"cwd":   repo,
		"model": "google/gemini-3-flash-preview",
	}, &hoffSrc); err != nil {
		t.Fatalf("session.create(hoffSrc): %v", err)
	}
	hoffSrcID := hoffSrc.Session.ID
	_ = cli.Call(ctx, "session.subscribe", map[string]any{"id": hoffSrcID, "since": 0}, nil)
	if err := cli.Call(ctx, "session.prompt", map[string]any{
		"id":   hoffSrcID,
		"text": "Persist secret architecture token KRYPTON-R4-888",
	}, nil); err != nil {
		t.Fatalf("hoffSrc prompt: %v", err)
	}
	if _, _, err := cli.WaitForTurnIdle(hoffSrcID, 1, 12*time.Second); err != nil {
		t.Fatalf("hoffSrc idle: %v", err)
	}

	failPrompt := "Continue migration [error:transient 503]"
	var promptedHoff struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(ctx, "session.handoff", map[string]any{
		"id":          hoffSrcID,
		"targetAgent": "fakeacp",
		"cwd":         repo,
		"prompt":      failPrompt,
	}, &promptedHoff); err != nil {
		t.Fatalf("prompted session.handoff: %v", err)
	}
	hoffDstID := promptedHoff.Session.ID

	// Wait for failed Turn 1 to settle idle with LastError, then inspect snapshot item ordering & clean user bubble.
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		var rows []SessionSummaryWire
		if err := cli.Call(ctx, "sessions.list", map[string]any{}, &rows); err == nil {
			var matched bool
			for _, r := range rows {
				if r.ID == hoffDstID && r.State == "idle" && strings.Contains(r.Error, "transient 503") {
					matched = true
					break
				}
			}
			if matched {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
	}

	if err := cli.Call(ctx, "session.subscribe", map[string]any{"id": hoffDstID, "since": 0}, nil); err != nil {
		t.Fatalf("subscribe(hoffDst): %v", err)
	}
	_, failItems, err := cli.WaitForSession(hoffDstID, 5*time.Second, func(sum SessionSummaryWire, _ *SessionDetailWire, items []TranscriptItemWire) bool {
		return len(items) >= 3
	})
	if err != nil {
		t.Fatalf("WaitForSession(hoffDst failed Turn 1): %v", err)
	}
	if len(failItems) < 3 || failItems[0].Kind != "notice" || failItems[1].Kind != "user" {
		t.Fatalf("expected [notice, user, ...] on failed prompted handoff, got %+v", failItems)
	}
	if failItems[1].Text != failPrompt {
		t.Fatalf("prompted handoff polluted visible user bubble: got %q, want %q", failItems[1].Text, failPrompt)
	}

	if err := cli.Call(ctx, "session.prompt", map[string]any{
		"id":   hoffDstID,
		"text": "Retry handoff turn cleanly",
	}, nil); err != nil {
		t.Fatalf("retry prompt on hoffDst: %v", err)
	}
	_, retryItems, err := cli.WaitForTurnIdle(hoffDstID, 1, 12*time.Second)
	if err != nil {
		t.Fatalf("WaitForTurnIdle(hoffDst retry): %v", err)
	}
	if !strings.Contains(collectAgentText(retryItems), "KRYPTON-R4-888") {
		t.Fatalf("retry Turn 1 did not receive retained SeedHandoff KRYPTON-R4-888: %+v", retryItems)
	}

	// Part C (R4-D03): Generic ACP provider ("customacp") session.fork, session.openInTerminal, and session.rewind + session.reload.
	if err := cli.Call(ctx, "providers.upsert", map[string]any{
		"id":      "customacp",
		"name":    "Custom ACP Harness",
		"driver":  "acp",
		"enabled": true,
		"command": fakeacpBin,
	}, nil); err != nil {
		t.Fatalf("providers.upsert(customacp): %v", err)
	}
	var acpCreated struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(ctx, "session.create", map[string]any{
		"agent": "customacp",
		"cwd":   repo,
	}, &acpCreated); err != nil {
		t.Fatalf("session.create(customacp): %v", err)
	}
	customID := acpCreated.Session.ID
	_ = cli.Call(ctx, "session.subscribe", map[string]any{"id": customID, "since": 0}, nil)
	if err := cli.Call(ctx, "session.prompt", map[string]any{"id": customID, "text": "Turn 1 ALPHA-CUSTOM-111"}, nil); err != nil {
		t.Fatalf("customacp Turn 1: %v", err)
	}
	if _, _, err := cli.WaitForTurnIdle(customID, 1, 12*time.Second); err != nil {
		t.Fatalf("customacp Turn 1 idle: %v", err)
	}
	if err := cli.Call(ctx, "session.prompt", map[string]any{"id": customID, "text": "Turn 2 BETA-CUSTOM-222"}, nil); err != nil {
		t.Fatalf("customacp Turn 2: %v", err)
	}
	_, customItems, err := cli.WaitForTurnIdle(customID, 2, 12*time.Second)
	if err != nil {
		t.Fatalf("customacp Turn 2 idle: %v", err)
	}

	var forkedACP struct {
		Session SessionSummaryWire `json:"session"`
	}
	if err := cli.Call(ctx, "session.fork", map[string]any{"id": customID}, &forkedACP); err != nil {
		t.Fatalf("session.fork(customacp): %v", err)
	}
	if forkedACP.Session.Agent != "customacp" {
		t.Fatalf("session.fork(customacp) returned agent=%q, want customacp", forkedACP.Session.Agent)
	}

	var customTerm struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
		Name string `json:"name"`
	}
	if err := cli.Call(ctx, "session.openInTerminal", map[string]any{
		"id":   customID,
		"cols": 120,
		"rows": 30,
	}, &customTerm); err != nil {
		t.Fatalf("session.openInTerminal(customacp): %v", err)
	}
	defer func() {
		_ = cli.Call(ctx, "term.kill", map[string]any{"id": customTerm.ID}, nil)
	}()
	if customTerm.Kind != "shell" {
		t.Fatalf("session.openInTerminal(customacp) kind=%q, want shell", customTerm.Kind)
	}

	var firstUserItemID string
	for _, it := range customItems {
		if it.Kind == "user" {
			firstUserItemID = it.ID
			break
		}
	}
	if err := cli.Call(ctx, "session.rewind", map[string]any{
		"id":               customID,
		"itemId":           firstUserItemID,
		"conversationOnly": true,
	}, nil); err != nil {
		t.Fatalf("session.rewind(customacp): %v", err)
	}
	if err := cli.Call(ctx, "session.reload", map[string]any{"id": customID}, nil); err != nil {
		t.Fatalf("session.reload(customacp): %v", err)
	}
	cli2 := td.PairAndConnect(t, "T5-11-verify", "linux")
	if err := cli2.Call(ctx, "session.subscribe", map[string]any{"id": customID, "since": 0}, nil); err != nil {
		t.Fatalf("cli2 subscribe(customacp): %v", err)
	}
	_, verifyItems, err := cli2.WaitForSession(customID, 5*time.Second, func(_ SessionSummaryWire, _ *SessionDetailWire, items []TranscriptItemWire) bool {
		return len(items) >= 1
	})
	if err != nil {
		t.Fatalf("cli2 WaitForSession(customacp): %v", err)
	}
	for _, it := range verifyItems {
		if it.Kind == "user" || it.Kind == "agent" {
			t.Fatalf("session.reload after rewind resurrected item %+v", it)
		}
	}
}
