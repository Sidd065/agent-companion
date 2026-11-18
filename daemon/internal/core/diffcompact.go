package core

import (
	"bytes"
	"encoding/json"
	"strings"
)

// agy's ACP "diff" blocks carry the whole file before and after an edit. Kept
// as they come, a few hundred edits of a large file make a session file tens
// of megabytes (written again on every save), and the head/tail cut made for
// the phone drops the middle of the file, which is where the change is.
// compactDiffs keeps the changed lines plus diffContext lines around them and
// records the line the kept text starts at, so the phone can still number it.

const (
	diffContext  = 3
	compactAbove = 4 * 1024 // small diffs stay whole
)

// compactDiffs rewrites the diff blocks of a tool call's content, returning
// raw unchanged when there is nothing to trim.
func compactDiffs(raw json.RawMessage) json.RawMessage {
	if len(raw) < compactAbove || !bytes.Contains(raw, []byte(`"diff"`)) {
		return raw
	}
	var blocks []map[string]any
	if json.Unmarshal(raw, &blocks) != nil {
		return raw
	}
	changed := false
	for _, b := range blocks {
		if b["type"] != "diff" {
			continue
		}
		if _, done := b["line"]; done {
			continue
		}
		oldText, hasOld := b["oldText"].(string)
		newText, _ := b["newText"].(string)
		if !hasOld || len(oldText)+len(newText) < compactAbove {
			continue // a new file is all change
		}
		o, n, first := trimToChange(oldText, newText, diffContext)
		if len(o) == len(oldText) && len(n) == len(newText) {
			continue
		}
		b["oldText"], b["newText"], b["line"] = o, n, first
		changed = true
	}
	if !changed {
		return raw
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if enc.Encode(blocks) != nil {
		return raw
	}
	return bytes.TrimRight(buf.Bytes(), "\n")
}

// trimToChange drops the lines oldText and newText share at the start and at
// the end, keeping ctx of them on each side of the change. first is the
// 1-based number of the first kept line, the same in both texts.
func trimToChange(oldText, newText string, ctx int) (string, string, int) {
	o := strings.SplitAfter(oldText, "\n")
	n := strings.SplitAfter(newText, "\n")
	p := 0
	for p < len(o) && p < len(n) && o[p] == n[p] {
		p++
	}
	q := 0
	for q < len(o)-p && q < len(n)-p && o[len(o)-1-q] == n[len(n)-1-q] {
		q++
	}
	start := max(0, p-ctx)
	oEnd := len(o) - max(0, q-ctx)
	nEnd := len(n) - max(0, q-ctx)
	return strings.Join(o[start:oEnd], ""), strings.Join(n[start:nEnd], ""), start + 1
}

// compactToolDiffs applies compactDiffs to every tool item (used when loading
// sessions saved before diffs were compacted). It reports whether any changed.
func compactToolDiffs(items []*Item) bool {
	changed := false
	for _, it := range items {
		if it != nil && it.Tool != nil && len(it.Tool.Content) >= compactAbove {
			c := compactDiffs(it.Tool.Content)
			if len(c) != len(it.Tool.Content) {
				it.Tool.Content, changed = c, true
			}
		}
	}
	return changed
}
