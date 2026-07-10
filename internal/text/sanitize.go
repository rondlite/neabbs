// Package text holds input sanitization: every string a player types that can
// end up on another player's terminal must pass through here on write.
package text

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var handleRe = regexp.MustCompile(`^[a-z0-9_-]{3,16}$`)

// ValidHandle reports whether h is an acceptable player handle:
// 3-16 chars, lowercase [a-z0-9_-].
func ValidHandle(h string) bool {
	return handleRe.MatchString(h)
}

// Clean strips all control characters (including ESC, so ANSI/terminal escape
// sequences can never be smuggled into other players' screens) from s.
// Newlines and tabs are preserved for multi-line bodies.
func Clean(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f: // C0 controls incl. ESC (0x1b), DEL
			// drop
		case r >= 0x80 && r <= 0x9f: // C1 controls (CSI 0x9b etc.)
			// drop
		case r == utf8.RuneError:
			// Drop invalid bytes (raw 0x80-0x9f C1 bytes arrive as RuneError);
			// a literal U+FFFD has no legitimate use in player input either.
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// CleanLine is Clean for single-line fields (handles, subjects, chat):
// control chars dropped, newlines and tabs collapsed to single spaces,
// then trimmed.
func CleanLine(s string) string {
	c := Clean(s)
	c = strings.ReplaceAll(c, "\n", " ")
	c = strings.ReplaceAll(c, "\t", " ")
	return strings.TrimSpace(c)
}
