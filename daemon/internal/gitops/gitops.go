// Package gitops provides singleflight-cached Git status, branching, staging,
// committing, remote sync (with default-branch push protection), stashes,
// structured scoped diffs, and Git worktree lifecycle management.
package gitops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// FileStatus represents one changed/untracked/conflicted path in git status.
type FileStatus struct {
	Path       string `json:"path"`
	OrigPath   string `json:"origPath,omitempty"`
	Staged     string `json:"staged,omitempty"`   // M|A|D|R|C|U|""
	Unstaged   string `json:"unstaged,omitempty"` // M|D|U|?|""
	Untracked  bool   `json:"untracked,omitempty"`
	Conflicted bool   `json:"conflicted,omitempty"`
	Additions  int    `json:"additions"`
	Deletions  int    `json:"deletions"`
}

// Status is a rich snapshot of a repository working tree.
type Status struct {
	IsRepo           bool         `json:"isRepo"`
	Root             string       `json:"root,omitempty"`
	Branch           string       `json:"branch,omitempty"`
	HeadSHA          string       `json:"headSha,omitempty"`
	Upstream         string       `json:"upstream,omitempty"`
	Ahead            int          `json:"ahead"`
	Behind           int          `json:"behind"`
	Detached         bool         `json:"detached,omitempty"`
	DefaultBranch    string       `json:"defaultBranch,omitempty"`
	IsDefaultBranch  bool         `json:"isDefaultBranch"`
	MergeInProgress  bool         `json:"mergeInProgress,omitempty"`
	RebaseInProgress bool         `json:"rebaseInProgress,omitempty"`
	StashCount       int          `json:"stashCount"`
	Files            []FileStatus `json:"files"`
	TotalAdd         int          `json:"totalAdd"`
	TotalDel         int          `json:"totalDel"`
	RemoteURL        string       `json:"remoteUrl,omitempty"`
	Forge            string       `json:"forge,omitempty"` // github | gitlab | other
	RepoSlug         string       `json:"repoSlug,omitempty"`
	CheckedAt        int64        `json:"checkedAt"`
}

// BranchInfo describes a local or remote git branch.
type BranchInfo struct {
	Name         string `json:"name"`
	Remote       bool   `json:"remote,omitempty"`
	Current      bool   `json:"current,omitempty"`
	Upstream     string `json:"upstream,omitempty"`
	SHA          string `json:"sha,omitempty"`
	Subject      string `json:"subject,omitempty"`
	WorktreePath string `json:"worktreePath,omitempty"`
	UpdatedAt    int64  `json:"updatedAt,omitempty"`
}

// WorktreeInfo describes one git worktree checkout.
type WorktreeInfo struct {
	Path       string `json:"path"`
	Branch     string `json:"branch,omitempty"`
	Head       string `json:"head,omitempty"`
	IsMain     bool   `json:"isMain,omitempty"`
	IsDetached bool   `json:"isDetached,omitempty"`
	Prunable   bool   `json:"prunable,omitempty"`
}

// StashEntry is one item from git stash list.
type StashEntry struct {
	Ref     string `json:"ref"`
	Message string `json:"message"`
	Date    string `json:"date,omitempty"`
}

// FileDiff is a structured diff for a single file.
type FileDiff struct {
	Path      string `json:"path"`
	OldPath   string `json:"oldPath,omitempty"`
	Status    string `json:"status"` // modified | added | deleted | renamed | untracked | binary
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Binary    bool   `json:"binary,omitempty"`
	Patch     string `json:"patch"`
}

// DiffResult is the response of a scoped diff query.
type DiffResult struct {
	Scope     string     `json:"scope"`
	BaseRef   string     `json:"baseRef,omitempty"`
	HeadRef   string     `json:"headRef,omitempty"`
	Files     []FileDiff `json:"files"`
	TotalAdd  int        `json:"totalAdd"`
	TotalDel  int        `json:"totalDel"`
	Truncated bool       `json:"truncated,omitempty"`
	RawDiff   string     `json:"rawDiff,omitempty"`
}

type statusCacheEntry struct {
	val Status
	at  time.Time
}

// Service runs git operations with singleflight + short TTL caching on Status
// to prevent git spawn storms.
type Service struct {
	mu       sync.Mutex
	cache    map[string]statusCacheEntry
	inflight map[string]*sfCall
}

type sfCall struct {
	done chan struct{}
	val  Status
	err  error
}

// New creates a gitops Service.
func New() *Service {
	return &Service{
		cache:    map[string]statusCacheEntry{},
		inflight: map[string]*sfCall{},
	}
}

