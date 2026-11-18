package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/flynn/noise"

	"agentc/internal/secure"
)

// transcript mirrors the app's item store.
type transcript struct {
	seq   float64
	items []map[string]any
	sess  map[string]any
}

func (t *transcript) idx(id string) int {
	for i, it := range t.items {
		if it["id"] == id {
			return i
		}
	}
	return -1
}

func (t *transcript) apply(ev map[string]any, sid string) bool {
	data, _ := ev["data"].(map[string]any)
	if data == nil || data["id"] != sid {
		return false
	}
	switch ev["event"] {
	case "session.snapshot":
		t.items = nil
		for _, it := range data["items"].([]any) {
			t.items = append(t.items, it.(map[string]any))
		}
		t.seq = data["seq"].(float64)
		t.sess, _ = data["session"].(map[string]any)
	case "session.delta", "session.ops":
		if s, ok := data["session"].(map[string]any); ok {
			t.sess = s
		}
		ops, _ := data["ops"].([]any)
		for _, o := range ops {
			op := o.(map[string]any)
			seq := op["seq"].(float64)
			if seq <= t.seq {
				continue
			}
			t.seq = seq
			id, _ := op["id"].(string)
			switch op["op"] {
			case "add":
				t.items = append(t.items, op["item"].(map[string]any))
			case "set":
				if i := t.idx(id); i >= 0 {
					t.items[i] = op["item"].(map[string]any)
				} else {
					t.items = append(t.items, op["item"].(map[string]any))
				}
			case "append":
				if i := t.idx(id); i >= 0 {
					txt, _ := t.items[i]["text"].(string)
					t.items[i]["text"] = txt + op["text"].(string)
				}
			case "remove":
				if i := t.idx(id); i >= 0 {
					t.items = append(t.items[:i], t.items[i+1:]...)
				}
			case "reset":
				t.items = nil
				if its, ok := op["items"].([]any); ok {
					for _, it := range its {
						t.items = append(t.items, it.(map[string]any))
					}
				}
			}
		}
	case "session.config":
		if s, ok := data["session"].(map[string]any); ok {
			t.sess = s
		}
	default:
		return false
	}
	return true
}

func (t *transcript) dump() {
	for _, it := range t.items {
		k := it["kind"]
		line := fmt.Sprintf("  [%v] %v", it["id"], k)
		switch k {
		case "user", "agent", "thought", "notice":
			txt, _ := it["text"].(string)
			if len(txt) > 120 {
				txt = txt[:120] + "…"
			}
			line += fmt.Sprintf(" state=%v %q", it["state"], txt)
		case "tool":
			tl := it["tool"].(map[string]any)
			line += fmt.Sprintf(" %v/%v %q status=%v", tl["name"], tl["kind"], tl["title"], tl["status"])
		case "perm":
			line += fmt.Sprintf(" state=%v perm=%v", it["state"], it["perm"])
		case "turn":
			line += fmt.Sprintf(" %v", it["turn"])
		}
		fmt.Println(line)
	}
}

func (t *transcript) find(pred func(map[string]any) bool) map[string]any {
	for _, it := range t.items {
		if pred(it) {
			return it
		}
	}
	return nil
}

type tester struct {
	c       *Client
	cr      *Creds
	fails   int
	ws      string
	tr      map[string]*transcript
	sums    map[string]map[string]any
	notifs  []map[string]any
	created []string
	asides  map[string]map[string]any // side questions by id, from session.aside events
}

func (t *tester) check(ok bool, format string, a ...any) {
	if ok {
		fmt.Printf("  ✔ %s\n", fmt.Sprintf(format, a...))
	} else {
		t.fails++
		fmt.Printf("  ✘ FAIL: %s\n", fmt.Sprintf(format, a...))
	}
}

func (t *tester) call(method string, params any) map[string]any {
	res, err := t.c.Call(method, params, 6*time.Minute)
	if err != nil {
		t.check(false, "%s: %v", method, err)
		return nil
	}
	m, _ := res.(map[string]any)
	if m == nil {
		m = map[string]any{"_": res}
	}
	return m
}

func (t *tester) callErr(method string, params any) (any, error) {
	return t.c.Call(method, params, 6*time.Minute)
}

// pump processes events until cond() is true or the timeout passes.
func (t *tester) pump(timeout time.Duration, cond func() bool) bool {
	deadline := time.After(timeout)
	for {
		if cond() {
			return true
		}
		select {
		case ev, ok := <-t.c.Events:
			if !ok {
				return false
			}
			t.handle(ev)
		case <-deadline:
			return cond()
		}
	}
}

