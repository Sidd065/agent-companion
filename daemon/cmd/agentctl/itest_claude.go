package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Claude Code scenarios (real `claude` turns on a cheap model).
const claudeModel = "haiku"

func (t *tester) claudeSession(name string, extra map[string]any) (string, *transcript, string) {
	dir := filepath.Join(t.ws, name)
	_ = os.MkdirAll(dir, 0o755)
	params := map[string]any{"agent": "claude", "cwd": dir, "model": claudeModel, "skipPermissions": false}
	for k, v := range extra {
		params[k] = v
	}
	id := t.create(params)
	if id == "" {
		return "", nil, dir
	}
	return id, t.subscribe(id), dir
}

func (t *tester) pendingPerm(tr *transcript, timeout time.Duration, kind string, notReq ...string) map[string]any {
	var perm map[string]any
	t.pump(timeout, func() bool {
		perm = tr.find(func(m map[string]any) bool {
			if m["kind"] != "perm" || m["state"] != "pending" {
				return false
			}
			p, _ := m["perm"].(map[string]any)
			for _, r := range notReq {
				if p["reqId"] == r {
					return false
				}
			}
			k, _ := p["kind"].(string)
			return kind == "" || k == kind || (kind == "tool" && k == "")
		})
		return perm != nil
	})
	if perm == nil {
		return nil
	}
	return perm["perm"].(map[string]any)
}

func lastAgentText(tr *transcript) string {
	for i := len(tr.items) - 1; i >= 0; i-- {
		if tr.items[i]["kind"] == "agent" {
			s, _ := tr.items[i]["text"].(string)
			return s
		}
	}
	return ""
}

func (t *tester) scClaudeBasic() {
	id, tr, dir := t.claudeSession("claude-basic", map[string]any{"mode": "default"})
	if id == "" {
		return
	}
	d := t.sums[id]
	t.check(d["agent"] == "claude", "agent=claude (%v)", d["agent"])
	t.call("session.prompt", map[string]any{"id": id, "text": "Use the Write tool to create hello.txt containing exactly: hi there. Then reply with the single word done."})
	p := t.pendingPerm(tr, 2*time.Minute, "tool")
	t.check(p != nil && p["tool"] == "Write", "Write permission surfaced (%v)", p["tool"])
	t.pump(5*time.Second, func() bool { return t.sums[id]["state"] == "waiting" })
	t.check(t.sums[id]["state"] == "waiting", "state waiting (%v)", t.sums[id]["state"])
	if p != nil {
		names := []string{}
		for _, o := range p["options"].([]any) {
			names = append(names, o.(map[string]any)["optionId"].(string))
		}
		t.check(strings.Contains(strings.Join(names, ","), "allow") && strings.Contains(strings.Join(names, ","), "deny"), "options %v", names)
		t.call("session.permission", map[string]any{"id": id, "reqId": p["reqId"], "optionId": "allow"})
	}
	ok := t.waitTurns(id, 1, 3*time.Minute)
	t.check(ok, "turn finished")
	tr.dump()
	b, err := os.ReadFile(filepath.Join(dir, "hello.txt"))
	t.check(err == nil && strings.Contains(string(b), "hi there"), "file written (%q)", b)
	w := tr.find(func(m map[string]any) bool {
		tl, _ := m["tool"].(map[string]any)
		return tl != nil && tl["name"] == "Write" && tl["status"] == "completed"
	})
	t.check(w != nil, "Write tool item completed")
	if w != nil {
		c, _ := json.Marshal(w["tool"].(map[string]any)["content"])
		t.check(strings.Contains(string(c), `"diff"`), "Write shows a diff")
	}
	turn := tr.find(func(m map[string]any) bool { return m["kind"] == "turn" })
	cost := 0.0
	if turn != nil {
		if u, ok := turn["turn"].(map[string]any)["usage"].(map[string]any); ok {
			cost, _ = u["costUsd"].(float64)
		}
	}
	t.check(cost > 0, "turn reports cost $%.4f", cost)
	user := tr.find(func(m map[string]any) bool { return m["kind"] == "user" })
	t.check(user != nil && user["ref"] != nil && user["state"] == nil, "user message delivered with ref (%v, state %v)", user["ref"], user["state"])
	s := t.sums[id]
	t.check(s["model"] != nil && s["mode"] == "default", "summary model %v mode %v", s["model"], s["mode"])
	// effort + mode switching
	t.call("session.setConfig", map[string]any{"id": id, "configId": "thought_level", "value": "low"})
	r := t.call("session.setMode", map[string]any{"id": id, "mode": "acceptEdits"})
	t.check(r != nil && r["mode"] == "acceptEdits", "mode switched (%v)", r["mode"])
	r = t.call("session.setModel", map[string]any{"id": id, "model": "sonnet"})
	t.check(r != nil && r["model"] == "sonnet", "model switched (%v)", r["model"])
}

