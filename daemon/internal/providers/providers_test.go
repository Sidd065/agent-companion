package providers

import (
	"context"
	"path/filepath"
	"testing"
)

func TestStoreUpsertReloadAndHealth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "providers.json")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.Upsert(ProviderConfig{ID: "my-acp", Name: "My ACP", Driver: "acp", Enabled: true, Command: "sh", Args: []string{"-c", "echo ok"}})
	if err != nil || p.ID != "my-acp" {
		t.Fatalf("Upsert: %v %+v", err, p)
	}
	if err := st.Remove("claude"); err == nil {
		t.Fatal("expected error deleting built-in provider")
	}
	st2, err := Open(path)
	if err != nil || len(st2.List()) != 1 {
		t.Fatalf("reload: %v %+v", err, st2.List())
	}
	hr := CheckHealth(context.Background(), "my-acp", "acp", "sh", nil)
	if !hr.Available {
		t.Fatalf("expected sh available: %+v", hr)
	}
	if err := st2.Remove("my-acp"); err != nil {
		t.Fatal(err)
	}
}
