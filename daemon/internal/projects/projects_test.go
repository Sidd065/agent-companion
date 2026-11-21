package projects

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestStoreLifecycleAndInit(t *testing.T) {
	tmp := t.TempDir()
	storePath := filepath.Join(tmp, "projects.json")
	st, err := Open(storePath)
	if err != nil {
		t.Fatal(err)
	}
	proj, err := st.Init(context.Background(), tmp, "demo-app", true)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if !proj.IsGit || proj.Name != "demo-app" {
		t.Fatalf("unexpected project: %+v", proj)
	}
	sub := filepath.Join(proj.Path, "src", "pkg")
	_ = os.MkdirAll(sub, 0o755)
	found, ok := st.FindByPath(sub)
	if !ok || found.ID != proj.ID {
		t.Fatalf("FindByPath failed: ok=%v found=%+v", ok, found)
	}
	pinned := true
	upd, err := st.Update(proj.ID, UpdatePatch{Pinned: &pinned})
	if err != nil || !upd.Pinned {
		t.Fatalf("Update: %v %+v", err, upd)
	}
	// Reload from disk to verify atomic persistence.
	st2, err := Open(storePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(st2.List()) != 1 || !st2.List()[0].Pinned {
		t.Fatalf("reloaded projects mismatch: %+v", st2.List())
	}
	removed, err := st2.Remove(proj.ID)
	if err != nil || !removed || len(st2.List()) != 0 {
		t.Fatalf("Remove failed: removed=%v err=%v", removed, err)
	}
}
