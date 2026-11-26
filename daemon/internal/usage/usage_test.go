package usage

import (
	"path/filepath"
	"testing"
	"time"
)

func TestQueryWindowHoursDaysAndAllTime(t *testing.T) {
	log := Open(filepath.Join(t.TempDir(), "usage.jsonl"))
	now := time.Now()
	log.Append(Record{Ts: now.Add(-2 * time.Hour).UnixMilli(), Agent: "agy", Model: "gemini", InputTokens: 100, OutputTokens: 50, CostUSD: 0.1})
	log.Append(Record{Ts: now.Add(-10 * time.Hour).UnixMilli(), Agent: "codex", Model: "gpt", InputTokens: 200, OutputTokens: 100, CostUSD: 0.2})
	log.Append(Record{Ts: now.Add(-120 * 24 * time.Hour).UnixMilli(), Agent: "claude", Model: "opus", InputTokens: 300, OutputTokens: 150, CostUSD: 0.3})
	log.Append(Record{Ts: now.Add(-250 * 24 * time.Hour).UnixMilli(), Agent: "claude", Model: "sonnet", InputTokens: 400, OutputTokens: 200, CostUSD: 0.4})

	if s5h := log.QueryWindow(0, 5, false); s5h.Turns != 1 || s5h.Hours != 5 {
		t.Fatalf("5h window = %+v", s5h)
	}
	if s12h := log.QueryWindow(0, 12, false); s12h.Turns != 2 || len(s12h.ByDay) == 0 {
		t.Fatalf("12h window = %+v", s12h)
	}
	if s180d := log.QueryWindow(180, 0, false); s180d.Turns != 3 || s180d.Days != 180 {
		t.Fatalf("180d window = %+v", s180d)
	}
	if sAll := log.QueryWindow(0, 0, true); sAll.Turns != 4 || !sAll.AllTime {
		t.Fatalf("allTime window = %+v", sAll)
	}
}