// Invalidate clears cached status for dir (or all dirs if dir == "").
func (s *Service) Invalidate(dir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if dir == "" {
		s.cache = map[string]statusCacheEntry{}
		return
	}
	clean := filepath.Clean(dir)
	for k := range s.cache {
		if k == clean || strings.HasPrefix(k, clean+string(filepath.Separator)) || strings.HasPrefix(clean, k+string(filepath.Separator)) {
			delete(s.cache, k)
		}
	}
}

func runGit(ctx context.Context, dir string, timeout time.Duration, args ...string) (string, string, error) {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// Status returns a cached (2s TTL) or fresh GitStatus, coalescing concurrent calls.
func (s *Service) Status(ctx context.Context, dir string, force bool) (Status, error) {
	key := filepath.Clean(dir)
	s.mu.Lock()
	if !force {
		if ce, ok := s.cache[key]; ok && time.Since(ce.at) < 2*time.Second {
			s.mu.Unlock()
			return ce.val, nil
		}
	}
	if call, ok := s.inflight[key]; ok {
		s.mu.Unlock()
		<-call.done
		return call.val, call.err
	}
	call := &sfCall{done: make(chan struct{})}
	s.inflight[key] = call
	s.mu.Unlock()

	val, err := computeStatus(ctx, key)

	s.mu.Lock()
	if err == nil {
		s.cache[key] = statusCacheEntry{val: val, at: time.Now()}
	}
	delete(s.inflight, key)
	call.val, call.err = val, err
	close(call.done)
	s.mu.Unlock()
	return val, err
}

func computeStatus(ctx context.Context, dir string) (Status, error) {
	res := Status{Files: []FileStatus{}, CheckedAt: time.Now().UnixMilli()}
	rootOut, _, err := runGit(ctx, dir, 5*time.Second, "rev-parse", "--show-toplevel")
	if err != nil {
		return res, nil // not a git repo
	}
	res.IsRepo = true
	res.Root = strings.TrimSpace(rootOut)

	// Detect merge/rebase state from git-dir.
	if gdOut, _, err := runGit(ctx, dir, 5*time.Second, "rev-parse", "--git-dir"); err == nil {
		gd := strings.TrimSpace(gdOut)
		if !filepath.IsAbs(gd) {
			gd = filepath.Join(dir, gd)
		}
		if _, err := os.Stat(filepath.Join(gd, "MERGE_HEAD")); err == nil {
			res.MergeInProgress = true
		}
		if _, err := os.Stat(filepath.Join(gd, "rebase-merge")); err == nil {
			res.RebaseInProgress = true
		} else if _, err := os.Stat(filepath.Join(gd, "rebase-apply")); err == nil {
			res.RebaseInProgress = true
		}
	}

	// Single porcelain v2 call for branch, upstream, ahead/behind, and files!
	porcelain, _, err := runGit(ctx, dir, 10*time.Second, "status", "--porcelain=v2", "--branch", "-uall")
	if err != nil {
		return res, err
	}
	byPath := map[string]*FileStatus{}
	var order []string
	for _, line := range strings.Split(porcelain, "\n") {
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "# branch.oid "):
			oid := strings.TrimPrefix(line, "# branch.oid ")
			if oid != "(initial)" {
				res.HeadSHA = oid
			}
		case strings.HasPrefix(line, "# branch.head "):
			head := strings.TrimPrefix(line, "# branch.head ")
			if head == "(detached)" {
				res.Detached = true
				res.Branch = "HEAD"
			} else {
				res.Branch = head
			}
		case strings.HasPrefix(line, "# branch.upstream "):
			res.Upstream = strings.TrimPrefix(line, "# branch.upstream ")
		case strings.HasPrefix(line, "# branch.ab "):
			parts := strings.Fields(strings.TrimPrefix(line, "# branch.ab "))
			if len(parts) == 2 {
				res.Ahead, _ = strconv.Atoi(strings.TrimPrefix(parts[0], "+"))
				res.Behind, _ = strconv.Atoi(strings.TrimPrefix(parts[1], "-"))
			}
		case strings.HasPrefix(line, "1 "):
			// 1 XY sub mH mI mW hH hI path
			fields := strings.SplitN(line, " ", 9)
			if len(fields) == 9 {
				xy, path := fields[1], fields[8]
				fs := &FileStatus{Path: path}
				if xy[0] != '.' {
					fs.Staged = string(xy[0])
				}
				if xy[1] != '.' {
					fs.Unstaged = string(xy[1])
				}
				byPath[path] = fs
				order = append(order, path)
			}
		case strings.HasPrefix(line, "2 "):
			// 2 XY sub mH mI mW hH hI Xscore path\torigPath
			fields := strings.SplitN(line, " ", 10)
			if len(fields) == 10 {
				xy := fields[1]
				paths := strings.SplitN(fields[9], "\t", 2)
				path := paths[0]
				orig := ""
				if len(paths) == 2 {
					orig = paths[1]
				}
				fs := &FileStatus{Path: path, OrigPath: orig}
				if xy[0] != '.' {
					fs.Staged = string(xy[0])
				}
				if xy[1] != '.' {
					fs.Unstaged = string(xy[1])
				}
				byPath[path] = fs
				order = append(order, path)
			}
		case strings.HasPrefix(line, "u "):
			fields := strings.SplitN(line, " ", 11)
			if len(fields) == 11 {
				path := fields[10]
				fs := &FileStatus{Path: path, Staged: "U", Unstaged: "U", Conflicted: true}
				byPath[path] = fs
				order = append(order, path)
			}
		case strings.HasPrefix(line, "? "):
			path := strings.TrimPrefix(line, "? ")
			fs := &FileStatus{Path: path, Unstaged: "?", Untracked: true}
			byPath[path] = fs
			order = append(order, path)
		}
	}

	// Numstat for staged + unstaged additions/deletions.
	parseNumstat := func(out string) {
		for _, line := range strings.Split(out, "\n") {
			parts := strings.SplitN(line, "\t", 3)
			if len(parts) != 3 {
				continue
			}
			a, _ := strconv.Atoi(parts[0])
			d, _ := strconv.Atoi(parts[1])
			p := parts[2]
			if fs, ok := byPath[p]; ok {
				fs.Additions += a
				fs.Deletions += d
			}
		}
	}
	if out, _, err := runGit(ctx, dir, 8*time.Second, "diff", "--numstat"); err == nil {
		parseNumstat(out)
	}
	if out, _, err := runGit(ctx, dir, 8*time.Second, "diff", "--cached", "--numstat"); err == nil {
		parseNumstat(out)
	}

	for _, p := range order {
		fs := byPath[p]
		if fs.Untracked {
			// Quick line count for small untracked files (<= 256 KiB).
			full := filepath.Join(res.Root, fs.Path)
			if st, err := os.Stat(full); err == nil && !st.IsDir() && st.Size() <= 256*1024 {
				if b, err := os.ReadFile(full); err == nil && !bytes.ContainsRune(b, 0) {
					lines := bytes.Count(b, []byte("\n"))
					if len(b) > 0 && b[len(b)-1] != '\n' {
						lines++
					}
					fs.Additions = lines
				}
			}
		}
		res.TotalAdd += fs.Additions
		res.TotalDel += fs.Deletions
		res.Files = append(res.Files, *fs)
	}

	// Remote URL + default branch + forge detection.
	if remOut, _, err := runGit(ctx, dir, 5*time.Second, "remote", "get-url", "origin"); err == nil {
		res.RemoteURL = strings.TrimSpace(remOut)
		res.Forge, res.RepoSlug = parseRemote(res.RemoteURL)
	}
	res.DefaultBranch = detectDefaultBranch(ctx, dir)
	res.IsDefaultBranch = res.Branch != "" && (res.Branch == res.DefaultBranch || res.Branch == "main" || res.Branch == "master")

	if stOut, _, err := runGit(ctx, dir, 5*time.Second, "stash", "list"); err == nil {
		for _, l := range strings.Split(strings.TrimSpace(stOut), "\n") {
			if strings.TrimSpace(l) != "" {
				res.StashCount++
			}
		}
	}
	return res, nil
}

