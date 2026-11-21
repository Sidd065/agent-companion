package core

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"agentc/internal/agyproc"
	"agentc/internal/lsapi"
)

// Live is what a conversation is doing beyond its transcript: background
// tasks (shell commands that outlived their tool call), artifacts, subagents
// and messages agents/tasks sent back. agy exposes these through its language
// server and its per-conversation "brain" directory rather than over ACP.
type Live struct {
	Tasks     []Task         `json:"tasks"`
	Artifacts []ArtifactInfo `json:"artifacts"`
	Subagents []Subagent     `json:"subagents"`
	Messages  []AgentMessage `json:"messages"`
	Hook      string         `json:"hook,omitempty"`
	Running   bool           `json:"running"` // agy process is up (tasks are only known then)
}

// Task is a background command.
type Task struct {
	ID          string `json:"id"`
	Tool        string `json:"tool"`
	Summary     string `json:"summary"`
	Description string `json:"description"`
	Start       int64  `json:"start"`
	Step        int    `json:"step"`
	Log         string `json:"log"`
	LogSize     int64  `json:"logSize"`
	Daemon      bool   `json:"daemon,omitempty"`
}

// ArtifactInfo is one artifact file in the conversation's brain directory.
type ArtifactInfo struct {
	Name            string `json:"name"`
	Path            string `json:"path"`
	URI             string `json:"uri"`
	Kind            string `json:"kind"` // markdown|image|video|html|text|file|url
	Summary         string `json:"summary,omitempty"`
	Size            int64  `json:"size"`
	Updated         int64  `json:"updated"`
	Version         int64  `json:"version,omitempty"`
	RequestFeedback bool   `json:"requestFeedback,omitempty"`
	UserFacing      bool   `json:"userFacing,omitempty"`
	Review          string `json:"review,omitempty"`
	URL             string `json:"url,omitempty"`
	// Answered: the user wrote to the agent after this version was written
	// (comments, Proceed or any message), so the review request is settled.
	Answered bool `json:"answered,omitempty"`
}

// Subagent is a child agent spawned by the conversation.
type Subagent struct {
	ID        string `json:"id"`
	Role      string `json:"role"`
	Type      string `json:"type,omitempty"`
	State     string `json:"state"`
	SpawnStep int    `json:"spawnStep"`
	Updated   int64  `json:"updated"`
}

// AgentMessage is a message delivered to the conversation by a subagent or a
// finished background task.
type AgentMessage struct {
	ID      string `json:"id"`
	Sender  string `json:"sender"`
	Title   string `json:"title"`
	Content string `json:"content"`
	Ts      int64  `json:"ts"`
	Read    bool   `json:"read"`
	// Kind says what the message is: "task" (a background command's result:
	// console output), "user" (your own message, queued while agy worked),
	// "notice" (from agy itself) or "" (from a subagent).
	Kind string `json:"kind,omitempty"`
}

func (m *Manager) brainDir(id string) (string, error) {
	if id == "" || strings.ContainsAny(id, "/\\") || strings.Contains(id, "..") {
		return "", errors.New("bad id")
	}
	return filepath.Join(m.cfg.AgyDataDir, "brain", id), nil
}

func artifactKind(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md", ".markdown":
		return "markdown"
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".svg":
		return "image"
	case ".mp4", ".webm", ".mov", ".mkv":
		return "video"
	case ".html", ".htm":
		return "html"
	case ".txt", ".json", ".log", ".csv", ".yaml", ".yml", ".py", ".go", ".js", ".ts", ".sh":
		return "text"
	}
	return "file"
}

func parseTime(s string) int64 {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return 0
	}
	return t.UnixMilli()
}

