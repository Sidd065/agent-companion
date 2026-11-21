// Package checkpoints captures non-destructive per-turn workspace snapshots as
// hidden Git refs (`refs/agentc/checkpoints/<session>/<turn>`) using an
// ephemeral GIT_INDEX_FILE so the user's real index, staged files, and branches
// are never touched.
//
// Safety guards (learned from T3 Code #26227/#38102/#15124):
//   - Ephemeral index directory is always cleaned up via defer.
//   - Strict 8s timeout; never blocks or fails an agent turn.
//   - Unignored untracked files > 10 MiB or inside heavy build-cache directories
//     are skipped to prevent .git object bloat.
package checkpoints

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"agentc/internal/gitops"
)

// FileStat summarizes one file changed in a turn checkpoint.
type FileStat struct {
	Path      string `json:"path"`
	Status    string `json:"status"` // modified | added | deleted | renamed
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

// TurnCheckpoint records the pre/post snapshot refs and diff summary for a turn.
type TurnCheckpoint struct {
	TurnIndex    int        `json:"turnIndex"`
	PreRef       string     `json:"preRef,omitempty"`
	PostRef      string     `json:"postRef,omitempty"`
	PreSHA       string     `json:"preSha,omitempty"`
	PostSHA      string     `json:"postSha,omitempty"`
	Files        []FileStat `json:"files,omitempty"`
	TotalAdd     int        `json:"totalAdd"`
	TotalDel     int        `json:"totalDel"`
	SkippedLarge int        `json:"skippedLarge,omitempty"`
	CreatedAt    int64      `json:"createdAt"`
	Error        string     `json:"error,omitempty"`
}

const (
	maxUntrackedFileBytes = 10 << 20 // 10 MiB
	captureTimeout        = 8 * time.Second
)

var skipCacheDirs = map[string]bool{
	"node_modules": true, ".next": true, "target": true, "dist": true,
	"build": true, ".gradle": true, "__pycache__": true, ".venv": true,
	"venv": true, ".cache": true,
}

var refSafeRe = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func safeSessionKey(sessionID string) string {
	s := refSafeRe.ReplaceAllString(sessionID, "_")
	if s == "" {
		return "unknown"
	}
	return s
}

func refName(sessionID, label string) string {
	return fmt.Sprintf("refs/agentc/checkpoints/%s/%s", safeSessionKey(sessionID), label)
}

func runGitEnv(ctx context.Context, dir string, extraEnv []string, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	cmd.Env = append(cmd.Env, extraEnv...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// Capture takes a bounded, non-destructive working-tree snapshot into a hidden git ref.
// Returns ("", "", 0, nil) cleanly if dir is not a git repository.
func Capture(ctx context.Context, dir, sessionID, label string) (ref string, sha string, skipped int, err error) {
	cctx, cancel := context.WithTimeout(ctx, captureTimeout)
	defer cancel()

	rootOut, _, err := runGitEnv(cctx, dir, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", "", 0, nil // non-git workspace: no-op
	}
	root := strings.TrimSpace(rootOut)

	tmpDir, err := os.MkdirTemp("", "agentc-cp-*")
	if err != nil {
		return "", "", 0, err
	}
	defer os.RemoveAll(tmpDir)

	idxFile := filepath.Join(tmpDir, "index")
	env := []string{"GIT_INDEX_FILE=" + idxFile}

	// Seed temp index from HEAD if HEAD exists (handles brand-new unborn repos too).
	hasHead := false
	headSHA := ""
	if out, _, herr := runGitEnv(cctx, root, nil, "rev-parse", "--verify", "HEAD"); herr == nil {
		hasHead = true
		headSHA = strings.TrimSpace(out)
		if _, stderr, rerr := runGitEnv(cctx, root, env, "read-tree", "HEAD"); rerr != nil {
			return "", "", 0, fmt.Errorf("read-tree HEAD: %s", strings.TrimSpace(stderr))
		}
	}

	// Stage all tracked modifications and deletions safely into the temp index.
	if hasHead {
		if _, stderr, uerr := runGitEnv(cctx, root, env, "add", "-u"); uerr != nil {
			return "", "", 0, fmt.Errorf("add -u: %s", strings.TrimSpace(stderr))
		}
	}

	// Enumerate non-ignored untracked files and filter out huge files / build caches.
	untrackedOut, _, _ := runGitEnv(cctx, root, nil, "ls-files", "--others", "--exclude-standard", "-z")
	if untrackedOut != "" {
		var toAdd []string
		for _, rel := range strings.Split(untrackedOut, "\x00") {
			if rel == "" {
				continue
			}
			if isSkippedPath(rel) {
				skipped++
				continue
			}
			full := filepath.Join(root, rel)
			st, serr := os.Lstat(full)
			if serr != nil || st.IsDir() {
				continue
			}
			if st.Size() > maxUntrackedFileBytes {
				skipped++
				continue
			}
			toAdd = append(toAdd, rel)
		}
		// Add in batches to avoid ARG_MAX.
		const batch = 200
		for i := 0; i < len(toAdd); i += batch {
			end := min(i+batch, len(toAdd))
			args := append([]string{"add", "--"}, toAdd[i:end]...)
			_, _, _ = runGitEnv(cctx, root, env, args...)
		}
	}

	treeOut, stderr, err := runGitEnv(cctx, root, env, "write-tree")
	if err != nil {
		return "", "", skipped, fmt.Errorf("write-tree: %s", strings.TrimSpace(stderr))
	}
	treeSHA := strings.TrimSpace(treeOut)

	commitArgs := []string{"commit-tree", treeSHA, "-m", fmt.Sprintf("agentc checkpoint %s %s", sessionID, label)}
	if hasHead && headSHA != "" {
		commitArgs = append(commitArgs, "-p", headSHA)
	}
	commitEnv := append(env,
		"GIT_AUTHOR_NAME=agentc", "GIT_AUTHOR_EMAIL=agentc@local",
		"GIT_COMMITTER_NAME=agentc", "GIT_COMMITTER_EMAIL=agentc@local",
	)
	commitOut, stderr, err := runGitEnv(cctx, root, commitEnv, commitArgs...)
	if err != nil {
		return "", "", skipped, fmt.Errorf("commit-tree: %s", strings.TrimSpace(stderr))
	}
	sha = strings.TrimSpace(commitOut)
	ref = refName(sessionID, label)
	if _, stderr, err := runGitEnv(cctx, root, nil, "update-ref", ref, sha); err != nil {
		return "", "", skipped, fmt.Errorf("update-ref: %s", strings.TrimSpace(stderr))
	}
	return ref, sha, skipped, nil
}

func isSkippedPath(rel string) bool {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for _, p := range parts {
		if skipCacheDirs[p] {
			return true
		}
	}
	return false
}

// SummarizeDiff computes file stats (+add -del) between two checkpoint refs/SHAs.
func SummarizeDiff(ctx context.Context, dir, fromRef, toRef string) (files []FileStat, totalAdd, totalDel int, err error) {
	if fromRef == "" || toRef == "" || fromRef == toRef {
		return nil, 0, 0, nil
	}
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	statusOut, _, err := runGitEnv(cctx, dir, nil, "diff", "--name-status", fromRef, toRef)
	if err != nil {
		return nil, 0, 0, err
	}
	statusMap := map[string]string{}
	for _, l := range strings.Split(statusOut, "\n") {
		if l == "" {
			continue
		}
		parts := strings.Split(l, "\t")
		if len(parts) < 2 {
			continue
		}
		code := parts[0]
		path := parts[len(parts)-1]
		switch {
		case strings.HasPrefix(code, "A"):
			statusMap[path] = "added"
		case strings.HasPrefix(code, "D"):
			statusMap[path] = "deleted"
		case strings.HasPrefix(code, "R"):
			statusMap[path] = "renamed"
		default:
			statusMap[path] = "modified"
		}
	}

	numOut, _, err := runGitEnv(cctx, dir, nil, "diff", "--numstat", fromRef, toRef)
	if err != nil {
		return nil, 0, 0, err
	}
	for _, l := range strings.Split(numOut, "\n") {
		if l == "" {
			continue
		}
		parts := strings.SplitN(l, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		a, _ := strconv.Atoi(parts[0])
		d, _ := strconv.Atoi(parts[1])
		p := parts[2]
		st := statusMap[p]
		if st == "" {
			st = "modified"
		}
		files = append(files, FileStat{Path: p, Status: st, Additions: a, Deletions: d})
		totalAdd += a
		totalDel += d
	}
	return files, totalAdd, totalDel, nil
}

// Diff returns the structured DiffResult between two checkpoint refs/SHAs.
func Diff(ctx context.Context, dir, scope, fromRef, toRef string) (gitops.DiffResult, error) {
	res := gitops.DiffResult{Scope: scope, BaseRef: fromRef, HeadRef: toRef, Files: []gitops.FileDiff{}}
	if fromRef == "" || toRef == "" || fromRef == toRef {
		return res, nil
	}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	raw, stderr, err := runGitEnv(cctx, dir, nil, "diff", "--patch", "--no-color", fromRef, toRef)
	if err != nil {
		return res, fmt.Errorf("checkpoint diff: %s", strings.TrimSpace(stderr))
	}
	const maxRaw = 2 << 20
	if len(raw) > maxRaw {
		raw = raw[:maxRaw]
		res.Truncated = true
	}
	res.Files = gitops.ParseUnifiedDiff(raw)
	for _, f := range res.Files {
		res.TotalAdd += f.Additions
		res.TotalDel += f.Deletions
	}
	res.RawDiff = raw
	return res, nil
}

// Revert restores the working tree to targetRef after first saving a safety backup snapshot.
func Revert(ctx context.Context, dir, sessionID, targetRef string) (backupRef string, err error) {
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	rootOut, stderr, err := runGitEnv(cctx, dir, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("not a git repository: %s", strings.TrimSpace(stderr))
	}
	root := strings.TrimSpace(rootOut)

	// Save a safety snapshot before touching any file!
	backupRef, _, _, _ = Capture(cctx, root, sessionID, fmt.Sprintf("pre-revert-%d", time.Now().Unix()))

	// Compute files present now vs targetRef so files created AFTER targetRef are cleanly removed.
	curRef, _, _, err := Capture(cctx, root, sessionID, fmt.Sprintf("revert-cur-%d", time.Now().UnixNano()))
	if err == nil && curRef != "" {
		defer DeleteRef(context.Background(), root, curRef)
		nameStatus, _, _ := runGitEnv(cctx, root, nil, "diff", "--name-status", targetRef, curRef)
		for _, line := range strings.Split(nameStatus, "\n") {
			parts := strings.Split(line, "\t")
			if len(parts) >= 2 && strings.HasPrefix(parts[0], "A") {
				// File was added after targetRef -> remove it.
				_ = os.Remove(filepath.Join(root, parts[1]))
			}
		}
	}

	// Restore all files from targetRef tree without altering HEAD or user's index.
	tmpDir, err := os.MkdirTemp("", "agentc-rev-*")
	if err != nil {
		return backupRef, err
	}
	defer os.RemoveAll(tmpDir)
	env := []string{"GIT_INDEX_FILE=" + filepath.Join(tmpDir, "index")}
	if _, stderr, err := runGitEnv(cctx, root, env, "read-tree", targetRef); err != nil {
		return backupRef, fmt.Errorf("read-tree target: %s", strings.TrimSpace(stderr))
	}
	if _, stderr, err := runGitEnv(cctx, root, env, "checkout-index", "-a", "-f"); err != nil {
		return backupRef, fmt.Errorf("checkout-index: %s", strings.TrimSpace(stderr))
	}
	return backupRef, nil
}

// DeleteRef deletes a single hidden checkpoint ref.
func DeleteRef(ctx context.Context, dir, ref string) {
	if ref == "" {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, _, _ = runGitEnv(cctx, dir, nil, "update-ref", "-d", ref)
}

// Cleanup removes all hidden checkpoint refs for sessionID.
func Cleanup(ctx context.Context, dir, sessionID string) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	prefix := fmt.Sprintf("refs/agentc/checkpoints/%s/", safeSessionKey(sessionID))
	out, _, err := runGitEnv(cctx, dir, nil, "for-each-ref", "--format=%(refname)", prefix)
	if err != nil {
		return
	}
	for _, ref := range strings.Split(out, "\n") {
		if r := strings.TrimSpace(ref); r != "" {
			_, _, _ = runGitEnv(cctx, dir, nil, "update-ref", "-d", r)
		}
	}
}
