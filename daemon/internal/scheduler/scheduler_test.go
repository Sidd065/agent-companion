package scheduler

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestCronParsingAndRunNow(t *testing.T) {
	base := time.Date(2026, 11, 21, 10, 15, 0, 0, time.UTC)
	nxt, err := NextFire(Task{Kind: "cron", Cron: "*/30 * * * *"}, base)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 11, 21, 10, 30, 0, 0, time.UTC)
	if !nxt.Equal(want) {
		t.Fatalf("got %v, want %v", nxt, want)
	}

	done := make(chan string, 1)
	eng, err := Open(filepath.Join(t.TempDir(), "schedules.json"), func(ctx context.Context, task Task) (string, string, error) {
		done <- task.Prompt
		return "sess-sched-1", "done", nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := eng.Upsert(Task{Name: "Nightly check", Enabled: true, Kind: "interval", IntervalSec: 3600, Cwd: "/tmp", Agent: "claude", Prompt: "check tests"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.RunNow(task.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case p := <-done:
		if p != "check tests" {
			t.Fatalf("unexpected prompt %q", p)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("scheduled runner did not fire")
	}
}