func (t *tester) handle(ev map[string]any) {
	name, _ := ev["event"].(string)
	switch name {
	case "session.changed":
		d := ev["data"].(map[string]any)
		t.sums[d["id"].(string)] = d
	case "notify":
		t.notifs = append(t.notifs, ev["data"].(map[string]any))
	case "session.aside":
		if a, ok := ev["data"].(map[string]any)["aside"].(map[string]any); ok {
			t.asides[a["id"].(string)] = a
		}
	default:
		if data, ok := ev["data"].(map[string]any); ok {
			if id, ok := data["id"].(string); ok {
				if tr := t.tr[id]; tr != nil {
					tr.apply(ev, id)
				}
			}
		}
	}
}

func (t *tester) subscribe(id string) *transcript {
	tr := &transcript{}
	t.tr[id] = tr
	t.call("session.subscribe", map[string]any{"id": id})
	return tr
}

func (t *tester) idle(id string) func() bool {
	return func() bool {
		s := t.sums[id]
		return s != nil && (s["state"] == "idle" || s["state"] == "sleeping") && s["queued"].(float64) == 0
	}
}

func (t *tester) waitTurns(id string, n float64, timeout time.Duration) bool {
	return t.pump(timeout, func() bool {
		s := t.sums[id]
		return s != nil && s["turns"].(float64) >= n && (s["state"] == "idle") && s["queued"].(float64) == 0
	})
}

const testModel = "gemini-3.8-flash-low"

func itest(c *Client, cr *Creds, scenarios []string) int {
	t := &tester{c: c, cr: cr, tr: map[string]*transcript{}, sums: map[string]map[string]any{}, asides: map[string]map[string]any{}}
	ws, _ := os.MkdirTemp("", "agentc-itest-")
	t.ws = ws
	fmt.Println("workspace:", ws)
	if len(scenarios) == 0 {
		scenarios = []string{"hello", "basic", "queue", "cancel", "perm", "question", "ops", "fs", "term", "history", "flags", "live"}
	}
	for _, sc := range scenarios {
		fmt.Printf("\n=== %s\n", sc)
		start := time.Now()
		switch sc {
		case "hello":
			t.scHello()
		case "basic":
			t.scBasic()
		case "queue":
			t.scQueue()
		case "cancel":
			t.scCancel()
		case "perm":
			t.scPerm()
		case "question":
			t.scQuestion()
		case "plan":
			t.scPlan()
		case "ops":
			t.scOps()
		case "fs":
			t.scFS()
		case "term":
			t.scTerm()
		case "history":
			t.scHistory()
		case "flags":
			t.scFlags()
		case "live":
			t.scLive()
		case "agy-auto":
			t.scAgyAuto()
		case "agy-aside":
			t.scAgyAside()
		case "claude-basic":
			t.scClaudeBasic()
		case "claude-question":
			t.scClaudeQuestion()
		case "claude-plan":
			t.scClaudePlan()
		case "claude-steer":
			t.scClaudeSteer()
		case "claude-cancel":
			t.scClaudeCancel()
		case "claude-ops":
			t.scClaudeOps()
		case "claude-history":
			t.scClaudeHistory()
		case "claude-live":
			t.scClaudeLive()
		case "claude-control":
			t.scClaudeControl()
		case "claude-follow":
			t.scClaudeFollow()
		case "claude":
			t.scClaudeBasic()
			t.scClaudeQuestion()
			t.scClaudePlan()
			t.scClaudeSteer()
			t.scClaudeCancel()
			t.scClaudeOps()
			t.scClaudeHistory()
			t.scClaudeLive()
			t.scClaudeControl()
			t.scClaudeFollow()
		default:
			fmt.Println("unknown scenario", sc)
			t.fails++
		}
		fmt.Printf("  (%s)\n", time.Since(start).Round(time.Millisecond))
	}
	fmt.Printf("\ncreated sessions: %v\n", t.created)
	if t.fails > 0 {
		fmt.Printf("\n%d FAILURES\n", t.fails)
		return 1
	}
	fmt.Println("\nALL PASSED")
	return 0
}

func (t *tester) scHello() {
	r := t.call("hello", map[string]any{"device": "itest"})
	t.check(r != nil && r["version"] != nil, "hello returns version %v", r["version"])
	agents, _ := r["agents"].([]any)
	t.check(len(agents) >= 1, "agents listed: %d", len(agents))
}

func (t *tester) create(params map[string]any) string {
	r := t.call("session.create", params)
	if r == nil {
		return ""
	}
	s := r["session"].(map[string]any)
	id := s["id"].(string)
	t.sums[id] = s
	t.created = append(t.created, id)
	return id
}

