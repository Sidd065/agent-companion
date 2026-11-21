// Package scheduler manages recurring (5-field cron, interval, or one-shot)
// scheduled agent tasks persisted to schedules.json with delivery history.
package scheduler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Task is a scheduled prompt definition.
type Task struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Enabled       bool   `json:"enabled"`
	Kind          string `json:"kind"` // cron | interval | once
	Cron          string `json:"cron,omitempty"`
	IntervalSec   int    `json:"intervalSec,omitempty"`
	RunAt         int64  `json:"runAt,omitempty"` // unix ms for kind=once
	ProjectID     string `json:"projectId,omitempty"`
	Cwd           string `json:"cwd"`
	Agent         string `json:"agent"`
	Model         string `json:"model,omitempty"`
	Mode          string `json:"mode,omitempty"`
	Prompt        string `json:"prompt"`
	UseWorktree   bool   `json:"useWorktree,omitempty"`
	NextRunAt     int64  `json:"nextRunAt,omitempty"`
	LastRunAt     int64  `json:"lastRunAt,omitempty"`
	LastStatus    string `json:"lastStatus,omitempty"` // ok | error | running
	LastSessionID string `json:"lastSessionId,omitempty"`
	LastError     string `json:"lastError,omitempty"`
	CreatedAt     int64  `json:"createdAt"`
	UpdatedAt     int64  `json:"updatedAt"`
}

// RunRecord records one execution of a scheduled task.
type RunRecord struct {
	ID         string `json:"id"`
	TaskID     string `json:"taskId"`
	TaskName   string `json:"taskName"`
	SessionID  string `json:"sessionId,omitempty"`
	Status     string `json:"status"` // running | ok | error
	StartedAt  int64  `json:"startedAt"`
	FinishedAt int64  `json:"finishedAt,omitempty"`
	Error      string `json:"error,omitempty"`
	Summary    string `json:"summary,omitempty"`
}

// Runner executes a scheduled task and returns the created session ID and optional summary.
type Runner func(ctx context.Context, t Task) (sessionID string, summary string, err error)

type persisted struct {
	Tasks []Task      `json:"tasks"`
	Runs  []RunRecord `json:"runs"`
}

// Engine runs the background schedule loop and persists tasks + history.
type Engine struct {
	path     string
	runner   Runner
	onChange func()
	mu       sync.Mutex
	tasks    map[string]*Task
	runs     []RunRecord
	stopCh   chan struct{}
	once     sync.Once
}

// Open loads schedules from path and prepares the engine.
func Open(path string, runner Runner, onChange func()) (*Engine, error) {
	e := &Engine{
		path:     path,
		runner:   runner,
		onChange: onChange,
		tasks:    map[string]*Task{},
		stopCh:   make(chan struct{}),
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return e, nil
	}
	if err != nil {
		return nil, err
	}
	var p persisted
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("schedules %s: %w", path, err)
	}
	now := time.Now()
	for _, t := range p.Tasks {
		tc := t
		if tc.Enabled && tc.NextRunAt == 0 {
			if nxt, err := NextFire(tc, now); err == nil && !nxt.IsZero() {
				tc.NextRunAt = nxt.UnixMilli()
			}
		}
		e.tasks[tc.ID] = &tc
	}
	e.runs = p.Runs
	return e, nil
}

// Start begins ticking every 15s to fire due tasks.
func (e *Engine) Start() {
	go func() {
		tick := time.NewTicker(15 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-e.stopCh:
				return
			case now := <-tick.C:
				e.checkDue(now)
			}
		}
	}()
}

// Stop halts the scheduler loop.
func (e *Engine) Stop() {
	e.once.Do(func() { close(e.stopCh) })
}

func newID(prefix string) string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// List returns all tasks and recent run records.
func (e *Engine) List() ([]Task, []RunRecord) {
	e.mu.Lock()
	defer e.mu.Unlock()
	tasks := make([]Task, 0, len(e.tasks))
	for _, t := range e.tasks {
		tasks = append(tasks, *t)
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].CreatedAt < tasks[j].CreatedAt })
	runs := append([]RunRecord(nil), e.runs...)
	return tasks, runs
}