func (t *tester) scClaudeQuestion() {
	id, tr, _ := t.claudeSession("claude-question", nil)
	if id == "" {
		return
	}
	t.call("session.prompt", map[string]any{"id": id, "text": "Use the AskUserQuestion tool to ask me which color I prefer (options: Red, Blue). Then tell me in one short sentence what I picked."})
	p := t.pendingPerm(tr, 2*time.Minute, "question")
	t.check(p != nil, "question surfaced as a permission card")
	t.pump(3*time.Second, func() bool { return t.sums[id]["attention"] == "question" })
	t.check(t.sums[id]["attention"] == "question", "attention=question (%v)", t.sums[id]["attention"])
	if p == nil {
		return
	}
	var in struct {
		Questions []struct {
			Question string `json:"question"`
		} `json:"questions"`
	}
	b, _ := json.Marshal(p["input"])
	_ = json.Unmarshal(b, &in)
	t.check(len(in.Questions) > 0, "question input passed to the app (%d)", len(in.Questions))
	answers := map[string]any{}
	for _, q := range in.Questions {
		answers[q.Question] = "Blue"
	}
	t.call("session.permission", map[string]any{"id": id, "reqId": p["reqId"], "optionId": "answer", "answers": answers})
	ok := t.waitTurns(id, 1, 3*time.Minute)
	t.check(ok, "turn finished")
	tr.dump()
	t.check(strings.Contains(strings.ToLower(lastAgentText(tr)), "blue"), "Claude used the answer: %q", lastAgentText(tr))
}

func (t *tester) scClaudePlan() {
	id, tr, dir := t.claudeSession("claude-plan", map[string]any{"mode": "plan"})
	if id == "" {
		return
	}
	t.call("session.prompt", map[string]any{"id": id, "text": "Create fib.py that prints the first 10 Fibonacci numbers. Keep the plan to two short steps, then ask for approval with ExitPlanMode."})
	p := t.pendingPerm(tr, 3*time.Minute, "plan")
	t.check(p != nil, "plan surfaced for review")
	if p == nil {
		tr.dump()
		return
	}
	t.pump(3*time.Second, func() bool { return t.sums[id]["attention"] == "plan" })
	t.check(t.sums[id]["attention"] == "plan", "attention=plan (%v)", t.sums[id]["attention"])
	in, _ := p["input"].(map[string]any)
	planPath, _ := in["planFilePath"].(string)
	t.check(in["plan"] != nil, "plan text passed to the app (file %s)", planPath)
	if planPath != "" {
		art := t.call("session.artifact", map[string]any{"id": id, "path": planPath})
		t.check(art != nil && strings.TrimSpace(art["content"].(string)) != "", "plan readable as an artifact")
		live := t.call("session.live", map[string]any{"id": id})
		arts := list(live, "artifacts")
		t.check(len(arts) > 0 && arts[0].(map[string]any)["requestFeedback"] == true, "live state lists the plan for review (%d)", len(arts))
	}
	// Feedback through the artifact comment flow sends it back for revision.
	t.call("session.comment", map[string]any{"id": id, "comments": []map[string]any{{"kind": "artifact", "uri": "file://" + planPath, "comment": "Also print the sum of the numbers at the end."}}})
	p2 := t.pendingPerm(tr, 3*time.Minute, "plan", p["reqId"].(string))
	t.check(p2 != nil, "revised plan came back")
	if p2 == nil {
		tr.dump()
		return
	}
	t.call("session.permission", map[string]any{"id": id, "reqId": p2["reqId"], "optionId": "plan_accept_edits"})
	ok := t.approveUntilDone(id, tr, 1, 4*time.Minute)
	t.check(ok, "turn finished after approval")
	tr.dump()
	_, err := os.Stat(filepath.Join(dir, "fib.py"))
	t.check(err == nil, "fib.py created")
	t.check(t.sums[id]["mode"] == "acceptEdits", "mode now acceptEdits (%v)", t.sums[id]["mode"])
}

