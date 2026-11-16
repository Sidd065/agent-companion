// Package qrterm renders QR codes for terminals.
package qrterm

import (
	"strings"

	"rsc.io/qr"
)

// Render returns text as a QR code drawn with half-block characters. Colors
// are forced (black modules on white) so it scans on dark and light themes.
func Render(text string) (string, error) {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return "", err
	}
	const quiet = 2
	n := code.Size + 2*quiet
	dark := func(x, y int) bool {
		x, y = x-quiet, y-quiet
		return x >= 0 && y >= 0 && x < code.Size && y < code.Size && code.Black(x, y)
	}
	const fgDark, fgLight, bgDark, bgLight = "30", "97", "40", "107"
	var b strings.Builder
	for y := 0; y < n; y += 2 {
		for x := 0; x < n; x++ {
			fg, bg := fgLight, bgLight
			if dark(x, y) {
				fg = fgDark
			}
			if dark(x, y+1) {
				bg = bgDark
			}
			b.WriteString("\x1b[" + fg + ";" + bg + "m▀")
		}
		b.WriteString("\x1b[0m\n")
	}
	return b.String(), nil
}