func (t *tester) scBasic() {
	dir := filepath.Join(t.ws, "basic")
	_ = os.MkdirAll(dir, 0o755)
	id := t.create(map[string]any{"cwd": dir, "model": testModel})
	t.check(id != "", "created session %s", id)
	if id == "" {
		return
	}
	tr := t.subscribe(id)
	t.pump(10*time.Second, func() bool { return tr.sess != nil })
	t.check(tr.sess != nil && tr.sess["models"] != nil, "snapshot has models")
	t.call("session.prompt", map[string]any{"id": id, "text": "Create a file named a.txt containing the word hello, then run `ls -la` and reply with one short sentence."})
	ok := t.waitTurns(id, 1, 4*time.Minute)
	t.check(ok, "turn finished (state=%v)", t.sums[id]["state"])
	tr.dump()
	b, err := os.ReadFile(filepath.Join(dir, "a.txt"))
	t.check(err == nil && strings.Contains(string(b), "hello"), "a.txt created in the session cwd")
	t.check(tr.find(func(m map[string]any) bool { return m["kind"] == "user" && m["state"] == nil }) != nil, "user message delivered")
	t.check(tr.find(func(m map[string]any) bool {
		tl, _ := m["tool"].(map[string]any)
		return tl != nil && tl["kind"] == "execute" && tl["status"] == "completed"
	}) != nil, "execute tool completed")
	t.check(tr.find(func(m map[string]any) bool { return m["kind"] == "agent" && m["text"] != "" }) != nil, "agent message present")
	t.pump(5*time.Second, func() bool { return tr.find(func(m map[string]any) bool { return m["kind"] == "turn" }) != nil })
	t.check(tr.find(func(m map[string]any) bool { return m["kind"] == "turn" }) != nil, "turn marker present")
	t.check(t.sums[id]["lastText"] != nil && t.sums[id]["lastText"] != "", "summary lastText: %v", t.sums[id]["lastText"])
	n := 0
	t.pump(5*time.Second, func() bool {
		n = 0
		for _, x := range t.notifs {
			if x["sessionId"] == id && x["kind"] == "done" {
				n++
			}
		}
		return n > 0
	})
	t.check(n >= 1, "done notification received")
	// resubscribe with delta
	seq := tr.seq
	tr2 := &transcript{seq: seq, items: append([]map[string]any(nil), tr.items...)}
	t.tr[id] = tr2
	t.call("session.subscribe", map[string]any{"id": id, "since": seq})
	t.pump(5*time.Second, func() bool { return tr2.sess != nil })
	t.check(tr2.sess != nil && len(tr2.items) == len(tr.items), "delta resubscribe keeps %d items", len(tr2.items))
}

func (t *tester) scQueue() {
	dir := filepath.Join(t.ws, "queue")
	_ = os.MkdirAll(dir, 0o755)
	id := t.create(map[string]any{"cwd": dir, "model": testModel})
	if id == "" {
		return
	}
	tr := t.subscribe(id)
	t.call("session.prompt", map[string]any{"id": id, "text": "Run the shell command `sleep 12` and wait for it to finish (it must run in the foreground), then reply with exactly: FIRST-DONE"})
	t.pump(20*time.Second, func() bool { return t.sums[id] != nil && t.sums[id]["state"] == "running" })
	r := t.call("session.prompt", map[string]any{"id": id, "text": "Now reply with exactly: QUEUED-OK", "delivery": "queue"})
	t.check(r != nil && r["queued"] == true, "second message queued")
	t.pump(3*time.Second, func() bool {
		return tr.find(func(m map[string]any) bool { return m["kind"] == "user" && m["state"] == "queued" }) != nil
	})
	t.check(tr.find(func(m map[string]any) bool { return m["kind"] == "user" && m["state"] == "queued" }) != nil, "queued item visible")
	ok := t.waitTurns(id, 2, 5*time.Minute)
	t.check(ok, "both turns finished")
	tr.dump()
	last := ""
	for _, it := range tr.items {
		if it["kind"] == "agent" {
			last, _ = it["text"].(string)
		}
	}
	t.check(strings.Contains(last, "QUEUED-OK"), "queued message answered (last agent text %q)", last)
}

func (t *tester) scCancel() {
	dir := filepath.Join(t.ws, "cancel")
	_ = os.MkdirAll(dir, 0o755)
	id := t.create(map[string]any{"cwd": dir, "model": testModel})
	if id == "" {
		return
	}
	tr := t.subscribe(id)
	t.call("session.prompt", map[string]any{"id": id, "text": "Run the shell command `sleep 90` in the foreground and wait for it to finish, then say done."})
	t.pump(40*time.Second, func() bool {
		return tr.find(func(m map[string]any) bool {
			tl, _ := m["tool"].(map[string]any)
			return tl != nil && tl["kind"] == "execute"
		}) != nil
	})
	time.Sleep(2 * time.Second)
	t.call("session.cancel", map[string]any{"id": id})
	ok := t.pump(60*time.Second, func() bool {
		return tr.find(func(m map[string]any) bool {
			tu, _ := m["turn"].(map[string]any)
			return tu != nil && tu["stopReason"] == "cancelled"
		}) != nil
	})
	t.check(ok, "turn ended as cancelled")
	tr.dump()
}

