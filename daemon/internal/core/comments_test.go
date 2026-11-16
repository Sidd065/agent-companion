package core

import (
	"strings"
	"testing"
)

func TestBuildCommentPrompt(t *testing.T) {
	p := BuildCommentPrompt([]Comment{
		{Kind: "artifact", URI: "/b/plan.md", StartLine: 3, EndLine: 4, Quote: "## Step 2\nadd tests", Comment: "Use pytest"},
		{Kind: "artifact", URI: "/b/plan.md", Quote: "Verification", Comment: "also lint"},
		{Kind: "diff", URI: "/w/app.py", StartLine: 10, Quote: "x = 1", Comment: "rename x"},
		{Kind: "artifact", URI: "/b/plan.md", Comment: "   "}, // empty: dropped
	}, "thanks")
	for _, want := range []string{
		"The user has left some comments on their files.",
		"Comments on artifact URI: file:///b/plan.md",
		"They commented between lines 3 and 4, which had the below contents.\n```\n## Step 2\nadd tests\n```\nThe user's comment was: Use pytest",
		"They commented on the below contents.\n```\nVerification\n```\nThe user's comment was: also lint",
		"The user has left some comments on the file diffs.",
		"Comments on file: /w/app.py\nThey commented between lines 10 and 10",
		"thanks\n\nPlease respond to the user and take whatever actions they ask you to.",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("missing %q in:\n%s", want, p)
		}
	}
	if strings.Count(p, "Comments on artifact URI") != 1 {
		t.Errorf("artifact header repeated:\n%s", p)
	}
}