// ComputeLive gathers the live state of a conversation. ls may be nil (the
// session isn't running): artifacts, subagents and messages still come from disk.
func (m *Manager) ComputeLive(ctx context.Context, id string, ls *lsapi.Client) (*Live, error) {
	dir, err := m.brainDir(id)
	if err != nil {
		return nil, err
	}
	live := &Live{Running: ls != nil, Tasks: []Task{}, Artifacts: []ArtifactInfo{}, Subagents: []Subagent{}, Messages: []AgentMessage{}}
	// Artifacts on disk (top level + subfolders, skipping agy's private dirs).
	byPath := map[string]int{}
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if p != dir && (strings.HasPrefix(name, ".") || (d.IsDir() && name == "scratch")) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || strings.HasSuffix(name, ".metadata.json") || strings.HasSuffix(name, ".resolved") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		a := ArtifactInfo{Name: rel, Path: p, URI: "file://" + p, Kind: artifactKind(name), Size: info.Size(), Updated: info.ModTime().UnixMilli()}
		if b, err := os.ReadFile(p + ".metadata.json"); err == nil {
			var md struct {
				Summary         string `json:"summary"`
				UpdatedAt       string `json:"updatedAt"`
				RequestFeedback bool   `json:"requestFeedback"`
				UserFacing      bool   `json:"userFacing"`
				Version         any    `json:"version"`
			}
			if json.Unmarshal(b, &md) == nil {
				a.Summary, a.RequestFeedback, a.UserFacing = md.Summary, md.RequestFeedback, md.UserFacing
				if t := parseTime(md.UpdatedAt); t > 0 {
					a.Updated = t
				}
			}
		}
		byPath[p] = len(live.Artifacts)
		live.Artifacts = append(live.Artifacts, a)
		return nil
	})
	if ls != nil {
		var snaps struct {
			ArtifactSnapshots []struct {
				Name   string `json:"artifactName"`
				URI    string `json:"artifactAbsoluteUri"`
				Edited string `json:"lastEdited"`
				Review *struct {
					Status string `json:"status"`
				} `json:"reviewState"`
				Meta *struct {
					Type            string `json:"artifactType"`
					Summary         string `json:"summary"`
					Version         any    `json:"version"`
					RequestFeedback bool   `json:"requestFeedback"`
					UserFacing      bool   `json:"userFacing"`
				} `json:"artifactMetadata"`
				URL *struct {
					URL   string `json:"url"`
					Title string `json:"title"`
				} `json:"urlArtifactPayload"`
			} `json:"artifactSnapshots"`
		}
		if err := ls.Call(ctx, "GetArtifactSnapshots", map[string]any{"cascadeId": id}, &snaps); err == nil {
			for _, sn := range snaps.ArtifactSnapshots {
				p := strings.TrimPrefix(sn.URI, "file://")
				i, ok := byPath[p]
				if !ok {
					if sn.URL == nil || sn.URL.URL == "" {
						continue
					}
					live.Artifacts = append(live.Artifacts, ArtifactInfo{Name: firstNonEmpty(sn.URL.Title, sn.Name), URI: sn.URI, Kind: "url", URL: sn.URL.URL, Updated: parseTime(sn.Edited), UserFacing: true})
					continue
				}
				a := &live.Artifacts[i]
				if sn.Review != nil {
					a.Review = strings.TrimPrefix(sn.Review.Status, "ARTIFACT_REVIEW_STATUS_")
				}
				if sn.Meta != nil {
					a.Summary = firstNonEmpty(sn.Meta.Summary, a.Summary)
					a.RequestFeedback = a.RequestFeedback || sn.Meta.RequestFeedback
					a.UserFacing = a.UserFacing || sn.Meta.UserFacing
					fmt.Sscan(fmt.Sprint(sn.Meta.Version), &a.Version)
				}
			}
		}
		var tasks struct {
			Tasks []struct {
				ConversationID string `json:"conversationId"`
				Task           struct {
					TaskID      string `json:"taskId"`
					ToolName    string `json:"toolName"`
					ToolSummary string `json:"toolSummary"`
					Description string `json:"description"`
					StartTime   string `json:"startTime"`
					StepIndex   int    `json:"stepIndex"`
					LogURI      string `json:"logUri"`
					IsDaemon    bool   `json:"isDaemon"`
				} `json:"task"`
			} `json:"tasks"`
		}
		if err := ls.Call(ctx, "ListBackgroundTasks", map[string]any{}, &tasks); err == nil {
			for _, t := range tasks.Tasks {
				if t.ConversationID != id {
					continue
				}
				task := Task{ID: t.Task.TaskID, Tool: t.Task.ToolName, Summary: t.Task.ToolSummary, Description: t.Task.Description,
					Start: parseTime(t.Task.StartTime), Step: t.Task.StepIndex, Log: strings.TrimPrefix(t.Task.LogURI, "file://"), Daemon: t.Task.IsDaemon}
				if st, err := os.Stat(task.Log); err == nil {
					task.LogSize = st.Size()
				}
				live.Tasks = append(live.Tasks, task)
			}
		}
	}
	sort.SliceStable(live.Artifacts, func(i, j int) bool { return live.Artifacts[i].Updated > live.Artifacts[j].Updated })
	// Subagents.
	sys := filepath.Join(dir, ".system_generated")
	if ents, err := os.ReadDir(filepath.Join(sys, "subagents")); err == nil {
		for _, e := range ents {
			if !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(sys, "subagents", e.Name()))
			if err != nil {
				continue
			}
			var sa struct {
				ConversationID string `json:"conversationId"`
				Desc           struct {
					TypeName string `json:"typeName"`
					Role     string `json:"role"`
				} `json:"subagentDescriptor"`
				State string `json:"state"`
				Spawn int    `json:"spawnStepIndex"`
			}
			if json.Unmarshal(b, &sa) != nil || sa.ConversationID == "" {
				continue
			}
			sub := Subagent{ID: sa.ConversationID, Role: firstNonEmpty(sa.Desc.Role, sa.Desc.TypeName, "Subagent"), Type: sa.Desc.TypeName,
				State: strings.TrimPrefix(sa.State, "SUBAGENT_STATE_"), SpawnStep: sa.Spawn}
			if st, err := os.Stat(filepath.Join(m.cfg.AgyDataDir, "brain", sa.ConversationID, ".system_generated", "logs", "transcript.jsonl")); err == nil {
				sub.Updated = st.ModTime().UnixMilli()
			}
			live.Subagents = append(live.Subagents, sub)
		}
		sort.Slice(live.Subagents, func(i, j int) bool { return live.Subagents[i].SpawnStep < live.Subagents[j].SpawnStep })
	}
	// Messages from subagents and finished tasks.
	read := map[string]bool{}
	if b, err := os.ReadFile(filepath.Join(sys, "messages", "read.json")); err == nil {
		_ = json.Unmarshal(b, &read)
	}
	if ents, err := os.ReadDir(filepath.Join(sys, "messages")); err == nil {
		for _, e := range ents {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || e.Name() == "read.json" {
				continue
			}
			b, err := os.ReadFile(filepath.Join(sys, "messages", e.Name()))
			if err != nil {
				continue
			}
			var msg struct {
				ID        string `json:"id"`
				Sender    string `json:"sender"`
				Timestamp string `json:"timestamp"`
				Render    struct {
					Title string `json:"messageTitle"`
				} `json:"renderDetails"`
				Content     string `json:"content"`
				StepPayload string `json:"stepPayload"`
			}
			if json.Unmarshal(b, &msg) != nil || msg.ID == "" {
				continue
			}
			am := AgentMessage{ID: msg.ID, Sender: msg.Sender, Title: msg.Render.Title, Content: msg.Content,
				Ts: parseTime(msg.Timestamp), Read: read[msg.ID]}
			switch {
			case msg.Sender == "user":
				// A message you sent while agy was busy: its text is only in the step payload.
				am.Kind, am.Title = "user", firstNonEmpty(am.Title, "Your message")
				if strings.TrimSpace(am.Content) == "" {
					am.Content = userPayloadText(msg.StepPayload)
				}
			case msg.Sender == "system":
				am.Kind, am.Title = "notice", firstNonEmpty(am.Title, "Notice")
			case strings.HasPrefix(am.Content, "Task id "):
				am.Kind = "task"
			}
			if strings.TrimSpace(am.Content) == "" {
				continue // nothing to show
			}
			if len(am.Content) > 4000 {
				am.Content = am.Content[:4000] + "…"
			}
			live.Messages = append(live.Messages, am)
		}
		sort.Slice(live.Messages, func(i, j int) bool { return live.Messages[i].Ts < live.Messages[j].Ts })
		if n := len(live.Messages); n > 50 {
			live.Messages = live.Messages[n-50:]
		}
	}
	if s := m.Get(id); s != nil {
		markAnswered(live.Artifacts, s.lastUserTs())
	}
	return live, nil
}

