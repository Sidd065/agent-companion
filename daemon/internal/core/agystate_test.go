package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
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
		liveWatches: map[string]*liveWatch{}}
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
