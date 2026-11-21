package checkpoints

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCheckpointCaptureDiffAndRevertPreservesUserIndex(t *testing.T) {
	ctx := context.Background()
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
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644)
	run("add", "-A")
	run("commit", "-m", "init")

	// User stages a change in their own index before turn starts — checkpoint must NOT clobber it!
	_ = os.WriteFile(filepath.Join(dir, "staged.txt"), []byte("keep staged\n"), 0o644)
	run("add", "staged.txt")

	preRef, _, _, err := Capture(ctx, dir, "sess-1", "t1-pre")
	if err != nil || preRef == "" {
		t.Fatalf("pre capture: ref=%q err=%v", preRef, err)
	}

	// Agent edits a.txt, adds new.txt, AND creates an unignored cache file inside node_modules (should be skipped).
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\nworld\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "new.txt"), []byte("brand new\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(dir, "node_modules", "pkg"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "node_modules", "pkg", "x.js"), []byte("ignored cache"), 0o644)

	postRef, _, skipped, err := Capture(ctx, dir, "sess-1", "t1-post")
	if err != nil || postRef == "" {
		t.Fatalf("post capture: ref=%q err=%v", postRef, err)
	}
	if skipped != 1 {
		t.Fatalf("expected 1 skipped node_modules file, got %d", skipped)
	}

	// Verify user's staged file is still staged and new.txt is NOT staged in user's index!
	cachedOut, _ := exec.Command("git", "-C", dir, "diff", "--cached", "--name-only").Output()
	if string(cachedOut) != "staged.txt\n" {
		t.Fatalf("user git index was polluted: %q", string(cachedOut))
	}

	files, add, del, err := SummarizeDiff(ctx, dir, preRef, postRef)
	if err != nil || len(files) != 2 || add != 2 || del != 0 {
		t.Fatalf("SummarizeDiff: files=%+v add=%d del=%d err=%v", files, add, del, err)
	}

	diffRes, err := Diff(ctx, dir, "turn", preRef, postRef)
	if err != nil || len(diffRes.Files) != 2 {
		t.Fatalf("Diff: %+v err=%v", diffRes, err)
	}

	// Revert back to preRef: a.txt restored, new.txt deleted!
	backup, err := Revert(ctx, dir, "sess-1", preRef)
	if err != nil || backup == "" {
		t.Fatalf("Revert: backup=%q err=%v", backup, err)
	}
	gotA, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
	if string(gotA) != "hello\n" {
		t.Fatalf("a.txt not reverted: %q", string(gotA))
	}
	if _, err := os.Stat(filepath.Join(dir, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("new.txt should have been removed on revert")
	}

	Cleanup(ctx, dir, "sess-1")
}
