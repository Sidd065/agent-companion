package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"agentc/internal/acp"
	"agentc/internal/providers"
)

type stubLiveAgentProc struct {
	exited chan struct{}
}

func (p stubLiveAgentProc) ACP() *acp.Conn          { return nil }
func (p stubLiveAgentProc) Pid() int                { return 99999 }
func (p stubLiveAgentProc) Alive() bool             { return true }
func (p stubLiveAgentProc) Exited() <-chan struct{} { return p.exited }
func (p stubLiveAgentProc) ExitErr() error          { return nil }
func (p stubLiveAgentProc) Close(_ time.Duration)   {}
func (p stubLiveAgentProc) RSSKiB() int64           { return 0 }

func TestSessionFlushEmitStrictSeqAndIdleOrdering(t *testing.T) {
	root := nonTmpTestDir(t, "flush-emit-seq")
	stateDir := filepath.Join(root, "state")
	cfgDir := filepath.Join(root, "config")
	homeDir := filepath.Join(root, "home")
	workDir := filepath.Join(root, "work")
	for _, d := range []string{stateDir, cfgDir, homeDir, workDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	hub := NewHub()
	mgr := NewManager(Config{
		AgyPath:    "nonexistent-agy",
		ClaudePath: "nonexistent-claude",
		CodexPath:  "nonexistent-codex",
		StateDir:   stateDir,
		ConfigDir:  cfgDir,
		HomeDir:    homeDir,
	}, hub)
	defer mgr.Shutdown()

	type wireOp struct {
		Seq  int64  `json:"seq"`
		Op   string `json:"op"`
		ID   string `json:"id"`
		Text string `json:"text"`
		Item *struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
			Text string `json:"text"`
		} `json:"item"`
	}
	type wireFrame struct {
		Event string          `json:"event"`
		Data  json.RawMessage `json:"data"`
	}

	drainFrames := func(c *Client) []wireFrame {
		t.Helper()
		var out []wireFrame
		for {
			select {
			case b, ok := <-c.Outbox():
				if !ok {
					return out
				}
				var f wireFrame
				if err := json.Unmarshal(b, &f); err != nil {
					t.Fatalf("unmarshal frame: %v", err)
				}
				out = append(out, f)
			default:
				return out
			}
		}
	}

	// Simulate Desktop (transcript-reducer.ts) & Android (HostConnection.kt) monotonic-seq reducers:
	// any op with op.Seq <= seq is silently dropped without resyncing.
	verifyStrictClientStream := func(sid string, frames []wireFrame, initSeq int64, initItems map[string]string, requireIdleAfterOps bool) (map[string]string, bool) {
		t.Helper()
		seq := initSeq
		items := map[string]string{}
		for k, v := range initItems {
			items[k] = v
		}
		hasTurnItem := false
		sawIdle := false
		for idx, f := range frames {
			switch f.Event {
			case "session.snapshot":
				var snap struct {
					ID    string `json:"id"`
					Seq   int64  `json:"seq"`
					Items []struct {
						ID   string `json:"id"`
						Kind string `json:"kind"`
						Text string `json:"text"`
					} `json:"items"`
				}
				if err := json.Unmarshal(f.Data, &snap); err != nil {
					t.Fatalf("unmarshal snapshot: %v", err)
				}
				if snap.ID == sid {
					seq = snap.Seq
					items = map[string]string{}
					for _, it := range snap.Items {
						items[it.ID] = it.Text
						if it.Kind == "turn" {
							hasTurnItem = true
						}
					}
				}
			case "session.delta":
				var delta struct {
					ID  string   `json:"id"`
					Seq int64    `json:"seq"`
					Ops []wireOp `json:"ops"`
				}
				if err := json.Unmarshal(f.Data, &delta); err != nil {
					t.Fatalf("unmarshal delta: %v", err)
				}
				if delta.ID == sid {
					for _, op := range delta.Ops {
						if op.Seq > 0 && op.Seq <= seq {
							continue
						}
						seq = op.Seq
						if op.Op == "add" && op.Item != nil {
							items[op.Item.ID] = op.Item.Text
							if op.Item.Kind == "turn" {
								hasTurnItem = true
							}
						} else if op.Op == "append" {
							items[op.ID] += op.Text
						}
					}
					if delta.Seq > seq {
						seq = delta.Seq
					}
				}
			case "session.ops":
				var ev struct {
					ID  string   `json:"id"`
					Ops []wireOp `json:"ops"`
				}
				if err := json.Unmarshal(f.Data, &ev); err != nil {
					t.Fatalf("unmarshal ops: %v", err)
				}
				if ev.ID != sid {
					continue
				}
				if requireIdleAfterOps && sawIdle {
					t.Fatalf("frame %d delivered session.ops AFTER session.changed(state=idle)", idx)
				}
				for _, op := range ev.Ops {
					if op.Seq > 0 && op.Seq <= seq {
						t.Fatalf("Seq inversion on %s: op.Seq=%d <= prevSeq=%d (op=%+v)", sid, op.Seq, seq, op)
					}
					seq = op.Seq
					if op.Op == "add" && op.Item != nil {
						items[op.Item.ID] = op.Item.Text
						if op.Item.Kind == "turn" {
							hasTurnItem = true
						}
					} else if op.Op == "append" {
						items[op.ID] += op.Text
					}
				}
			case "session.changed":
				var sum Summary
				if err := json.Unmarshal(f.Data, &sum); err == nil && sum.ID == sid && sum.State == "idle" && sum.Turns >= 1 {
					sawIdle = true
				}
			}
		}
		if requireIdleAfterOps && !sawIdle {
			t.Fatalf("expected session.changed(state=idle) on %s", sid)
		}
		return items, hasTurnItem
	}

	// Part 1: High-contention concurrent flush() + emit() + Subscribe(0) + Subscribe(since) stress.
	for trial := 0; trial < 60; trial++ {
		sid := fmt.Sprintf("sess-race-%03d", trial)
		s := newSession(mgr, SessionMeta{ID: sid, Agent: "codex", Cwd: workDir, Title: "Flush Race"})
		s.proc = stubLiveAgentProc{exited: make(chan struct{})}
		mgr.mu.Lock()
		mgr.sessions[sid] = s
		mgr.mu.Unlock()

		c1 := NewClient(fmt.Sprintf("c1-%d", trial))
		c2 := NewClient(fmt.Sprintf("c2-%d", trial))
		c3 := NewClient(fmt.Sprintf("c3-%d", trial))
		hub.Add(c1)
		hub.Add(c2)
		hub.Add(c3)

		s.Subscribe(c1, 0)
		initFrames := drainFrames(c1)
		initItems, _ := verifyStrictClientStream(sid, initFrames, 0, nil, false)
		s.mu.Lock()
		seq0 := s.tr.Seq
		s.beginTurnLocked()
		_, uOp := s.tr.AddUser("hello", nil, "delivered")
		s.queueOpsLocked(uOp)
		var wantBuilder strings.Builder
		for chunkIdx := 0; chunkIdx < 24; chunkIdx++ {
			part := fmt.Sprintf("[chunk-%02d:%s]", chunkIdx, strings.Repeat("x", 96))
			wantBuilder.WriteString(part)
			s.queueOpsLocked(s.tr.TextChunk("agent", part)...)
		}
		aID := s.tr.Items[len(s.tr.Items)-1].ID
		s.mu.Unlock()
		wantText := wantBuilder.String()

		startGate := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(4)
		go func() {
			defer wg.Done()
			<-startGate
			s.flush()
		}()
		go func() {
			defer wg.Done()
			<-startGate
			s.mu.Lock()
			s.inflight = 0
			s.meta.Turns = 1
			s.queueOpsLocked(s.tr.AddTurn(&Turn{TurnIndex: 1, StopReason: "end_turn"}))
			s.mu.Unlock()
			s.emit()
		}()
		go func() {
			defer wg.Done()
			<-startGate
			s.Subscribe(c2, 0)
		}()
		go func() {
			defer wg.Done()
			<-startGate
			s.Subscribe(c3, seq0)
		}()
		close(startGate)
		wg.Wait()

		s.mu.Lock()
		timerStillSet := s.flushTimer != nil
		s.mu.Unlock()
		if timerStillSet {
			t.Fatalf("trial %d: expected s.flushTimer stopped & cleared after emit()", trial)
		}

		items1, turn1 := verifyStrictClientStream(sid, drainFrames(c1), seq0, initItems, true)
		if items1[aID] != wantText || !turn1 {
			t.Fatalf("trial %d c1 dropped assistant text or turn (got len=%d want=%d turn=%v)", trial, len(items1[aID]), len(wantText), turn1)
		}
		items2, turn2 := verifyStrictClientStream(sid, drainFrames(c2), 0, nil, false)
		if items2[aID] != wantText || !turn2 {
			t.Fatalf("trial %d c2 dropped assistant text or turn (got len=%d want=%d turn=%v)", trial, len(items2[aID]), len(wantText), turn2)
		}
		items3, turn3 := verifyStrictClientStream(sid, drainFrames(c3), seq0, initItems, false)
		if items3[aID] != wantText || !turn3 {
			t.Fatalf("trial %d c3 delta dropped assistant text or turn (got len=%d want=%d turn=%v)", trial, len(items3[aID]), len(wantText), turn3)
		}

		hub.Remove(c1)
		hub.Remove(c2)
		hub.Remove(c3)
		c1.Close()
		c2.Close()
		c3.Close()
		s.mu.Lock()
		s.proc = nil
		s.mu.Unlock()
		s.stopTimers()
	}

	// Part 2: Real 60ms time.AfterFunc flushTimer boundary race.
	for trial := 0; trial < 8; trial++ {
		sid := fmt.Sprintf("sess-timer-%02d", trial)
		s := newSession(mgr, SessionMeta{ID: sid, Agent: "codex", Cwd: workDir, Title: "Timer Boundary"})
		s.proc = stubLiveAgentProc{exited: make(chan struct{})}
		mgr.mu.Lock()
		mgr.sessions[sid] = s
		mgr.mu.Unlock()

		c := NewClient(fmt.Sprintf("ct-%d", trial))
		hub.Add(c)
		s.Subscribe(c, 0)
		_ = drainFrames(c)

		s.mu.Lock()
		seq0 := s.tr.Seq
		s.beginTurnLocked()
		var want strings.Builder
		for i := 0; i < 16; i++ {
			chunk := fmt.Sprintf("part-%02d-%s;", i, strings.Repeat("z", 128))
			want.WriteString(chunk)
			s.queueOpsLocked(s.tr.TextChunk("agent", chunk)...)
		}
		aID := s.tr.Items[len(s.tr.Items)-1].ID
		s.mu.Unlock()

		time.Sleep(time.Duration(58+trial%4) * time.Millisecond)
		s.mu.Lock()
		s.inflight = 0
		s.meta.Turns = 1
		s.queueOpsLocked(s.tr.AddTurn(&Turn{TurnIndex: 1, StopReason: "end_turn"}))
		s.mu.Unlock()
		s.emit()
		time.Sleep(10 * time.Millisecond)

		items, hasTurn := verifyStrictClientStream(sid, drainFrames(c), seq0, nil, true)
		if items[aID] != want.String() || !hasTurn {
			t.Fatalf("timer trial %d dropped message or turn (got len=%d want=%d hasTurn=%v)", trial, len(items[aID]), want.Len(), hasTurn)
		}
		hub.Remove(c)
		c.Close()
		s.mu.Lock()
		s.proc = nil
		s.mu.Unlock()
		s.stopTimers()
	}
}