func (t *tester) scPerm() {
	dir := filepath.Join(t.ws, "perm")
	_ = os.MkdirAll(dir, 0o755)
	id := t.create(map[string]any{"cwd": dir, "model": testModel, "skipPermissions": false, "mode": "default"})
	if id == "" {
		return
	}
	tr := t.subscribe(id)
	t.call("session.prompt", map[string]any{"id": id, "text": "Run the shell command `echo perm-ok` and tell me its output."})
	var perm map[string]any
	ok := t.pump(90*time.Second, func() bool {
		perm = tr.find(func(m map[string]any) bool { return m["kind"] == "perm" && m["state"] == "pending" })
		return perm != nil
	})
	t.check(ok, "permission request surfaced")
	t.pump(5*time.Second, func() bool {
		for _, n := range t.notifs {
			if n["sessionId"] == id && n["kind"] == "permission" {
				return t.sums[id]["state"] == "waiting"
			}
		}
		return false
	})
	gotNotif := false
	for _, n := range t.notifs {
		if n["sessionId"] == id && n["kind"] == "permission" {
			gotNotif = true
		}
	}
	t.check(gotNotif, "permission notification sent")
	t.check(t.sums[id]["state"] == "waiting", "session state waiting (%v)", t.sums[id]["state"])
	if perm != nil {
		p := perm["perm"].(map[string]any)
		opt := ""
		for _, o := range p["options"].([]any) {
			om := o.(map[string]any)
			if strings.HasPrefix(om["kind"].(string), "allow") {
				opt = om["optionId"].(string)
				break
			}
		}
		t.call("session.permission", map[string]any{"id": id, "reqId": p["reqId"], "optionId": opt})
	}
	ok = t.waitTurns(id, 1, 3*time.Minute)
	t.check(ok, "turn finished after approval")
	tr.dump()
	t.check(tr.find(func(m map[string]any) bool { return m["kind"] == "perm" && m["state"] == "resolved" }) != nil, "permission resolved")
}

func (t *tester) scQuestion() {
	dir := filepath.Join(t.ws, "question")
	_ = os.MkdirAll(dir, 0o755)
	id := t.create(map[string]any{"cwd": dir, "model": testModel})
	if id == "" {
		return
	}
	tr := t.subscribe(id)
	t.call("session.prompt", map[string]any{"id": id, "text": "Use your ask_question tool to ask me which color I prefer (options: Red, Blue). Do not do anything else."})
	ok := t.waitTurns(id, 1, 3*time.Minute)
	t.check(ok, "turn finished")
	tr.dump()
	t.check(t.sums[id]["attention"] == "question", "attention=question (%v)", t.sums[id]["attention"])
	t.call("session.prompt", map[string]any{"id": id, "text": "Which color do you prefer?: Blue"})
	ok = t.waitTurns(id, 2, 3*time.Minute)
	t.check(ok && t.sums[id]["attention"] == nil, "answered; attention cleared (%v)", t.sums[id]["attention"])
}

func (t *tester) scPlan() {
	dir := filepath.Join(t.ws, "plan")
	_ = os.MkdirAll(dir, 0o755)
	id := t.create(map[string]any{"cwd": dir, "model": testModel, "mode": "plan"})
	if id == "" {
		return
	}
	tr := t.subscribe(id)
	t.call("session.prompt", map[string]any{"id": id, "text": "Create fib.py that prints the first 10 Fibonacci numbers. Keep the plan very short."})
	ok := t.waitTurns(id, 1, 4*time.Minute)
	t.check(ok, "turn finished")
	tr.dump()
	t.check(t.sums[id]["attention"] == "plan", "attention=plan (%v)", t.sums[id]["attention"])
}

