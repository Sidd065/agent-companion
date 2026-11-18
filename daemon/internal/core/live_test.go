package core

import (
	"encoding/base64"
	"testing"
)

func TestUserPayloadText(t *testing.T) {
	// A CortexStep with a few scalar fields, then the user input (field 19)
	// holding the text (field 2) and a nested copy (field 3).
	text := "Keep going, and deploy when green"
	inner := append([]byte{0x12, byte(len(text))}, text...)
	inner = append(inner, 0x1a, 0x04, 0x0a, 0x02, 'h', 'i')
	msg := []byte{0x08, 0x0e, 0x20, 0x03, 0x2a, 0x02, 0x0a, 0x00}
	msg = append(msg, 0x9a, 0x01, byte(len(inner)))
	msg = append(msg, inner...)
	if got := userPayloadText(base64.StdEncoding.EncodeToString(msg)); got != text {
		t.Fatalf("got %q", got)
	}
	if got := userPayloadText(base64.RawStdEncoding.EncodeToString(msg)); got != text {
		t.Fatalf("unpadded: got %q", got)
	}
	for _, bad := range []string{"", "!!!", base64.StdEncoding.EncodeToString([]byte{0x9a, 0x01, 0x7f})} {
		if got := userPayloadText(bad); got != "" {
			t.Errorf("%q -> %q", bad, got)
		}
	}
}
