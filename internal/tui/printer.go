package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// printer emulates a dial-up connection: output is revealed at a fixed
// chars/sec rate inside the View, committed to scrollback when a block
// finishes. Any key mid-draw skips to the end of the current block
// (authentic — everyone did this). Long output pauses on a
// `-- Meer? (J/n) --` pager prompt.
//
// The reveal is ANSI-aware: colour/escape sequences are atomic zero-width
// cells, so a coloured block never tears mid-escape at any baud rate.
//
// cps == 0 disables the throttle (NEABBS_BAUD=0): blocks print instantly.
type printer struct {
	cps int // visible chars per second; 0 = instant

	raw      string // the block currently being revealed ("" = idle)
	cells    []cell // raw tokenised into visible runes + atomic escapes
	revealed int    // how many cells are shown
	queue    []page
	more     bool   // waiting at a -- Meer? -- prompt
	lang     string // display language for the pager prompt ("nl"/"en")
}

// cell is one reveal unit: a single visible rune, or a whole ANSI escape
// sequence (visible == false, zero width, emitted for free).
type cell struct {
	text    string
	visible bool
}

// page is one screenful. cont marks a continuation page of the same block:
// the pager prompt only appears between pages of one block, never between
// independently enqueued blocks (those flow automatically).
type page struct {
	text string
	cont bool
}

// pageLines is how many lines fit between pager prompts.
const pageLines = 22

// printTickMsg drives the reveal animation.
type printTickMsg struct{}

func printTick() tea.Cmd {
	return tea.Tick(40*time.Millisecond, func(time.Time) tea.Msg { return printTickMsg{} })
}

// active reports whether a block is mid-reveal (owns the screen).
func (p *printer) active() bool { return p.raw != "" }

// busy reports whether the printer owns the screen (revealing or paging).
func (p *printer) busy() bool { return p.raw != "" || len(p.queue) > 0 || p.more }

// enqueue splits text into pages and starts printing. Returns the command
// that begins the reveal (nil if the printer was already running).
func (p *printer) enqueue(text string) tea.Cmd {
	pages := paginate(text, pageLines)
	wasBusy := p.busy()
	for i, pg := range pages {
		p.queue = append(p.queue, page{text: pg, cont: i > 0})
	}
	if wasBusy {
		return nil
	}
	return p.next()
}

// next pulls the following page into current.
func (p *printer) next() tea.Cmd {
	if len(p.queue) == 0 {
		return nil
	}
	p.raw = p.queue[0].text
	p.cells = tokenize(p.raw)
	p.queue = p.queue[1:]
	p.revealed = 0
	if p.cps <= 0 {
		return p.finishBlock()
	}
	return printTick()
}

// tick advances the reveal by up to `step` visible cells; escape cells are
// flushed for free so colour never tears.
func (p *printer) tick() tea.Cmd {
	if p.raw == "" {
		return nil
	}
	step := p.cps * 40 / 1000
	if step < 1 {
		step = 1
	}
	budget := step
	for p.revealed < len(p.cells) {
		c := p.cells[p.revealed]
		if c.visible && budget == 0 {
			break
		}
		if c.visible {
			budget--
		}
		p.revealed++
	}
	if p.revealed >= len(p.cells) {
		return p.finishBlock()
	}
	return printTick()
}

// finishBlock commits the fully revealed page to scrollback, then pauses at
// the pager (continuation of the same block) or flows into the next block.
func (p *printer) finishBlock() tea.Cmd {
	block := p.raw
	p.raw = ""
	p.cells = nil
	p.revealed = 0
	commit := tea.Println(block)
	if len(p.queue) == 0 {
		return commit
	}
	if p.queue[0].cont {
		p.more = true
		return commit
	}
	return tea.Sequence(commit, p.next())
}

// skip is called on any key while revealing: jump to the end of the block.
func (p *printer) skip() tea.Cmd {
	if p.raw == "" {
		return nil
	}
	return p.finishBlock()
}

// moreKey handles a keypress at the -- Meer? -- prompt. n/N drops the rest
// of the current block (independent later blocks still print).
func (p *printer) moreKey(k string) tea.Cmd {
	if !p.more {
		return nil
	}
	p.more = false
	if k == "n" || k == "N" {
		for len(p.queue) > 0 && p.queue[0].cont {
			p.queue = p.queue[1:]
		}
	}
	return p.next()
}

// view renders the partially revealed block (and pager prompt).
func (p *printer) view() string {
	if p.more {
		return dimmed.Render(trl(p.lang, "-- Meer? (J/n) --", "-- More? (Y/n) --"))
	}
	if p.raw == "" {
		return ""
	}
	var b strings.Builder
	for i := 0; i < p.revealed && i < len(p.cells); i++ {
		b.WriteString(p.cells[i].text)
	}
	return b.String()
}

// tokenize splits s into reveal cells: whole CSI escape sequences
// (ESC [ … final-byte) become zero-width cells, every other rune is one
// visible cell.
func tokenize(s string) []cell {
	runes := []rune(s)
	cells := make([]cell, 0, len(runes))
	for i := 0; i < len(runes); {
		if runes[i] == 0x1b && i+1 < len(runes) && runes[i+1] == '[' {
			j := i + 2
			for j < len(runes) && !(runes[j] >= '@' && runes[j] <= '~') {
				j++
			}
			if j < len(runes) {
				j++ // include the final byte
			}
			cells = append(cells, cell{text: string(runes[i:j]), visible: false})
			i = j
			continue
		}
		cells = append(cells, cell{text: string(runes[i]), visible: true})
		i++
	}
	return cells
}

// paginate splits text into pages of at most n lines each.
func paginate(text string, n int) []string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) <= n {
		return []string{strings.Join(lines, "\n")}
	}
	var pages []string
	for len(lines) > 0 {
		k := n
		if k > len(lines) {
			k = len(lines)
		}
		pages = append(pages, strings.Join(lines[:k], "\n"))
		lines = lines[k:]
	}
	return pages
}
