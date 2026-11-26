// Package github wraps the authenticated local `gh` CLI to provide Pull Request
// and GitHub Issue workflows (list, view, checks, diff, create, comment, review,
// merge, checkout into worktree) without separate OAuth tokens.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"agentc/internal/gitops"
)

// PRCheck represents one CI status check on a pull request.
type PRCheck struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion,omitempty"`
	URL        string `json:"url,omitempty"`
}

// PRComment represents a review or issue comment on a PR.
type PRComment struct {
	ID        string `json:"id,omitempty"`
	Author    string `json:"author"`
	Body      string `json:"body"`
	Path      string `json:"path,omitempty"`
	Line      int    `json:"line,omitempty"`
	CreatedAt string `json:"createdAt"`
}

// PRFile represents a changed file in a PR.
type PRFile struct {
	Path      string `json:"path"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

// PullRequest is a normalized GitHub PR view.
type PullRequest struct {
	Number         int         `json:"number"`
	Title          string      `json:"title"`
	Body           string      `json:"body,omitempty"`
	State          string      `json:"state"` // OPEN | CLOSED | MERGED
	IsDraft        bool        `json:"isDraft"`
	URL            string      `json:"url"`
	Author         string      `json:"author"`
	HeadRefName    string      `json:"headRefName"`
	BaseRefName    string      `json:"baseRefName"`
	ReviewDecision string      `json:"reviewDecision,omitempty"`
	Mergeable      string      `json:"mergeable,omitempty"`
	Additions      int         `json:"additions"`
	Deletions      int         `json:"deletions"`
	ChangedFiles   int         `json:"changedFiles"`
	UpdatedAt      string      `json:"updatedAt"`
	CreatedAt      string      `json:"createdAt,omitempty"`
	Checks         []PRCheck   `json:"checks,omitempty"`
	Files          []PRFile    `json:"files,omitempty"`
	Comments       []PRComment `json:"comments,omitempty"`
	Diff           string      `json:"diff,omitempty"`
}

// Issue is a normalized GitHub Issue view.
type Issue struct {
	Number    int         `json:"number"`
	Title     string      `json:"title"`
	Body      string      `json:"body,omitempty"`
	State     string      `json:"state"`
	URL       string      `json:"url"`
	Author    string      `json:"author"`
	Labels    []string    `json:"labels,omitempty"`
	Assignees []string    `json:"assignees,omitempty"`
	UpdatedAt string      `json:"updatedAt"`
	CreatedAt string      `json:"createdAt,omitempty"`
	Comments  []PRComment `json:"comments,omitempty"`
}

func runGH(ctx context.Context, dir string, timeout time.Duration, args ...string) (string, error) {
	ghBin, err := exec.LookPath("gh")
	if err != nil {
		return "", errors.New("gh CLI not installed")
	}
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, ghBin, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	return stdout.String(), nil
}

type rawPR struct {
	Number         int                    `json:"number"`
	Title          string                 `json:"title"`
	Body           string                 `json:"body"`
	State          string                 `json:"state"`
	IsDraft        bool                   `json:"isDraft"`
	URL            string                 `json:"url"`
	Author         struct{ Login string } `json:"author"`
	HeadRefName    string                 `json:"headRefName"`
	BaseRefName    string                 `json:"baseRefName"`
	ReviewDecision string                 `json:"reviewDecision"`
	Mergeable      string                 `json:"mergeable"`
	Additions      int                    `json:"additions"`
	Deletions      int                    `json:"deletions"`
	ChangedFiles   int                    `json:"changedFiles"`
	UpdatedAt      string                 `json:"updatedAt"`
	CreatedAt      string                 `json:"createdAt"`
	Files          []PRFile               `json:"files"`
	Comments       []struct {
		ID        string                 `json:"id"`
		Author    struct{ Login string } `json:"author"`
		Body      string                 `json:"body"`
		CreatedAt string                 `json:"createdAt"`
	} `json:"comments"`
	StatusCheckRollup []struct {
		Name       string `json:"name"`
		Context    string `json:"context"`
		Status     string `json:"status"`
		State      string `json:"state"`
		Conclusion string `json:"conclusion"`
		DetailsURL string `json:"detailsUrl"`
		TargetURL  string `json:"targetUrl"`
	} `json:"statusCheckRollup"`
}

func (r rawPR) toPR() PullRequest {
	pr := PullRequest{
		Number: r.Number, Title: r.Title, Body: r.Body, State: r.State, IsDraft: r.IsDraft,
		URL: r.URL, Author: r.Author.Login, HeadRefName: r.HeadRefName, BaseRefName: r.BaseRefName,
		ReviewDecision: r.ReviewDecision, Mergeable: r.Mergeable, Additions: r.Additions,
		Deletions: r.Deletions, ChangedFiles: r.ChangedFiles, UpdatedAt: r.UpdatedAt,
		CreatedAt: r.CreatedAt, Files: r.Files,
	}
	for _, c := range r.StatusCheckRollup {
		name := c.Name
		if name == "" {
			name = c.Context
		}
		st := c.Status
		if st == "" {
			st = c.State
		}
		u := c.DetailsURL
		if u == "" {
			u = c.TargetURL
		}
		pr.Checks = append(pr.Checks, PRCheck{Name: name, Status: st, Conclusion: c.Conclusion, URL: u})
	}
	for _, cm := range r.Comments {
		pr.Comments = append(pr.Comments, PRComment{ID: cm.ID, Author: cm.Author.Login, Body: cm.Body, CreatedAt: cm.CreatedAt})
	}
	return pr
}

// PRList returns pull requests for the repo at dir.
func PRList(ctx context.Context, dir, state string, limit int) ([]PullRequest, error) {
	if state == "" {
		state = "open"
	}
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	fields := "number,title,state,isDraft,url,author,headRefName,baseRefName,reviewDecision,additions,deletions,changedFiles,updatedAt,statusCheckRollup"
	out, err := runGH(ctx, dir, 20*time.Second, "pr", "list", "--state", state, "--limit", strconv.Itoa(limit), "--json", fields)
	if err != nil {
		return nil, err
	}
	var raw []rawPR
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, err
	}
	res := make([]PullRequest, 0, len(raw))
	for _, r := range raw {
		res = append(res, r.toPR())
	}
	return res, nil
}

// PRForBranch returns the PR associated with the current (or given) branch, or nil if none.
func PRForBranch(ctx context.Context, dir, branch string) (*PullRequest, error) {
	fields := "number,title,body,state,isDraft,url,author,headRefName,baseRefName,reviewDecision,mergeable,additions,deletions,changedFiles,updatedAt,createdAt,statusCheckRollup,files,comments"
	args := []string{"pr", "view"}
	if strings.TrimSpace(branch) != "" {
		args = append(args, strings.TrimSpace(branch))
	}
	args = append(args, "--json", fields)
	out, err := runGH(ctx, dir, 20*time.Second, args...)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no pull requests found") {
			return nil, nil
		}
		return nil, err
	}
	var r rawPR
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		return nil, err
	}
	pr := r.toPR()
	return &pr, nil
}

// PRGet returns full PR details including optional diff.
func PRGet(ctx context.Context, dir string, number int, includeDiff bool) (PullRequest, error) {
	fields := "number,title,body,state,isDraft,url,author,headRefName,baseRefName,reviewDecision,mergeable,additions,deletions,changedFiles,updatedAt,createdAt,statusCheckRollup,files,comments"
	out, err := runGH(ctx, dir, 20*time.Second, "pr", "view", strconv.Itoa(number), "--json", fields)
	if err != nil {
		return PullRequest{}, err
	}
	var r rawPR
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		return PullRequest{}, err
	}
	pr := r.toPR()
	if includeDiff {
		if d, derr := runGH(ctx, dir, 25*time.Second, "pr", "diff", strconv.Itoa(number)); derr == nil {
			if len(d) > 2<<20 {
				d = d[:2<<20]
			}
			pr.Diff = d
		}
	}
	return pr, nil
}

// PRCreate creates a GitHub PR for the current branch.
func PRCreate(ctx context.Context, dir, title, body, base string, draft bool) (map[string]any, error) {
	args := []string{"pr", "create", "--title", title, "--body", body}
	if strings.TrimSpace(base) != "" {
		args = append(args, "--base", strings.TrimSpace(base))
	}
	if draft {
		args = append(args, "--draft")
	}
	out, err := runGH(ctx, dir, 30*time.Second, args...)
	if err != nil {
		return nil, err
	}
	return map[string]any{"url": strings.TrimSpace(out)}, nil
}

// PRComment adds a comment to a pull request.
func AddPRComment(ctx context.Context, dir string, number int, body string) error {
	_, err := runGH(ctx, dir, 20*time.Second, "pr", "comment", strconv.Itoa(number), "--body", body)
	return err
}

// PRReview submits a review (approve | comment | request-changes).
func PRReview(ctx context.Context, dir string, number int, event, body string) error {
	args := []string{"pr", "review", strconv.Itoa(number)}
	switch strings.ToLower(strings.TrimSpace(event)) {
	case "approve":
		args = append(args, "--approve")
	case "request-changes", "request_changes":
		args = append(args, "--request-changes")
	default:
		args = append(args, "--comment")
	}
	if strings.TrimSpace(body) != "" {
		args = append(args, "--body", body)
	}
	_, err := runGH(ctx, dir, 20*time.Second, args...)
	return err
}

// PRMerge merges a PR using merge|squash|rebase.
func PRMerge(ctx context.Context, dir string, number int, method string, deleteBranch bool) (string, error) {
	args := []string{"pr", "merge", strconv.Itoa(number)}
	switch method {
	case "squash":
		args = append(args, "--squash")
	case "rebase":
		args = append(args, "--rebase")
	default:
		args = append(args, "--merge")
	}
	if deleteBranch {
		args = append(args, "--delete-branch")
	}
	out, err := runGH(ctx, dir, 30*time.Second, args...)
	return strings.TrimSpace(out), err
}

// PRCheckoutWorktree fetches a PR ref into a dedicated git worktree.
func PRCheckoutWorktree(ctx context.Context, git *gitops.Service, dir string, number int, worktreeRoot string) (gitops.WorktreeInfo, error) {
	pr, err := PRGet(ctx, dir, number, false)
	if err != nil {
		return gitops.WorktreeInfo{}, err
	}
	branch := fmt.Sprintf("pr-%d-%s", number, pr.HeadRefName)
	// Fetch pull/<num>/head directly so forks work too!
	cmd := exec.CommandContext(ctx, "git", "fetch", "origin", fmt.Sprintf("pull/%d/head:%s", number, branch))
	cmd.Dir = dir
	if out, ferr := cmd.CombinedOutput(); ferr != nil {
		// Branch may already exist locally; try updating ref or using existing.
		if !strings.Contains(string(out), "already exists") {
			return gitops.WorktreeInfo{}, fmt.Errorf("fetch PR #%d: %s", number, strings.TrimSpace(string(out)))
		}
	}
	return git.WorktreeCreate(ctx, dir, "", branch, worktreeRoot)
}

// IssuesList lists GitHub issues for the repo at dir.
func IssuesList(ctx context.Context, dir, state string, limit int) ([]Issue, error) {
	if state == "" {
		state = "open"
	}
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	out, err := runGH(ctx, dir, 20*time.Second, "issue", "list", "--state", state, "--limit", strconv.Itoa(limit),
		"--json", "number,title,body,state,url,author,labels,assignees,updatedAt,createdAt")
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Number    int                      `json:"number"`
		Title     string                   `json:"title"`
		Body      string                   `json:"body"`
		State     string                   `json:"state"`
		URL       string                   `json:"url"`
		Author    struct{ Login string }   `json:"author"`
		Labels    []struct{ Name string }  `json:"labels"`
		Assignees []struct{ Login string } `json:"assignees"`
		UpdatedAt string                   `json:"updatedAt"`
		CreatedAt string                   `json:"createdAt"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, err
	}
	res := make([]Issue, 0, len(raw))
	for _, r := range raw {
		is := Issue{Number: r.Number, Title: r.Title, Body: r.Body, State: r.State, URL: r.URL, Author: r.Author.Login, UpdatedAt: r.UpdatedAt, CreatedAt: r.CreatedAt}
		for _, l := range r.Labels {
			is.Labels = append(is.Labels, l.Name)
		}
		for _, a := range r.Assignees {
			is.Assignees = append(is.Assignees, a.Login)
		}
		res = append(res, is)
	}
	return res, nil
}
