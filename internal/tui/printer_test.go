package tui

import (
	"strings"
	"testing"
)

// hasPartialEscape reports whether s ends in the middle of a CSI escape
// sequence (an ESC without its terminating final byte).
func hasPartialEscape(s string) bool {
	i := strings.LastIndexByte(s, 0x1b)
	if i == -1 {
		return false
	}
	rest := s[i:]
	// A complete CSI ends in a byte in the range @-~.
	for _, r := range rest[1:] {
		if r >= '@' && r <= '~' {
			return false
		}
	}
	return true
}

func TestTokenizeKeepsEscapesAtomic(t *testing.T) {
	// amber "hi" then reset, around a plain char.
	s := "\x1b[38;5;214mhi\x1b[0m!"
	cells := tokenize(s)
	// Expect: [esc][h][i][esc][!] = 5 cells; escapes zero-width.
	visible := 0
	for _, c := range cells {
		if c.visible {
			visible++
		}
	}
	if visible != 3 { // h, i, !
		t.Fatalf("visible cells = %d, want 3 (%+v)", visible, cells)
	}
	if cells[0].visible || cells[3].visible {
		t.Fatalf("escape cells marked visible: %+v", cells)
	}
}

func TestRevealNeverTearsEscape(t *testing.T) {
	// A coloured block revealed one visible char at a time (cps→1/tick).
	block := "\x1b[38;5;214mNEABBS\x1b[0m\n\x1b[1mbold\x1b[0m line"
	p := &printer{cps: 25} // 25*40/1000 = 1 visible cell per tick
	p.raw = block
	p.cells = tokenize(block)

	for step := 0; step < 100 && p.raw != ""; step++ {
		if frame := p.view(); hasPartialEscape(frame) {
			t.Fatalf("frame %d ends mid-escape: %q", step, frame)
		}
		p.tick() // may finish the block (clears state)
	}

	// A fully-revealed reveal reproduces the block exactly.
	q := &printer{cps: 25, raw: block, cells: tokenize(block)}
	q.revealed = len(q.cells)
	if got := q.view(); got != block {
		t.Fatalf("full reveal = %q, want %q", got, block)
	}
}
