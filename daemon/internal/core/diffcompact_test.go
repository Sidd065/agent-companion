package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func numbered(from, to int) string {
	var b strings.Builder
	for i := from; i <= to; i++ {
		fmt.Fprintf(&b, "line %d <tag> & more\n", i)
	}
	return b.String()
}

func TestTrimToChange(t *testing.T) {
	old := numbered(1, 1000)
	neu := strings.Replace(old, "line 500 <tag> & more\n", "line 500 changed\nan added line\n", 1)
	o, n, first := trimToChange(old, neu, 3)
	if first != 497 {
		t.Fatalf("first = %d, want 497", first)
	}
	if want := numbered(497, 503); o != want {
		t.Errorf("old kept %q, want %q", o, want)
	}
	if !strings.HasPrefix(n, "line 497") || !strings.Contains(n, "line 500 changed\nan added line\n") || !strings.HasSuffix(n, "line 503 <tag> & more\n") {
		t.Errorf("new kept %q", n)
	}
	// A change on the first line keeps nothing before it.
	o, _, first = trimToChange("a\nb\nc\nd\ne\nf\ng\nh\n", "A\nb\nc\nd\ne\nf\ng\nh\n", 3)
	if first != 1 || o != "a\nb\nc\nd\n" {
		t.Errorf("first-line change: first=%d old=%q", first, o)
	}
	// Appending at the end (no trailing newline before) keeps the tail.
	o, n, first = trimToChange("a\nb\nc\nd\ne\nf", "a\nb\nc\nd\ne\nf\ng", 3)
	if first != 3 || o != "c\nd\ne\nf" || n != "c\nd\ne\nf\ng" {
		t.Errorf("append: first=%d old=%q new=%q", first, o, n)
	}
}

func TestCompactDiffs(t *testing.T) {
	old := numbered(1, 2000)
	neu := strings.Replace(old, "line 1500 <tag> & more\n", "line 1500 edited\n", 1)
	raw, _ := json.Marshal([]map[string]any{
		{"type": "content", "content": map[string]any{"type": "text", "text": "kept"}},
		{"type": "diff", "path": "/x/big.py", "oldText": old, "newText": neu},
		{"type": "diff", "path": "/x/new.py", "newText": numbered(1, 400)}, // a new file stays whole
		{"type": "diff", "path": "/x/small.py", "oldText": "a\n", "newText": "b\n"},
	})
	out := compactDiffs(raw)
	if len(out) >= len(raw)/2 {
		t.Fatalf("not compacted: %d -> %d bytes", len(raw), len(out))
	}
	var blocks []map[string]any
	if err := json.Unmarshal(out, &blocks); err != nil {
		t.Fatal(err)
	}
	if blocks[0]["type"] != "content" {
		t.Errorf("other blocks must stay: %v", blocks[0])
	}
	big := blocks[1]
	if big["line"] != float64(1497) || big["oldText"] != numbered(1497, 1503) {
		t.Errorf("big diff: line=%v old=%q", big["line"], big["oldText"])
	}
	if !strings.Contains(big["newText"].(string), "line 1500 edited\n") {
		t.Errorf("big diff lost the change: %q", big["newText"])
	}
	if strings.Contains(string(out), "\\u003c") {
		t.Errorf("text was HTML-escaped")
	}
	if _, ok := blocks[2]["line"]; ok || blocks[2]["newText"] != numbered(1, 400) {
		t.Errorf("new file was trimmed")
	}
	if blocks[3]["oldText"] != "a\n" {
		t.Errorf("small diff changed")
	}
	// Compacting again changes nothing.
	if again := compactDiffs(out); string(again) != string(out) {
		t.Errorf("second pass changed the content")
	}
	// Small or diff-free content is returned as is.
	small := json.RawMessage(`[{"type":"diff","path":"a","oldText":"x","newText":"y"}]`)
	if got := compactDiffs(small); string(got) != string(small) {
		t.Errorf("small content changed: %s", got)
	}
}

func TestToolCallCompactsDiffs(t *testing.T) {
	tr := NewTranscript()
	old := numbered(1, 3000)
	neu := strings.Replace(old, "line 10 <tag> & more\n", "line 10 edited\n", 1)
	content, _ := json.Marshal([]map[string]any{{"type": "diff", "path": "/x/f.go", "oldText": old, "newText": neu}})
	upd, _ := json.Marshal(map[string]any{"toolCallId": "c1", "kind": "edit", "title": "f.go", "status": "completed", "content": json.RawMessage(content)})
	_, it := tr.ToolCall(upd, true)
	if it == nil || it.Tool == nil {
		t.Fatal("no tool item")
	}
	if len(it.Tool.Content) > 2000 {
		t.Errorf("stored diff is %d bytes; want it trimmed to the change", len(it.Tool.Content))
	}
	if !strings.Contains(string(it.Tool.Content), `"line":7`) {
		t.Errorf("missing start line: %s", it.Tool.Content)
	}
}

func TestLoadCompactsSavedDiffs(t *testing.T) {
	dir := t.TempDir()
	old := numbered(1, 4000)
	neu := strings.Replace(old, "line 2000 <tag> & more\n", "line 2000 edited\n", 1)
	content, _ := json.Marshal([]map[string]any{{"type": "diff", "path": "/x/f.py", "oldText": old, "newText": neu}})
	ss := savedSession{
		Meta:  SessionMeta{ID: "11111111-2222-4333-8444-555555555555", Agent: "agy", Cwd: dir, Title: "t"},
		Seq:   2,
		Items: []*Item{{ID: "tc:c1", Kind: "tool", Seq: 1, Tool: &Tool{CallID: "c1", Kind: "edit", Title: "f.py", Status: "completed", Content: content}}},
	}
	b, _ := json.Marshal(ss)
	if err := os.MkdirAll(filepath.Join(dir, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(dir, "sessions", ss.Meta.ID+".json")
	if err := os.WriteFile(f, b, 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewManager(Config{StateDir: dir, HomeDir: dir, AgyDataDir: dir}, NewHub())
	defer m.Shutdown()
	s := m.Get(ss.Meta.ID)
	if s == nil {
		t.Fatal("session not loaded")
	}
	if it := s.FullItem("tc:c1"); it == nil || len(it.Tool.Content) > 2000 {
		t.Fatalf("loaded diff not compacted")
	}
	// The smaller file is written back.
	deadline := time.Now().Add(5 * time.Second)
	for {
		fi, err := os.Stat(f)
		if err == nil && fi.Size() < int64(len(b))/10 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("session file not rewritten: %d bytes (was %d)", fi.Size(), len(b))
		}
		time.Sleep(50 * time.Millisecond)
	}
}
