// Package usage persists per-turn token and USD spend records (92-day local
// retention) and aggregates 24h / 7d / 30d / 90d usage dashboards.
package usage

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Record is one appended turn usage entry in usage.jsonl.
type Record struct {
	Ts                int64   `json:"ts"`
	SessionID         string  `json:"sessionId"`
	ProjectID         string  `json:"projectId,omitempty"`
	Agent             string  `json:"agent"`
	Model             string  `json:"model,omitempty"`
	InputTokens       int64   `json:"inputTokens"`
	OutputTokens      int64   `json:"outputTokens"`
	CachedReadTokens  int64   `json:"cachedReadTokens,omitempty"`
	CachedWriteTokens int64   `json:"cachedWriteTokens,omitempty"`
	ThoughtTokens     int64   `json:"thoughtTokens,omitempty"`
	TotalTokens       int64   `json:"totalTokens"`
	CostUSD           float64 `json:"costUsd"`
	DurationMs        int64   `json:"durationMs,omitempty"`
}

// Bucket aggregates token and cost metrics.
type Bucket struct {
	Key          string  `json:"key"`
	Turns        int     `json:"turns"`
	InputTokens  int64   `json:"inputTokens"`
	OutputTokens int64   `json:"outputTokens"`
	CachedTokens int64   `json:"cachedTokens"`
	TotalTokens  int64   `json:"totalTokens"`
	CostUSD      float64 `json:"costUsd"`
}

// Summary is the response of `usage.summary`.
type Summary struct {
	Days         int      `json:"days"`
	Hours        int      `json:"hours,omitempty"`
	AllTime      bool     `json:"allTime,omitempty"`
	Turns        int      `json:"turns"`
	InputTokens  int64    `json:"inputTokens"`
	OutputTokens int64    `json:"outputTokens"`
	CachedTokens int64    `json:"cachedTokens"`
	TotalTokens  int64    `json:"totalTokens"`
	CostUSD      float64  `json:"costUsd"`
	ByDay        []Bucket `json:"byDay"`
	ByAgent      []Bucket `json:"byAgent"`
	ByModel      []Bucket `json:"byModel"`
	ByProject    []Bucket `json:"byProject"`
}

// Log appends and queries local usage records.
type Log struct {
	path string
	mu   sync.Mutex
}

// Open opens a usage jsonl log at path.
func Open(path string) *Log {
	return &Log{path: path}
}

// Append writes one usage record to disk.
func (l *Log) Append(r Record) {
	if l == nil || l.path == "" {
		return
	}
	if r.Ts == 0 {
		r.Ts = time.Now().UnixMilli()
	}
	if r.TotalTokens == 0 {
		r.TotalTokens = r.InputTokens + r.OutputTokens + r.CachedReadTokens + r.CachedWriteTokens + r.ThoughtTokens
	}
	b, err := json.Marshal(r)
	if err != nil {
		return
	}
	b = append(b, '\n')
	l.mu.Lock()
	defer l.mu.Unlock()
	_ = os.MkdirAll(filepath.Dir(l.path), 0o700)
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	_, _ = f.Write(b)
	_ = f.Close()
}

// Query aggregates records over the last `days` days (default 30; <0 means all time).
func (l *Log) Query(days int) Summary {
	return l.QueryWindow(days, 0, days < 0)
}

// QueryWindow aggregates usage records over hours (`5h`, `12h`, `24h`), days (`7d`..`366d`), or all time.
func (l *Log) QueryWindow(days, hours int, allTime bool) Summary {
	res := Summary{ByDay: []Bucket{}, ByAgent: []Bucket{}, ByModel: []Bucket{}, ByProject: []Bucket{}}
	var cutoff int64
	hourlyBuckets := false
	switch {
	case allTime || days < 0:
		res.AllTime = true
		cutoff = 0
	case hours > 0:
		if hours > 366*24 {
			hours = 366 * 24
		}
		res.Hours = hours
		hourlyBuckets = hours <= 48
		cutoff = time.Now().Add(-time.Duration(hours) * time.Hour).UnixMilli()
	default:
		if days <= 0 {
			days = 30
		}
		if days > 366 {
			days = 366
		}
		res.Days = days
		cutoff = time.Now().Add(-time.Duration(days) * 24 * time.Hour).UnixMilli()
	}
	if l == nil || l.path == "" {
		return res
	}
	l.mu.Lock()
	f, err := os.Open(l.path)
	l.mu.Unlock()
	if err != nil {
		return res
	}
	defer f.Close()

	byDay := map[string]*Bucket{}
	byAgent := map[string]*Bucket{}
	byModel := map[string]*Bucket{}
	byProject := map[string]*Bucket{}
	addTo := func(m map[string]*Bucket, key string, r Record) {
		if key == "" {
			key = "other"
		}
		b := m[key]
		if b == nil {
			b = &Bucket{Key: key}
			m[key] = b
		}
		b.Turns++
		b.InputTokens += r.InputTokens
		b.OutputTokens += r.OutputTokens
		b.CachedTokens += r.CachedReadTokens + r.CachedWriteTokens
		b.TotalTokens += r.TotalTokens
		b.CostUSD += r.CostUSD
	}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var r Record
		if json.Unmarshal(sc.Bytes(), &r) != nil || r.Ts < cutoff {
			continue
		}
		res.Turns++
		res.InputTokens += r.InputTokens
		res.OutputTokens += r.OutputTokens
		res.CachedTokens += r.CachedReadTokens + r.CachedWriteTokens
		res.TotalTokens += r.TotalTokens
		res.CostUSD += r.CostUSD
		dayFmt := "2006-01-02"
		if hourlyBuckets {
			dayFmt = "2006-01-02 15:00"
		}
		day := time.UnixMilli(r.Ts).Format(dayFmt)
		addTo(byDay, day, r)
		addTo(byAgent, r.Agent, r)
		addTo(byModel, r.Model, r)
		addTo(byProject, r.ProjectID, r)
	}

	toSorted := func(m map[string]*Bucket, byKey bool) []Bucket {
		out := make([]Bucket, 0, len(m))
		for _, b := range m {
			out = append(out, *b)
		}
		sort.Slice(out, func(i, j int) bool {
			if byKey {
				return out[i].Key < out[j].Key
			}
			if out[i].CostUSD != out[j].CostUSD {
				return out[i].CostUSD > out[j].CostUSD
			}
			return out[i].TotalTokens > out[j].TotalTokens
		})
		return out
	}
	res.ByDay = toSorted(byDay, true)
	res.ByAgent = toSorted(byAgent, false)
	res.ByModel = toSorted(byModel, false)
	res.ByProject = toSorted(byProject, false)
	return res
}
