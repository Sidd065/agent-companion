package gitops

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, string(out))
		}
	}
	run("init", "-b", "main")
	run("config", "user.name", "Test")
	run("config", "user.email", "test@example.com")
	_ = os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("line1\nline2\n"), 0o644)
	run("add", "-A")
	run("commit", "-m", "initial")
	return dir
}

func TestGitOpsStatusCommitBranchWorktreeAndGuard(t *testing.T) {
	ctx := context.Background()
	dir := initTestRepo(t)
	svc := New()

	// Modify tracked file + add untracked file.
	_ = os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("line1\nline2-mod\nline3\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "new.txt"), []byte("alpha\nbeta\n"), 0o644)

	st, err := svc.Status(ctx, dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if !st.IsRepo || st.Branch != "main" || !st.IsDefaultBranch {
		t.Fatalf("unexpected status: %+v", st)
	}
	if len(st.Files) != 2 || st.TotalAdd < 3 {
		t.Fatalf("expected 2 changed files, got %+v", st.Files)
	}

	// Default-branch push guard must refuse pushing to main without allowDefault.
	_, err = svc.Push(ctx, dir, false, false, false)
	if !errors.Is(err, ErrDefaultBranchGuard) {
		t.Fatalf("expected ErrDefaultBranchGuard, got %v", err)
	}

	// Scoped diff includes both modified and untracked files.
	diff, err := svc.ScopedDiff(ctx, dir, "uncommitted", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Files) != 2 {
		t.Fatalf("expected 2 diff files, got %d", len(diff.Files))
	}

	// Stage & commit.
	if _, err := svc.Commit(ctx, dir, "add new file", true, false); err != nil {
		t.Fatal(err)
	}
	st2, err := svc.Status(ctx, dir, true)
	if err != nil || len(st2.Files) != 0 {
		t.Fatalf("expected clean tree after commit, got %+v err=%v", st2.Files, err)
	}

	// Worktree create + edit + commit + merge + remove.
	wtRoot := t.TempDir()
	wt, err := svc.WorktreeCreate(ctx, dir, "main", "feat/calc", wtRoot)
	if err != nil {
		t.Fatalf("WorktreeCreate: %v", err)
	}
	if wt.Branch != "feat/calc" || !strings.HasPrefix(wt.Path, wtRoot) {
		t.Fatalf("unexpected worktree: %+v", wt)
	}
	_ = os.WriteFile(filepath.Join(wt.Path, "calc.txt"), []byte("42\n"), 0o644)
	if _, err := svc.Commit(ctx, wt.Path, "add calc", true, false); err != nil {
		t.Fatal(err)
	}
	wts, err := svc.WorktreeList(ctx, dir)
	if err != nil || len(wts) != 2 {
		t.Fatalf("expected 2 worktrees, got %+v err=%v", wts, err)
	}
	if _, err := svc.WorktreeMerge(ctx, dir, "feat/calc", "main", false); err != nil {
		t.Fatalf("WorktreeMerge: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "calc.txt")); err != nil {
		t.Fatalf("merged file missing in main checkout: %v", err)
	}
	if err := svc.WorktreeRemove(ctx, dir, wt.Path, false, true); err != nil {
		t.Fatalf("WorktreeRemove: %v", err)
	}
}