func (t *tester) scOps() {
	dir := filepath.Join(t.ws, "ops")
	_ = os.MkdirAll(dir, 0o755)
	id := t.create(map[string]any{"cwd": dir, "model": testModel})
	if id == "" {
		return
	}
	tr := t.subscribe(id)
	t.call("session.prompt", map[string]any{"id": id, "text": "Reply with exactly: ONE"})
	t.waitTurns(id, 1, 3*time.Minute)
	t.call("session.prompt", map[string]any{"id": id, "text": "Reply with exactly: TWO"})
	t.waitTurns(id, 2, 3*time.Minute)
	tr.dump()
	// rename
	t.call("session.rename", map[string]any{"id": id, "title": "itest renamed"})
	t.pump(3*time.Second, func() bool { return t.sums[id]["title"] == "itest renamed" })
	t.check(t.sums[id]["title"] == "itest renamed", "renamed (%v)", t.sums[id]["title"])
	// fork before the second user message
	var second string
	n := 0
	for _, it := range tr.items {
		if it["kind"] == "user" {
			n++
			if n == 2 {
				second = it["id"].(string)
			}
		}
	}
	r := t.call("session.fork", map[string]any{"id": id, "itemId": second})
	if r != nil && r["session"] != nil {
		fid := r["session"].(map[string]any)["id"].(string)
		t.created = append(t.created, fid)
		t.check(r["prefill"] == "Reply with exactly: TWO", "fork prefill = %q", r["prefill"])
		ftr := t.subscribe(fid)
		t.pump(30*time.Second, func() bool { return len(ftr.items) > 0 && t.sums[fid] != nil && t.sums[fid]["state"] == "idle" })
		ftr.dump()
		users := 0
		for _, it := range ftr.items {
			if it["kind"] == "user" {
				users++
			}
		}
		t.check(users == 1, "fork has 1 user message (got %d)", users)
		// delete the fork
		t.call("session.delete", map[string]any{"id": fid})
		_, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".gemini", "antigravity-cli", "conversations", fid+".db"))
		t.check(os.IsNotExist(err), "fork deleted from agy storage")
	}
	// rewind the original to before the second message
	r = t.call("session.rewind", map[string]any{"id": id, "itemId": second, "conversationOnly": true})
	t.check(r != nil && r["prefill"] == "Reply with exactly: TWO", "rewind prefill %v", r["prefill"])
	t.pump(20*time.Second, func() bool {
		u := 0
		for _, it := range tr.items {
			if it["kind"] == "user" {
				u++
			}
		}
		return u == 1
	})
	users := 0
	for _, it := range tr.items {
		if it["kind"] == "user" {
			users++
		}
	}
	tr.dump()
	t.check(users == 1, "after rewind 1 user message (got %d)", users)
	// archive / unarchive / pin
	t.call("session.archive", map[string]any{"id": id, "archived": true})
	list := t.listMap(true)
	t.check(list[id] != nil && list[id]["archived"] == true, "archived flag set")
	t.call("session.archive", map[string]any{"id": id, "archived": false})
	list = t.listMap(true)
	t.check(list[id] != nil && list[id]["archived"] == false, "archived flag cleared")
	t.call("session.pin", map[string]any{"id": id, "pinned": true})
	list = t.listMap(true)
	t.check(list[id] != nil && list[id]["pinned"] == true, "pinned flag set")
	// export, changes, diff, artifacts
	ex := t.call("session.export", map[string]any{"id": id})
	t.check(ex != nil && strings.Contains(fmt.Sprint(ex["markdown"]), "ONE"), "export markdown contains ONE")
	d := t.call("session.diff", map[string]any{"id": id})
	t.check(d != nil && d["repo"] == false, "git diff reports non-repo")
	_, err := t.callErr("session.changes", map[string]any{"id": id})
	t.check(err == nil, "agent changes call ok (%v)", err)
	// model change
	t.call("session.setModel", map[string]any{"id": id, "model": "gemini-3.8-flash-medium"})
	t.pump(3*time.Second, func() bool { return t.sums[id]["model"] == "gemini-3.8-flash-medium" })
	t.check(t.sums[id]["model"] == "gemini-3.8-flash-medium", "model switched (%v)", t.sums[id]["model"])
	// stop (hibernate) and wake on prompt
	t.call("session.stop", map[string]any{"id": id})
	t.pump(30*time.Second, func() bool { return t.sums[id]["state"] == "sleeping" })
	t.check(t.sums[id]["state"] == "sleeping", "session hibernated")
	t.call("session.prompt", map[string]any{"id": id, "text": "Reply with exactly: THREE"})
	ok := t.pump(4*time.Minute, func() bool {
		return tr.find(func(m map[string]any) bool {
			return m["kind"] == "agent" && strings.Contains(fmt.Sprint(m["text"]), "THREE")
		}) != nil
	})
	t.check(ok, "resumed after hibernation and answered")
	t.waitTurns(id, 3, time.Minute)
	tr.dump()
}

func (t *tester) listMap(force bool) map[string]map[string]any {
	res, err := t.c.Call("sessions.list", map[string]any{"force": force}, 2*time.Minute)
	out := map[string]map[string]any{}
	if err != nil {
		t.check(false, "sessions.list: %v", err)
		return out
	}
	for _, s := range res.([]any) {
		m := s.(map[string]any)
		out[m["id"].(string)] = m
	}
	return out
}

