package fsops

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveAndFileLifecycle(t *testing.T) {
	dir := t.TempDir()
	origHome := Home
	Home = dir
	t.Cleanup(func() { Home = origHome })

	if got := Resolve("~"); got != dir {
		t.Fatalf("Resolve(~) = %q, want %q", got, dir)
	}
	if got := Resolve("~/sub/file.txt"); got != filepath.Join(dir, "sub/file.txt") {
		t.Fatalf("Resolve(~/sub/file.txt) = %q", got)
	}
	if got := Resolve("file://" + filepath.Join(dir, "a.txt")); got != filepath.Join(dir, "a.txt") {
		t.Fatalf("Resolve(file://...) = %q", got)
	}

	// Write text with CreateDirs
	e, err := Write(WriteParams{
		Path:       "notes/hello.txt",
		Text:       "hello world\n",
		CreateDirs: true,
	})
	if err != nil {
		t.Fatalf("Write text: %v", err)
	}
	if e.Name != "hello.txt" || e.Type != "file" || e.Size != 12 {
		t.Fatalf("unexpected entry: %+v", e)
	}

	// NoOverwrite rejects existing file
	if _, err := Write(WriteParams{Path: "notes/hello.txt", Text: "x", NoOverwrite: true}); err == nil {
		t.Fatalf("expected error on NoOverwrite")
	}

	// Conflict detection on mismatched ExpectMtime
	if _, err := Write(WriteParams{Path: "notes/hello.txt", Text: "x", ExpectMtime: e.Mtime - 1000}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
	}

	// Read text + truncated read
	r, err := Read("notes/hello.txt", 1024)
	if err != nil || r.Binary || r.Truncated || r.Text != "hello world\n" {
		t.Fatalf("unexpected Read text result: %+v, err=%v", r, err)
	}
	rt, err := Read("notes/hello.txt", 5)
	if err != nil || !rt.Truncated || rt.Text != "hello" {
		t.Fatalf("unexpected truncated Read result: %+v, err=%v", rt, err)
	}

	// Read directory fails cleanly
	if _, err := Read("notes", 1024); err == nil {
		t.Fatalf("expected error reading directory")
	}

	// Write binary via base64 and read back
	raw := []byte{0x00, 0xFF, 0x01, 0x7F, 0xDE, 0xAD, 0xBE, 0xEF}
	if _, err := Write(WriteParams{
		Path:   "notes/blob.bin",
		Base64: base64.StdEncoding.EncodeToString(raw),
	}); err != nil {
		t.Fatalf("Write binary: %v", err)
	}
	rb, err := Read("notes/blob.bin", 1024)
	if err != nil || !rb.Binary {
		t.Fatalf("expected binary read, got %+v, err=%v", rb, err)
	}
	dec, err := base64.StdEncoding.DecodeString(rb.Base64)
	if err != nil || string(dec) != string(raw) {
		t.Fatalf("binary round-trip mismatch")
	}

	// Hidden file + List filtering
	if _, err := Create("notes/.secret"); err != nil {
		t.Fatalf("Create hidden: %v", err)
	}
	if _, err := Mkdir("notes/subdir", false); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	listedNoHidden, err := List("notes", false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	entries := listedNoHidden["entries"].([]Entry)
	if len(entries) != 3 || entries[0].Type != "dir" {
		t.Fatalf("expected dirs-first 3 visible entries, got %+v", entries)
	}
	listedAll, err := List("notes", true)
	if err != nil {
		t.Fatalf("List hidden: %v", err)
	}
	if len(listedAll["entries"].([]Entry)) != 4 {
		t.Fatalf("expected 4 entries with hidden, got %d", len(listedAll["entries"].([]Entry)))
	}

	// Copy non-overwrite auto-names copy, Rename moves into subdir
	cp, err := Copy("notes/hello.txt", "notes/hello.txt", false)
	if err != nil || cp.Name != "hello copy.txt" {
		t.Fatalf("unexpected Copy entry: %+v, err=%v", cp, err)
	}
	mv, err := Rename("notes/hello copy.txt", "notes/subdir", false)
	if err != nil || mv.Path != filepath.Join(dir, "notes/subdir/hello copy.txt") {
		t.Fatalf("unexpected Rename entry: %+v, err=%v", mv, err)
	}
	if _, err := os.Stat(mv.Path); err != nil {
		t.Fatalf("moved file missing: %v", err)
	}
}