func (t *tester) scClaudeSteer() {
	id, tr, _ := t.claudeSession("claude-steer", map[string]any{"skipPermissions": true})
	if id == "" {
		return
	}
	t.call("session.prompt", map[string]any{"id": id, "text": "Run `sleep 6; echo first-done` with Bash, then reply with one short sentence."})
	t.pump(90*time.Second, func() bool {
		return tr.find(func(m map[string]any) bool {
			tl, _ := m["tool"].(map[string]any)
			return tl != nil && tl["name"] == "Bash"
		}) != nil
	})
	r := t.call("session.prompt", map[string]any{"id": id, "text": "Also tell me what 3+4 is.", "delivery": "steer"})
	t.check(r != nil && r["queued"] == false, "second message sent mid-turn")
	ok := t.waitTurns(id, 1, 3*time.Minute)
	t.check(ok, "turn(s) finished")
	tr.dump()
	n := 0
	for _, it := range tr.items {
		if it["kind"] == "user" && it["state"] == nil {
			n++
		}
	}
	t.check(n == 2, "both messages delivered (%d)", n)
	t.check(strings.Contains(lastAgentText(tr), "7"), "answer covers the steered message: %q", lastAgentText(tr))
}

func (t *tester) scClaudeCancel() {
	id, tr, _ := t.claudeSession("claude-cancel", map[string]any{"skipPermissions": true})
	if id == "" {
		return
	}
	t.call("session.prompt", map[string]any{"id": id, "text": "Run `sleep 40; echo late` with Bash and report its output."})
	t.pump(90*time.Second, func() bool {
		return tr.find(func(m map[string]any) bool {
			tl, _ := m["tool"].(map[string]any)
			return tl != nil && tl["name"] == "Bash"
		}) != nil
	})
	time.Sleep(2 * time.Second)
	t.call("session.cancel", map[string]any{"id": id})
	ok := t.pump(60*time.Second, t.idle(id))
	t.check(ok, "idle after cancel")
	turn := tr.find(func(m map[string]any) bool { return m["kind"] == "turn" })
	sr := ""
	if turn != nil {
		sr, _ = turn["turn"].(map[string]any)["stopReason"].(string)
	}
	t.check(sr == "cancelled", "stop reason cancelled (%q)", sr)
}