func (t *tester) scFS() {
	base := filepath.Join(t.ws, "fs")
	h := t.call("fs.home", nil)
	t.check(h != nil && h["home"] != nil, "fs.home")
	t.call("fs.mkdir", map[string]any{"path": base + "/a/b", "parents": true})
	w := t.call("fs.write", map[string]any{"path": base + "/a/b/x.txt", "text": "héllo\nworld\n"})
	t.check(w != nil && w["size"].(float64) == 13, "write size %v", w["size"])
	r := t.call("fs.read", map[string]any{"path": base + "/a/b/x.txt"})
	t.check(r != nil && r["text"] == "héllo\nworld\n", "read back text")
	_, err := t.callErr("fs.write", map[string]any{"path": base + "/a/b/x.txt", "text": "z", "expectMtime": 1})
	t.check(err != nil && strings.Contains(err.Error(), "changed on disk"), "conflict detected (%v)", err)
	t.call("fs.copy", map[string]any{"from": base + "/a/b/x.txt", "to": base + "/a/y.txt"})
	t.call("fs.rename", map[string]any{"from": base + "/a/y.txt", "to": base + "/a/z.txt"})
	l := t.call("fs.list", map[string]any{"path": base + "/a"})
	names := []string{}
	if l != nil {
		for _, e := range l["entries"].([]any) {
			names = append(names, e.(map[string]any)["name"].(string))
		}
	}
	t.check(strings.Join(names, ",") == "b,z.txt", "list = %v", names)
	s, err := t.callErr("fs.search", map[string]any{"root": base, "query": "x.t"})
	t.check(err == nil && len(s.([]any)) == 1, "search finds x.txt")
	cpl, err := t.callErr("fs.complete", map[string]any{"cwd": base, "query": "xtx"})
	t.check(err == nil && cpl != nil && len(cpl.([]any)) >= 1, "complete fuzzy matches")
	_, err = t.callErr("fs.delete", map[string]any{"paths": []string{base + "/a/z.txt"}})
	t.check(err != nil && strings.Contains(err.Error(), "TRASH_UNSUPPORTED"), "trash on /tmp reports TRASH_UNSUPPORTED")
	t.call("fs.delete", map[string]any{"paths": []string{base}, "permanent": true})
	_, err = os.Stat(base)
	t.check(os.IsNotExist(err), "permanently deleted dir")
	// Chunked upload/download over the encrypted channel (binary-safe, multi-chunk).
	payload := make([]byte, 2_500_000)
	for i := range payload {
		payload[i] = byte(i*31 + i/7)
	}
	up := t.call("fs.uploadStart", map[string]any{"path": t.ws + "/up/u.bin", "mkdirs": true})
	ok := up != nil
	if ok {
		id := up["id"]
		for off := 0; off < len(payload) && ok; off += 700_000 {
			end := min(off+700_000, len(payload))
			r := t.call("fs.uploadChunk", map[string]any{"id": id, "offset": off, "data": base64.StdEncoding.EncodeToString(payload[off:end])})
			ok = r != nil && int(r["written"].(float64)) == end
		}
		fin := t.call("fs.uploadFinish", map[string]any{"id": id})
		ok = ok && fin != nil && int(fin["bytes"].(float64)) == len(payload)
	}
	t.check(ok, "chunked upload (%d bytes)", len(payload))
	var got []byte
	for off := 0; ; {
		r := t.call("fs.download", map[string]any{"path": t.ws + "/up/u.bin", "offset": off, "length": 1 << 20})
		if r == nil {
			break
		}
		chunk, _ := base64.StdEncoding.DecodeString(r["data"].(string))
		got = append(got, chunk...)
		off += len(chunk)
		if r["eof"] == true || len(chunk) == 0 {
			break
		}
	}
	t.check(bytes.Equal(got, payload), "chunked download returns the same %d bytes", len(got))
	// Security: an unpaired key can't connect, and the old plaintext API is gone.
	stranger := *t.cr
	k, _ := noise.DH25519.GenerateKeypair(rand.Reader)
	stranger.DevicePriv, stranger.DevicePub = base64.StdEncoding.EncodeToString(k.Private), base64.StdEncoding.EncodeToString(k.Public)
	_, err = dial(&stranger, secure.Hello{Purpose: "rpc"})
	t.check(err != nil, "unpaired device refused (%v)", err)
	httpBase := strings.Replace(strings.Replace(t.cr.URL, "wss://", "https://", 1), "ws://", "http://", 1)
	res, err := http.Get(strings.TrimRight(httpBase, "/") + "/api/info")
	t.check(err == nil && res.StatusCode == 404, "no plaintext API (/api/info → %v)", statusOf(res, err))
}

func statusOf(res *http.Response, err error) string {
	if err != nil {
		return err.Error()
	}
	return res.Status
}

func (t *tester) scTerm() {
	r := t.call("term.create", map[string]any{"kind": "shell", "cwd": t.ws, "cols": 100, "rows": 30})
	if r == nil {
		return
	}
	id := r["id"].(string)
	t.check(id != "", "terminal %s created", id)
	conn, err := dial(t.cr, secure.Hello{Purpose: "term", Term: id, Cols: 100, Rows: 30})
	t.check(err == nil, "attached over an encrypted connection (%v)", err)
	if err == nil {
		_ = conn.WriteMessage(secure.TextMessage, []byte(`{"type":"resize","cols":100,"rows":30}`)) // first message starts output
	}
	if err == nil {
		got := make(chan string, 1)
		go func() {
			var acc strings.Builder
			for {
				_, b, err := conn.ReadMessage()
				if err != nil {
					got <- acc.String()
					return
				}
				acc.Write(b)
				if strings.Contains(acc.String(), "agentc-term-ok-42") {
					got <- acc.String()
					return
				}
			}
		}()
		time.Sleep(1500 * time.Millisecond)
		_ = conn.WriteMessage(secure.BinaryMessage, []byte("echo agentc-term-ok-$((40+2))\r"))
		select {
		case s := <-got:
			t.check(strings.Contains(s, "agentc-term-ok-42"), "shell output received through PTY")
		case <-time.After(15 * time.Second):
			t.check(false, "terminal output timeout")
		}
		_ = conn.WriteMessage(secure.TextMessage, []byte(`{"type":"resize","cols":80,"rows":20}`))
		time.Sleep(500 * time.Millisecond)
		conn.Close()
	}
	cap := t.call("term.capture", map[string]any{"id": id})
	t.check(cap != nil && strings.Contains(fmt.Sprint(cap["text"]), "agentc-term-ok-42"), "capture shows output")
	lst, _ := t.callErr("term.list", nil)
	found := false
	for _, x := range lst.([]any) {
		if x.(map[string]any)["id"] == id {
			found = true
		}
	}
	t.check(found, "terminal listed")
	t.call("term.kill", map[string]any{"id": id})
}