var sshRemoteRe = regexp.MustCompile(`^[^@]+@([^:]+):(.+)$`)

func parseRemote(raw string) (forge, slug string) {
	u := strings.TrimSpace(raw)
	u = strings.TrimSuffix(u, ".git")
	host := ""
	path := ""
	if strings.Contains(u, "://") {
		rest := u[strings.Index(u, "://")+3:]
		if idx := strings.IndexByte(rest, '/'); idx >= 0 {
			host = rest[:idx]
			path = rest[idx+1:]
		}
	} else if mm := sshRemoteRe.FindStringSubmatch(u); mm != nil {
		host = mm[1]
		path = mm[2]
	}
	hostLow := strings.ToLower(host)
	switch {
	case strings.Contains(hostLow, "github"):
		forge = "github"
	case strings.Contains(hostLow, "gitlab"):
		forge = "gitlab"
	case host != "":
		forge = "other"
	}
	slug = strings.Trim(path, "/")
	return forge, slug
}

func detectDefaultBranch(ctx context.Context, dir string) string {
	if out, _, err := runGit(ctx, dir, 5*time.Second, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil {
		b := strings.TrimSpace(out)
		b = strings.TrimPrefix(b, "origin/")
		if b != "" {
			return b
		}
	}
	for _, cand := range []string{"main", "master"} {
		if _, _, err := runGit(ctx, dir, 5*time.Second, "show-ref", "--verify", "--quiet", "refs/heads/"+cand); err == nil {
			return cand
		}
	}
	return "main"
}

// Branches returns local and remote branches plus worktree mapping.
func (s *Service) Branches(ctx context.Context, dir string) ([]BranchInfo, error) {
	wtMap := map[string]string{}
	if wts, err := s.WorktreeList(ctx, dir); err == nil {
		for _, w := range wts {
			if w.Branch != "" {
				wtMap[w.Branch] = w.Path
			}
		}
	}
	out, stderr, err := runGit(ctx, dir, 10*time.Second, "for-each-ref",
		"--sort=-committerdate",
		"--format=%(refname)\t%(HEAD)\t%(objectname:short)\t%(upstream:short)\t%(committerdate:unix)\t%(contents:subject)",
		"refs/heads", "refs/remotes")
	if err != nil {
		return nil, fmt.Errorf("git branches: %s", strings.TrimSpace(stderr))
	}
	var list []BranchInfo
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 6)
		if len(parts) < 6 {
			continue
		}
		ref := parts[0]
		if strings.HasSuffix(ref, "/HEAD") {
			continue
		}
		bi := BranchInfo{
			Current:  parts[1] == "*",
			SHA:      parts[2],
			Upstream: parts[3],
			Subject:  parts[5],
		}
		if sec, err := strconv.ParseInt(parts[4], 10, 64); err == nil {
			bi.UpdatedAt = sec * 1000
		}
		if strings.HasPrefix(ref, "refs/heads/") {
			bi.Name = strings.TrimPrefix(ref, "refs/heads/")
			bi.WorktreePath = wtMap[bi.Name]
		} else if strings.HasPrefix(ref, "refs/remotes/") {
			bi.Name = strings.TrimPrefix(ref, "refs/remotes/")
			bi.Remote = true
		} else {
			continue
		}
		list = append(list, bi)
	}
	return list, nil
}

