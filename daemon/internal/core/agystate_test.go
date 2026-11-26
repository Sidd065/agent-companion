package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"sync"
	"net"
	"context"
	"time"

	"agentc/internal/acp"
)

type fakeProc struct{ exited chan struct{} }

func (f *fakeProc) ACP() *acp.Conn          { return nil }
func (f *fakeProc) Pid() int                { return 4242 }
func (f *fakeProc) Alive() bool             { return true }
func (f *fakeProc) Exited() <-chan struct{} { return f.exited }
func (f *fakeProc) ExitErr() error          { return nil }
func (f *fakeProc) Close(time.Duration)     {}
func (f *fakeProc) RSSKiB() int64           { return 0 }

func agyUpdate(status string, fullyIdle ...bool) json.RawMessage {
	u := map[string]any{"status": "CASCADE_RUN_STATUS_" + status}
	if len(fullyIdle) > 0 {
		u["fullyIdle"] = fullyIdle[0]
	}
	b, _ := json.Marshal(map[string]any{"update": u})
	return b
}

func newAgyTestSession(t *testing.T) *Session {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "sessions"), 0o700)
	m := &Manager{cfg: Config{StateDir: dir, IdleHibernate: time.Minute}, hub: NewHub(), sessions: map[string]*Session{},
		stopCh: make(chan struct{}), liveWatches: map[string]*liveWatch{}}
	t.Cleanup(m.Shutdown)
	s := newSession(m, SessionMeta{ID: "c1", Agent: "agy"})
	s.proc = &fakeProc{exited: make(chan struct{})}
	m.sessions["c1"] = s
	return s
}

func (s *Session) testState() (state string, turns, items int, busy bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stateLocked(), s.meta.Turns, len(s.tr.Items), s.agyBusyLocked()
}

func TestAgyAutoTurns(t *testing.T) {
	s := newAgyTestSession(t)

	// The stream connects after a turn left a background task running.
	s.onAgyState(agyUpdate("IDLE"))
	if st, _, _, _ := s.testState(); st != "idle" {
		t.Fatalf("connected idle: state %s", st)
	}

	// agy wakes up by itself when the task reports back: that's a turn.
	s.onAgyState(agyUpdate("RUNNING"))
	if st, _, _, busy := s.testState(); st != "running" || !busy {
		t.Fatalf("woke up: state %s busy %v", st, busy)
	}
	s.onUpdate(json.RawMessage(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"FINISHED"}}`))
	s.onAgyState(agyUpdate("RUNNING")) // still working: no second turn
	s.onAgyState(agyUpdate("IDLE", true))
	st, turns, items, busy := s.testState()
	if st != "idle" || turns != 1 || busy {
		t.Fatalf("after the turn: state %s turns %d busy %v", st, turns, busy)
	}
	if it := s.tr.Items[len(s.tr.Items)-1]; it.Kind != "turn" {
		t.Fatalf("no turn marker: last item %s", it.Kind)
	}

	// A wake-up that adds nothing leaves no trace.
	s.onAgyState(agyUpdate("RUNNING"))
	s.onAgyState(agyUpdate("IDLE", true))
	if st, n, after, _ := s.testState(); st != "idle" || n != 1 || after != items {
		t.Fatalf("quiet wake-up: state %s turns %d items %d→%d", st, n, items, after)
	}

	// Stopping agy's own turn ends it as cancelled.
	s.onAgyState(agyUpdate("RUNNING"))
	s.onUpdate(json.RawMessage(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"partial"}}`))
	s.mu.Lock()
	s.autoCancel = s.autoTurn // what Cancel does (it also notifies agy, which a fake can't take)
	s.mu.Unlock()
	s.onAgyState(agyUpdate("IDLE", true))
	last := s.tr.Items[len(s.tr.Items)-1]
	if last.Kind != "turn" || last.Turn == nil || last.Turn.StopReason != "cancelled" {
		t.Fatalf("cancelled turn marker: %+v", last)
	}

	// While a prompt runs, RUNNING is the prompt's own work.
	s.mu.Lock()
	s.beginTurnLocked()
	s.mu.Unlock()
	s.onAgyState(agyUpdate("RUNNING"))
	s.mu.Lock()
	auto := s.autoTurn
	s.mu.Unlock()
	if auto {
		t.Fatal("a prompt's own work started an extra turn")
	}
	// The prompt returns while a background task keeps running.
	s.finishPrompt("", &acp.PromptResult{StopReason: "end_turn"}, nil)
	s.onAgyState(agyUpdate("IDLE"))
	if _, _, _, busy := s.testState(); !busy {
		t.Fatal("background work after the turn should keep agy busy")
	}
	// Hibernation waits for it.
	s.mu.Lock()
	s.lastActivity = time.Now().Add(-time.Hour)
	s.mu.Unlock()
	if d := s.m.idleFor(s); d != 0 {
		t.Fatalf("idleFor with background work = %v, want 0", d)
	}
	s.onAgyState(agyUpdate("IDLE", true))
	if d := s.m.idleFor(s); d < 59*time.Minute {
		t.Fatalf("idleFor once fully idle = %v", d)
	}
}

