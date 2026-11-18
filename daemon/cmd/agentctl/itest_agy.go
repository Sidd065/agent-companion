package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// scAgyAuto: agy keeps working on its own after a turn ends (here, when a
// background command reports back). The session must show as working while
// it does, stream that work to subscribers, and settle as idle afterwards.
func (t *tester) scAgyAuto() {
	dir := filepath.Join(t.ws, "agy-auto")
	_ = os.MkdirAll(dir, 0o755)
	id := t.create(map[string]any{"cwd": dir, "model": testModel, "skipPermissions": true})
	if id == "" {
		return
	}
	fmt.Println("  session", id)
	tr := t.subscribe(id)
	t.call("session.prompt", map[string]any{"id": id, "text": "Start this shell command in the background and do not wait for it: `sleep 30; echo AUTO-DONE`. " +
		"Then reply with exactly STARTED and end your turn. Later, when the background command has finished, reply with exactly FINISHED."})
	t.check(t.waitTurns(id, 1, 3*time.Minute), "first turn ended")
	start := time.Now()
	seen := map[string]bool{}
	for _, it := range tr.items {
		seen[it["id"].(string)] = true
	}
	var states []string
	last, sawRunning := "", false
	var finishedAt time.Duration
	t.pump(4*time.Minute, func() bool {
		el := time.Since(start).Round(time.Second)
		st, _ := t.sums[id]["state"].(string)
		if st != last {
			states = append(states, fmt.Sprintf("%s@%s", st, el))
			last = st
		}
		if st == "running" {
			sawRunning = true
		}
		for _, it := range tr.items {
			iid := it["id"].(string)
			if seen[iid] {
				continue
			}
			seen[iid] = true
			txt, _ := it["text"].(string)
			if tl, ok := it["tool"].(map[string]any); ok {
				txt, _ = tl["title"].(string)
			}
			if len(txt) > 60 {
				txt = txt[:60]
			}
			fmt.Printf("  +%s %s %q (state %s)\n", el, it["kind"], strings.ReplaceAll(txt, "\n", " "), st)
			if s, _ := it["text"].(string); it["kind"] == "agent" && strings.Contains(s, "FINISHED") && finishedAt == 0 {
				finishedAt = el
			}
		}
		// Done once FINISHED arrived and the session has been idle again for a while.
		return finishedAt > 0 && st == "idle" && el > finishedAt+20*time.Second
	})
	fmt.Println("  states:", strings.Join(states, " → "))
	t.check(finishedAt > 0, "agy continued on its own (FINISHED after %s)", finishedAt)
	t.check(sawRunning, "the session showed as working while agy worked on its own")
	s := t.sums[id]
	t.check(s != nil && s["state"] == "idle", "the session settled as idle (%v)", s["state"])
}

// scAgyAside: a side question about an agy conversation is answered from its
// context on a throwaway copy: nothing joins the conversation, no file changes.
func (t *tester) scAgyAside() {
	dir := filepath.Join(t.ws, "agy-aside")
	_ = os.MkdirAll(dir, 0o755)
	id := t.create(map[string]any{"cwd": dir, "model": testModel, "skipPermissions": true})
	if id == "" {
		return
	}
	tr := t.subscribe(id)
	t.call("session.prompt", map[string]any{"id": id, "text": "Remember this: the codeword is PAPAYA-9. Reply with just OK."})
	t.check(t.waitTurns(id, 1, 3*time.Minute), "first turn ended")
	before := len(tr.items)
	hist := len(t.listMap(true))
	t.checkAside(id, "What is the codeword? Answer in one line.", "PAPAYA-9")
	x := t.askAside(id, "Create a file named side.txt containing hi in the current folder, then run `touch side2.txt`. Tell me what happened.")
	ans, _ := x["answer"].(string)
	fmt.Printf("  write attempt answered: %.120q\n", ans)
	_, e1 := os.Stat(filepath.Join(dir, "side.txt"))
	_, e2 := os.Stat(filepath.Join(dir, "side2.txt"))
	t.check(os.IsNotExist(e1) && os.IsNotExist(e2), "a side question can't change files")
	t.pump(3*time.Second, func() bool { return false })
	t.check(len(tr.items) == before, "nothing was added to the conversation (%d → %d items)", before, len(tr.items))
	t.check(len(t.listMap(true)) == hist, "the copies were removed from agy's history (%d → %d)", hist, len(t.listMap(true)))
	s := t.sums[id]
	t.check(s != nil && s["state"] == "idle", "the session stayed idle (%v)", s["state"])
}