// markAnswered settles review requests the user replied to after the artifact's
// last change.
func markAnswered(arts []ArtifactInfo, lastUser int64) {
	for i := range arts {
		if a := &arts[i]; a.RequestFeedback && lastUser > a.Updated {
			a.Answered = true
		}
	}
}

// lastUserTs is when the user last sent the agent a message from agentc (0 if
// never). Transcript times can't tell: a reload replays agy's history as new.
func (s *Session) lastUserTs() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.meta.LastPrompt
}

// liveLS returns the language server of a running session (nil if not running).
func (m *Manager) liveLS(ctx context.Context, id string) *lsapi.Client {
	s := m.Get(id)
	if s == nil {
		return nil
	}
	s.mu.Lock()
	p, _ := s.proc.(*agyproc.Proc)
	s.mu.Unlock()
	if p == nil || !p.Alive() {
		return nil
	}
	port, err := p.LSPort(ctx)
	if err != nil {
		return nil
	}
	return lsapi.New(port)
}

// LiveState returns the current live state of a conversation.
func (m *Manager) LiveState(ctx context.Context, id string) (*Live, error) {
	if s := m.Get(id); s != nil && s.isClaudeSafe() {
		return m.claudeLive(s), nil
	}
	if !m.isAgyID(id) {
		return &Live{}, nil
	}
	live, err := m.ComputeLive(ctx, id, m.liveLS(ctx, id))
	if err == nil {
		live.Hook = m.hookFor(id)
	}
	return live, err
}