// agy doesn't echo a prompt while it runs: its first output marks it delivered,
// so the phone stops saying "Sending…" while agy works on it.
func TestAgyPromptDelivered(t *testing.T) {
	s := newAgyTestSession(t)
	s.mu.Lock()
	it, op := s.tr.AddUser("plan the week", nil, "sent")
	s.queueOpsLocked(op)
	s.awaitAck = true
	s.beginTurnLocked()
	s.mu.Unlock()

	s.onUpdate(json.RawMessage(`{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"Thinking"}}`))
	if it.State != "delivered" {
		t.Fatalf("after agy's first output: state %q", it.State)
	}
	// A later echo of the same prompt (a reload) doesn't duplicate it.
	n := len(s.tr.Items)
	s.onUpdate(json.RawMessage(`{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"plan the week"}}`))
	if len(s.tr.Items) != n || it.State != "" {
		t.Fatalf("echo: items %d→%d state %q", n, len(s.tr.Items), it.State)
	}

	// A prompt that fails after delivery is marked failed.
	s.mu.Lock()
	it2, op := s.tr.AddUser("and the next", nil, "sent")
	s.queueOpsLocked(op)
	s.awaitAck = true
	s.beginTurnLocked()
	s.mu.Unlock()
	s.onUpdate(json.RawMessage(`{"sessionUpdate":"tool_call","toolCallId":"t1","title":"Read","status":"pending"}`))
	if it2.State != "delivered" {
		t.Fatalf("tool call: state %q", it2.State)
	}
	s.finishPrompt(it2.ID, &acp.PromptResult{}, os.ErrDeadlineExceeded)
	if it2.State != "failed" {
		t.Fatalf("failed prompt: state %q", it2.State)
	}
}

// A review request is settled once the user writes to agy after the artifact
// was last written, and stays settled across history reloads.
func TestReviewAnswered(t *testing.T) {
	s := newAgyTestSession(t)
	arts := []ArtifactInfo{
		{Name: "plan.md", RequestFeedback: true, Updated: 1000},
		{Name: "walkthrough.md", RequestFeedback: false, Updated: 500},
		{Name: "plan2.md", RequestFeedback: true, Updated: 3000},
	}
	s.mu.Lock()
	s.promptSentLocked()
	s.meta.LastPrompt = 2000
	s.mu.Unlock()
	markAnswered(arts, s.lastUserTs())
	if !arts[0].Answered || arts[1].Answered || arts[2].Answered {
		t.Fatalf("answered: %v %v %v", arts[0].Answered, arts[1].Answered, arts[2].Answered)
	}
	// A reload replays the history with fresh timestamps; that's no answer.
	s.mu.Lock()
	s.queueOpsLocked(s.tr.Reset())
	s.mu.Unlock()
	s.onUpdate(json.RawMessage(`{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"old prompt"}}`))
	if got := s.lastUserTs(); got != 2000 {
		t.Fatalf("after reload: last prompt %d", got)
	}
}