// Upsert creates or updates a scheduled task.
func (e *Engine) Upsert(in Task) (Task, error) {
	if strings.TrimSpace(in.Name) == "" {
		return Task{}, errors.New("task name is required")
	}
	if strings.TrimSpace(in.Prompt) == "" {
		return Task{}, errors.New("prompt is required")
	}
	if strings.TrimSpace(in.Cwd) == "" {
		return Task{}, errors.New("working directory is required")
	}
	if in.Kind == "" {
		in.Kind = "interval"
	}
	now := time.Now()
	if in.Enabled {
		nxt, err := NextFire(in, now)
		if err != nil {
			return Task{}, err
		}
		if !nxt.IsZero() {
			in.NextRunAt = nxt.UnixMilli()
		}
	} else {
		in.NextRunAt = 0
	}

	e.mu.Lock()
	if in.ID == "" {
		in.ID = newID("sched-")
		in.CreatedAt = now.UnixMilli()
	} else if prev, ok := e.tasks[in.ID]; ok {
		in.CreatedAt = prev.CreatedAt
		in.LastRunAt = prev.LastRunAt
		in.LastStatus = prev.LastStatus
		in.LastSessionID = prev.LastSessionID
		in.LastError = prev.LastError
	} else {
		in.CreatedAt = now.UnixMilli()
	}
	in.UpdatedAt = now.UnixMilli()
	cp := in
	e.tasks[cp.ID] = &cp
	err := e.saveLocked()
	e.mu.Unlock()
	if err == nil && e.onChange != nil {
		e.onChange()
	}
	return cp, err
}

// Delete removes a task by ID.
func (e *Engine) Delete(id string) (bool, error) {
	e.mu.Lock()
	if _, ok := e.tasks[id]; !ok {
		e.mu.Unlock()
		return false, nil
	}
	delete(e.tasks, id)
	err := e.saveLocked()
	e.mu.Unlock()
	if err == nil && e.onChange != nil {
		e.onChange()
	}
	return true, err
}

// RunNow triggers a task immediately in the background.
func (e *Engine) RunNow(id string) (RunRecord, error) {
	e.mu.Lock()
	t, ok := e.tasks[id]
	if !ok {
		e.mu.Unlock()
		return RunRecord{}, errors.New("scheduled task not found")
	}
	tc := *t
	e.mu.Unlock()
	return e.fire(tc), nil
}

func (e *Engine) checkDue(now time.Time) {
	nowMs := now.UnixMilli()
	var due []Task
	e.mu.Lock()
	for _, t := range e.tasks {
		if !t.Enabled || t.NextRunAt == 0 || t.NextRunAt > nowMs || t.LastStatus == "running" {
			continue
		}
		due = append(due, *t)
		if t.Kind == "once" {
			t.Enabled = false
			t.NextRunAt = 0
		} else if nxt, err := NextFire(*t, now.Add(time.Second)); err == nil && !nxt.IsZero() {
			t.NextRunAt = nxt.UnixMilli()
		}
	}
	if len(due) > 0 {
		_ = e.saveLocked()
	}
	e.mu.Unlock()
	for _, t := range due {
		e.fire(t)
	}
}

func (e *Engine) fire(t Task) RunRecord {
	rec := RunRecord{
		ID:        newID("run-"),
		TaskID:    t.ID,
		TaskName:  t.Name,
		Status:    "running",
		StartedAt: time.Now().UnixMilli(),
	}
	e.mu.Lock()
	if cur, ok := e.tasks[t.ID]; ok {
		cur.LastRunAt = rec.StartedAt
		cur.LastStatus = "running"
		cur.LastError = ""
	}
	e.runs = append([]RunRecord{rec}, e.runs...)
	if len(e.runs) > 100 {
		e.runs = e.runs[:100]
	}
	_ = e.saveLocked()
	e.mu.Unlock()
	if e.onChange != nil {
		e.onChange()
	}

	go func() {
		if e.runner == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		sid, summary, err := e.runner(ctx, t)
		finished := time.Now().UnixMilli()
		e.mu.Lock()
		status := "ok"
		errMsg := ""
		if err != nil {
			status = "error"
			errMsg = err.Error()
			log.Printf("scheduler task %s (%s) failed: %v", t.ID, t.Name, err)
		}
		if cur, ok := e.tasks[t.ID]; ok {
			cur.LastStatus = status
			cur.LastError = errMsg
			if sid != "" {
				cur.LastSessionID = sid
			}
		}
		for i := range e.runs {
			if e.runs[i].ID == rec.ID {
				e.runs[i].Status = status
				e.runs[i].FinishedAt = finished
				e.runs[i].SessionID = sid
				e.runs[i].Summary = summary
				e.runs[i].Error = errMsg
				break
			}
		}
		_ = e.saveLocked()
		e.mu.Unlock()
		if e.onChange != nil {
			e.onChange()
		}
	}()
	return rec
}