// Checkout switches to branch (creating it from base if create is true).
func (s *Service) Checkout(ctx context.Context, dir, branch string, create bool, base string) error {
	defer s.Invalidate(dir)
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return errors.New("branch name required")
	}
	var args []string
	if create {
		args = []string{"checkout", "-b", branch}
		if strings.TrimSpace(base) != "" {
			args = append(args, strings.TrimSpace(base))
		}
	} else {
		args = []string{"checkout", branch}
	}
	_, stderr, err := runGit(ctx, dir, 30*time.Second, args...)
	if err != nil {
		return fmt.Errorf("git checkout: %s", strings.TrimSpace(stderr))
	}
	return nil
}

// DeleteBranch removes a local branch.
func (s *Service) DeleteBranch(ctx context.Context, dir, branch string, force bool) error {
	defer s.Invalidate(dir)
	flag := "-d"
	if force {
		flag = "-D"
	}
	_, stderr, err := runGit(ctx, dir, 15*time.Second, "branch", flag, branch)
	if err != nil {
		return fmt.Errorf("git branch delete: %s", strings.TrimSpace(stderr))
	}
	return nil
}

// Stage stages specific files or all changes when files is empty.
func (s *Service) Stage(ctx context.Context, dir string, files []string) error {
	defer s.Invalidate(dir)
	args := []string{"add"}
	if len(files) == 0 {
		args = append(args, "-A")
	} else {
		args = append(args, "--")
		args = append(args, files...)
	}
	_, stderr, err := runGit(ctx, dir, 20*time.Second, args...)
	if err != nil {
		return fmt.Errorf("git add: %s", strings.TrimSpace(stderr))
	}
	return nil
}

// Unstage unstages specific files or all staged changes when files is empty.
func (s *Service) Unstage(ctx context.Context, dir string, files []string) error {
	defer s.Invalidate(dir)
	args := []string{"reset", "HEAD"}
	if len(files) > 0 {
		args = append(args, "--")
		args = append(args, files...)
	}
	_, stderr, err := runGit(ctx, dir, 20*time.Second, args...)
	if err != nil {
		return fmt.Errorf("git reset: %s", strings.TrimSpace(stderr))
	}
	return nil
}