func (t *tester) scClaudeOps() {
	id, tr, dir := t.claudeSession("claude-ops", map[string]any{"skipPermissions": true})
	if id == "" {
		return
	}
	f := filepath.Join(dir, "a.txt")
	t.call("session.prompt", map[string]any{"id": id, "text": "Use the Write tool to create a.txt containing exactly: one. Reply with just ok."})
	t.check(t.waitTurns(id, 1, 3*time.Minute), "turn 1")
	t.call("session.prompt", map[string]any{"id": id, "text": "Overwrite a.txt so it contains exactly: two. Reply with just ok."})
	t.check(t.waitTurns(id, 2, 3*time.Minute), "turn 2")
	b, _ := os.ReadFile(f)
	t.check(strings.TrimSpace(string(b)) == "two", "a.txt is two (%q)", b)
	// rename
	t.call("session.rename", map[string]any{"id": id, "title": "Claude ops test"})
	t.pump(3*time.Second, func() bool { return t.sums[id]["title"] == "Claude ops test" })
	t.check(t.sums[id]["title"] == "Claude ops test", "renamed (%v)", t.sums[id]["title"])
	// revert preview + rewind to the second message (files and conversation)
	var second map[string]any
	n := 0
	for _, it := range tr.items {
		if it["kind"] == "user" {
			n++
			if n == 2 {
				second = it
			}
		}
	}
	if second == nil {
		t.check(false, "second user message present")
		return
	}
	pv := t.call("session.revertPreview", map[string]any{"id": id, "itemId": second["id"]})
	pj, _ := json.Marshal(pv)
	t.check(strings.Contains(string(pj), "a.txt"), "revert preview lists a.txt: %s", pj)
	r := t.call("session.rewind", map[string]any{"id": id, "itemId": second["id"]})
	t.check(r != nil && strings.Contains(r["prefill"].(string), "two"), "rewind returns the message as prefill")
	b, _ = os.ReadFile(f)
	t.check(strings.TrimSpace(string(b)) == "one", "a.txt restored to one (%q)", b)
	t.pump(2*time.Second, func() bool { return false })
	users := 0
	for _, it := range tr.items {
		if it["kind"] == "user" {
			users++
		}
	}
	t.check(users == 1, "transcript cut at the message (%d user messages)", users)
	t.call("session.prompt", map[string]any{"id": id, "text": "List every message I have sent you in this conversation, one per line, nothing else."})
	t.check(t.waitTurns(id, 3, 3*time.Minute), "turn after rewind")
	txt := lastAgentText(tr)
	t.check(!strings.Contains(strings.ToLower(txt), "two"), "Claude doesn't remember the rewound message: %q", txt)
	// fork at the end → new session that knows the history
	fr := t.call("session.fork", map[string]any{"id": id})
	if fr == nil || fr["session"] == nil {
		t.check(false, "fork")
		return
	}
	fid := fr["session"].(map[string]any)["id"].(string)
	t.created = append(t.created, fid)
	ftr := t.subscribe(fid)
	t.pump(5*time.Second, func() bool { return len(ftr.items) > 2 })
	t.check(len(ftr.items) > 2, "fork shows the copied history (%d items)", len(ftr.items))
	t.call("session.prompt", map[string]any{"id": fid, "text": "What did a.txt contain after my first message? Reply with just the word."})
	t.check(t.waitTurns(fid, 1, 3*time.Minute), "fork turn")
	t.check(strings.Contains(strings.ToLower(lastAgentText(ftr)), "one"), "fork knows the history: %q", lastAgentText(ftr))
	// export
	ex := t.call("session.export", map[string]any{"id": id})
	t.check(ex != nil && strings.Contains(ex["markdown"].(string), "## You"), "export markdown")
	// archive / pin
	t.call("session.archive", map[string]any{"id": id, "archived": true})
	lm := t.listMap(true)
	t.check(lm[id] != nil && lm[id]["archived"] == true, "archived in list")
	t.call("session.archive", map[string]any{"id": id, "archived": false})
	t.call("session.pin", map[string]any{"id": id, "pinned": true})
	lm = t.listMap(true)
	t.check(lm[id] != nil && lm[id]["pinned"] == true, "pinned in list")
	t.call("session.pin", map[string]any{"id": id, "pinned": false})
}

func (t *tester) scClaudeHistory() {
	id, tr, _ := t.claudeSession("claude-history", map[string]any{"skipPermissions": true})
	if id == "" {
		return
	}
	t.call("session.prompt", map[string]any{"id": id, "text": "Run `echo history-ok` with Bash and reply with its output."})
	t.check(t.waitTurns(id, 1, 3*time.Minute), "turn")
	before := len(tr.items)
	t.call("session.forget", map[string]any{"id": id})
	lm := t.listMap(true)
	h := lm[id]
	t.check(h != nil && h["agent"] == "claude" && h["state"] == "history", "listed from Claude's history (%v)", h)
	tr2 := t.subscribe(id)
	t.pump(10*time.Second, func() bool { return len(tr2.items) > 0 })
	tr2.dump()
	t.check(len(tr2.items) >= before-2, "replayed transcript (%d items, had %d)", len(tr2.items), before)
	t.check(tr2.find(func(m map[string]any) bool {
		tl, _ := m["tool"].(map[string]any)
		return tl != nil && tl["name"] == "Bash"
	}) != nil, "replay has the Bash call")
	t.check(t.sums[id] == nil || t.sums[id]["live"] != true, "no process started just to read history")
	t.call("session.prompt", map[string]any{"id": id, "text": "What did the command print? One word."})
	t.check(t.waitTurns(id, 1, 3*time.Minute), "resumed turn")
	t.check(strings.Contains(strings.ToLower(lastAgentText(tr2)), "history-ok"), "resumed with context: %q", lastAgentText(tr2))
}

