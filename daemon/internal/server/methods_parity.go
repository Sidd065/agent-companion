package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"agentc/internal/core"
	"agentc/internal/fsops"
	"agentc/internal/github"
	"agentc/internal/projects"
	"agentc/internal/providers"
	"agentc/internal/scheduler"
	"agentc/internal/scripts"
	"agentc/internal/term"
)

// ProtocolVersion is advertised to clients for capability negotiation.
const ProtocolVersion = 2

// Capabilities lists the v2 feature families supported by this daemon.
var Capabilities = []string{
	"projects",
	"gitops",
	"worktrees",
	"checkpoints",
	"github_pr",
	"github_issues",
	"scripts",
	"providers_acp",
	"codex",
	"handoff",
	"inline_exec",
	"search",
	"usage",
	"schedules",
	"host_health",
	"settle_snooze",
	"remain_on_exit",
}

func (s *Server) registerParityMethods() {
	m := s.methods

	// Wire worktree creation hook to auto-run RunOnWorktreeCreate scripts.
	s.Mgr.OnWorktreeCreated = func(ctx context.Context, projectID, sessionID, wtPath string) {
		if s.Terms == nil || wtPath == "" {
			return
		}
		var custom []projects.ScriptDef
		if projectID != "" && s.Mgr.Projects != nil {
			if pr, ok := s.Mgr.Projects.Get(projectID); ok {
				custom = pr.Scripts
			}
		}
		for _, sc := range scripts.Discover(wtPath, custom) {
			if !sc.RunOnWorktreeCreate {
				continue
			}
			cwd := wtPath
			if sc.Cwd != "" {
				cwd = fsops.Resolve(sc.Cwd)
			}
			_, _ = s.Terms.Create(ctx, term.CreateOpts{
				Kind: "cmd", Cwd: cwd, Command: sc.Command, Name: "setup · " + sc.Name,
				SessionID: sessionID, ScriptID: sc.ID, RemainOnExit: true, Origin: "worktree-setup",
			})
			terms, _ := s.Terms.List(ctx)
			s.Mgr.Hub().Broadcast("terms.changed", terms)
		}
	}

	// ---- Projects ----
	m["projects.list"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		return s.Mgr.Projects.List(), nil
	}
	m["projects.add"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, err := bind[struct {
			Path string `json:"path"`
			Name string `json:"name"`
		}](p)
		if err != nil {
			return nil, err
		}
		pr, err := s.Mgr.Projects.Add(fsops.Resolve(in.Path), in.Name)
		if err != nil {
			return nil, err
		}
		s.Mgr.Hub().Broadcast("projects.changed", s.Mgr.Projects.List())
		return pr, nil
	}
	m["projects.init"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, err := bind[struct {
			ParentDir     string `json:"parentDir"`
			Name          string `json:"name"`
			InitialCommit *bool  `json:"initialCommit"`
		}](p)
		if err != nil {
			return nil, err
		}
		parent := fsops.Resolve(in.ParentDir)
		if in.ParentDir == "" {
			parent = fsops.Home
		}
		initCommit := true
		if in.InitialCommit != nil {
			initCommit = *in.InitialCommit
		}
		pr, err := s.Mgr.Projects.Init(ctx, parent, in.Name, initCommit)
		if err != nil {
			return nil, err
		}
		s.Mgr.Hub().Broadcast("projects.changed", s.Mgr.Projects.List())
		return pr, nil
	}
	m["projects.clone"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, err := bind[struct {
			URL       string `json:"url"`
			ParentDir string `json:"parentDir"`
			DirName   string `json:"dirName"`
		}](p)
		if err != nil {
			return nil, err
		}
		parent := fsops.Resolve(in.ParentDir)
		if in.ParentDir == "" {
			parent = fsops.Home
		}
		pr, err := s.Mgr.Projects.Clone(ctx, in.URL, parent, in.DirName)
		if err != nil {
			return nil, err
		}
		s.Mgr.Hub().Broadcast("projects.changed", s.Mgr.Projects.List())
		return pr, nil
	}
	m["projects.update"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, err := bind[struct {
			ID string `json:"id"`
			projects.UpdatePatch
		}](p)
		if err != nil {
			return nil, err
		}
		pr, err := s.Mgr.Projects.Update(in.ID, in.UpdatePatch)
		if err != nil {
			return nil, err
		}
		s.Mgr.Hub().Broadcast("projects.changed", s.Mgr.Projects.List())
		return pr, nil
	}
	m["projects.remove"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[idParam](p)
		ok, err := s.Mgr.Projects.Remove(in.ID)
		if err != nil {
			return nil, err
		}
		s.Mgr.Hub().Broadcast("projects.changed", s.Mgr.Projects.List())
		return map[string]any{"removed": ok}, nil
	}

	// ---- Full Git & Source Control ----
	m["git.status"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Path  string `json:"path"`
			Force bool   `json:"force"`
		}](p)
		return s.Mgr.Git.Status(ctx, fsops.Resolve(in.Path), in.Force)
	}
	m["git.branches"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Path string `json:"path"`
		}](p)
		return s.Mgr.Git.Branches(ctx, fsops.Resolve(in.Path))
	}
	m["git.checkout"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Path   string `json:"path"`
			Branch string `json:"branch"`
			Create bool   `json:"create"`
			Base   string `json:"base"`
		}](p)
		err := s.Mgr.Git.Checkout(ctx, fsops.Resolve(in.Path), in.Branch, in.Create, in.Base)
		return map[string]any{"ok": err == nil}, err
	}
	m["git.branch.delete"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Path   string `json:"path"`
			Branch string `json:"branch"`
			Force  bool   `json:"force"`
		}](p)
		err := s.Mgr.Git.DeleteBranch(ctx, fsops.Resolve(in.Path), in.Branch, in.Force)
		return map[string]any{"ok": err == nil}, err
	}
	m["git.stage"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Path  string   `json:"path"`
			Files []string `json:"files"`
		}](p)
		err := s.Mgr.Git.Stage(ctx, fsops.Resolve(in.Path), in.Files)
		return map[string]any{"ok": err == nil}, err
	}
	m["git.unstage"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Path  string   `json:"path"`
			Files []string `json:"files"`
		}](p)
		err := s.Mgr.Git.Unstage(ctx, fsops.Resolve(in.Path), in.Files)
		return map[string]any{"ok": err == nil}, err
	}
	m["git.discard"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Path  string   `json:"path"`
			Files []string `json:"files"`
		}](p)
		err := s.Mgr.Git.Discard(ctx, fsops.Resolve(in.Path), in.Files)
		return map[string]any{"ok": err == nil}, err
	}
	m["git.commit"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Path     string `json:"path"`
			Message  string `json:"message"`
			StageAll bool   `json:"stageAll"`
			Amend    bool   `json:"amend"`
		}](p)
		return s.Mgr.Git.Commit(ctx, fsops.Resolve(in.Path), in.Message, in.StageAll, in.Amend)
	}
	m["git.commitMessage"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Path string `json:"path"`
		}](p)
		msg, err := s.Mgr.GenerateCommitMessage(ctx, fsops.Resolve(in.Path))
		return map[string]any{"message": msg}, err
	}
	m["git.fetch"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Path string `json:"path"`
		}](p)
		out, err := s.Mgr.Git.Fetch(ctx, fsops.Resolve(in.Path))
		return map[string]any{"output": out}, err
	}
	m["git.pull"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Path   string `json:"path"`
			Rebase bool   `json:"rebase"`
		}](p)
		out, err := s.Mgr.Git.Pull(ctx, fsops.Resolve(in.Path), in.Rebase)
		return map[string]any{"output": out}, err
	}
	m["git.push"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Path           string `json:"path"`
			SetUpstream    bool   `json:"setUpstream"`
			ForceWithLease bool   `json:"forceWithLease"`
			AllowDefault   bool   `json:"allowDefault"`
		}](p)
		out, err := s.Mgr.Git.Push(ctx, fsops.Resolve(in.Path), in.SetUpstream, in.ForceWithLease, in.AllowDefault)
		return map[string]any{"output": out}, err
	}
	m["git.stash.list"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Path string `json:"path"`
		}](p)
		return s.Mgr.Git.StashList(ctx, fsops.Resolve(in.Path))
	}
	m["git.stash.push"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Path             string `json:"path"`
			Message          string `json:"message"`
			IncludeUntracked bool   `json:"includeUntracked"`
		}](p)
		err := s.Mgr.Git.StashPush(ctx, fsops.Resolve(in.Path), in.Message, in.IncludeUntracked)
		return map[string]any{"ok": err == nil}, err
	}
	m["git.stash.pop"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Path string `json:"path"`
			Ref  string `json:"ref"`
		}](p)
		err := s.Mgr.Git.StashPop(ctx, fsops.Resolve(in.Path), in.Ref)
		return map[string]any{"ok": err == nil}, err
	}
	m["git.stash.drop"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Path string `json:"path"`
			Ref  string `json:"ref"`
		}](p)
		err := s.Mgr.Git.StashDrop(ctx, fsops.Resolve(in.Path), in.Ref)
		return map[string]any{"ok": err == nil}, err
	}
	m["git.scopedDiff"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Path    string `json:"path"`
			Scope   string `json:"scope"`
			BaseRef string `json:"baseRef"`
			HeadRef string `json:"headRef"`
		}](p)
		return s.Mgr.Git.ScopedDiff(ctx, fsops.Resolve(in.Path), in.Scope, in.BaseRef, in.HeadRef)
	}

	// ---- Git Worktrees ----
	m["git.worktrees.list"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Path string `json:"path"`
		}](p)
		return s.Mgr.Git.WorktreeList(ctx, fsops.Resolve(in.Path))
	}
	m["git.worktrees.create"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Path       string `json:"path"`
			BaseBranch string `json:"baseBranch"`
			BranchName string `json:"branchName"`
		}](p)
		return s.Mgr.Git.WorktreeCreate(ctx, fsops.Resolve(in.Path), in.BaseBranch, in.BranchName, s.Mgr.Cfg().WorktreeDir)
	}
	m["git.worktrees.remove"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Path         string `json:"path"`
			WorktreePath string `json:"worktreePath"`
			Force        bool   `json:"force"`
			DeleteBranch bool   `json:"deleteBranch"`
		}](p)
		err := s.Mgr.Git.WorktreeRemove(ctx, fsops.Resolve(in.Path), fsops.Resolve(in.WorktreePath), in.Force, in.DeleteBranch)
		return map[string]any{"ok": err == nil}, err
	}
	m["git.worktrees.merge"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Path         string `json:"path"`
			SourceBranch string `json:"sourceBranch"`
			TargetBranch string `json:"targetBranch"`
			Squash       bool   `json:"squash"`
		}](p)
		out, err := s.Mgr.Git.WorktreeMerge(ctx, fsops.Resolve(in.Path), in.SourceBranch, in.TargetBranch, in.Squash)
		return map[string]any{"output": out}, err
	}

	// ---- GitHub PRs & Issues ----
	m["pr.list"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Path  string `json:"path"`
			State string `json:"state"`
			Limit int    `json:"limit"`
		}](p)
		return github.PRList(ctx, fsops.Resolve(in.Path), in.State, in.Limit)
	}
	m["pr.forBranch"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Path   string `json:"path"`
			Branch string `json:"branch"`
		}](p)
		pr, err := github.PRForBranch(ctx, fsops.Resolve(in.Path), in.Branch)
		return map[string]any{"pr": pr}, err
	}
	m["pr.get"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Path        string `json:"path"`
			Number      int    `json:"number"`
			IncludeDiff bool   `json:"includeDiff"`
		}](p)
		return github.PRGet(ctx, fsops.Resolve(in.Path), in.Number, in.IncludeDiff)
	}
	m["pr.create"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Path  string `json:"path"`
			Title string `json:"title"`
			Body  string `json:"body"`
			Base  string `json:"base"`
			Draft bool   `json:"draft"`
		}](p)
		return github.PRCreate(ctx, fsops.Resolve(in.Path), in.Title, in.Body, in.Base, in.Draft)
	}
	m["pr.comment"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Path   string `json:"path"`
			Number int    `json:"number"`
			Body   string `json:"body"`
		}](p)
		err := github.AddPRComment(ctx, fsops.Resolve(in.Path), in.Number, in.Body)
		return map[string]any{"ok": err == nil}, err
	}
	m["pr.review"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Path   string `json:"path"`
			Number int    `json:"number"`
			Event  string `json:"event"`
			Body   string `json:"body"`
		}](p)
		err := github.PRReview(ctx, fsops.Resolve(in.Path), in.Number, in.Event, in.Body)
		return map[string]any{"ok": err == nil}, err
	}
	m["pr.merge"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Path         string `json:"path"`
			Number       int    `json:"number"`
			Method       string `json:"method"`
			DeleteBranch bool   `json:"deleteBranch"`
		}](p)
		out, err := github.PRMerge(ctx, fsops.Resolve(in.Path), in.Number, in.Method, in.DeleteBranch)
		return map[string]any{"output": out}, err
	}
	m["pr.checkoutWorktree"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Path   string `json:"path"`
			Number int    `json:"number"`
		}](p)
		return github.PRCheckoutWorktree(ctx, s.Mgr.Git, fsops.Resolve(in.Path), in.Number, s.Mgr.Cfg().WorktreeDir)
	}
	m["issues.list"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Path  string `json:"path"`
			State string `json:"state"`
			Limit int    `json:"limit"`
		}](p)
		return github.IssuesList(ctx, fsops.Resolve(in.Path), in.State, in.Limit)
	}

	// ---- Project Scripts ----
	m["scripts.list"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Path      string `json:"path"`
			ProjectID string `json:"projectId"`
		}](p)
		dir := fsops.Resolve(in.Path)
		var custom []projects.ScriptDef
		if in.ProjectID != "" && s.Mgr.Projects != nil {
			if pr, ok := s.Mgr.Projects.Get(in.ProjectID); ok {
				custom = pr.Scripts
				if in.Path == "" {
					dir = pr.Path
				}
			}
		} else if dir != "" && s.Mgr.Projects != nil {
			if pr, ok := s.Mgr.Projects.FindByPath(dir); ok {
				custom = pr.Scripts
			}
		}
		return scripts.Discover(dir, custom), nil
	}
	m["scripts.run"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Path      string `json:"path"`
			Command   string `json:"command"`
			Name      string `json:"name"`
			ScriptID  string `json:"scriptId"`
			SessionID string `json:"sessionId"`
			Cols      int    `json:"cols"`
			Rows      int    `json:"rows"`
		}](p)
		if strings.TrimSpace(in.Command) == "" {
			return nil, errors.New("command is required")
		}
		t, err := s.Terms.Create(ctx, term.CreateOpts{
			Kind: "cmd", Cwd: fsops.Resolve(in.Path), Command: in.Command, Name: firstNonEmpty(in.Name, in.Command),
			SessionID: in.SessionID, ScriptID: in.ScriptID, RemainOnExit: true, Cols: in.Cols, Rows: in.Rows, Origin: "script",
		})
		if err == nil {
			terms, _ := s.Terms.List(ctx)
			s.Mgr.Hub().Broadcast("terms.changed", terms)
		}
		return t, err
	}

	// ---- Session Parity: Checkpoints, Settle/Snooze, Draft, Handoff, Inline Exec ----
	m["session.turnDiff"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			ID        string `json:"id"`
			Scope     string `json:"scope"`
			TurnIndex int    `json:"turnIndex"`
		}](p)
		return s.Mgr.TurnDiff(ctx, in.ID, in.Scope, in.TurnIndex)
	}
	m["session.checkpointRevert"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			ID        string `json:"id"`
			TurnIndex int    `json:"turnIndex"`
			ToPre     bool   `json:"toPre"`
		}](p)
		return s.Mgr.CheckpointRevert(ctx, in.ID, in.TurnIndex, in.ToPre)
	}
	m["session.settle"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			ID      string `json:"id"`
			Settled bool   `json:"settled"`
		}](p)
		err := s.Mgr.Settle(in.ID, in.Settled)
		return map[string]any{"ok": err == nil}, err
	}
	m["session.snooze"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			ID    string `json:"id"`
			Until int64  `json:"until"`
		}](p)
		err := s.Mgr.Snooze(in.ID, in.Until)
		return map[string]any{"ok": err == nil}, err
	}
	m["session.draft.set"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			ID    string `json:"id"`
			Draft string `json:"draft"`
		}](p)
		err := s.Mgr.SetDraft(in.ID, in.Draft)
		return map[string]any{"ok": err == nil}, err
	}
	m["session.handoff"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			ID          string `json:"id"`
			TargetAgent string `json:"targetAgent"`
			TargetModel string `json:"targetModel"`
			TargetMode  string `json:"targetMode"`
			Cwd         string `json:"cwd"`
			Prompt      string `json:"prompt"`
		}](p)
		ns, handoffText, err := s.Mgr.Handoff(ctx, in.ID, in.TargetAgent, in.TargetModel, in.TargetMode, in.Cwd, in.Prompt)
		if ns == nil && err != nil {
			return nil, err
		}
		res := map[string]any{"handoff": handoffText}
		if ns != nil {
			res["session"] = ns.Summary()
		}
		if err != nil {
			res["warning"] = err.Error()
		}
		return res, nil
	}
	m["session.exec"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			ID      string `json:"id"`
			Command string `json:"command"`
		}](p)
		ss, err := s.session(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		return ss.ExecInline(ctx, in.Command)
	}

	// ---- Providers Registry & Health ----
	m["providers.list"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		return map[string]any{
			"agents":  s.Mgr.Agents(),
			"configs": s.Mgr.Providers.List(),
		}, nil
	}
	m["providers.upsert"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, err := bind[providers.ProviderConfig](p)
		if err != nil {
			return nil, err
		}
		saved, err := s.Mgr.Providers.Upsert(in)
		if err != nil {
			return nil, err
		}
		s.Mgr.InvalidateAgentsCache()
		return saved, nil
	}
	m["providers.remove"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[idParam](p)
		if err := s.Mgr.Providers.Remove(in.ID); err != nil {
			return nil, err
		}
		s.Mgr.InvalidateAgentsCache()
		return map[string]any{"ok": true}, nil
	}
	m["providers.health"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[idParam](p)
		var out []providers.HealthResult
		for _, a := range s.Mgr.Agents() {
			if in.ID != "" && a.ID != in.ID {
				continue
			}
			env := s.Mgr.AgyEnv()
			if cfg, ok := s.Mgr.Providers.Get(a.ID); ok {
				env = providers.MergeEnv(env, cfg.Env)
			}
			out = append(out, providers.CheckHealth(ctx, a.ID, a.Driver, a.Command, env))
		}
		return out, nil
	}

	// ---- Schedules ----
	m["schedules.list"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		if s.Mgr.Scheduler == nil {
			return map[string]any{"tasks": []any{}, "runs": []any{}}, nil
		}
		tasks, runs := s.Mgr.Scheduler.List()
		return map[string]any{"tasks": tasks, "runs": runs}, nil
	}
	m["schedules.upsert"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		if s.Mgr.Scheduler == nil {
			return nil, errors.New("scheduler not initialized")
		}
		in, err := bind[scheduler.Task](p)
		if err != nil {
			return nil, err
		}
		in.Cwd = fsops.Resolve(in.Cwd)
		return s.Mgr.Scheduler.Upsert(in)
	}
	m["schedules.delete"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		if s.Mgr.Scheduler == nil {
			return nil, errors.New("scheduler not initialized")
		}
		in, _ := bind[idParam](p)
		ok, err := s.Mgr.Scheduler.Delete(in.ID)
		return map[string]any{"removed": ok}, err
	}
	m["schedules.runNow"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		if s.Mgr.Scheduler == nil {
			return nil, errors.New("scheduler not initialized")
		}
		in, _ := bind[idParam](p)
		return s.Mgr.Scheduler.RunNow(in.ID)
	}

	// ---- Search, Usage, Skills, Host Health ----
	m["search.query"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Query     string `json:"query"`
			ProjectID string `json:"projectId"`
			Limit     int    `json:"limit"`
		}](p)
		return s.Mgr.GlobalSearch(ctx, in.Query, in.ProjectID, in.Limit), nil
	}
	m["usage.summary"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Days int `json:"days"`
		}](p)
		return s.Mgr.Usage.Query(in.Days), nil
	}
	m["skills.list"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Cwd string `json:"cwd"`
		}](p)
		cwd := ""
		if in.Cwd != "" {
			cwd = fsops.Resolve(in.Cwd)
		}
		return s.Mgr.DiscoverSkills(cwd), nil
	}
	m["daemon.health"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		return s.Mgr.HostHealth(), nil
	}
	m["daemon.killProcess"] = func(ctx context.Context, c *core.Client, p json.RawMessage) (any, error) {
		in, _ := bind[struct {
			Pid int `json:"pid"`
		}](p)
		err := s.Mgr.KillManagedProcess(in.Pid)
		return map[string]any{"ok": err == nil}, err
	}
}
