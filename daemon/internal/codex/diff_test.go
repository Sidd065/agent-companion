package codex

import (
	"testing"
)

func TestParseFileChangeAndDualPayloads(t *testing.T) {
	cwd := "/workspace/demo"

	var addCh FileUpdateChange
	addCh.Path = "/workspace/demo/src/new.go"
	addCh.Kind.Type = "add"
	addCh.Diff = "package main\n\nfunc main() {}\n"

	pdAdd := ParseFileChange(cwd, addCh)
	if pdAdd.OldText != "" || pdAdd.NewText != addCh.Diff || pdAdd.RelPath != "src/new.go" {
		t.Fatalf("unexpected add parse: %+v", pdAdd)
	}

	var updCh FileUpdateChange
	updCh.Path = "/workspace/demo/src/app.go"
	updCh.Kind.Type = "update"
	updCh.Diff = "@@ -10,3 +10,4 @@\n func greet() {\n-\tprintln(\"old\")\n+\tprintln(\"new\")\n+\tprintln(\"extra\")\n }\n"

	toolName, title, locs, content, rawIn := BuildFileChangePayloads(cwd, []FileUpdateChange{updCh})
	if toolName != "Edit" || title != "Edit src/app.go" || len(locs) != 1 {
		t.Fatalf("unexpected header: name=%q title=%q locs=%+v", toolName, title, locs)
	}
	if len(content) != 1 || content[0]["type"] != "diff" || content[0]["line"] != 10 {
		t.Fatalf("unexpected diff content block: %+v", content)
	}
	if rawIn["path"] != "/workspace/demo/src/app.go" || rawIn["file_path"] != "/workspace/demo/src/app.go" {
		t.Fatalf("unexpected rawInput paths: %+v", rawIn)
	}
	if rawIn["oldText"] == "" || rawIn["newText"] == "" || rawIn["oldText"] == rawIn["newText"] {
		t.Fatalf("unexpected rawInput texts: %+v", rawIn)
	}
}