func (t *tester) scClaudeLive() {
	id, tr, _ := t.claudeSession("claude-live", map[string]any{"skipPermissions": true})
	if id == "" {
		return
	}
	t.call("session.prompt", map[string]any{"id": id, "text": "Run this with the Bash tool with run_in_background set to true, and don't wait for it: for i in 1 2 3 4 5 6 7 8; do echo tick$i; sleep 1; done. End your turn right after starting it with one short sentence."})
	l := t.live(id, 2*time.Minute, func(l map[string]any) bool { return len(list(l, "tasks")) > 0 })
	t.check(l != nil, "background task listed")
	if l != nil {
		task := list(l, "tasks")[0].(map[string]any)
		var out string
		t.pump(20*time.Second, func() bool {
			r, err := t.callErr("session.taskOutput", map[string]any{"id": id, "taskId": task["id"], "offset": 0})
			if err == nil {
				out, _ = r.(map[string]any)["data"].(string)
			}
			if !strings.Contains(out, "tick") {
				time.Sleep(time.Second)
				return false
			}
			return true
		})
		t.check(strings.Contains(out, "tick"), "live output readable (%q)", out)
	}
	m := t.live(id, 90*time.Second, func(l map[string]any) bool { return len(list(l, "messages")) > 0 && len(list(l, "tasks")) == 0 })
	t.check(m != nil, "finished task reported")
	_ = tr
}

func (t *tester) scClaudeControl() {
	id, _, _ := t.claudeSession("claude-control", map[string]any{"skipPermissions": true})
	if id == "" {
		return
	}
	cu := t.call("session.control", map[string]any{"id": id, "subtype": "get_context_usage"})
	t.check(cu != nil && cu["categories"] != nil && cu["gridRows"] == nil, "context usage (grid dropped)")
	sq := t.call("session.control", map[string]any{"id": id, "subtype": "side_question", "params": map[string]any{"question": "What is 2+2? Answer with just the number."}})
	t.check(sq != nil && strings.Contains(sq["response"].(string), "4"), "side question: %v", sq["response"])
	t.checkAside(id, "What is 3+5? Answer with just the number.", "8")
	if _, err := t.callErr("session.aside", map[string]any{"id": id, "question": " "}); err == nil {
		t.check(false, "an empty side question is refused")
	}
	us := t.call("claude.usage", nil)
	t.check(us != nil && us["usage"] != nil, "claude.usage")
	if _, err := t.callErr("session.control", map[string]any{"id": id, "subtype": "end_session"}); err == nil {
		t.check(false, "disallowed control request rejected")
	} else {
		t.check(true, "disallowed control request rejected")
	}
	info := t.call("claude.info", nil)
	t.check(info != nil && info["available"] == true, "claude.info available (%v)", info["version"])
}

// checkAside asks a side question the way the app does: the daemon answers
// in the background, reports it with a session.aside event and keeps it.
func (t *tester) checkAside(id, question, want string) {
	a := t.call("session.aside", map[string]any{"id": id, "question": question})
	aid, _ := a["id"].(string)
	t.check(aid != "" && a["state"] == "pending", "side question accepted (%v)", a["state"])
	if _, err := t.callErr("session.aside", map[string]any{"id": id, "question": "And 1+1?"}); err == nil {
		t.check(false, "a second side question waits for the first")
	}
	t.pump(3*time.Minute, func() bool { x := t.asides[aid]; return x != nil && x["state"] != "pending" })
	x := t.asides[aid]
	ans, _ := x["answer"].(string)
	t.check(x != nil && x["state"] == "done" && strings.Contains(ans, want), "side answer arrives as an event (%v %q %v)", x["state"], ans, x["error"])
	raw, _ := t.callErr("session.asides", map[string]any{"id": id})
	l, _ := raw.([]any)
	t.check(len(l) == 1 && l[0].(map[string]any)["answer"] == ans, "side questions are kept (%d)", len(l))
	t.call("session.asideDelete", map[string]any{"id": id, "asideId": aid})
	raw, _ = t.callErr("session.asides", map[string]any{"id": id})
	l, _ = raw.([]any)
	t.check(len(l) == 0, "side question deleted")
}