// scFlags checks that the app's saved agy flags reach the real processes.
func (t *tester) scFlags() {
	dir := filepath.Join(t.ws, "flags")
	_ = os.MkdirAll(dir, 0o755)
	id := t.create(map[string]any{"cwd": dir, "model": testModel, "skipPermissions": false, "sandbox": true,
		"thought": "max", "extraArgs": []string{"--include-default-customizations=false"}})
	if id == "" {
		return
	}
	t.subscribe(id)
	t.call("session.prompt", map[string]any{"id": id, "text": "Reply with just the word OK."})
	t.check(t.waitTurns(id, 1, 3*time.Minute), "turn finished")
	pid, _ := t.sums[id]["pid"].(float64)
	t.check(pid > 0, "session has a live agy process (pid %v)", pid)
	if pid > 0 {
		b, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", int(pid)))
		args := strings.Split(strings.TrimRight(string(b), "\x00"), "\x00")
		has := func(a string) bool {
			for _, x := range args {
				if x == a {
					return true
				}
			}
			return false
		}
		t.check(err == nil && has("--sandbox") && has("--include-default-customizations=false"), "agy acp got --sandbox and the extra args")
		t.check(!has("--dangerously-skip-permissions"), "no --dangerously-skip-permissions when switched off")
	}
	th := t.sums[id]["thought"]
	t.check(th != nil && th != "max" && th != "", "effort max fitted to a supported thought level (%v)", th)

	r := t.call("term.create", map[string]any{"kind": "agy", "cwd": dir, "skipPermissions": false, "args": []string{"--sandbox", "--effort", "max"}})
	if r != nil {
		cmd, _ := r["command"].(string)
		plain := strings.ReplaceAll(cmd, "'", "") // arguments are shell-quoted
		t.check(strings.Contains(plain, "--sandbox --effort max") && !strings.Contains(plain, "--dangerously-skip-permissions"), "agy terminal command %q", cmd)
		t.call("term.kill", map[string]any{"id": r["id"]})
	}
	r = t.call("term.create", map[string]any{"kind": "agy", "cwd": dir, "skipPermissions": true})
	if r != nil {
		cmd, _ := r["command"].(string)
		t.check(strings.Contains(cmd, "--dangerously-skip-permissions"), "skip on: terminal command %q", cmd)
		t.call("term.kill", map[string]any{"id": r["id"]})
	}
}

func (t *tester) scHistory() {
	list := t.listMap(true)
	hist := 0
	var target string
	for id, s := range list {
		if s["state"] == "history" && s["agent"] != "claude" {
			hist++
			if target == "" && strings.Contains(fmt.Sprint(s["title"]), "Hello.txt") {
				target = id
			}
		}
	}
	t.check(len(list) > 0, "sessions.list returned %d (%d history)", len(list), hist)
	if target == "" {
		for id, s := range list {
			if s["state"] == "history" && s["agent"] != "claude" {
				target = id
				break
			}
		}
	}
	if target == "" {
		return
	}
	tr := t.subscribe(target)
	ok := t.pump(90*time.Second, func() bool { return len(tr.items) > 0 && t.sums[target] != nil && t.sums[target]["state"] == "idle" })
	t.check(ok, "history conversation %s loaded with %d items", target, len(tr.items))
	tr.dump()
	t.call("session.stop", map[string]any{"id": target})
	t.call("session.forget", map[string]any{"id": target})
	_ = json.Marshal
}

// live polls session.live until cond holds.
func (t *tester) live(id string, timeout time.Duration, cond func(l map[string]any) bool) map[string]any {
	deadline := time.Now().Add(timeout)
	var l map[string]any
	for time.Now().Before(deadline) {
		l = t.call("session.live", map[string]any{"id": id})
		if l != nil && cond(l) {
			return l
		}
		t.pump(1500*time.Millisecond, func() bool { return false })
	}
	return l
}

func list(m map[string]any, k string) []any {
	v, _ := m[k].([]any)
	return v
}

