package codex

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// FileUpdateChange matches Codex 0.172.0 FileUpdateChange wire items.
type FileUpdateChange struct {
	Path string `json:"path"`
	Diff string `json:"diff"`
	Kind struct {
		Type     string `json:"type"` // "add" | "delete" | "update"
		MovePath string `json:"move_path,omitempty"`
	} `json:"kind"`
}

// ParsedDiff holds normalized old/new text and starting line for both
// Android (Transcript.kt content diff blocks) and Desktop (Timeline.tsx rawInput).
type ParsedDiff struct {
	Path    string
	RelPath string
	Kind    string
	OldText string
	NewText string
	Line    int
}

var hunkHeaderRe = regexp.MustCompile(`^@@\s*-(\d+)(?:,(\d+))?\s+\+(\d+)(?:,(\d+))?\s*@@`)

// ParseFileChange converts one Codex FileUpdateChange into old/new text + line.
// In Codex 0.172.0:
// - kind.type == "add": diff is raw new file text (or unified hunks if prefixed with @@)
// - kind.type == "delete": diff is raw deleted file text (or unified hunks)
// - kind.type == "update": diff is unified diff hunks (@@ -a,b +c,d @@)
func ParseFileChange(cwd string, ch FileUpdateChange) ParsedDiff {
	absPath := ch.Path
	if absPath != "" && !filepath.IsAbs(absPath) && cwd != "" {
		absPath = filepath.Join(cwd, absPath)
	}
	rel := ch.Path
	if cwd != "" && filepath.IsAbs(absPath) {
		if r, err := filepath.Rel(cwd, absPath); err == nil && !strings.HasPrefix(r, "..") {
			rel = r
		}
	}
	kind := ch.Kind.Type
	if kind == "" {
		kind = "update"
	}
	pd := ParsedDiff{
		Path:    firstNonEmpty(absPath, ch.Path),
		RelPath: firstNonEmpty(rel, filepath.Base(ch.Path)),
		Kind:    kind,
		Line:    1,
	}

	switch kind {
	case "add":
		if containsUnifiedHunk(ch.Diff) {
			_, newTxt, line := parseUnifiedHunks(ch.Diff)
			pd.NewText = newTxt
			pd.Line = line
		} else {
			pd.NewText = ch.Diff
		}
	case "delete":
		if containsUnifiedHunk(ch.Diff) {
			oldTxt, _, line := parseUnifiedHunks(ch.Diff)
			pd.OldText = oldTxt
			pd.Line = line
		} else {
			pd.OldText = ch.Diff
		}
	default: // "update"
		if containsUnifiedHunk(ch.Diff) {
			oldTxt, newTxt, line := parseUnifiedHunks(ch.Diff)
			pd.OldText = oldTxt
			pd.NewText = newTxt
			pd.Line = line
		} else {
			pd.NewText = ch.Diff
		}
	}
	return pd
}

func containsUnifiedHunk(s string) bool {
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, "@@") {
			return true
		}
	}
	return false
}

func parseUnifiedHunks(diff string) (oldText, newText string, firstLine int) {
	firstLine = 1
	sawHunk := false
	var oldLines, newLines []string
	lines := strings.Split(strings.ReplaceAll(diff, "\r\n", "\n"), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	hunkCount := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git ") ||
			strings.HasPrefix(line, "index ") ||
			strings.HasPrefix(line, "--- ") ||
			strings.HasPrefix(line, "+++ ") ||
			strings.HasPrefix(line, "new file mode") ||
			strings.HasPrefix(line, "deleted file mode") {
			continue
		}
		if m := hunkHeaderRe.FindStringSubmatch(line); m != nil {
			if hunkCount > 0 && (len(oldLines) > 0 || len(newLines) > 0) {
				oldLines = append(oldLines, "...")
				newLines = append(newLines, "...")
			}
			if !sawHunk {
				if n, err := strconv.Atoi(m[3]); err == nil && n > 0 {
					firstLine = n
				}
				sawHunk = true
			}
			hunkCount++
			continue
		}
		if !sawHunk {
			continue
		}
		if strings.HasPrefix(line, `\ No newline at end of file`) {
			continue
		}
		switch {
		case strings.HasPrefix(line, "-"):
			oldLines = append(oldLines, line[1:])
		case strings.HasPrefix(line, "+"):
			newLines = append(newLines, line[1:])
		case strings.HasPrefix(line, " "):
			oldLines = append(oldLines, line[1:])
			newLines = append(newLines, line[1:])
		default:
			oldLines = append(oldLines, line)
			newLines = append(newLines, line)
		}
	}
	return strings.Join(oldLines, "\n"), strings.Join(newLines, "\n"), firstLine
}