// Discard reverts unstaged working-tree changes (and removes specified untracked files).
func (s *Service) Discard(ctx context.Context, dir string, files []string) error {
	defer s.Invalidate(dir)
	if len(files) == 0 {
		_, stderr, err := runGit(ctx, dir, 20*time.Second, "checkout", "--", ".")
		if err != nil {
			return fmt.Errorf("git discard: %s", strings.TrimSpace(stderr))
		}
		return nil
	}
	// Check which files are untracked vs tracked.
	st, _ := s.Status(ctx, dir, true)
	untracked := map[string]bool{}
	for _, f := range st.Files {
		if f.Untracked {
			untracked[f.Path] = true
		}
	}
	var tracked []string
	for _, p := range files {
		if untracked[p] {
			_ = os.RemoveAll(filepath.Join(st.Root, p))
		} else {
			tracked = append(tracked, p)
		}
	}
	if len(tracked) > 0 {
		args := append([]string{"checkout", "--"}, tracked...)
		_, stderr, err := runGit(ctx, dir, 20*time.Second, args...)
		if err != nil {
			return fmt.Errorf("git discard: %s", strings.TrimSpace(stderr))
		}
	}
	return nil
}

// Commit commits staged changes (optionally staging all first or amending).
func (s *Service) Commit(ctx context.Context, dir, message string, stageAll, amend bool) (map[string]any, error) {
	defer s.Invalidate(dir)
	message = strings.TrimSpace(message)
	if message == "" && !amend {
		return nil, errors.New("commit message is required")
	}
	if stageAll {
		if err := s.Stage(ctx, dir, nil); err != nil {
			return nil, err
		}
	}
	args := []string{"commit"}
	if amend {
		args = append(args, "--amend")
		if message == "" {
			args = append(args, "--no-edit")
		}
	}
	if message != "" {
		args = append(args, "-m", message)
	}
	stdout, stderr, err := runGit(ctx, dir, 60*time.Second, args...)
	if err != nil {
		msg := strings.TrimSpace(stderr)
		if msg == "" {
			msg = strings.TrimSpace(stdout)
		}
		return nil, fmt.Errorf("git commit: %s", msg)
	}
	sha, _, _ := runGit(ctx, dir, 5*time.Second, "rev-parse", "--short", "HEAD")
	return map[string]any{"ok": true, "sha": strings.TrimSpace(sha), "output": strings.TrimSpace(stdout + "\n" + stderr)}, nil
}

// Fetch fetches from remotes.
func (s *Service) Fetch(ctx context.Context, dir string) (string, error) {
	defer s.Invalidate(dir)
	stdout, stderr, err := runGit(ctx, dir, 60*time.Second, "fetch", "--all", "--prune")
	if err != nil {
		return "", fmt.Errorf("git fetch: %s", strings.TrimSpace(stderr))
	}
	return strings.TrimSpace(stdout + "\n" + stderr), nil
}

// Pull pulls from upstream (ff-only or standard).
func (s *Service) Pull(ctx context.Context, dir string, rebase bool) (string, error) {
	defer s.Invalidate(dir)
	args := []string{"pull"}
	if rebase {
		args = append(args, "--rebase")
	}
	stdout, stderr, err := runGit(ctx, dir, 90*time.Second, args...)
	if err != nil {
		return "", fmt.Errorf("git pull: %s", strings.TrimSpace(stderr))
	}
	return strings.TrimSpace(stdout + "\n" + stderr), nil
}

// ErrDefaultBranchGuard is returned when pushing to main/master without explicit confirmation.
var ErrDefaultBranchGuard = errors.New("default_branch_push_guard")

// Push pushes current branch, enforcing default-branch push protection unless allowDefault is true.
func (s *Service) Push(ctx context.Context, dir string, setUpstream, forceWithLease, allowDefault bool) (string, error) {
	defer s.Invalidate(dir)
	st, err := s.Status(ctx, dir, true)
	if err != nil {
		return "", err
	}
	if !st.IsRepo {
		return "", errors.New("not a git repository")
	}
	if st.IsDefaultBranch && !allowDefault {
		return "", fmt.Errorf("%w: pushing directly to %q requires explicit confirmation (or checkout a feature branch)", ErrDefaultBranchGuard, st.Branch)
	}
	args := []string{"push"}
	if forceWithLease {
		args = append(args, "--force-with-lease")
	}
	if setUpstream || st.Upstream == "" {
		args = append(args, "-u", "origin", st.Branch)
	}
	stdout, stderr, err := runGit(ctx, dir, 90*time.Second, args...)
	if err != nil {
		return "", fmt.Errorf("git push: %s", strings.TrimSpace(stderr))
	}
	return strings.TrimSpace(stdout + "\n" + stderr), nil
}

