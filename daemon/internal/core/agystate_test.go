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
