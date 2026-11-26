package core

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"

	"agentc/internal/lsapi"
)

func TestUserPayloadText(t *testing.T) {
	text := "Keep going, and deploy when green"
	inner := append([]byte{0x12, byte(len(text))}, text...)
	inner = append(inner, 0x1a, 0x04, 0x0a, 0x02, 'h', 'i')
	msg := []byte{0x08, 0x0e, 0x20, 0x03, 0x2a, 0x02, 0x0a, 0x00}
	msg = append(msg, 0x9a, 0x01, byte(len(inner)))
	msg = append(msg, inner...)
	if got := userPayloadText(base64.StdEncoding.EncodeToString(msg)); got != text {
		t.Fatalf("got %q", got)
	}
	if got := userPayloadText(base64.RawStdEncoding.EncodeToString(msg)); got != text {
		t.Fatalf("unpadded: got %q", got)
	}
	for _, bad := range []string{"", "!!!", base64.StdEncoding.EncodeToString([]byte{0x9a, 0x01, 0x7f})} {
		if got := userPayloadText(bad); got != "" {
			t.Errorf("%q -> %q", bad, got)
		}
	}
}

func TestComputeLiveSubagentRealStates(t *testing.T) {
	agyDir := t.TempDir()
	parentID := "parent-conv"
	m := &Manager{
		cfg:         Config{AgyDataDir: agyDir, StateDir: agyDir, IdleHibernate: time.Minute},
		hub:         NewHub(),
		sessions:    map[string]*Session{},
		liveWatches: map[string]*liveWatch{},
		stopCh:      make(chan struct{}),
	}
	defer m.Shutdown()

	writeSub := func(ownerID, subID, role, rawState, lastJSONL string, spawn int) {
		subMetaDir := filepath.Join(agyDir, "brain", ownerID, ".system_generated", "subagents")
		_ = os.MkdirAll(subMetaDir, 0o700)
		meta := `{"conversationId":"` + subID + `","subagentDescriptor":{"typeName":"research","role":"` + role + `"},"state":"` + rawState + `","spawnStepIndex":` + string(rune('0'+spawn)) + `}`
		_ = os.WriteFile(filepath.Join(subMetaDir, subID+".json"), []byte(meta), 0o600)
		if lastJSONL != "" {
			logDir := filepath.Join(agyDir, "brain", subID, ".system_generated", "logs")
			_ = os.MkdirAll(logDir, 0o700)
			_ = os.WriteFile(filepath.Join(logDir, "transcript.jsonl"), []byte(lastJSONL+"\n"), 0o600)
		}
	}

	// 1. Finished subagent whose descriptor still says SUBAGENT_STATE_ALIVE (Screenshot 2 bug!).
	writeSub(parentID, "sub-done", "T3 Docs Survey", "SUBAGENT_STATE_ALIVE",
		`{"step_index":12,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","content":"Finished survey."}`, 1)
	// 2. Actively executing subagent with in-flight tool_calls.
	writeSub(parentID, "sub-working", "Active Builder", "SUBAGENT_STATE_ALIVE",
		`{"step_index":4,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","tool_calls":[{"name":"run_command","args":{}}]}`, 2)
	// 3. Coordinator subagent whose child is also DONE.
	writeSub(parentID, "sub-team", "Teamwork Coordinator", "SUBAGENT_STATE_ALIVE",
		`{"step_index":99,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","content":"VICTORY CONFIRMED"}`, 3)
	writeSub("sub-team", "sub-leaf", "Leaf Worker", "SUBAGENT_STATE_ALIVE",
		`{"step_index":20,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","content":"Done"}`, 1)
	// 4. Killed subagent.
	writeSub(parentID, "sub-killed", "Stopped Helper", "SUBAGENT_STATE_KILLED", "", 4)

	// While parent agy process is live (non-nil ls client): sub-done & sub-team must be DONE, sub-working RUNNING, sub-killed STOPPED!
	dummyLS := lsapi.New(1) // port 1 fails RPCs gracefully while indicating ls != nil
	live, err := m.ComputeLive(context.Background(), parentID, dummyLS)
	if err != nil {
		t.Fatalf("ComputeLive: %v", err)
	}
	got := map[string]string{}
	for _, s := range live.Subagents {
		got[s.ID] = s.State
	}
	if got["sub-done"] != "DONE" || got["sub-working"] != "RUNNING" || got["sub-team"] != "DONE" || got["sub-killed"] != "STOPPED" {
		t.Fatalf("while parent active: got subagent states %+v", got)
	}

	// Once parent sleeps (ls == nil), even sub-working is no longer running -> DONE.
	liveIdle, err := m.ComputeLive(context.Background(), parentID, nil)
	if err != nil {
		t.Fatalf("ComputeLive idle: %v", err)
	}
	for _, s := range liveIdle.Subagents {
		if s.ID == "sub-working" && s.State != "DONE" {
			t.Fatalf("expected sleeping parent sub-working to be DONE, got %q", s.State)
		}
	}
}