func TestPromptlessHandoffSeedsFirstTurnAcrossHarnesses(t *testing.T) {
	root := nonTmpTestDir(t, "promptless-handoff")
	codexBin, logPath := writeCoreFakeCodex(t, root)
	t.Setenv("FAKE_CODEX_CORE_LOG", logPath)
	t.Setenv("HOME", root)

	stateDir := filepath.Join(root, "state")
	cfgDir := filepath.Join(root, "config")
	workDir := filepath.Join(root, "work")
	for _, d := range []string{stateDir, cfgDir, workDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// Minimal generic ACP harness script recording received prompts and echoing whether ZEPHYR-9000 arrived.
	acpBin := filepath.Join(root, "fake-generic-acp.py")
	acpScript := `#!/usr/bin/env python3
import json, sys
turn_n = 0
for raw in sys.stdin:
    raw = raw.strip()
    if not raw:
        continue
    msg = json.loads(raw)
    rid = msg.get("id")
    method = msg.get("method", "")
    params = msg.get("params") or {}
    if method == "initialize":
        sys.stdout.write(json.dumps({"jsonrpc": "2.0", "id": rid, "result": {"protocolVersion": 1, "agentCapabilities": {"loadSession": True}}}) + "\n")
        sys.stdout.flush()
    elif method in ("session/new", "session/resume", "session/load"):
        sid = params.get("sessionId") or "fakeacp-1"
        sys.stdout.write(json.dumps({"jsonrpc": "2.0", "id": rid, "result": {"sessionId": sid}}) + "\n")
        sys.stdout.flush()
    elif method == "session/prompt":
        turn_n += 1
        blocks = params.get("prompt") or []
        txt = "".join(b.get("text", "") for b in blocks)
        has_zephyr = "ZEPHYR-9000" in txt
        reply = f"ACP_TURN_{turn_n}:HAS_ZEPHYR={has_zephyr}"
        sid = params.get("sessionId") or "fakeacp-1"
        sys.stdout.write(json.dumps({"jsonrpc": "2.0", "method": "session/update", "params": {"sessionId": sid, "update": {"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": reply}}}}) + "\n")
        sys.stdout.write(json.dumps({"jsonrpc": "2.0", "id": rid, "result": {"stopReason": "end_turn"}}) + "\n")
        sys.stdout.flush()
    elif rid is not None:
        sys.stdout.write(json.dumps({"jsonrpc": "2.0", "id": rid, "result": {}}) + "\n")
        sys.stdout.flush()
`
	if err := os.WriteFile(acpBin, []byte(acpScript), 0o755); err != nil {
		t.Fatal(err)
	}

	newMgr := func() *Manager {
		m := NewManager(Config{
			AgyPath:          "nonexistent-agy",
			ClaudePath:       "nonexistent-claude",
			CodexPath:        codexBin,
			OpenRouterAPIKey: "sk-or-v1-handoff-test",
			StateDir:         stateDir,
			ConfigDir:        cfgDir,
			WorktreeDir:      filepath.Join(root, "worktrees"),
			HomeDir:          root,
		}, NewHub())
		if _, err := m.Providers.Upsert(providers.ProviderConfig{
			ID:      "fakeacp",
			Name:    "Fake ACP",
			Driver:  "acp",
			Enabled: true,
			Command: acpBin,
		}); err != nil {
			t.Fatalf("Providers.Upsert: %v", err)
		}
		return m
	}

	mgr := newMgr()
	ctx := context.Background()

	waitIdleTurns := func(s *Session, wantTurns int) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			sum := s.Summary()
			if sum.State == "idle" && sum.Turns >= wantTurns {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for idle turns>=%d (got %+v)", wantTurns, s.Summary())
	}

	// Create source session containing HANDOFF_CODE:ZEPHYR-9000.
	src, err := mgr.Create(ctx, CreateParams{
		Agent: "codex",
		Cwd:   workDir,
		Title: "Source Zephyr Session",
		Flags: Flags{Model: "google/gemini-3-flash-preview", Mode: "full-access"},
	})
	if err != nil {
		t.Fatalf("Create src: %v", err)
	}
	if _, err := src.Prompt(PromptInput{Text: "Store token HANDOFF_CODE:ZEPHYR-9000"}); err != nil {
		t.Fatalf("Prompt src: %v", err)
	}
	waitIdleTurns(src, 1)

	// 1. Promptless Handoff to codex AND generic ACP (fakeacp), then restart Manager to verify disk persistence of SeedHandoff.
	nsCodex, handoffCodex, err := mgr.Handoff(ctx, src.ID(), "codex", "google/gemini-3-flash-preview", "full-access", workDir, "")
	if err != nil {
		t.Fatalf("Handoff(codex, empty prompt): %v", err)
	}
	if !strings.Contains(handoffCodex, "HANDOFF_CODE:ZEPHYR-9000") || nsCodex.meta.SeedHandoff == "" {
		t.Fatalf("expected non-empty SeedHandoff with ZEPHYR-9000 on codex handoff, got len=%d", len(nsCodex.meta.SeedHandoff))
	}

	nsACP, handoffACP, err := mgr.Handoff(ctx, src.ID(), "fakeacp", "", "", workDir, "   ")
	if err != nil {
		t.Fatalf("Handoff(fakeacp, whitespace prompt): %v", err)
	}
	if !strings.Contains(handoffACP, "HANDOFF_CODE:ZEPHYR-9000") || nsACP.meta.SeedHandoff == "" {
		t.Fatalf("expected non-empty SeedHandoff with ZEPHYR-9000 on fakeacp handoff, got len=%d", len(nsACP.meta.SeedHandoff))
	}

	codexID := nsCodex.ID()
	acpID := nsACP.ID()
	mgr.Shutdown()

	// Reload from disk and run Turn 1 + Turn 2 on both sessions.
	mgr2 := newMgr()
	defer mgr2.Shutdown()

	reloadedCodex, err := mgr2.Open(ctx, codexID)
	if err != nil {
		t.Fatalf("Open(codexID) after restart: %v", err)
	}
	if _, err := reloadedCodex.Prompt(PromptInput{Text: "RECALL"}); err != nil {
		t.Fatalf("Prompt codex Turn 1: %v", err)
	}
	waitIdleTurns(reloadedCodex, 1)
	if got := reloadedCodex.Summary().LastText; !strings.Contains(got, "ZEPHYR-9000") {
		t.Fatalf("codex Turn 1 after promptless handoff missed ZEPHYR-9000: got %q", got)
	}
	reloadedCodex.mu.Lock()
	codexSeedAfterT1 := reloadedCodex.meta.SeedHandoff
	reloadedCodex.mu.Unlock()
	if codexSeedAfterT1 != "" {
		t.Fatalf("expected codex SeedHandoff cleared after Turn 1, got len=%d", len(codexSeedAfterT1))
	}

	reloadedACP, err := mgr2.Open(ctx, acpID)
	if err != nil {
		t.Fatalf("Open(acpID) after restart: %v", err)
	}
	if _, err := reloadedACP.Prompt(PromptInput{Text: "First turn on ACP"}); err != nil {
		t.Fatalf("Prompt fakeacp Turn 1: %v", err)
	}
	waitIdleTurns(reloadedACP, 1)
	if got := reloadedACP.Summary().LastText; !strings.Contains(got, "ACP_TURN_1:HAS_ZEPHYR=True") {
		t.Fatalf("fakeacp Turn 1 after promptless handoff missed SeedHandoff: got %q", got)
	}

	// Turn 2 on fakeacp MUST NOT re-inject SeedHandoff.
	if _, err := reloadedACP.Prompt(PromptInput{Text: "Second turn on ACP"}); err != nil {
		t.Fatalf("Prompt fakeacp Turn 2: %v", err)
	}
	waitIdleTurns(reloadedACP, 2)
	if got := reloadedACP.Summary().LastText; !strings.Contains(got, "ACP_TURN_2:HAS_ZEPHYR=False") {
		t.Fatalf("fakeacp Turn 2 unexpectedly re-injected SeedHandoff: got %q", got)
	}
}

func TestPromptedHandoffFailureRetainsSeedAndGenericACPSessionOps(t *testing.T) {
	root := nonTmpTestDir(t, "r4-prompted-handoff-acp")
	codexBin, logPath := writeCoreFakeCodex(t, root)
	t.Setenv("FAKE_CODEX_CORE_LOG", logPath)
	t.Setenv("HOME", root)

	stateDir := filepath.Join(root, "state")
	cfgDir := filepath.Join(root, "config")
	workDir := filepath.Join(root, "work")
	otherDir := filepath.Join(root, "other-work")
	for _, d := range []string{stateDir, cfgDir, workDir, otherDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	acpStatePath := filepath.Join(root, "custom-acp-state.json")
	acpBin := filepath.Join(root, "custom-acp.py")
	acpScript := `#!/usr/bin/env python3
import json, os, sys
state_path = os.environ["CUSTOM_ACP_STATE_PATH"]
def load_st():
    if os.path.exists(state_path):
        with open(state_path, "r") as f:
            return json.load(f)
    return {"seq": 0, "sessions": {}}
def save_st(st):
    with open(state_path, "w") as f:
        json.dump(st, f)
cur_sid = ""
for raw in sys.stdin:
    raw = raw.strip()
    if not raw:
        continue
    msg = json.loads(raw)
    rid = msg.get("id")
    method = msg.get("method", "")
    params = msg.get("params") or {}
    st = load_st()
    if method == "initialize":
        sys.stdout.write(json.dumps({"jsonrpc": "2.0", "id": rid, "result": {"protocolVersion": 1, "agentCapabilities": {"loadSession": True}}}) + "\n")
        sys.stdout.flush()
    elif method == "session/new":
        st["seq"] += 1
        cur_sid = "acp-sess-" + str(st["seq"])
        st["sessions"][cur_sid] = []
        save_st(st)
        sys.stdout.write(json.dumps({"jsonrpc": "2.0", "id": rid, "result": {"sessionId": cur_sid}}) + "\n")
        sys.stdout.flush()
    elif method in ("session/resume", "session/load"):
        cur_sid = params.get("sessionId") or "acp-default"
        turns = st["sessions"].get(cur_sid, [])
        if method == "session/load":
            for u_txt, a_txt in turns:
                sys.stdout.write(json.dumps({"jsonrpc": "2.0", "method": "session/update", "params": {"sessionId": cur_sid, "update": {"sessionUpdate": "user_message_chunk", "content": {"type": "text", "text": u_txt}}}}) + "\n")
                sys.stdout.write(json.dumps({"jsonrpc": "2.0", "method": "session/update", "params": {"sessionId": cur_sid, "update": {"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": a_txt}}}}) + "\n")
            sys.stdout.flush()
        sys.stdout.write(json.dumps({"jsonrpc": "2.0", "id": rid, "result": {"sessionId": cur_sid}}) + "\n")
        sys.stdout.flush()
    elif method == "session/prompt":
        blocks = params.get("prompt") or []
        txt = "".join(b.get("text", "") for b in blocks)
        if "[fail-transient]" in txt:
            sys.stdout.write(json.dumps({"jsonrpc": "2.0", "id": rid, "error": {"code": -32000, "message": "transient 503 overload"}}) + "\n")
            sys.stdout.flush()
            continue
        has_alpha = "ALPHA-SECRET-111" in txt
        has_beta = "BETA-SECRET-222" in txt
        reply = f"CUSTOMACP_OK sid={cur_sid} alpha={has_alpha} beta={has_beta}"
        st["sessions"].setdefault(cur_sid, []).append((txt, reply))
        save_st(st)
        sys.stdout.write(json.dumps({"jsonrpc": "2.0", "method": "session/update", "params": {"sessionId": cur_sid, "update": {"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": reply}}}}) + "\n")
        sys.stdout.write(json.dumps({"jsonrpc": "2.0", "id": rid, "result": {"stopReason": "end_turn"}}) + "\n")
        sys.stdout.flush()
    elif rid is not None:
        sys.stdout.write(json.dumps({"jsonrpc": "2.0", "id": rid, "result": {}}) + "\n")
        sys.stdout.flush()
`
	t.Setenv("CUSTOM_ACP_STATE_PATH", acpStatePath)
	if err := os.WriteFile(acpBin, []byte(acpScript), 0o755); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(Config{
		AgyPath:          "nonexistent-agy",
		ClaudePath:       "nonexistent-claude",
		CodexPath:        codexBin,
		OpenRouterAPIKey: "sk-or-v1-r4-handoff",
		StateDir:         stateDir,
		ConfigDir:        cfgDir,
		WorktreeDir:      filepath.Join(root, "worktrees"),
		HomeDir:          root,
	}, NewHub())
	defer mgr.Shutdown()

	if _, err := mgr.Providers.Upsert(providers.ProviderConfig{
		ID:      "customacp",
		Name:    "Custom ACP",
		Driver:  "acp",
		Enabled: true,
		Command: acpBin,
	}); err != nil {
		t.Fatalf("Providers.Upsert: %v", err)
	}

	ctx := context.Background()
	waitIdle := func(s *Session, cond func(Summary) bool) Summary {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			sum := s.Summary()
			if sum.State == "idle" && cond(sum) {
				return sum
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for session condition: %+v", s.Summary())
		return Summary{}
	}

	// Build 2-turn customacp source session.
	src, err := mgr.Create(ctx, CreateParams{
		Agent: "customacp",
		Cwd:   workDir,
		Flags: Flags{AgentName: "sec-reviewer", DisableTelemetry: true},
	})
	if err != nil {
		t.Fatalf("Create(customacp): %v", err)
	}
	src.mu.Lock()
	src.meta.WorktreePath = filepath.Join(root, "fake-wt")
	src.meta.Branch = "feat/src-branch"
	src.mu.Unlock()

	if _, err := src.Prompt(PromptInput{Text: "Turn 1 ALPHA-SECRET-111"}); err != nil {
		t.Fatalf("src Turn 1: %v", err)
	}
	waitIdle(src, func(sum Summary) bool { return sum.Turns >= 1 })
	if _, err := src.Prompt(PromptInput{Text: "Turn 2 BETA-SECRET-222"}); err != nil {
		t.Fatalf("src Turn 2: %v", err)
	}
	waitIdle(src, func(sum Summary) bool { return sum.Turns >= 2 })

	// Part A: Prompted Manager.Handoff where Turn 1 fails -> SeedHandoff retained, clean bubble, [notice, user, notice], cross-cwd worktree cleared.
	userInstruction := "Run migration [fail-transient]"
	ns, handoffText, err := mgr.Handoff(ctx, src.ID(), "customacp", "", "", otherDir, userInstruction)
	if err != nil {
		t.Fatalf("Manager.Handoff(prompted): %v", err)
	}
	failSum := waitIdle(ns, func(sum Summary) bool { return strings.Contains(sum.Error, "transient 503") })
	if failSum.Turns != 0 {
		t.Fatalf("expected Turns=0 after failed Turn 1, got %d", failSum.Turns)
	}
	if failSum.WorktreePath != "" || failSum.Branch != "" {
		t.Fatalf("cross-cwd handoff leaked source worktree/branch: wt=%q branch=%q", failSum.WorktreePath, failSum.Branch)
	}

	ns.mu.Lock()
	retainedSeed := ns.meta.SeedHandoff
	var kinds []string
	var firstUserText string
	for _, it := range ns.tr.Items {
		kinds = append(kinds, it.Kind)
		if it.Kind == "user" && firstUserText == "" {
			firstUserText = it.Text
		}
	}
	ns.mu.Unlock()

	if retainedSeed != handoffText || !strings.Contains(retainedSeed, "ALPHA-SECRET-111") {
		t.Fatalf("SeedHandoff lost after failed prompted Turn 1: len=%d", len(retainedSeed))
	}
	if len(kinds) < 3 || kinds[0] != "notice" || kinds[1] != "user" || kinds[2] != "notice" {
		t.Fatalf("expected item order [notice user notice], got %v", kinds)
	}
	if firstUserText != userInstruction {
		t.Fatalf("user bubble polluted with handoff markdown: got %q, want %q", firstUserText, userInstruction)
	}

	// Retry Turn 1 cleanly -> delivers retained SeedHandoff and then clears it before Turn 2.
	if _, err := ns.Prompt(PromptInput{Text: "Retry migration cleanly"}); err != nil {
		t.Fatalf("ns retry Prompt: %v", err)
	}
	waitIdle(ns, func(sum Summary) bool { return sum.Turns >= 1 })
	ns.mu.Lock()
	seedAfterSuccess := ns.meta.SeedHandoff
	var retryAgentReply string
	for _, it := range ns.tr.Items {
		if it.Kind == "agent" {
			retryAgentReply = it.Text
		}
	}
	ns.mu.Unlock()
	if !strings.Contains(retryAgentReply, "alpha=True") {
		t.Fatalf("retry Turn 1 did not deliver retained SeedHandoff: %q", retryAgentReply)
	}
	if seedAfterSuccess != "" {
		t.Fatalf("SeedHandoff not cleared after successful retry: len=%d", len(seedAfterSuccess))
	}

	// Part B: Generic ACP Fork preserves Agent/AgentName/NoTelemetry, and Rewind + Reload does not resurrect rewound turns.
	forked, _, err := mgr.Fork(ctx, src.ID(), "")
	if err != nil {
		t.Fatalf("Fork(customacp): %v", err)
	}
	if forked.Summary().Agent != "customacp" {
		t.Fatalf("forked generic ACP session corrupted Agent to %q, want customacp", forked.Summary().Agent)
	}
	forked.mu.Lock()
	fAgentName, fNoTel := forked.meta.AgentName, forked.meta.NoTelemetry
	forked.mu.Unlock()
	if fAgentName != "sec-reviewer" || !fNoTel {
		t.Fatalf("forked generic ACP session lost AgentName/NoTelemetry: agentName=%q noTel=%v", fAgentName, fNoTel)
	}
	if _, err := forked.Prompt(PromptInput{Text: "Continue on forked customacp"}); err != nil {
		t.Fatalf("forked Prompt: %v", err)
	}
	waitIdle(forked, func(sum Summary) bool { return sum.Turns >= 3 })

	// Partial rewind at Turn 2 user item -> keeps Turn 1, sets fresh remote session + SeedHandoff with ALPHA only.
	var firstUserID, secondUserID string
	src.mu.Lock()
	for _, it := range src.tr.Items {
		if it.Kind == "user" {
			if firstUserID == "" {
				firstUserID = it.ID
			} else if secondUserID == "" {
				secondUserID = it.ID
			}
		}
	}
	src.mu.Unlock()

	if _, err := mgr.Rewind(ctx, src.ID(), secondUserID, true); err != nil {
		t.Fatalf("partial Rewind(customacp): %v", err)
	}
	if _, err := src.Prompt(PromptInput{Text: "Post-partial-rewind turn"}); err != nil {
		t.Fatalf("post-partial-rewind Prompt: %v", err)
	}
	waitIdle(src, func(sum Summary) bool { return sum.Turns >= 2 })
	src.mu.Lock()
	var postPartialReply string
	for _, it := range src.tr.Items {
		if it.Kind == "agent" {
			postPartialReply = it.Text
		}
	}
	src.mu.Unlock()
	if !strings.Contains(postPartialReply, "alpha=True") || strings.Contains(postPartialReply, "beta=True") {
		t.Fatalf("partial rewind expected alpha=True beta=False on fresh remote ACP session, got %q", postPartialReply)
	}

	// Rewind to Turn 1 (isFirst) + Reload -> must NOT resurrect old ACP turns!
	if _, err := mgr.Rewind(ctx, src.ID(), firstUserID, true); err != nil {
		t.Fatalf("isFirst Rewind(customacp): %v", err)
	}
	if err := mgr.Reload(ctx, src.ID()); err != nil {
		t.Fatalf("Reload after Rewind(customacp): %v", err)
	}
	src.mu.Lock()
	reloadedCount := len(src.tr.Items)
	var reloadedKinds []string
	for _, it := range src.tr.Items {
		reloadedKinds = append(reloadedKinds, it.Kind+":"+it.Text)
	}
	src.mu.Unlock()
	if reloadedCount != 1 || !strings.HasPrefix(reloadedKinds[0], "notice:Rewound conversation") {
		t.Fatalf("Reload resurrected rewound generic ACP turns: count=%d items=%v", reloadedCount, reloadedKinds)
	}
}
