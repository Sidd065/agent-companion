package core

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"agentc/internal/acp"
	"agentc/internal/agyproc"
	"agentc/internal/lsapi"
)

// agy reports its run state on the language server's agent state stream:
// status RUNNING while it works, including turns it starts by itself when a
// background task or subagent reports back after a turn ended; IDLE when a
// turn ends; and fullyIdle once no background work is left either. ACP has
// no equivalent, so this is how agentc knows agy is busy without a prompt.
type agyRunState struct {
	known     bool // the stream is connected and has reported a status
	running   bool
	fullyIdle bool // idle with no background tasks or subagents left
}

// watchAgyState follows the agent state stream for as long as p runs.
func (s *Session) watchAgyState(p *agyproc.Proc) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-p.Exited()
		cancel()
	}()
	backoff := time.Second
	for ctx.Err() == nil {
		port, err := p.LSPort(ctx)
		if err != nil {
			return
		}
		started := time.Now()
		err = lsapi.New(port).Stream(ctx, "StreamAgentStateUpdates",
			map[string]any{"conversationId": s.ID(), "subscriberId": "agentc-state"},
			func(msg json.RawMessage) error {
				s.onAgyState(msg)
				return nil
			})
		s.mu.Lock()
		s.agy.known = false
		s.mu.Unlock()
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Printf("session %s: agent state stream: %v", s.ID(), err)
		}
		if time.Since(started) > time.Minute {
			backoff = time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func (s *Session) onAgyState(msg json.RawMessage) {
	var r struct {
		Update struct {
			Status    string `json:"status"`
			FullyIdle *bool  `json:"fullyIdle"`
		} `json:"update"`
	}
	if json.Unmarshal(msg, &r) != nil || r.Update.Status == "" {
		return
	}
	running := r.Update.Status == "CASCADE_RUN_STATUS_RUNNING"
	s.mu.Lock()
	wasKnown, wasRunning := s.agy.known, s.agy.running
	s.agy.known, s.agy.running = true, running
	switch {
	case running:
		s.agy.fullyIdle = false
	case r.Update.FullyIdle != nil:
		s.agy.fullyIdle = *r.Update.FullyIdle
	case wasRunning:
		s.agy.fullyIdle = false // the turn ended with background work left
	}
	// agy started working without a prompt from us: show it as a turn.
	begin := running && (!wasKnown || !wasRunning) && s.inflight == 0 && !s.autoTurn && s.proc != nil
	if begin {
		s.beginAutoTurnLocked()
	}
	end := !running && s.autoTurn
	quiet, reason := false, "end_turn"
	if end {
		s.autoTurn = false
		if s.autoCancel {
			reason = "cancelled"
		}
		s.autoCancel = false
		// A wake-up that added nothing (agy looked and went back to sleep)
		// isn't worth a turn marker or a notification.
		quiet = len(s.tr.Items) == s.autoFrom && len(s.meta.Queue) == 0
		if quiet {
			s.inflight = max(0, s.inflight-1)
		}
	}
	s.mu.Unlock()
	if begin || quiet {
		s.emit()
	}
	if end && !quiet {
		s.finishPrompt("", &acp.PromptResult{StopReason: reason}, nil)
	}
}

// beginAutoTurnLocked marks a turn the agent started without a prompt.
func (s *Session) beginAutoTurnLocked() {
	s.autoTurn, s.autoCancel = true, false
	s.autoFrom = len(s.tr.Items)
	s.beginTurnLocked()
}

// agyStillRunning catches agy carrying straight on after a prompt returned
// (a background task finished during the turn): the stream never reported
// IDLE in between, so no transition starts a turn.
func (s *Session) agyStillRunning() {
	time.AfterFunc(2*time.Second, func() {
		s.mu.Lock()
		begin := s.agy.known && s.agy.running && s.inflight == 0 && !s.autoTurn && s.proc != nil
		if begin {
			s.beginAutoTurnLocked()
		}
		s.mu.Unlock()
		if begin {
			s.emit()
		}
	})
}

// agyBusyLocked reports background work agy is still doing after its turn
// ended (only known while the state stream is connected).
func (s *Session) agyBusyLocked() bool {
	return s.agy.known && !s.agy.fullyIdle
}