// scLive: background tasks with live output, artifacts + comments, subagents.
func (t *tester) scLive() {
	dir := filepath.Join(t.ws, "live")
	_ = os.MkdirAll(dir, 0o755)
	id := t.create(map[string]any{"cwd": dir, "model": testModel, "skipPermissions": true})
	if id == "" {
		return
	}
	tr := t.subscribe(id)
	// 1. A long background command: visible as a task, with live output.
	t.call("session.prompt", map[string]any{"id": id, "text": "Run this shell command in the background without waiting for it: `for i in $(seq 1 40); do echo tick $i; sleep 1; done`. Then end your turn immediately."})
	l := t.live(id, 90*time.Second, func(l map[string]any) bool { return len(list(l, "tasks")) > 0 })
	tasks := list(l, "tasks")
	t.check(len(tasks) == 1, "background task listed (%d)", len(tasks))
	if len(tasks) == 1 {
		task := tasks[0].(map[string]any)
		taskID := task["id"].(string)
		var out string
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) && !strings.Contains(out, "tick 3") {
			r := t.call("session.taskOutput", map[string]any{"id": id, "taskId": taskID, "offset": 0})
			if r != nil {
				out, _ = r["data"].(string)
			}
			time.Sleep(time.Second)
		}
		t.check(strings.Contains(out, "tick 3"), "task output streams live (%q)", strings.TrimSpace(out[max(0, len(out)-30):]))
		t.call("session.taskStop", map[string]any{"id": id, "step": task["step"]})
		l = t.live(id, 30*time.Second, func(l map[string]any) bool { return len(list(l, "tasks")) == 0 })
		t.check(len(list(l, "tasks")) == 0, "task stopped")
	}
	t.waitTurns(id, 1, 3*time.Minute)
	// 2. An artifact that asks for feedback, then a comment on it.
	n := t.sums[id]["turns"].(float64)
	t.call("session.prompt", map[string]any{"id": id, "text": "Write a short implementation plan artifact for adding a hello.py script to this folder (two steps). Only write the plan artifact; do not implement it."})
	t.waitTurns(id, n+1, 4*time.Minute)
	l = t.live(id, 30*time.Second, func(l map[string]any) bool {
		for _, a := range list(l, "artifacts") {
			if a.(map[string]any)["kind"] == "markdown" {
				return true
			}
		}
		return false
	})
	var art map[string]any
	for _, a := range list(l, "artifacts") {
		if a.(map[string]any)["kind"] == "markdown" {
			art = a.(map[string]any)
		}
	}
	t.check(art != nil, "markdown artifact listed (%v, requestFeedback=%v)", art["name"], art["requestFeedback"])
	if art != nil {
		r := t.call("session.artifact", map[string]any{"id": id, "path": art["path"]})
		content, _ := r["content"].(string)
		t.check(strings.Contains(strings.ToLower(content), "hello"), "artifact content readable (%d bytes)", len(content))
		n = t.sums[id]["turns"].(float64)
		t.call("session.comment", map[string]any{"id": id, "comments": []map[string]any{{"kind": "artifact", "uri": art["uri"], "startLine": 1, "endLine": 1,
			"quote": strings.SplitN(content, "\n", 2)[0], "comment": "Please add a third step that prints the Python version. Update the plan artifact; still don't implement."}}})
		ok := t.waitTurns(id, n+1, 4*time.Minute)
		t.check(ok, "comment delivered and the agent responded")
		t.check(tr.find(func(m map[string]any) bool {
			return m["kind"] == "user" && strings.Contains(fmt.Sprint(m["text"]), "Comments on artifact URI")
		}) != nil, "comment arrived as agy-style feedback")
		r = t.call("session.artifact", map[string]any{"id": id, "path": art["path"]})
		c2, _ := r["content"].(string)
		t.check(c2 != content && strings.Contains(strings.ToLower(c2), "version"), "agent updated the artifact from the comment")
	}
	// 3. A subagent with a readable transcript.
	n = t.sums[id]["turns"].(float64)
	t.call("session.prompt", map[string]any{"id": id, "text": "Use a subagent to count the files in this folder and report the number back to me."})
	t.waitTurns(id, n+1, 4*time.Minute)
	l = t.live(id, 60*time.Second, func(l map[string]any) bool { return len(list(l, "subagents")) > 0 })
	subs := list(l, "subagents")
	t.check(len(subs) > 0, "subagent listed (%v)", subs)
	if len(subs) > 0 {
		sub := subs[0].(map[string]any)
		var steps []any
		for i := 0; i < 30 && len(steps) < 2; i++ {
			r := t.call("session.subagent", map[string]any{"id": id, "subId": sub["id"]})
			steps = list(r, "steps")
			time.Sleep(2 * time.Second)
		}
		t.check(len(steps) >= 2, "subagent transcript readable (%d steps, role %v)", len(steps), sub["role"])
	}
	t.check(len(list(l, "messages")) > 0, "agent messages listed (%d)", len(list(l, "messages")))
}