// ParseUnifiedGitDiff splits a multi-file `diff --git` string (from turn/diff/updated)
// into ParsedDiff entries.
func ParseUnifiedGitDiff(cwd, gitDiff string) []ParsedDiff {
	if strings.TrimSpace(gitDiff) == "" {
		return nil
	}
	var out []ParsedDiff
	chunks := strings.Split("\n"+strings.ReplaceAll(gitDiff, "\r\n", "\n"), "\ndiff --git ")
	for _, chunk := range chunks[1:] {
		full := "diff --git " + chunk
		lines := strings.Split(full, "\n")
		var path string
		kind := "update"
		for _, l := range lines {
			if strings.HasPrefix(l, "new file mode") {
				kind = "add"
			} else if strings.HasPrefix(l, "deleted file mode") {
				kind = "delete"
			} else if strings.HasPrefix(l, "+++ b/") {
				path = strings.TrimPrefix(l, "+++ b/")
			} else if path == "" && strings.HasPrefix(l, "--- a/") {
				path = strings.TrimPrefix(l, "--- a/")
			}
		}
		if path == "" && len(lines) > 0 {
			fields := strings.Fields(lines[0])
			if len(fields) >= 4 {
				path = strings.TrimPrefix(fields[3], "b/")
			}
		}
		if path == "" {
			continue
		}
		var fc FileUpdateChange
		fc.Path = path
		fc.Diff = full
		fc.Kind.Type = kind
		out = append(out, ParseFileChange(cwd, fc))
	}
	return out
}

// BuildFileChangePayloads formats parsed file changes into dual-compatible ACP
// tool fields:
// - content: [{type: "diff", path, oldText, newText, line}] (Android Transcript.kt + diffcompact.go)
// - rawInput: {file_path, path, oldText, newText} (Desktop Timeline.tsx)
func BuildFileChangePayloads(cwd string, changes []FileUpdateChange) (toolName, title string, locations []map[string]any, content []map[string]any, rawInput map[string]any) {
	toolName = "Edit"
	if len(changes) == 1 && changes[0].Kind.Type == "add" {
		toolName = "Write"
	}
	var names []string
	rawInput = map[string]any{}
	for i, ch := range changes {
		pd := ParseFileChange(cwd, ch)
		names = append(names, pd.RelPath)
		locations = append(locations, map[string]any{"path": pd.Path})
		content = append(content, map[string]any{
			"type":    "diff",
			"path":    pd.Path,
			"oldText": pd.OldText,
			"newText": pd.NewText,
			"line":    pd.Line,
		})
		if i == 0 {
			rawInput["file_path"] = pd.Path
			rawInput["path"] = pd.Path
			rawInput["oldText"] = pd.OldText
			rawInput["newText"] = pd.NewText
		}
	}
	switch len(names) {
	case 0:
		title = toolName
	case 1:
		title = fmt.Sprintf("%s %s", toolName, names[0])
	case 2:
		title = fmt.Sprintf("%s %s, %s", toolName, names[0], names[1])
	default:
		title = fmt.Sprintf("%s %s (+%d more)", toolName, names[0], len(names)-1)
	}
	return toolName, title, locations, content, rawInput
}
