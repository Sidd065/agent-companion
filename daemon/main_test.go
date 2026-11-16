package main

import (
	"reflect"
	"testing"
)

func TestAgyrArgs(t *testing.T) {
	skipDefault := []string{"--dangerously-skip-permissions"}
	cases := []struct {
		defaults, args, want []string
		skip                 bool
	}{
		{skipDefault, nil, nil, true},
		{skipDefault, []string{"--safe"}, nil, false},
		{skipDefault, []string{"-c", "--model", "x"}, []string{"-c", "--model", "x"}, true},
		{[]string{"--model", "gemini-4-argon", "--dangerously-skip-permissions", "--sandbox"}, []string{"--model", "y"},
			[]string{"--model", "gemini-4-argon", "--sandbox", "--model", "y"}, true},
		{[]string{}, []string{"--conversation", "abc"}, []string{"--conversation", "abc"}, false},
	}
	for i, c := range cases {
		got, skip := agyrArgs(c.defaults, c.args)
		if !reflect.DeepEqual(got, c.want) || skip != c.skip {
			t.Errorf("case %d: got %q skip=%v, want %q skip=%v", i, got, skip, c.want, c.skip)
		}
	}
}