// StashList lists all stashes.
func (s *Service) StashList(ctx context.Context, dir string) ([]StashEntry, error) {
	out, stderr, err := runGit(ctx, dir, 10*time.Second, "stash", "list", "--format=%gd\t%cr\t%gs")
	if err != nil {
		return nil, fmt.Errorf("git stash list: %s", strings.TrimSpace(stderr))
	}
	var list []StashEntry
	for _, l := range strings.Split(out, "\n") {
		if l == "" {
			continue
		}
		parts := strings.SplitN(l, "\t", 3)
		if len(parts) == 3 {
			list = append(list, StashEntry{Ref: parts[0], Date: parts[1], Message: parts[2]})
		}
	}
	return list, nil
}

// StashPush creates a new stash.
func (s *Service) StashPush(ctx context.Context, dir, message string, includeUntracked bool) error {
	defer s.Invalidate(dir)
	args := []string{"stash", "push"}
	if includeUntracked {
		args = append(args, "-u")
	}
	if strings.TrimSpace(message) != "" {
		args = append(args, "-m", strings.TrimSpace(message))
	}
	_, stderr, err := runGit(ctx, dir, 20*time.Second, args...)
	if err != nil {
		return fmt.Errorf("git stash push: %s", strings.TrimSpace(stderr))
	}
	return nil
}

// StashPop pops a stash entry.
func (s *Service) StashPop(ctx context.Context, dir, ref string) error {
	defer s.Invalidate(dir)
	args := []string{"stash", "pop"}
	if strings.TrimSpace(ref) != "" {
		args = append(args, ref)
	}
	_, stderr, err := runGit(ctx, dir, 20*time.Second, args...)
	if err != nil {
		return fmt.Errorf("git stash pop: %s", strings.TrimSpace(stderr))
	}
	return nil
}

// StashDrop deletes a stash entry.
func (s *Service) StashDrop(ctx context.Context, dir, ref string) error {
	defer s.Invalidate(dir)
	args := []string{"stash", "drop"}
	if strings.TrimSpace(ref) != "" {
		args = append(args, ref)
	}
	_, stderr, err := runGit(ctx, dir, 20*time.Second, args...)
	if err != nil {
		return fmt.Errorf("git stash drop: %s", strings.TrimSpace(stderr))
	}
	return nil
}

// ScopedDiff returns structured per-file diffs for scope: uncommitted|staged|unstaged|branch|commit|range.
func (s *Service) ScopedDiff(ctx context.Context, dir, scope, baseRef, headRef string) (DiffResult, error) {
	if scope == "" {
		scope = "uncommitted"
	}
	res := DiffResult{Scope: scope, BaseRef: baseRef, HeadRef: headRef, Files: []FileDiff{}}
	var raw string
	var err error
	switch scope {
	case "staged":
		raw, _, err = runGit(ctx, dir, 20*time.Second, "diff", "--cached", "--patch", "--no-color")
	case "unstaged":
		raw, _, err = runGit(ctx, dir, 20*time.Second, "diff", "--patch", "--no-color")
	case "uncommitted":
		// Diff working tree against HEAD (includes both staged and unstaged tracked changes).
		if _, _, headErr := runGit(ctx, dir, 5*time.Second, "rev-parse", "--verify", "HEAD"); headErr == nil {
			raw, _, err = runGit(ctx, dir, 20*time.Second, "diff", "HEAD", "--patch", "--no-color")
		} else {
			raw, _, err = runGit(ctx, dir, 20*time.Second, "diff", "--cached", "--patch", "--no-color")
		}
	case "branch":
		base := baseRef
		if base == "" {
			base = detectDefaultBranch(ctx, dir)
		}
		res.BaseRef = base
		if mb, _, mbErr := runGit(ctx, dir, 5*time.Second, "merge-base", base, "HEAD"); mbErr == nil {
			mb = strings.TrimSpace(mb)
			raw, _, err = runGit(ctx, dir, 20*time.Second, "diff", mb, "--patch", "--no-color")
		} else {
			raw, _, err = runGit(ctx, dir, 20*time.Second, "diff", base+"...HEAD", "--patch", "--no-color")
		}
	case "commit":
		if headRef == "" {
			headRef = "HEAD"
		}
		res.HeadRef = headRef
		raw, _, err = runGit(ctx, dir, 20*time.Second, "show", "--format=", "--patch", "--no-color", headRef)
	case "range":
		if baseRef == "" || headRef == "" {
			return res, errors.New("baseRef and headRef required for range scope")
		}
		raw, _, err = runGit(ctx, dir, 20*time.Second, "diff", baseRef, headRef, "--patch", "--no-color")
	default:
		return res, fmt.Errorf("unknown diff scope %q", scope)
	}
	if err != nil {
		return res, err
	}

	const maxRaw = 2 << 20 // 2 MiB safety bound
	if len(raw) > maxRaw {
		raw = raw[:maxRaw]
		res.Truncated = true
	}
	res.Files = ParseUnifiedDiff(raw)

	// Include untracked files in uncommitted / unstaged scopes!
	if scope == "uncommitted" || scope == "unstaged" {
		if st, err := s.Status(ctx, dir, false); err == nil {
			for _, f := range st.Files {
				if !f.Untracked {
					continue
				}
				fd := synthesizeUntrackedDiff(st.Root, f.Path)
				res.Files = append(res.Files, fd)
			}
		}
	}

	for _, f := range res.Files {
		res.TotalAdd += f.Additions
		res.TotalDel += f.Deletions
	}
	res.RawDiff = raw
	return res, nil
}

