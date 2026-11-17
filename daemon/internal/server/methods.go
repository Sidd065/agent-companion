package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"agentc/internal/core"
	"agentc/internal/fsops"
	"agentc/internal/push"
	"agentc/internal/term"
)

type method func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error)

func bind[T any](p json.RawMessage) (T, error) {
	var v T
	if len(p) == 0 || string(p) == "null" {
		return v, nil
	}
	if err := json.Unmarshal(p, &v); err != nil {
		return v, fmt.Errorf("bad params: %w", err)
	}
	return v, nil
}

type idParam struct {
	ID string `json:"id"`
}

func (s *Server) session(ctx context.Context, id string) (*core.Session, error) {
	return s.sessionWith(ctx, id, nil)
}

func (s *Server) sessionWith(ctx context.Context, id string, flags *core.Flags) (*core.Session, error) {
	if strings.TrimSpace(id) == "" {
		return nil, errors.New("missing session id")
	}
	if ss := s.Mgr.Get(id); ss != nil {
		return ss, nil
	}
	return s.Mgr.OpenWith(ctx, id, flags)
}

func (s *Server) registerMethods() {
	m := map[string]method{}
	s.methods = m

	m["hello"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Device string `json:"device"`
			App    string `json:"app"`
		}](p)
		if in.Device != "" {
			c.Device = in.Device
		}
		info := s.info(ctx)
		info["sessions"] = s.Mgr.List(ctx, false)
		info["client"] = map[string]any{"id": c.ID, "deviceId": c.Peer, "deviceName": c.Device}
		return info, nil
	}
	m["daemon.info"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		info := s.info(ctx)
		var clients []map[string]any
		for _, cl := range s.Mgr.Hub().Clients() {
			clients = append(clients, map[string]any{"id": cl.ID, "device": cl.Device, "deviceId": cl.Peer})
		}
		info["clients"] = clients
		if s.ConfigGet != nil {
			info["config"] = s.ConfigGet()
		}
		return info, nil
	}
	m["ping"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		return map[string]any{"ok": true}, nil
	}
	m["push.register"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Token   string `json:"token"`
			HostKey string `json:"hostKey"`
			Device  string `json:"device"`
		}](p)
		if s.Push == nil || in.Token == "" {
			return nil, errors.New("push is not available")
		}
		// The token belongs to the authenticated device; the host key is this
		// daemon's identity (the app routes notifications by it).
		s.Push.Register(push.Device{Token: in.Token, HostKey: s.Identity.ID(), Device: firstNonEmpty(in.Device, c.Device), DeviceID: c.Peer})
		return s.Push.Status(), nil
	}
	m["push.unregister"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Token string `json:"token"`
		}](p)
		if s.Push != nil {
			s.Push.Unregister(in.Token)
		}
		return map[string]any{"ok": true}, nil
	}
	m["push.status"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		if s.Push == nil {
			return map[string]any{"configured": false}, nil
		}
		return s.Push.Status(), nil
	}
	m["push.test"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Token   string `json:"token"`
			HostKey string `json:"hostKey"`
		}](p)
		if s.Push == nil {
			return nil, errors.New("push is not available")
		}
		host, _ := os.Hostname()
		err := s.Push.SendTo(ctx, in.Token, s.Identity.ID(), push.Message{Kind: "test", Title: "Test notification", Body: "Push notifications from " + host + " are working.", Ts: time.Now().UnixMilli(), ID: fmt.Sprintf("test-%d", time.Now().UnixNano())})
		if err != nil {
			return nil, err
		}
		return map[string]any{"ok": true}, nil
	}
	// ---- paired devices ----
	m["devices.list"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		var out []map[string]any
		for _, d := range s.Devices.List() {
			out = append(out, map[string]any{"id": d.ID, "name": d.Name, "platform": d.Platform, "appVersion": d.AppVer,
				"pairedAt": d.PairedAt, "lastSeen": d.LastSeen, "self": d.ID == c.Peer})
		}
		return out, nil
	}
	m["devices.revoke"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[idParam](p)
		ok, err := s.revokeDevice(in.ID)
		return map[string]any{"removed": ok}, err
	}
	m["devices.rename"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}](p)
		if strings.TrimSpace(in.Name) == "" {
			return nil, errors.New("name is empty")
		}
		if err := s.Devices.Rename(in.ID, strings.TrimSpace(in.Name)); err != nil {
			return nil, err
		}
		s.Mgr.Hub().Broadcast("devices.changed", s.Devices.List())
		return map[string]any{"ok": true}, nil
	}
	m["daemon.config.set"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, err := bind[map[string]any](p)
		if err != nil {
			return nil, err
		}
		if s.ConfigSet == nil {
			return nil, errors.New("not supported")
		}
		return s.ConfigSet(in)
	}

	// ---- sessions ----
	m["sessions.list"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Force bool `json:"force"`
		}](p)
		return s.Mgr.List(ctx, in.Force), nil
	}
	m["session.create"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, err := bind[core.CreateParams](p)
		if err != nil {
			return nil, err
		}
		ss, err := s.Mgr.Create(ctx, in)
		if ss == nil {
			return nil, err
		}
		res := map[string]any{"session": ss.Summary()}
		if err != nil {
			res["warning"] = err.Error()
		}
		return res, nil
	}
	m["session.open"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			ID    string      `json:"id"`
			Flags *core.Flags `json:"flags"`
		}](p)
		ss, err := s.sessionWith(ctx, in.ID, in.Flags)
		if err != nil {
			return nil, err
		}
		return ss.Summary(), nil
	}
	m["session.subscribe"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			ID    string      `json:"id"`
			Since int64       `json:"since"`
			Flags *core.Flags `json:"flags"`
		}](p)
		ss, err := s.sessionWith(ctx, in.ID, in.Flags)
		if err != nil {
			return nil, err
		}
		ss.Subscribe(c, in.Since)
		return map[string]any{"ok": true}, nil
	}
	m["session.unsubscribe"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[idParam](p)
		c.Subscribe(in.ID, false)
		return map[string]any{"ok": true}, nil
	}
	m["session.prompt"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, err := bind[struct {
			ID string `json:"id"`
			core.PromptInput
		}](p)
		if err != nil {
			return nil, err
		}
		ss, err := s.session(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		return ss.Prompt(in.PromptInput)
	}
	m["session.cancel"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[idParam](p)
		ss := s.Mgr.Get(in.ID)
		if ss == nil {
			return nil, errors.New("session is not running")
		}
		return map[string]any{"ok": true}, ss.Cancel()
	}
	m["session.permission"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			ID       string         `json:"id"`
			ReqID    string         `json:"reqId"`
			OptionID string         `json:"optionId"`
			Answers  map[string]any `json:"answers"`
			Message  string         `json:"message"`
		}](p)
		ss := s.Mgr.Get(in.ID)
		if ss == nil {
			return nil, errors.New("unknown session")
		}
		var ans *core.PermAnswer
		if len(in.Answers) > 0 || strings.TrimSpace(in.Message) != "" {
			ans = &core.PermAnswer{Answers: in.Answers, Message: strings.TrimSpace(in.Message)}
		}
		return map[string]any{"ok": true}, ss.RespondPermissionWith(in.ReqID, in.OptionID, ans)
	}
	// Side questions (/btw): answered from the conversation, not added to it.
	m["session.aside"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			ID       string `json:"id"`
			Question string `json:"question"`
			FollowUp string `json:"followUp"`
		}](p)
		a, err := s.Mgr.AskAside(ctx, in.ID, in.Question, in.FollowUp)
		if err != nil {
			return nil, err
		}
		return a, nil
	}
	m["session.asides"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[idParam](p)
		return s.Mgr.Asides(in.ID)
	}
	m["session.asideDelete"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			ID      string `json:"id"`
			AsideID string `json:"asideId"`
		}](p)
		return map[string]any{"ok": true}, s.Mgr.DeleteAside(in.ID, in.AsideID)
	}
	// session.control sends a Claude Code control request (context usage,
	// usage and cost, side questions, MCP servers, …).
	m["session.control"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			ID      string         `json:"id"`
			Subtype string         `json:"subtype"`
			Params  map[string]any `json:"params"`
		}](p)
		raw, err := s.Mgr.ClaudeControl(ctx, in.ID, in.Subtype, in.Params)
		if err != nil {
			return nil, err
		}
		return raw, nil
	}
	m["session.setModel"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			ID    string `json:"id"`
			Model string `json:"model"`
		}](p)
		ss, err := s.session(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		err = ss.SetModel(ctx, in.Model)
		return ss.Summary(), err
	}
	m["session.setMode"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			ID   string `json:"id"`
			Mode string `json:"mode"`
		}](p)
		ss, err := s.session(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		err = ss.SetMode(ctx, in.Mode)
		return ss.Summary(), err
	}
	m["session.setConfig"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			ID       string `json:"id"`
			ConfigID string `json:"configId"`
			Value    string `json:"value"`
		}](p)
		ss, err := s.session(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		err = ss.SetConfig(ctx, in.ConfigID, in.Value)
		return ss.Summary(), err
	}
	m["session.queue"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			ID     string `json:"id"`
			ItemID string `json:"itemId"`
			Action string `json:"action"`
			Text   string `json:"text"`
		}](p)
		ss := s.Mgr.Get(in.ID)
		if ss == nil {
			return nil, errors.New("unknown session")
		}
		return map[string]any{"ok": true}, ss.QueueAction(in.ItemID, in.Action, in.Text)
	}
	m["session.rename"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		}](p)
		return map[string]any{"ok": true}, s.Mgr.Rename(ctx, in.ID, in.Title)
	}
	m["session.archive"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			ID       string `json:"id"`
			Archived bool   `json:"archived"`
		}](p)
		return map[string]any{"ok": true}, s.Mgr.SetFlag(ctx, in.ID, "archived", in.Archived)
	}
	m["session.pin"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			ID     string `json:"id"`
			Pinned bool   `json:"pinned"`
		}](p)
		return map[string]any{"ok": true}, s.Mgr.SetFlag(ctx, in.ID, "pinned", in.Pinned)
	}
	m["session.delete"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[idParam](p)
		return map[string]any{"ok": true}, s.Mgr.Delete(ctx, in.ID)
	}
	m["session.fork"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			ID     string `json:"id"`
			ItemID string `json:"itemId"`
		}](p)
		ns, prefill, err := s.Mgr.Fork(ctx, in.ID, in.ItemID)
		res := map[string]any{"prefill": prefill}
		if ns != nil {
			res["session"] = ns.Summary()
		}
		if err != nil && ns == nil {
			return nil, err
		}
		if err != nil {
			res["warning"] = err.Error()
		}
		return res, nil
	}
	m["session.rewind"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			ID               string `json:"id"`
			ItemID           string `json:"itemId"`
			ConversationOnly bool   `json:"conversationOnly"`
		}](p)
		prefill, err := s.Mgr.Rewind(ctx, in.ID, in.ItemID, in.ConversationOnly)
		if err != nil {
			return nil, err
		}
		return map[string]any{"prefill": prefill}, nil
	}
	m["session.revertPreview"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			ID     string `json:"id"`
			ItemID string `json:"itemId"`
		}](p)
		raw, idx, err := s.Mgr.RevertPreview(ctx, in.ID, in.ItemID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"preview": raw, "stepIndex": idx}, nil
	}
	m["session.stop"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[idParam](p)
		if ss := s.Mgr.Get(in.ID); ss != nil {
			ss.Hibernate()
		}
		return map[string]any{"ok": true}, nil
	}
	m["session.wake"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[idParam](p)
		return map[string]any{"ok": true}, s.Mgr.Wake(ctx, in.ID)
	}
	m["session.reload"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[idParam](p)
		return map[string]any{"ok": true}, s.Mgr.Reload(ctx, in.ID)
	}
	m["session.forget"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[idParam](p)
		s.Mgr.Forget(in.ID)
		return map[string]any{"ok": true}, nil
	}
	m["session.item"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			ID     string `json:"id"`
			ItemID string `json:"itemId"`
		}](p)
		ss := s.Mgr.Get(in.ID)
		if ss == nil {
			return nil, errors.New("unknown session")
		}
		it := ss.FullItem(in.ItemID)
		if it == nil {
			return nil, errors.New("item not found")
		}
		return it, nil
	}
	m["session.diff"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[idParam](p)
		ss, err := s.session(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		return core.GitDiff(ctx, ss.Summary().Cwd)
	}
	m["session.changes"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[idParam](p)
		return s.Mgr.AgentChanges(ctx, in.ID)
	}
	m["session.export"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[idParam](p)
		md, err := s.Mgr.Export(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"markdown": md}, nil
	}
	m["session.artifacts"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[idParam](p)
		return s.Mgr.Artifacts(in.ID)
	}
	// ---- live state: background tasks, artifacts, subagents, messages ----
	m["session.live"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[idParam](p)
		return s.Mgr.LiveState(ctx, in.ID)
	}
	m["session.taskOutput"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			ID     string `json:"id"`
			TaskID string `json:"taskId"`
			Offset int64  `json:"offset"`
		}](p)
		return s.Mgr.TaskOutput(ctx, in.ID, in.TaskID, in.Offset)
	}
	m["session.taskStop"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			ID     string `json:"id"`
			Step   int    `json:"step"`
			TaskID string `json:"taskId"`
		}](p)
		return map[string]any{"ok": true}, s.Mgr.StopTaskID(ctx, in.ID, in.TaskID, in.Step)
	}
	m["session.subagent"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			ID    string `json:"id"`
			SubID string `json:"subId"`
			Max   int    `json:"max"`
		}](p)
		if in.Max <= 0 {
			in.Max = 300
		}
		steps, err := s.Mgr.SubagentSteps(ctx, in.ID, in.SubID, in.Max)
		if err != nil {
			return nil, err
		}
		return map[string]any{"steps": steps}, nil
	}
	m["session.subagentStop"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			ID    string `json:"id"`
			SubID string `json:"subId"`
		}](p)
		return map[string]any{"ok": true}, s.Mgr.StopSubagent(ctx, in.ID, in.SubID)
	}
	m["session.artifact"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		}](p)
		return s.Mgr.ReadArtifact(in.ID, in.Path)
	}
	m["session.comment"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			ID       string         `json:"id"`
			Comments []core.Comment `json:"comments"`
			Text     string         `json:"text"`
			Delivery string         `json:"delivery"`
		}](p)
		if len(in.Comments) == 0 && strings.TrimSpace(in.Text) == "" {
			return nil, errors.New("no comments")
		}
		ss, err := s.session(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		return ss.Comment(in.Comments, in.Text, in.Delivery)
	}
	m["session.openInTerminal"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			ID              string   `json:"id"`
			Cols            int      `json:"cols"`
			Rows            int      `json:"rows"`
			SkipPermissions *bool    `json:"skipPermissions"`
			Args            []string `json:"args"`
		}](p)
		ss, err := s.session(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		sum := ss.Summary()
		if sum.State == "running" || sum.State == "waiting" {
			return nil, errors.New("the session is busy; stop it first")
		}
		ss.Hibernate() // one agent process per conversation at a time
		skip := sum.SkipPerms
		if in.SkipPermissions != nil {
			skip = *in.SkipPermissions
		}
		if sum.Agent == "claude" {
			if s.Mgr.ClaudeOpenElsewhere(sum.ID) {
				return nil, errors.New("this conversation is already open in another Claude Code process")
			}
			return s.Terms.Create(ctx, term.CreateOpts{Kind: "claude", Cwd: sum.Cwd, ConvID: sum.ID, Name: "claude · " + sum.Title,
				Cols: in.Cols, Rows: in.Rows, Skip: &skip, Args: in.Args})
		}
		return s.Terms.Create(ctx, term.CreateOpts{Kind: "agy", Cwd: sum.Cwd, ConvID: sum.ID, Name: "agy · " + sum.Title,
			Cols: in.Cols, Rows: in.Rows, Skip: &skip, Args: in.Args})
	}
	m["notifications.since"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Seq int64 `json:"seq"`
		}](p)
		return s.Mgr.NotificationsSince(in.Seq), nil
	}

	// ---- files ----
	m["fs.home"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		info := fsops.HomeInfo()
		var recent []string
		seen := map[string]bool{}
		for _, sm := range s.Mgr.List(ctx, false) {
			if sm.Cwd != "" && !seen[sm.Cwd] && len(recent) < 12 {
				seen[sm.Cwd] = true
				recent = append(recent, sm.Cwd)
			}
		}
		info["recent"] = recent
		return info, nil
	}
	m["fs.list"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Path   string `json:"path"`
			Hidden bool   `json:"hidden"`
		}](p)
		return fsops.List(in.Path, in.Hidden)
	}
	m["fs.stat"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Path string `json:"path"`
		}](p)
		return fsops.Stat(in.Path)
	}
	m["fs.read"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Path string `json:"path"`
			Max  int64  `json:"max"`
		}](p)
		return fsops.Read(in.Path, in.Max)
	}
	// Chunked transfers (all file bytes travel inside the encrypted channel).
	m["fs.download"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Path   string `json:"path"`
			Offset int64  `json:"offset"`
			Length int64  `json:"length"`
		}](p)
		return readChunk(fsops.Resolve(in.Path), in.Offset, in.Length)
	}
	m["fs.uploadStart"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Path      string `json:"path"`
			Overwrite bool   `json:"overwrite"`
			Mkdirs    bool   `json:"mkdirs"`
		}](p)
		id, err := s.uploads.start(fsops.Resolve(in.Path), in.Overwrite, in.Mkdirs)
		if err != nil {
			return nil, err
		}
		return map[string]any{"id": id}, nil
	}
	m["fs.uploadChunk"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			ID     string `json:"id"`
			Offset int64  `json:"offset"`
			Data   string `json:"data"`
		}](p)
		data, err := base64.StdEncoding.DecodeString(in.Data)
		if err != nil {
			return nil, fmt.Errorf("bad chunk: %w", err)
		}
		n, err := s.uploads.chunk(in.ID, in.Offset, data)
		if err != nil {
			return map[string]any{"written": n}, err
		}
		return map[string]any{"written": n}, nil
	}
	m["fs.uploadFinish"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[idParam](p)
		dest, n, err := s.uploads.finish(in.ID)
		if err != nil {
			return nil, err
		}
		e, _ := fsops.Stat(dest)
		return map[string]any{"entry": e, "bytes": n}, nil
	}
	m["fs.uploadAbort"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[idParam](p)
		s.uploads.abort(in.ID)
		return map[string]any{"ok": true}, nil
	}
	m["attachment.read"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Ref    string `json:"ref"`
			Offset int64  `json:"offset"`
			Length int64  `json:"length"`
		}](p)
		path, err := s.Mgr.AttachmentPath(strings.TrimPrefix(in.Ref, "att:"))
		if err != nil {
			return nil, err
		}
		return readChunk(path, in.Offset, in.Length)
	}
	m["fs.write"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, err := bind[fsops.WriteParams](p)
		if err != nil {
			return nil, err
		}
		return fsops.Write(in)
	}
	m["fs.mkdir"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Path    string `json:"path"`
			Parents bool   `json:"parents"`
		}](p)
		return fsops.Mkdir(in.Path, in.Parents)
	}
	m["fs.create"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Path string `json:"path"`
		}](p)
		return fsops.Create(in.Path)
	}
	m["fs.rename"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			From      string `json:"from"`
			To        string `json:"to"`
			Overwrite bool   `json:"overwrite"`
		}](p)
		return fsops.Rename(in.From, in.To, in.Overwrite)
	}
	m["fs.copy"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			From      string `json:"from"`
			To        string `json:"to"`
			Overwrite bool   `json:"overwrite"`
		}](p)
		return fsops.Copy(in.From, in.To, in.Overwrite)
	}
	m["fs.delete"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Paths     []string `json:"paths"`
			Permanent bool     `json:"permanent"`
		}](p)
		return map[string]any{"ok": true}, fsops.Delete(ctx, in.Paths, in.Permanent)
	}
	m["fs.search"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Root   string `json:"root"`
			Query  string `json:"query"`
			Max    int    `json:"max"`
			Hidden bool   `json:"hidden"`
		}](p)
		return fsops.Search(ctx, in.Root, in.Query, in.Max, in.Hidden)
	}
	m["fs.complete"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Cwd   string `json:"cwd"`
			Query string `json:"query"`
			Max   int    `json:"max"`
		}](p)
		return fsops.Complete(ctx, in.Cwd, in.Query, in.Max)
	}
	m["git.diff"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Path string `json:"path"`
		}](p)
		return core.GitDiff(ctx, fsops.Resolve(in.Path))
	}

	// ---- terminals ----
	m["term.list"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		return s.Terms.List(ctx)
	}
	m["term.create"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, err := bind[term.CreateOpts](p)
		if err != nil {
			return nil, err
		}
		in.Cwd = fsops.Resolve(in.Cwd)
		t, err := s.Terms.Create(ctx, in)
		if err == nil {
			terms, _ := s.Terms.List(ctx)
			s.Mgr.Hub().Broadcast("terms.changed", terms)
		}
		return t, err
	}
	m["term.kill"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[idParam](p)
		err := s.Terms.Kill(ctx, in.ID)
		terms, _ := s.Terms.List(ctx)
		s.Mgr.Hub().Broadcast("terms.changed", terms)
		return map[string]any{"ok": true}, err
	}
	m["term.rename"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}](p)
		err := s.Terms.Rename(ctx, in.ID, in.Name)
		terms, _ := s.Terms.List(ctx)
		s.Mgr.Hub().Broadcast("terms.changed", terms)
		return map[string]any{"ok": true}, err
	}
	m["term.sendKeys"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			ID    string `json:"id"`
			Text  string `json:"text"`
			Enter bool   `json:"enter"`
		}](p)
		return map[string]any{"ok": true}, s.Terms.SendKeys(ctx, in.ID, in.Text, in.Enter)
	}
	m["term.capture"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			ID    string `json:"id"`
			Lines int    `json:"lines"`
		}](p)
		txt, err := s.Terms.Capture(ctx, in.ID, in.Lines)
		return map[string]any{"text": txt}, err
	}

	// ---- Claude Code management ----
	m["claude.info"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		out := map[string]any{"path": s.Mgr.ClaudeBin(), "settingsPath": s.Mgr.ClaudeSettingsPath(), "live": s.Mgr.ClaudeLiveSessions()}
		if a := s.Mgr.Agent("claude"); a != nil {
			out["available"], out["version"], out["reason"] = a.Available, a.Version, a.Reason
		}
		if rl, at := s.Mgr.ClaudeRateLimit(); rl != nil {
			out["rateLimit"], out["rateLimitAt"] = rl, at
		}
		return out, nil
	}
	m["claude.usage"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		raw, err := s.Mgr.ClaudeQuery(ctx, "get_usage", nil)
		out := map[string]any{}
		if err != nil {
			out["error"] = err.Error()
		} else {
			var v map[string]any
			_ = json.Unmarshal(raw, &v)
			delete(v, "session") // the probe process's own (empty) session
			out["usage"] = v
		}
		if rl, at := s.Mgr.ClaudeRateLimit(); rl != nil {
			out["rateLimit"], out["rateLimitAt"] = rl, at
		}
		return out, nil
	}
	m["claude.query"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Subtype string         `json:"subtype"`
			Params  map[string]any `json:"params"`
		}](p)
		raw, err := s.Mgr.ClaudeQuery(ctx, in.Subtype, in.Params)
		if err != nil {
			return nil, err
		}
		return raw, nil
	}
	m["claude.catalog"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Force bool `json:"force"`
		}](p)
		return s.Mgr.ClaudeCatalog(ctx, in.Force)
	}
	m["claude.live"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		return s.Mgr.ClaudeLiveSessions(), nil
	}
	m["claude.settings.get"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		path := s.Mgr.ClaudeSettingsPath()
		b, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		var parsed any
		_ = json.Unmarshal(b, &parsed)
		return map[string]any{"text": string(b), "path": path, "parsed": parsed}, nil
	}
	m["claude.settings.set"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Text  *string `json:"text"`
			Key   string  `json:"key"`
			Value any     `json:"value"`
		}](p)
		path := s.Mgr.ClaudeSettingsPath()
		cur := map[string]any{}
		if b, err := os.ReadFile(path); err == nil {
			if err := json.Unmarshal(b, &cur); err != nil && in.Text == nil {
				return nil, fmt.Errorf("settings.json isn't valid JSON: %w", err)
			}
		}
		var out []byte
		switch {
		case in.Text != nil:
			var v map[string]any
			if err := json.Unmarshal([]byte(*in.Text), &v); err != nil {
				return nil, fmt.Errorf("not valid JSON: %w", err)
			}
			out = []byte(*in.Text)
		case in.Key != "":
			if in.Value == nil {
				delete(cur, in.Key)
			} else {
				cur[in.Key] = in.Value
			}
			out, _ = json.MarshalIndent(cur, "", "  ")
		default:
			return nil, errors.New("nothing to save")
		}
		if b, err := os.ReadFile(path); err == nil {
			_ = os.WriteFile(path+".agentc-backup", b, 0o600)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, out, 0o600); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true}, os.Rename(tmp, path)
	}
	m["claude.cli"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Args []string `json:"args"`
		}](p)
		allowed := map[string]time.Duration{"mcp list": time.Minute, "mcp get": time.Minute, "plugin list": time.Minute,
			"plugin marketplace list": time.Minute, "update": 3 * time.Minute, "auth status": 30 * time.Second, "agents": 30 * time.Second}
		key := strings.Join(in.Args, " ")
		var timeout time.Duration
		for k, t := range allowed {
			if key == k || (k == "mcp get" && len(in.Args) == 3 && in.Args[0] == "mcp" && in.Args[1] == "get") {
				timeout = t
			}
		}
		if timeout == 0 {
			return nil, fmt.Errorf("claude %s is not available from the app", key)
		}
		cctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		cmd := exec.CommandContext(cctx, s.Mgr.ClaudeBin(), in.Args...)
		cmd.Env = s.Mgr.ClaudeEnv()
		cmd.Dir = s.Mgr.Cfg().HomeDir
		var stdout, stderr strings.Builder
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		res := map[string]any{"stdout": stdout.String(), "stderr": stderr.String(), "ok": err == nil}
		if err != nil {
			res["error"] = err.Error()
		}
		return res, nil
	}

	// ---- agy management ----
	m["agy.info"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		out := map[string]any{"version": s.CLI.Version(ctx), "settings": s.Mgr.AgySettings(), "dataDir": s.CLI.DataDir}
		if st, err := s.Mgr.UserStatus(ctx); err == nil {
			out["status"] = st
		} else {
			out["statusError"] = err.Error()
		}
		return out, nil
	}
	m["agy.quota"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		return s.Mgr.UserStatus(ctx)
	}
	m["agy.models"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Force bool `json:"force"`
		}](p)
		return s.CLI.Models(ctx, in.Force)
	}
	m["agy.agents"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		return s.CLI.Run(ctx, time.Minute, "agents")
	}
	m["agy.mcp.list"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		servers, err := s.CLI.MCPServers()
		res := map[string]any{"servers": servers, "path": s.CLI.MCPConfigPath()}
		if err != nil {
			res["error"] = err.Error()
		}
		if r, err := s.CLI.Run(ctx, time.Minute, "mcp", "list"); err == nil {
			res["cli"] = strings.TrimSpace(r.Stdout + r.Stderr)
		}
		return res, nil
	}
	m["agy.mcp.add"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Name    string            `json:"name"`
			Type    string            `json:"type"`
			Target  string            `json:"target"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
			Headers map[string]string `json:"headers"`
		}](p)
		if in.Name == "" || in.Target == "" {
			return nil, errors.New("name and command/URL are required")
		}
		return s.CLI.MCPAdd(ctx, in.Name, in.Type, in.Target, in.Args, in.Env, in.Headers)
	}
	for _, act := range []string{"remove", "enable", "disable"} {
		act := act
		m["agy.mcp."+act] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
			in, _ := bind[struct {
				Name string `json:"name"`
			}](p)
			return s.CLI.Run(ctx, time.Minute, "mcp", act, in.Name)
		}
	}
	m["agy.plugin.list"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		return s.CLI.Plugins(ctx)
	}
	m["agy.plugin.run"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Action string   `json:"action"`
			Args   []string `json:"args"`
		}](p)
		switch in.Action {
		case "install", "uninstall", "enable", "disable", "import", "validate", "link", "list":
		default:
			return nil, fmt.Errorf("unknown plugin action %q", in.Action)
		}
		return s.CLI.Run(ctx, 5*time.Minute, append([]string{"plugin", in.Action}, in.Args...)...)
	}
	m["agy.changelog"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		txt, err := s.CLI.Changelog(ctx)
		return map[string]any{"text": txt}, err
	}
	m["agy.update"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		r, err := s.CLI.Run(ctx, 10*time.Minute, "update")
		s.CLI.Invalidate()
		return r, err
	}
	m["agy.settings.get"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		txt, err := s.CLI.Settings()
		return map[string]any{"text": txt, "path": s.CLI.SettingsPath(), "parsed": s.Mgr.AgySettings()}, err
	}
	m["agy.settings.set"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Text  *string `json:"text"`
			Key   string  `json:"key"`
			Value any     `json:"value"`
		}](p)
		var err error
		if in.Text != nil {
			err = s.CLI.SaveSettings(*in.Text)
		} else if in.Key != "" {
			err = s.CLI.SetSetting(in.Key, in.Value)
		} else {
			err = errors.New("nothing to save")
		}
		return map[string]any{"ok": err == nil}, err
	}
	m["agy.configFiles"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		return s.CLI.ConfigFiles(), nil
	}
	m["agy.skills"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Cwd string `json:"cwd"`
		}](p)
		return s.CLI.Skills(in.Cwd), nil
	}
	m["agy.remoteControl"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Action string `json:"action"`
		}](p)
		switch in.Action {
		case "", "status":
			return s.CLI.Run(ctx, time.Minute, "remote-control", "status")
		case "start", "stop":
			return s.CLI.Run(ctx, 2*time.Minute, "remote-control", in.Action)
		}
		return nil, fmt.Errorf("unknown action %q", in.Action)
	}
	m["agy.exec"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Args       []string `json:"args"`
			TimeoutSec int      `json:"timeoutSec"`
		}](p)
		if len(in.Args) == 0 {
			return nil, errors.New("no arguments")
		}
		if in.Args[0] == "acp" || in.Args[0] == "mic-serve" {
			return nil, errors.New("interactive subcommand; use a terminal")
		}
		t := time.Duration(in.TimeoutSec) * time.Second
		if t <= 0 || t > 15*time.Minute {
			t = 3 * time.Minute
		}
		return s.CLI.Run(ctx, t, in.Args...)
	}
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

const maxChunk = 1 << 20

// readChunk returns part of a file as base64 (callers loop until eof).
func readChunk(path string, offset, length int64) (map[string]any, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return nil, errors.New("not a file")
	}
	if length <= 0 || length > maxChunk {
		length = maxChunk
	}
	if offset < 0 || offset > st.Size() {
		return nil, errors.New("offset out of range")
	}
	buf := make([]byte, min(length, st.Size()-offset))
	n, err := f.ReadAt(buf, offset)
	if err != nil && n < len(buf) {
		return nil, err
	}
	return map[string]any{"data": base64.StdEncoding.EncodeToString(buf[:n]), "offset": offset, "size": st.Size(),
		"eof": offset+int64(n) >= st.Size(), "mtime": st.ModTime().UnixMilli(), "mime": mimeOf(path)}, nil
}

func mimeOf(path string) string {
	if t := mime.TypeByExtension(strings.ToLower(filepath.Ext(path))); t != "" {
		return t
	}
	return "application/octet-stream"
}
