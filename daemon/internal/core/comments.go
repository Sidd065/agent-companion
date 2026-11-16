package core

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Comment is feedback the user left on an artifact, a file or a diff line,
// from the app's comment mode.
type Comment struct {
	Kind      string `json:"kind"` // artifact | file | diff
	URI       string `json:"uri"`  // artifact URI (file://…) or file path
	StartLine int    `json:"startLine,omitempty"`
	EndLine   int    `json:"endLine,omitempty"`
	Quote     string `json:"quote,omitempty"` // the commented text
	Comment   string `json:"comment"`
	Approve   bool   `json:"approve,omitempty"` // "looks good, proceed"
}

// BuildCommentPrompt turns comments into a user message, using the same
// phrasing agy uses for comments left in Antigravity's own UI so the agent
// treats them the way it was trained to.
func BuildCommentPrompt(cs []Comment, extra string) string {
	var arts, diffs []Comment
	for _, c := range cs {
		if strings.TrimSpace(c.Comment) == "" && !c.Approve {
			continue
		}
		if c.Kind == "diff" {
			diffs = append(diffs, c)
		} else {
			arts = append(arts, c)
		}
	}
	var b strings.Builder
	write := func(header string, list []Comment) {
		if len(list) == 0 {
			return
		}
		b.WriteString(header + "\n\n")
		last := ""
		for _, c := range list {
			uri := c.URI
			if !strings.Contains(uri, "://") && c.Kind != "diff" {
				uri = "file://" + uri
			}
			if uri != last {
				if c.Kind == "diff" {
					b.WriteString("Comments on file: " + filepath.Clean(strings.TrimPrefix(uri, "file://")) + "\n")
				} else {
					b.WriteString("Comments on artifact URI: " + uri + "\n")
				}
				last = uri
			}
			if c.StartLine > 0 {
				end := max(c.EndLine, c.StartLine)
				b.WriteString(fmt.Sprintf("They commented between lines %d and %d, which had the below contents.\n", c.StartLine, end))
			} else if c.Quote != "" {
				b.WriteString("They commented on the below contents.\n")
			}
			if q := strings.TrimRight(c.Quote, "\n"); q != "" {
				b.WriteString("```\n" + q + "\n```\n")
			}
			if c.Approve && strings.TrimSpace(c.Comment) == "" {
				b.WriteString("The user approved this.\n\n")
			} else {
				b.WriteString("The user's comment was: " + strings.TrimSpace(c.Comment) + "\n\n")
			}
		}
	}
	write("The user has left some comments on their files.", arts)
	write("The user has left some comments on the file diffs.", diffs)
	if strings.TrimSpace(extra) != "" {
		b.WriteString(strings.TrimSpace(extra) + "\n\n")
	}
	b.WriteString("Please respond to the user and take whatever actions they ask you to.")
	return b.String()
}
