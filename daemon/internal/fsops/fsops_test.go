package fsops

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
	if err != nil || r.Binary || r.Truncated || r.Text == nil || *r.Text != "hello world\n" {
		t.Fatalf("unexpected Read text result: %+v, err=%v", r, err)
	}
	rt, err := Read("notes/hello.txt", 5)
	if err != nil || !rt.Truncated || rt.Text == nil || *rt.Text != "hello" {
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

func TestResolveDynamicAndUnsetHome(t *testing.T) {
	dynHome := t.TempDir()
	origHome := Home
	Home = startupHome
	t.Cleanup(func() { Home = origHome })

	t.Setenv("HOME", dynHome)
	if got := Resolve("~"); got != filepath.Clean(dynHome) {
		t.Fatalf("Resolve(~) with dynamic HOME = %q, want %q", got, dynHome)
	}
	if got := Resolve("~/alpha/beta.txt"); got != filepath.Join(dynHome, "alpha", "beta.txt") {
		t.Fatalf("Resolve(~/alpha/beta.txt) = %q", got)
	}
	if gotHome, _ := HomeInfo()["home"].(string); gotHome != filepath.Clean(dynHome) {
		t.Fatalf("HomeInfo()[home] = %q, want %q", gotHome, dynHome)
	}

	// Unset/empty $HOME must fall back via user.Current().HomeDir (never empty string).
	t.Setenv("HOME", "")
	fallback := Resolve("~")
	if strings.TrimSpace(fallback) == "" {
		t.Fatal("Resolve(~) with empty $HOME returned empty string")
	}
}

func TestScopedFSAndHeadlessTrashAndSelfRename(t *testing.T) {
	homeA := t.TempDir()
	homeB := t.TempDir()
	fsA := New(homeA)
	fsB := New(homeB)

	if fsA.Resolve("~") != filepath.Clean(homeA) || fsB.Resolve("~") != filepath.Clean(homeB) {
		t.Fatalf("scoped Resolve(~) mismatch: A=%q B=%q", fsA.Resolve("~"), fsB.Resolve("~"))
	}
	if _, err := fsA.Write(WriteParams{Path: "~/only_a.txt", Text: "host-A"}); err != nil {
		t.Fatalf("fsA.Write: %v", err)
	}
	if _, err := fsB.Write(WriteParams{Path: "~/only_b.txt", Text: "host-B"}); err != nil {
		t.Fatalf("fsB.Write: %v", err)
	}
	if _, err := fsA.Read("~/only_b.txt", 1024); !os.IsNotExist(err) {
		t.Fatalf("expected fsA isolation from host B, got err=%v", err)
	}
	if rA, err := fsA.Read("~/only_a.txt", 1024); err != nil || rA.Text == nil || *rA.Text != "host-A" {
		t.Fatalf("fsA.Read: %+v err=%v", rA, err)
	}
	if fsA.HomeInfo()["home"] != filepath.Clean(homeA) || fsB.HomeInfo()["home"] != filepath.Clean(homeB) {
		t.Fatalf("scoped HomeInfo mismatch")
	}

	// Refuses deleting scoped HomeDir.
	if err := fsA.Delete(context.Background(), []string{"~"}, true); err == nil || !strings.Contains(err.Error(), "refusing to delete") {
		t.Fatalf("expected refusal to delete scoped home, got %v", err)
	}

	// Non-existent path in non-permanent Delete reports stat error (never blank "trash ...: ").
	if err := fsA.Delete(context.Background(), []string{"~/does_not_exist.txt"}, false); err == nil || strings.HasSuffix(strings.TrimSpace(err.Error()), ":") {
		t.Fatalf("expected clean stat error for missing file, got %v", err)
	}

	// Headless / missing `gio` (empty PATH) returns TRASH_UNSUPPORTED while keeping file intact.
	t.Setenv("PATH", "")
	errTrash := fsA.Delete(context.Background(), []string{"~/only_a.txt"}, false)
	if errTrash == nil || !strings.Contains(errTrash.Error(), "TRASH_UNSUPPORTED:") {
		t.Fatalf("expected TRASH_UNSUPPORTED when gio is unavailable, got %v", errTrash)
	}
	if _, err := os.Stat(filepath.Join(homeA, "only_a.txt")); err != nil {
		t.Fatalf("only_a.txt should remain intact after TRASH_UNSUPPORTED: %v", err)
	}
	if err := fsA.Delete(context.Background(), []string{"~/only_a.txt"}, true); err != nil {
		t.Fatalf("permanent Delete failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(homeA, "only_a.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected only_a.txt removed on permanent Delete, got %v", err)
	}

	// Self-subdirectory Rename guard.
	if _, err := fsA.Mkdir("~/parent/sub", true); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if _, err := fsA.Rename("~/parent", "~/parent/sub", false); err == nil || !strings.Contains(err.Error(), "into itself") {
		t.Fatalf("expected self-move error on Rename(parent, parent/sub), got %v", err)
	}
}

func TestReadEmptyFileEmitsEmptyStringText(t *testing.T) {
	dir := t.TempDir()
	fsys := New(dir)

	if _, err := fsys.Create("~/empty.txt"); err != nil {
		t.Fatalf("Create empty.txt: %v", err)
	}
	rEmpty, err := fsys.Read("~/empty.txt", 1024)
	if err != nil {
		t.Fatalf("Read empty.txt: %v", err)
	}
	if rEmpty.Binary || rEmpty.Truncated || rEmpty.Size != 0 || rEmpty.Text == nil || *rEmpty.Text != "" {
		t.Fatalf("expected non-nil empty Text on 0-byte file, got %+v", rEmpty)
	}
	rawEmpty, err := json.Marshal(rEmpty)
	if err != nil {
		t.Fatalf("json.Marshal empty ReadResult: %v", err)
	}
	var emptyMap map[string]any
	if err := json.Unmarshal(rawEmpty, &emptyMap); err != nil {
		t.Fatalf("json.Unmarshal empty ReadResult: %v", err)
	}
	txtVal, hasText := emptyMap["text"]
	if !hasText || txtVal != "" {
		t.Fatalf("expected JSON key \"text\":\"\", got hasText=%v val=%#v raw=%s", hasText, txtVal, string(rawEmpty))
	}

	binBytes := []byte{0x00, 0x01}
	if _, err := fsys.Write(WriteParams{
		Path:   "~/blob.bin",
		Base64: base64.StdEncoding.EncodeToString(binBytes),
	}); err != nil {
		t.Fatalf("Write blob.bin: %v", err)
	}
	rBin, err := fsys.Read("~/blob.bin", 1024)
	if err != nil {
		t.Fatalf("Read blob.bin: %v", err)
	}
	if !rBin.Binary || rBin.Text != nil {
		t.Fatalf("expected Binary=true and Text=nil on binary file, got %+v", rBin)
	}
	rawBin, err := json.Marshal(rBin)
	if err != nil {
		t.Fatalf("json.Marshal binary ReadResult: %v", err)
	}
	var binMap map[string]any
	if err := json.Unmarshal(rawBin, &binMap); err != nil {
		t.Fatalf("json.Unmarshal binary ReadResult: %v", err)
	}
	if _, hasBinText := binMap["text"]; hasBinText {
		t.Fatalf("expected binary ReadResult JSON to omit \"text\", got %s", string(rawBin))
	}
}
