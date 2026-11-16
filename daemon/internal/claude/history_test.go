package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTranscript(t *testing.T, dir string, entries []map[string]any) string {
	t.Helper()
	p := filepath.Join(dir, "projects", SanitizePath("/work/demo"), "11111111-2222-4333-8444-555555555555.jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, e := range entries {
		j, _ := json.Marshal(e)
		b.Write(j)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func msg(role string, content any) map[string]any { return map[string]any{"role": role, "content": content} }

func sampleTranscript() []map[string]any {
	sid := "11111111-2222-4333-8444-555555555555"
	return []map[string]any{
		{"type": "queue-operation", "operation": "enqueue", "sessionId": sid},
		{"parentUuid": nil, "isSidechain": false, "type": "user", "uuid": "u1", "cwd": "/work/demo", "sessionId": sid, "entrypoint": "cli",
			"timestamp": "2026-11-16T10:00:00Z", "message": msg("user", "Fix the tests please")},
		{"parentUuid": "u1", "isSidechain": false, "type": "assistant", "uuid": "a1", "message": msg("assistant", []map[string]any{
			{"type": "thinking", "thinking": "Let me look"}, {"type": "text", "text": "Looking."},
			{"type": "tool_use", "id": "t1", "name": "Bash", "input": map[string]any{"command": "go test ./..."}}})},
		{"parentUuid": "a1", "isSidechain": false, "type": "user", "uuid": "r1", "toolUseResult": map[string]any{"stdout": "ok", "stderr": ""},
			"message": msg("user", []map[string]any{{"type": "tool_result", "tool_use_id": "t1", "content": "ok"}})},
		{"parentUuid": "r1", "isSidechain": true, "type": "assistant", "uuid": "side", "message": msg("assistant", []map[string]any{{"type": "text", "text": "sidechain"}})},
		{"parentUuid": "r1", "isSidechain": false, "type": "assistant", "uuid": "a2", "message": msg("assistant", []map[string]any{
			{"type": "tool_use", "id": "t2", "name": "TodoWrite", "input": map[string]any{"todos": []map[string]any{{"content": "Fix", "status": "in_progress"}}}}})},
		{"parentUuid": nil, "logicalParentUuid": "a2", "isSidechain": false, "type": "system", "subtype": "compact_boundary", "uuid": "c1", "content": "Conversation compacted"},
		{"parentUuid": "c1", "isSidechain": false, "type": "user", "uuid": "sum", "isCompactSummary": true, "message": msg("user", "summary of earlier")},
		{"parentUuid": "sum", "isSidechain": false, "type": "user", "uuid": "u2", "message": msg("user", "<command-name>/cost</command-name>\n<command-args></command-args>")},
		{"parentUuid": "u2", "isSidechain": false, "type": "system", "subtype": "local_command", "uuid": "l1", "content": "<local-command-stdout>Total cost: $1</local-command-stdout>"},
		{"parentUuid": "l1", "isSidechain": false, "type": "attachment", "uuid": "q1", "attachment": map[string]any{"type": "queued_command", "prompt": "and the docs", "source_uuid": "client-1", "commandMode": "prompt"}},
		{"parentUuid": "q1", "isSidechain": false, "type": "assistant", "uuid": "a3", "message": msg("assistant", []map[string]any{{"type": "text", "text": "Done."}})},
		{"type": "custom-title", "customTitle": "Fix tests", "sessionId": sid},
		{"type": "last-prompt", "lastPrompt": "and the docs", "sessionId": sid},
	}
}

func TestReadChainAndReplay(t *testing.T) {
	dir := t.TempDir()
	p := writeTranscript(t, dir, sampleTranscript())
	chain, truncated, err := ReadChain(p, 1000, 1<<20)
	if err != nil || truncated {
		t.Fatalf("ReadChain: %v truncated=%v", err, truncated)
	}
	var ids []string
	for _, e := range chain {
		ids = append(ids, e.UUID)
	}
	if got := strings.Join(ids, ","); got != "u1,a1,r1,a2,c1,sum,u2,l1,q1,a3" {
		t.Fatalf("chain = %s", got)
	}
	var kinds []string
	var users []string
	ReplayUpdates(chain, "/work/demo", func(u map[string]any) {
		k := u["sessionUpdate"].(string)
		kinds = append(kinds, k)
		if k == "user_message_chunk" {
			users = append(users, u["content"].(map[string]any)["text"].(string))
		}
	}, func(level, text string) { kinds = append(kinds, "notice:"+text) })
	got := strings.Join(kinds, " ")
	for _, want := range []string{"agent_thought_chunk", "tool_call", "tool_call_update", "plan", "notice:Conversation compacted", "agent_message_chunk"} {
		if !strings.Contains(got, want) {
			t.Errorf("replay missing %s: %s", want, got)
		}
	}
	if strings.Contains(got, "sidechain") {
		t.Errorf("sidechain leaked into replay")
	}
	if strings.Join(users, "|") != "Fix the tests please|/cost|and the docs" {
		t.Errorf("user messages = %q", users)
	}
	// Truncation keeps the newest entries.
	chain, truncated, _ = ReadChain(p, 3, 1<<20)
	if !truncated || len(chain) != 3 || chain[2].UUID != "a3" {
		t.Fatalf("limited chain: truncated=%v len=%d", truncated, len(chain))
	}
}

func TestSessionInfoAndPrompts(t *testing.T) {
	dir := t.TempDir()
	p := writeTranscript(t, dir, sampleTranscript())
	info, ok := ReadSessionInfo(p)
	if !ok || info.Title != "Fix tests" || info.Cwd != "/work/demo" || info.FirstPrompt != "Fix the tests please" || info.Entrypoint != "cli" {
		t.Fatalf("info = %+v ok=%v", info, ok)
	}
	list := ListSessions(dir)
	if len(list) != 1 || list[0].ID != "11111111-2222-4333-8444-555555555555" {
		t.Fatalf("list = %+v", list)
	}
	if found, err := FindSession(dir, list[0].ID, ""); err != nil || found != p {
		t.Fatalf("find: %v %s", err, found)
	}
	if n, _ := PromptsAfter(p, "u1"); n != 2 {
		t.Errorf("prompts after u1 = %d, want 2 (/cost and the queued one)", n)
	}
	if n, _ := PromptsAfter(p, "client-1"); n != 0 {
		t.Errorf("prompts after the queued message = %d", n)
	}
	if n, _ := PromptsAfter(p, "nope"); n != -1 {
		t.Errorf("unknown uuid = %d", n)
	}
	if e, err := FindEntry(p, "client-1"); err != nil || e.UUID != "q1" {
		t.Errorf("queued message by client uuid: %v %+v", err, e)
	}
	if err := SetArchived(p, true); err != nil {
		t.Fatal(err)
	}
	if info, _ := ReadSessionInfo(p); !info.Archived {
		t.Errorf("archive mark not read")
	}
	if err := AppendTitle(p, list[0].ID, "Renamed"); err != nil {
		t.Fatal(err)
	}
	if info, _ := ReadSessionInfo(p); info.Title != "Renamed" {
		t.Errorf("title after rename = %q", info.Title)
	}
}

func TestDescribeAndResults(t *testing.T) {
	ti := DescribeTool("Edit", map[string]any{"file_path": "/work/demo/a.go", "old_string": "x", "new_string": "y"}, "/work/demo")
	if ti.Title != "a.go" || ti.Kind != "edit" || len(ti.Content) != 1 {
		t.Fatalf("edit = %+v", ti)
	}
	if ti := DescribeTool("mcp__github__create_issue", map[string]any{"title": "Bug"}, ""); !strings.HasPrefix(ti.Title, "github · create_issue") {
		t.Fatalf("mcp = %+v", ti)
	}
	st, out, content := ToolResult("Bash", map[string]any{"command": "false"}, json.RawMessage(`"Exit code 1\nboom"`), true, nil)
	m := out.(map[string]any)
	if st != "failed" || m["exit_code"] != 1 || len(content) == 0 {
		t.Fatalf("bash failure = %s %v", st, m)
	}
	if SanitizePath("/home/me/.claude/x_y") != "-home-me--claude-x-y" {
		t.Fatalf("sanitize = %s", SanitizePath("/home/me/.claude/x_y"))
	}
}