func synthesizeUntrackedDiff(root, relPath string) FileDiff {
	full := filepath.Join(root, relPath)
	st, err := os.Stat(full)
	if err != nil || st.IsDir() {
		return FileDiff{Path: relPath, Status: "untracked"}
	}
	if st.Size() > 256*1024 {
		return FileDiff{Path: relPath, Status: "untracked", Binary: true, Patch: "(file too large to preview)"}
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return FileDiff{Path: relPath, Status: "untracked"}
	}
	if bytes.ContainsRune(b, 0) {
		return FileDiff{Path: relPath, Status: "untracked", Binary: true, Patch: "Binary file"}
	}
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	if len(b) == 0 {
		lines = nil
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("diff --git a/%s b/%s\nnew file mode 100644\n--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n", relPath, relPath, relPath, len(lines)))
	for _, l := range lines {
		sb.WriteString("+" + l + "\n")
	}
	return FileDiff{Path: relPath, Status: "added", Additions: len(lines), Patch: sb.String()}
}

// ParseUnifiedDiff splits a multi-file git unified diff into structured FileDiff entries.
func ParseUnifiedDiff(raw string) []FileDiff {
	if strings.TrimSpace(raw) == "" {
		return []FileDiff{}
	}
	var out []FileDiff
	chunks := strings.Split("\n"+raw, "\ndiff --git ")
	for _, chunk := range chunks[1:] {
		full := "diff --git " + chunk
		lines := strings.Split(full, "\n")
		fd := FileDiff{Status: "modified", Patch: full}
		// Header: diff --git a/foo b/foo
		header := lines[0]
		if idx := strings.Index(header, " b/"); idx >= 0 {
			fd.Path = header[idx+3:]
		}
		for _, l := range lines[1:] {
			switch {
			case strings.HasPrefix(l, "new file mode"):
				fd.Status = "added"
			case strings.HasPrefix(l, "deleted file mode"):
				fd.Status = "deleted"
			case strings.HasPrefix(l, "rename from "):
				fd.Status = "renamed"
				fd.OldPath = strings.TrimPrefix(l, "rename from ")
			case strings.HasPrefix(l, "rename to "):
				fd.Path = strings.TrimPrefix(l, "rename to ")
			case strings.HasPrefix(l, "Binary files ") || strings.HasPrefix(l, "GIT binary patch"):
				fd.Binary = true
			case strings.HasPrefix(l, "+++ b/"):
				fd.Path = strings.TrimPrefix(l, "+++ b/")
			case strings.HasPrefix(l, "+") && !strings.HasPrefix(l, "+++"):
				fd.Additions++
			case strings.HasPrefix(l, "-") && !strings.HasPrefix(l, "---"):
				fd.Deletions++
			}
		}
		out = append(out, fd)
	}
	return out
}

// ---- Worktrees ----

// WorktreeList parses `git worktree list --porcelain`.
func (s *Service) WorktreeList(ctx context.Context, dir string) ([]WorktreeInfo, error) {
	out, stderr, err := runGit(ctx, dir, 10*time.Second, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("git worktree list: %s", strings.TrimSpace(stderr))
	}
	var list []WorktreeInfo
	var cur *WorktreeInfo
	flush := func() {
		if cur != nil && cur.Path != "" {
			if len(list) == 0 {
				cur.IsMain = true
			}
			list = append(list, *cur)
		}
		cur = nil
	}
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			flush()
			continue
		}
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			cur = &WorktreeInfo{Path: strings.TrimPrefix(line, "worktree ")}
		case cur == nil:
		case strings.HasPrefix(line, "HEAD "):
			cur.Head = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch "):
			cur.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		case line == "detached":
			cur.IsDetached = true
		case strings.HasPrefix(line, "prunable"):
			cur.Prunable = true
		}
	}
	flush()
	return list, nil
}