type errFakeProc struct {
	exited chan struct{}
	err    error
}

func (f *errFakeProc) ACP() *acp.Conn          { return nil }
func (f *errFakeProc) Pid() int                { return 5151 }
func (f *errFakeProc) Alive() bool             { return false }
func (f *errFakeProc) Exited() <-chan struct{} { return f.exited }
func (f *errFakeProc) ExitErr() error          { return f.err }
func (f *errFakeProc) Close(time.Duration)     {}
func (f *errFakeProc) RSSKiB() int64           { return 0 }

func TestAgyNotificationFilteringAndHaltedAlerts(t *testing.T) {
	s := newAgyTestSession(t)
	var kinds []string
	s.m.Sink = func(n Notification) {
		// Sink runs in a goroutine; use NotificationsSince below for deterministic ordering.
		_ = n
	}

	// 1. Foreground prompt spawns background work: turn ends while RUNNING -> IDLE (fullyIdle=false).
	s.mu.Lock()
	it, op := s.tr.AddUser("run multi-agent task", nil, "sent")
	s.queueOpsLocked(op)
	s.beginTurnLocked()
	s.mu.Unlock()
	s.onAgyState(agyUpdate("RUNNING"))
	s.onUpdate(json.RawMessage(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Spawned 3 subagents, working..."}}`))
	s.finishPrompt(it.ID, &acp.PromptResult{StopReason: "end_turn"}, nil)
	// Stream reports IDLE with background work still active (fullyIdle=false): should emit "progress", NOT "done"!
	s.onAgyState(agyUpdate("IDLE", false))

	// 2. Two intermediate subagent reports arrive; each writes progress text with fullyIdle=false.
	for _, msg := range []string{"Subagent 1 finished.", "Subagent 2 finished."} {
		s.onAgyState(agyUpdate("RUNNING"))
		s.onUpdate(json.RawMessage(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"` + msg + `"}}`))
		s.onAgyState(agyUpdate("IDLE", false))
	}

	// 3. Final subagent finishes and agy writes final report with fullyIdle=true -> emits ONE "done"!
	s.onAgyState(agyUpdate("RUNNING"))
	s.onUpdate(json.RawMessage(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"All 3 subagents complete!"}}`))
	s.onAgyState(agyUpdate("IDLE", true))

	for _, n := range s.m.NotificationsSince(0) {
		kinds = append(kinds, n.Kind)
	}
	want := []string{"progress", "progress", "progress", "done"}
	if len(kinds) != len(want) {
		t.Fatalf("got notification kinds %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("notification[%d] = %q, want %q (all=%v)", i, kinds[i], want[i], kinds)
		}
	}

	// 4. Vertex/API error message starting with warning symbol classifies as "error" (halted), not "done".
	prevSeq := s.m.NotificationsSince(0)[len(kinds)-1].Seq
	s.mu.Lock()
	itErr, opErr := s.tr.AddUser("test carbon", nil, "sent")
	s.queueOpsLocked(opErr)
	s.beginTurnLocked()
	s.mu.Unlock()
	s.onUpdate(json.RawMessage(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"⚠️ Permission 'aiplatform.endpoints.predict' denied"}}`))
	s.finishPrompt(itErr.ID, &acp.PromptResult{StopReason: "end_turn"}, nil)
	gotErr := s.m.NotificationsSince(prevSeq)
	if len(gotErr) != 1 || gotErr[0].Kind != "error" {
		t.Fatalf("expected halted error notification for ⚠️ message, got %+v", gotErr)
	}

	// 5. Unexpected process exit in watchProc emits "error".
	prevSeq = gotErr[0].Seq
	ep := &errFakeProc{exited: make(chan struct{}), err: os.ErrClosed}
	s.mu.Lock()
	s.proc = ep
	s.closing = false
	s.mu.Unlock()
	doneWatch := make(chan struct{})
	go func() {
		s.watchProc(ep)
		close(doneWatch)
	}()
	close(ep.exited)
	<-doneWatch
	gotCrash := s.m.NotificationsSince(prevSeq)
	if len(gotCrash) != 1 || gotCrash[0].Kind != "error" {
		t.Fatalf("expected crash error notification, got %+v", gotCrash)
	}
}

func TestAgyAutoAllowReadsAutoApprovesWithoutNotification(t *testing.T) {
	dir := t.TempDir()
	hub := NewHub()
	var got []Notification
	var mu sync.Mutex
	m := NewManager(Config{
		StateDir: filepath.Join(dir, "state"),
		HomeDir:  dir,
	}, hub)
	m.Sink = func(n Notification) {
		mu.Lock()
		got = append(got, n)
		mu.Unlock()
	}

	s := newSession(m, SessionMeta{ID: "s-read", Agent: "agy", Title: "Webmirror"})
	m.mu.Lock()
	m.sessions["s-read"] = s
	m.mu.Unlock()

	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	agentSide := acp.NewConn(c1, c1, nil)
	daemonSide := acp.NewConn(c2, c2, procHandler{s: s})

	resCh := make(chan map[string]any, 1)
	go func() {
		var out map[string]any
		_ = agentSide.Call(context.Background(), "session/request_permission", map[string]any{
			"toolCall": map[string]any{
				"toolCallId": "tc-1",
				"title":      "File ../.claude/projects/memory/foo.md",
				"kind":       "read",
			},
			"options": []map[string]any{
				{"optionId": "allow-1", "name": "Allow once", "kind": "allow_once"},
				{"optionId": "deny-1", "name": "Deny", "kind": "reject_once"},
			},
		}, &out)
		resCh <- out
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s.Summary().Pending == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if s.Summary().Pending != 1 {
		t.Fatalf("expected 1 pending perm before auto-allow, got %d", s.Summary().Pending)
	}

	m.SetAgyAutoAllowReads(true)
	select {
	case out := <-resCh:
		outcome, _ := out["outcome"].(map[string]any)
		if outcome["optionId"] != "allow-1" {
			t.Fatalf("expected allow-1, got %#v", out)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for pending read permission to auto-resolve")
	}
	if s.Summary().Pending != 0 {
		t.Fatalf("expected 0 pending perms after SetAgyAutoAllowReads(true), got %d", s.Summary().Pending)
	}

	mu.Lock()
	got = nil
	mu.Unlock()
	var out2 map[string]any
	if err := agentSide.Call(context.Background(), "session/request_permission", map[string]any{
		"toolCall": map[string]any{
			"toolCallId": "tc-2",
			"title":      "File ../.claude/projects/memory/bar.md",
			"kind":       "read",
		},
		"options": []map[string]any{
			{"optionId": "allow-2", "name": "Allow once", "kind": "allow_once"},
			{"optionId": "deny-2", "name": "Deny", "kind": "reject_once"},
		},
	}, &out2); err != nil {
		t.Fatalf("second request_permission failed: %v", err)
	}
	outcome2, _ := out2["outcome"].(map[string]any)
	if outcome2["optionId"] != "allow-2" {
		t.Fatalf("expected allow-2, got %#v", out2)
	}
	mu.Lock()
	notifCount := len(got)
	mu.Unlock()
	if notifCount != 0 {
		t.Fatalf("expected 0 notifications for auto-allowed read, got %d", notifCount)
	}

	go func() {
		var out3 map[string]any
		_ = agentSide.Call(context.Background(), "session/request_permission", map[string]any{
			"toolCall": map[string]any{
				"toolCallId": "tc-3",
				"title":      "rm -rf /important",
				"kind":       "execute",
			},
			"options": []map[string]any{
				{"optionId": "allow-3", "name": "Allow once", "kind": "allow_once"},
				{"optionId": "deny-3", "name": "Deny", "kind": "reject_once"},
			},
		}, &out3)
	}()
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s.Summary().Pending == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if s.Summary().Pending != 1 {
		t.Fatalf("expected execute permission to remain pending, got %d", s.Summary().Pending)
	}
	_ = daemonSide
}
