package tui

import (
	"sort"

	"github.com/rondlite/neabbs/internal/content"
	"github.com/rondlite/neabbs/internal/store"
)

const (
	// callerRows is how many lines "laatste bellers" shows.
	callerRows = 10
	// oldFloor is how many of those rows are always reserved for the frozen
	// 1986 list. Ghosts call in often enough to fill the whole list within a
	// day, and if they did, the forty-year gap — the single clearest sign this
	// board was dead — would vanish from the screen. The list reads as a board
	// waking up: recent calls on top, the silence still visible underneath.
	oldFloor = 4
)

// callerRow is one line of the list: either a recent call (real player or
// ghost, carrying a timestamp) or an entry from the frozen 1986 seed.
type callerRow struct {
	handle string
	when   string // preformatted; the seed's dates are display strings already
	old    bool   // true = from the 1986 seed
}

// mergeCallers builds the "laatste bellers" list: real calls and ghost calls
// interleaved by time (newest first), with the bottom rows reserved for the
// 1986 seed so the gap stays visible.
func mergeCallers(real, ghost []store.Caller, seed []content.SeedCaller) []callerRow {
	all := append(append([]store.Caller{}, real...), ghost...)
	sort.SliceStable(all, func(i, j int) bool { return all[i].At.After(all[j].At) })

	// One row per caller, newest call wins — a "last callers" list names who
	// called, not how often. Without this, anyone who dials in a few times in a
	// row fills the whole list with their own handle and crowds everyone else
	// (ghosts included) off the screen, which reads as a dead board, not a busy
	// one.
	seen := map[string]bool{}
	var recent []store.Caller
	for _, c := range all {
		if seen[c.Handle] {
			continue
		}
		seen[c.Handle] = true
		recent = append(recent, c)
	}

	if max := callerRows - oldFloor; len(recent) > max {
		recent = recent[:max]
	}

	rows := make([]callerRow, 0, callerRows)
	for _, c := range recent {
		rows = append(rows, callerRow{handle: c.Handle, when: c.At.Format("02-01-06 15:04")})
	}
	for _, c := range seed {
		if len(rows) >= callerRows {
			break
		}
		rows = append(rows, callerRow{handle: c.Handle, when: c.Date, old: true})
	}
	return rows
}
