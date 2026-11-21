package usage

import (
	"path/filepath"
	"testing"
	"time"
)

func TestUsageAppendAndQuery(t *testing.T) {
	l := Open(filepath.Join(t.TempDir(), "usage.jsonl"))
	l.Append(Record{Ts: time.Now().UnixMilli(), SessionID: "s1", ProjectID: "p1", Agent: "claude", Model: "sonnet", InputTokens: 100, OutputTokens: 50, CostUSD: 0.01})
	l.Append(Record{Ts: time.Now().UnixMilli(), SessionID: "s2", ProjectID: "p1", Agent: "agy", Model: "gemini", InputTokens: 200, OutputTokens: 80, CostUSD: 0.02})
	sum := l.Query(7)
	if sum.Turns != 2 || sum.InputTokens != 300 || sum.OutputTokens != 130 || len(sum.ByAgent) != 2 {
		t.Fatalf("unexpected usage summary: %+v", sum)
	}
}