var slugRe = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// WorktreeCreate creates a new git worktree on a new or existing branch.
func (s *Service) WorktreeCreate(ctx context.Context, repoDir, baseBranch, branchName, worktreeRoot string) (WorktreeInfo, error) {
	defer s.Invalidate(repoDir)
	rootOut, stderr, err := runGit(ctx, repoDir, 10*time.Second, "rev-parse", "--show-toplevel")
	if err != nil {
		return WorktreeInfo{}, fmt.Errorf("not a git repo: %s", strings.TrimSpace(stderr))
	}
	root := strings.TrimSpace(rootOut)
	repoName := filepath.Base(root)

	branchName = strings.TrimSpace(branchName)
	if branchName == "" {
		branchName = fmt.Sprintf("agentc/%s", time.Now().Format("20060102-150405"))
	}
	slug := strings.Trim(slugRe.ReplaceAllString(strings.ReplaceAll(branchName, "/", "-"), "-"), "-")
	if slug == "" {
		slug = fmt.Sprintf("wt-%d", time.Now().Unix())
	}
	if worktreeRoot == "" {
		home, _ := os.UserHomeDir()
		worktreeRoot = filepath.Join(home, ".local", "share", "agentc", "worktrees")
	}
	wtDir := filepath.Join(worktreeRoot, repoName, slug)
	for i := 2; ; i++ {
		if _, err := os.Stat(wtDir); os.IsNotExist(err) {
			break
		}
		wtDir = filepath.Join(worktreeRoot, repoName, fmt.Sprintf("%s-%d", slug, i))
	}
	if err := os.MkdirAll(filepath.Dir(wtDir), 0o755); err != nil {
		return WorktreeInfo{}, err
	}

	// Check if branch already exists locally.
	_, _, existsErr := runGit(ctx, root, 5*time.Second, "show-ref", "--verify", "--quiet", "refs/heads/"+branchName)
	var args []string
	if existsErr == nil {
		args = []string{"worktree", "add", wtDir, branchName}
	} else {
		args = []string{"worktree", "add", "-b", branchName, wtDir}
		if strings.TrimSpace(baseBranch) != "" {
			args = append(args, strings.TrimSpace(baseBranch))
		}
	}
	_, stderr, err = runGit(ctx, root, 60*time.Second, args...)
	if err != nil {
		return WorktreeInfo{}, fmt.Errorf("git worktree add: %s", strings.TrimSpace(stderr))
	}
	head, _, _ := runGit(ctx, wtDir, 5*time.Second, "rev-parse", "HEAD")
	return WorktreeInfo{Path: wtDir, Branch: branchName, Head: strings.TrimSpace(head)}, nil
}

// WorktreeRemove removes a worktree and optionally deletes its branch.
func (s *Service) WorktreeRemove(ctx context.Context, repoDir, wtPath string, force, deleteBranch bool) error {
	defer s.Invalidate(repoDir)
	defer s.Invalidate(wtPath)
	var branch string
	if wts, err := s.WorktreeList(ctx, repoDir); err == nil {
		for _, w := range wts {
			if filepath.Clean(w.Path) == filepath.Clean(wtPath) {
				if w.IsMain {
					return errors.New("cannot remove the main worktree")
				}
				branch = w.Branch
			}
		}
	}
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, wtPath)
	_, stderr, err := runGit(ctx, repoDir, 30*time.Second, args...)
	if err != nil {
		return fmt.Errorf("git worktree remove: %s", strings.TrimSpace(stderr))
	}
	if deleteBranch && branch != "" {
		_ = s.DeleteBranch(ctx, repoDir, branch, force)
	}
	return nil
}

// WorktreeMerge merges sourceBranch into targetBranch inside repoDir.
func (s *Service) WorktreeMerge(ctx context.Context, repoDir, sourceBranch, targetBranch string, squash bool) (string, error) {
	defer s.Invalidate(repoDir)
	if strings.TrimSpace(sourceBranch) == "" {
		return "", errors.New("source branch is required")
	}
	if strings.TrimSpace(targetBranch) != "" {
		if err := s.Checkout(ctx, repoDir, targetBranch, false, ""); err != nil {
			return "", err
		}
	}
	args := []string{"merge"}
	if squash {
		args = append(args, "--squash")
	}
	args = append(args, sourceBranch)
	stdout, stderr, err := runGit(ctx, repoDir, 60*time.Second, args...)
	if err != nil {
		return "", fmt.Errorf("git merge: %s", strings.TrimSpace(stderr+"\n"+stdout))
	}
	return strings.TrimSpace(stdout + "\n" + stderr), nil
}
