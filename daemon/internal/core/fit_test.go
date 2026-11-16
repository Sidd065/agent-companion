package core

import (
	"testing"

	"agentc/internal/acp"
)

func TestFitThought(t *testing.T) {
	opts := []acp.ConfigOption{{ID: "thought_level", Options: []acp.ConfigOptionValue{{Value: "low"}, {Value: "medium"}, {Value: "high"}}}}
	cases := map[string]string{"": "", "low": "low", "medium": "medium", "high": "high", "xhigh": "high", "max": "high", "minimal": "low", "weird": "weird"}
	for in, want := range cases {
		if got := fitThought(opts, in); got != want {
			t.Errorf("fitThought(%q) = %q, want %q", in, got, want)
		}
	}
	if got := fitThought(nil, "max"); got != "max" {
		t.Errorf("no options: got %q", got)
	}
}
