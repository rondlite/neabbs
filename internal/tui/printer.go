package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// printer emulates a 1200/2400-baud connection: output is revealed at a
// fixed chars/sec rate inside the View, committed to scrollback when a block
// finishes. Any key mid-draw skips to the end of the current block
// (authentic — everyone did this). Long output pauses on a
// `-- Meer? (J/n) --` pager prompt.
//
// cps == 0 disables the throttle (NEABBS_BAUD=0): blocks print instantly.
type printer struct {
	cps int // chars per second; 0 = instant

	current  []rune // page being revealed
	revealed int    // how much of current is shown
	queue    []page
	more     bool // waiting at a -- Meer? -- prompt
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

// busy reports whether the printer owns the screen (revealing or paging).
func (p *printer) busy() bool { return len(p.current) > 0 || len(p.queue) > 0 || p.more }

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
	p.current = []rune(p.queue[0].text)
	p.queue = p.queue[1:]
	p.revealed = 0
	if p.cps <= 0 {
		return p.finishBlock()
	}
	return printTick()
}

// tick advances the reveal. Returns (commitCmd, stillRunning).
func (p *printer) tick() tea.Cmd {
	if len(p.current) == 0 {
		return nil
	}
	step := p.cps * 40 / 1000
	if step < 1 {
		step = 1
	}
	p.revealed += step
	if p.revealed < len(p.current) {
		return printTick()
	}
	return p.finishBlock()
}

// finishBlock commits the fully revealed page to scrollback, then pauses at
// the pager (continuation of the same block) or flows into the next block.
func (p *printer) finishBlock() tea.Cmd {
	block := string(p.current)
	p.current = nil
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
	if len(p.current) == 0 {
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
		return dimmed.Render("-- Meer? (J/n) --")
	}
	if len(p.current) == 0 {
		return ""
	}
	n := p.revealed
	if n > len(p.current) {
		n = len(p.current)
	}
	return string(p.current[:n])
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