// approveUntilDone allows every tool permission until the session finishes n turns.
func (t *tester) approveUntilDone(id string, tr *transcript, n float64, timeout time.Duration) bool {
	answered := map[string]bool{}
	return t.pump(timeout, func() bool {
		for _, it := range tr.items {
			if it["kind"] != "perm" || it["state"] != "pending" {
				continue
			}
			p := it["perm"].(map[string]any)
			rid, _ := p["reqId"].(string)
			if k, _ := p["kind"].(string); k != "" && k != "tool" || answered[rid] {
				continue
			}
			answered[rid] = true
			_, _ = t.callErr("session.permission", map[string]any{"id": id, "reqId": rid, "optionId": "allow"})
		}
		s := t.sums[id]
		return s != nil && s["turns"].(float64) >= n && s["state"] == "idle" && s["queued"].(float64) == 0
	})
}

// scClaudeFollow: a conversation continued by another Claude Code process (as
// in a terminal on the laptop) shows up live for a subscribed client.
func (t *tester) scClaudeFollow() {
	id, tr, dir := t.claudeSession("claude-follow", map[string]any{"skipPermissions": true})
	if id == "" {
		return
	}
	t.call("session.prompt", map[string]any{"id": id, "text": "Reply with exactly: ONE"})
	t.check(t.waitTurns(id, 1, 3*time.Minute), "turn through agentc")
	t.call("session.stop", map[string]any{"id": id})
	t.pump(5*time.Second, func() bool { return t.sums[id]["live"] == false })
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "CLAUDE") && !strings.HasPrefix(kv, "SSH_") {
			env = append(env, kv)
		}
	}
	cmd := exec.Command("claude", "-p", "--resume", id, "--model", claudeModel, "Reply with exactly: TWO")
	cmd.Dir, cmd.Env = dir, env
	out, err := cmd.CombinedOutput()
	t.check(err == nil && strings.Contains(string(out), "TWO"), "another claude process continued it (%q)", strings.TrimSpace(string(out)))
	ok := t.pump(30*time.Second, func() bool {
		return tr.find(func(m map[string]any) bool {
			s, _ := m["text"].(string)
			return m["kind"] == "agent" && strings.Contains(s, "TWO")
		}) != nil
	})
	t.check(ok, "the phone sees the reply written by the other process")
	ones, users := 0, 0
	for _, it := range tr.items {
		if s, _ := it["text"].(string); it["kind"] == "agent" && strings.Contains(s, "ONE") {
			ones++
		}
		if it["kind"] == "user" {
			users++
		}
	}
	t.check(ones == 1 && users == 2, "nothing duplicated (%d ONE replies, %d user messages)", ones, users)

	// While another process holds the conversation (a terminal), side
	// questions are answered by a throwaway fork that writes nothing.
	hold := exec.Command("claude", "-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
		"--resume", id, "--model", claudeModel)
	hold.Dir, hold.Env = dir, env
	stdin, _ := hold.StdinPipe()
	if err := hold.Start(); err != nil {
		t.check(false, "start a process holding the conversation: %v", err)
		return
	}
	defer func() { _ = stdin.Close(); _ = hold.Wait() }()
	home, _ := os.UserHomeDir()
	reg := filepath.Join(home, ".claude", "sessions", fmt.Sprintf("%d.json", hold.Process.Pid))
	t.pump(30*time.Second, func() bool {
		if _, err := os.Stat(reg); err == nil {
			return true
		}
		time.Sleep(300 * time.Millisecond)
		return false
	})
	paths, _ := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", id+".jsonl"))
	var before int64 = -1
	if len(paths) == 1 {
		if st, err := os.Stat(paths[0]); err == nil {
			before = st.Size()
		}
	}
	t.checkAside(id, "What exact word did you reply with most recently? Answer with just that word.", "TWO")
	after := int64(-2)
	if len(paths) == 1 {
		if st, err := os.Stat(paths[0]); err == nil {
			after = st.Size()
		}
	}
	t.check(before >= 0 && before == after, "the fork wrote nothing to the held conversation (%d → %d bytes)", before, after)
}