// TaskOutput returns a background task's log from offset (up to 256 KiB).
func (m *Manager) TaskOutput(ctx context.Context, id, taskID string, offset int64) (map[string]any, error) {
	if s := m.Get(id); s != nil && s.isClaudeSafe() {
		return m.claudeTaskOutput(s, taskID, offset)
	}
	dir, err := m.brainDir(id)
	if err != nil {
		return nil, err
	}
	// Task ids look like "<conversation>/task-N"; the log lives in the brain dir.
	name := filepath.Base(taskID)
	if !strings.HasPrefix(name, "task-") || strings.ContainsAny(name, "\\") {
		return nil, errors.New("bad task id")
	}
	path := filepath.Join(dir, ".system_generated", "tasks", name+".log")
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, _ := f.Stat()
	if offset < 0 || offset > st.Size() {
		offset = 0
	}
	if st.Size()-offset > 256<<10 && offset == 0 {
		offset = st.Size() - 256<<10 // first read: just the tail
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	b, _ := io.ReadAll(io.LimitReader(f, 256<<10))
	running := false
	if ls := m.liveLS(ctx, id); ls != nil {
		if live, err := m.ComputeLive(ctx, id, ls); err == nil {
			for _, t := range live.Tasks {
				running = running || t.ID == taskID
			}
		}
	}
	return map[string]any{"data": string(b), "offset": offset, "next": offset + int64(len(b)), "size": st.Size(), "running": running}, nil
}

// StopTaskID stops a background task by id (Claude), else by step (agy).
func (m *Manager) StopTaskID(ctx context.Context, id, taskID string, step int) error {
	if s := m.Get(id); s != nil && s.isClaudeSafe() {
		if taskID == "" {
			return errors.New("missing task id")
		}
		_, err := m.ClaudeControl(ctx, id, "stop_task", map[string]any{"task_id": taskID})
		return err
	}
	return m.StopTask(ctx, id, step)
}

// StopTask cancels a background task (by its step) in a running session.
func (m *Manager) StopTask(ctx context.Context, id string, step int) error {
	ls := m.liveLS(ctx, id)
	if ls == nil {
		return errors.New("the session isn't running")
	}
	return ls.Call(ctx, "CancelCascadeSteps", map[string]any{"cascadeId": id, "stepIndices": []int{step}}, nil)
}

// SubagentSteps returns a subagent's transcript for a session.
func (m *Manager) SubagentSteps(ctx context.Context, id, subID string, max int) ([]SubStep, error) {
	if s := m.Get(id); s != nil && s.isClaudeSafe() {
		return m.claudeSubagent(s, subID, max)
	}
	return m.SubagentTranscript(subID, max)
}

// StopSubagent force-stops a subagent's conversation tree.
func (m *Manager) StopSubagent(ctx context.Context, id, subID string) error {
	if s := m.Get(id); s != nil && s.isClaudeSafe() {
		_, err := m.ClaudeControl(ctx, id, "stop_task", map[string]any{"task_id": subID})
		return err
	}
	ls := m.liveLS(ctx, id)
	if ls == nil {
		return errors.New("the session isn't running")
	}
	return ls.Call(ctx, "ForceStopCascadeTree", map[string]any{"conversationId": subID}, nil)
}

// SubStep is one step of a subagent transcript.
type SubStep struct {
	Index    int       `json:"i"`
	Type     string    `json:"type"`
	Status   string    `json:"status"`
	Source   string    `json:"source,omitempty"`
	Ts       int64     `json:"ts"`
	Text     string    `json:"text,omitempty"`
	Thinking string    `json:"thinking,omitempty"`
	Tools    []SubTool `json:"tools,omitempty"`
	Model    string    `json:"model,omitempty"`
}

// SubTool is a tool call made by a subagent.
type SubTool struct {
	Name string `json:"name"`
	Args string `json:"args"`
}

// SubagentTranscript reads a subagent's transcript (agy writes it as JSONL).
func (m *Manager) SubagentTranscript(subID string, max int) ([]SubStep, error) {
	dir, err := m.brainDir(subID)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(filepath.Join(dir, ".system_generated", "logs", "transcript.jsonl"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []SubStep
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		var r struct {
			Index    int    `json:"step_index"`
			Type     string `json:"type"`
			Status   string `json:"status"`
			Source   string `json:"source"`
			Created  string `json:"created_at"`
			Content  any    `json:"content"`
			Thinking string `json:"thinking"`
			Model    string `json:"model"`
			Tools    []struct {
				Name string         `json:"name"`
				Args map[string]any `json:"args"`
			} `json:"tool_calls"`
		}
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			continue
		}
		st := SubStep{Index: r.Index, Type: r.Type, Status: r.Status, Source: r.Source, Ts: parseTime(r.Created), Thinking: r.Thinking, Model: r.Model}
		if s, ok := r.Content.(string); ok {
			st.Text = s
		} else if r.Content != nil {
			b, _ := json.Marshal(r.Content)
			st.Text = string(b)
		}
		if len(st.Text) > 12000 {
			st.Text = st.Text[:12000] + "…"
		}
		for _, t := range r.Tools {
			args := ""
			for k, v := range t.Args {
				vs := fmt.Sprint(v)
				vs = strings.Trim(vs, `"`)
				if len(vs) > 300 {
					vs = vs[:300] + "…"
				}
				args += k + ": " + vs + "\n"
			}
			st.Tools = append(st.Tools, SubTool{Name: t.Name, Args: strings.TrimSpace(args)})
		}
		out = append(out, st)
	}
	if max > 0 && len(out) > max {
		out = out[len(out)-max:]
	}
	return out, nil
}

// ---- live watcher: pushes "session.live" to subscribers of running sessions ----

type liveWatch struct {
	cancel context.CancelFunc
	last   string
	hook   string
}

var liveMu sync.Mutex

func (m *Manager) hookFor(id string) string {
	liveMu.Lock()
	defer liveMu.Unlock()
	if w := m.liveWatches[id]; w != nil {
		return w.hook
	}
	return ""
}

// liveLoop starts/stops watchers for running sessions that have subscribers.
func (m *Manager) liveLoop() {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for range t.C {
		for _, s := range m.all() {
			id := s.ID()
			want := m.hub.HasSubscribers(id) && m.liveLS(context.Background(), id) != nil
			liveMu.Lock()
			w := m.liveWatches[id]
			if want && w == nil {
				ctx, cancel := context.WithCancel(context.Background())
				w = &liveWatch{cancel: cancel}
				m.liveWatches[id] = w
				go m.watchLive(ctx, id, w)
			} else if !want && w != nil {
				w.cancel()
				delete(m.liveWatches, id)
			}
			liveMu.Unlock()
		}
	}
}

func (m *Manager) watchLive(ctx context.Context, id string, w *liveWatch) {
	ls := m.liveLS(ctx, id)
	if ls == nil {
		return
	}
	trigger := make(chan struct{}, 1)
	poke := func() {
		select {
		case trigger <- struct{}{}:
		default:
		}
	}
	go func() {
		err := ls.Stream(ctx, "StreamAgentStateUpdates", map[string]any{"conversationId": id, "subscriberId": "agentc-live"}, func(msg json.RawMessage) error {
			var r struct {
				Update map[string]json.RawMessage `json:"update"`
			}
			if json.Unmarshal(msg, &r) != nil {
				return nil
			}
			// activeHook is present while a hook runs (HookStatus{hookType, hookName}).
			hook := ""
			if h, ok := r.Update["activeHook"]; ok {
				var hk struct {
					Type string `json:"hookType"`
					Name string `json:"hookName"`
				}
				_ = json.Unmarshal(h, &hk)
				hook = firstNonEmpty(hk.Name, strings.ToLower(strings.TrimPrefix(hk.Type, "HOOK_TYPE_")), "hook")
			}
			liveMu.Lock()
			changed := w.hook != hook
			w.hook = hook
			liveMu.Unlock()
			if changed {
				poke()
			}
			for _, k := range []string{"backgroundTasksUpdate", "artifactSnapshotsUpdate", "pendingAgentMessagesUpdate", "turnsUpdate", "fullyIdle"} {
				if _, ok := r.Update[k]; ok {
					poke()
					break
				}
			}
			return nil
		})
		if err != nil && ctx.Err() == nil {
			log.Printf("session %s: agent state stream: %v (falling back to polling)", id, err)
		}
	}()
	refresh := func() {
		live, err := m.ComputeLive(ctx, id, ls)
		if err != nil {
			return
		}
		live.Hook = m.hookFor(id)
		b, _ := json.Marshal(live)
		if string(b) == w.last {
			return
		}
		w.last = string(b)
		m.hub.ToSubscribers(id, "session.live", map[string]any{"id": id, "live": live})
	}
	refresh()
	poll := time.NewTicker(3 * time.Second)
	defer poll.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-trigger:
			time.Sleep(300 * time.Millisecond) // coalesce bursts
			refresh()
		case <-poll.C:
			refresh()
		}
	}
}