func (e *Engine) saveLocked() error {
	if e.path == "" {
		return nil
	}
	tasks := make([]Task, 0, len(e.tasks))
	for _, t := range e.tasks {
		tasks = append(tasks, *t)
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].CreatedAt < tasks[j].CreatedAt })
	b, err := json.MarshalIndent(persisted{Tasks: tasks, Runs: e.runs}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(e.path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(e.path), filepath.Base(e.path)+".tmp*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err = f.Write(b); err == nil {
		err = f.Close()
	} else {
		_ = f.Close()
	}
	if err == nil {
		_ = os.Chmod(tmp, 0o600)
		err = os.Rename(tmp, e.path)
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}

// NextFire calculates the next trigger time for t strictly after from.
func NextFire(t Task, from time.Time) (time.Time, error) {
	switch t.Kind {
	case "once":
		if t.RunAt <= 0 {
			return time.Time{}, errors.New("runAt timestamp is required for one-shot tasks")
		}
		at := time.UnixMilli(t.RunAt)
		if at.Before(from) {
			return time.Time{}, nil
		}
		return at, nil
	case "interval":
		if t.IntervalSec < 30 {
			return time.Time{}, errors.New("interval must be at least 30 seconds")
		}
		return from.Add(time.Duration(t.IntervalSec) * time.Second), nil
	case "cron":
		return nextCron(t.Cron, from)
	default:
		return time.Time{}, fmt.Errorf("unknown schedule kind %q", t.Kind)
	}
}

func nextCron(expr string, from time.Time) (time.Time, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return time.Time{}, errors.New("cron expression must have 5 fields: min hour dom month dow")
	}
	mins, err := parseCronField(fields[0], 0, 59)
	if err != nil {
		return time.Time{}, fmt.Errorf("minute: %w", err)
	}
	hours, err := parseCronField(fields[1], 0, 23)
	if err != nil {
		return time.Time{}, fmt.Errorf("hour: %w", err)
	}
	doms, err := parseCronField(fields[2], 1, 31)
	if err != nil {
		return time.Time{}, fmt.Errorf("day-of-month: %w", err)
	}
	months, err := parseCronField(fields[3], 1, 12)
	if err != nil {
		return time.Time{}, fmt.Errorf("month: %w", err)
	}
	dows, err := parseCronField(fields[4], 0, 6)
	if err != nil {
		return time.Time{}, fmt.Errorf("day-of-week: %w", err)
	}

	cur := time.Date(from.Year(), from.Month(), from.Day(), from.Hour(), from.Minute()+1, 0, 0, from.Location())
	limit := cur.Add(366 * 24 * time.Hour)
	for cur.Before(limit) {
		if !months[int(cur.Month())] {
			cur = time.Date(cur.Year(), cur.Month()+1, 1, 0, 0, 0, 0, cur.Location())
			continue
		}
		if !doms[cur.Day()] || !dows[int(cur.Weekday())] {
			cur = time.Date(cur.Year(), cur.Month(), cur.Day()+1, 0, 0, 0, 0, cur.Location())
			continue
		}
		if !hours[cur.Hour()] {
			cur = time.Date(cur.Year(), cur.Month(), cur.Day(), cur.Hour()+1, 0, 0, 0, cur.Location())
			continue
		}
		if !mins[cur.Minute()] {
			cur = cur.Add(time.Minute)
			continue
		}
		return cur, nil
	}
	return time.Time{}, errors.New("no matching cron fire time within 1 year")
}

func parseCronField(s string, minVal, maxVal int) (map[int]bool, error) {
	out := map[int]bool{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		step := 1
		if idx := strings.IndexByte(part, '/'); idx >= 0 {
			st, err := strconv.Atoi(part[idx+1:])
			if err != nil || st <= 0 {
				return nil, fmt.Errorf("bad step %q", part)
			}
			step = st
			part = part[:idx]
		}
		lo, hi := minVal, maxVal
		if part != "*" && part != "" {
			if idx := strings.IndexByte(part, '-'); idx >= 0 {
				a, err1 := strconv.Atoi(part[:idx])
				b, err2 := strconv.Atoi(part[idx+1:])
				if err1 != nil || err2 != nil || a < minVal || b > maxVal || a > b {
					return nil, fmt.Errorf("bad range %q", part)
				}
				lo, hi = a, b
			} else {
				v, err := strconv.Atoi(part)
				if err != nil {
					return nil, fmt.Errorf("bad value %q", part)
				}
				if maxVal == 6 && v == 7 {
					v = 0
				}
				if v < minVal || v > maxVal {
					return nil, fmt.Errorf("out of range %d", v)
				}
				lo, hi = v, v
			}
		}
		for v := lo; v <= hi; v += step {
			out[v] = true
		}
	}
	if len(out) == 0 {
		return nil, errors.New("empty cron field")
	}
	return out, nil
}
