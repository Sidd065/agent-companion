package term

import (
	"io"
	"strings"
	"testing"
)

func TestParseOSC7501(t *testing.T) {
	s := ParseOSC7501("state=blocked:kind=permission:app=antigravity:msg=VHJ1c3QgdGhpcyBmb2xkZXI/")
	if s.State != "blocked" || s.Kind != "permission" || s.Msg != "Trust this folder?" {
		t.Fatalf("bad parse: %+v", s)
	}
	d := ParseOSC7501("state=done:app=antigravity:msg=T0s=")
	if d.State != "done" || d.Msg != "OK" {
		t.Fatalf("bad parse: %+v", d)
	}
}

type chunkReader struct {
	chunks []string
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if len(c.chunks) == 0 {
		return 0, ioEOF
	}
	n := copy(p, c.chunks[0])
	c.chunks = c.chunks[1:]
	return n, nil
}

func TestWatchOSCSplitSequences(t *testing.T) {
	// The watcher calls publish(); with an empty sock path tmux fails harmlessly.
	full := "hello\x1b]7501;state=working:app=antigravity\x07more\x1b]7501;state=done:app=antigravity:msg=T0s=\x1b\\end"
	for split := 1; split < len(full); split++ {
		r := &chunkReader{chunks: []string{full[:split], full[split:]}}
		if err := WatchOSC(r, "", "", "x", ""); err != nil {
			t.Fatal(err)
		}
	}
	_ = strings.TrimSpace
}

var ioEOF = io.EOF