// ReadArtifact returns an artifact's text (it must live in the conversation's
// brain directory).
func (m *Manager) ReadArtifact(id, path string) (map[string]any, error) {
	var dir string
	var err error
	if s := m.Get(id); s != nil && s.isClaudeSafe() {
		dir = filepath.Join(m.claudeDir(), "plans") // Claude's plan files
	} else if dir, err = m.brainDir(id); err != nil {
		return nil, err
	}
	p := filepath.Clean(strings.TrimPrefix(path, "file://"))
	if rel, err := filepath.Rel(dir, p); err != nil || strings.HasPrefix(rel, "..") {
		return nil, errors.New("not an artifact of this conversation")
	}
	st, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if st.Size() > 4<<20 {
		return nil, errors.New("artifact is too large to show (over 4 MB)")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	return map[string]any{"path": p, "uri": "file://" + p, "content": string(b), "size": st.Size(), "mtime": st.ModTime().UnixMilli(), "kind": artifactKind(p)}, nil
}

// userPayloadText digs the text out of a queued user message's step payload:
// a base64 CortexStep protobuf whose user input (field 19) holds the text in
// field 2.
func userPayloadText(b64 string) string {
	b, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		if b, err = base64.RawStdEncoding.DecodeString(b64); err != nil {
			return ""
		}
	}
	if in := protoField(b, 19); in != nil {
		if t := protoField(in, 2); t != nil && utf8.Valid(t) {
			return strings.TrimSpace(string(t))
		}
	}
	return ""
}

// protoField returns the first length-delimited field num of a protobuf
// message, or nil.
func protoField(b []byte, num uint64) []byte {
	for i := 0; i < len(b); {
		key, n := binary.Uvarint(b[i:])
		if n <= 0 {
			return nil
		}
		i += n
		switch key & 7 {
		case 0:
			if _, n = binary.Uvarint(b[i:]); n <= 0 {
				return nil
			}
			i += n
		case 1:
			i += 8
		case 5:
			i += 4
		case 2:
			l, n := binary.Uvarint(b[i:])
			if n <= 0 || l > uint64(len(b)-i-n) {
				return nil
			}
			i += n
			if key>>3 == num {
				return b[i : i+int(l)]
			}
			i += int(l)
		default:
			return nil
		}
	}
	return nil
}
