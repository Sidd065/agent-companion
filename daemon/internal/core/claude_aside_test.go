package core

import (
	"strings"
	"testing"
)

func TestAsidePrompt(t *testing.T) {
	list := []Aside{
		{ID: "a", Question: "Which file handles login?", Answer: "auth.go", State: "done"},
		{ID: "b", Question: "Why there?", Answer: "It owns sessions.", State: "done", FollowUp: "a"},
		{ID: "c", Question: "Broken", State: "failed"},
	}
	if p := asidePrompt(list, "Plain", ""); p != "Plain" {
		t.Fatalf("plain = %q", p)
	}
	if p := asidePrompt(list, "Plain", "c"); p != "Plain" {
		t.Fatalf("a failed side question is not context: %q", p)
	}
	p := asidePrompt(list, "And tests?", "b")
	for _, want := range []string{"Which file handles login?", "auth.go", "Why there?", "It owns sessions.", "Follow-up question: And tests?"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt is missing %q:\n%s", want, p)
		}
	}
	if strings.Index(p, "auth.go") > strings.Index(p, "It owns sessions.") {
		t.Errorf("the chain should be oldest first:\n%s", p)
	}
}
