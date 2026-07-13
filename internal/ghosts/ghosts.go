// Package ghosts keeps the board looking alive: the old 1980s callers dial in
// now and then, land in the "laatste bellers" list, and hang up. They are
// fiction and stay fiction — nothing here touches the database, occupies a
// phone line, or shows up in the public stats. The real caller log stays honest.
package ghosts

import (
	"math/rand"
	"sync"
	"time"

	"github.com/rondlite/neabbs/internal/store"
)

const (
	// minGap/maxGap bound the wait between two ghost calls. A small Dutch
	// board at 3am was quiet; this is a board in use, not a party.
	minGap = 20 * time.Minute
	maxGap = 90 * time.Minute
	// backfill is how much history a freshly booted board invents, so it does
	// not look suspiciously dead after a restart.
	backfill = 24 * time.Hour
	// maxCalls caps the ring; the list only ever shows a handful.
	maxCalls = 30
)

// Roster is the shared ghost-call log. One per daemon: every session reads the
// same one, so two callers comparing notes see the same list.
type Roster struct {
	handles []string

	mu    sync.Mutex
	rng   *rand.Rand
	calls []store.Caller // oldest first
}

// New builds a roster from the seeded handles and back-fills a plausible last
// day of calls. rng is injectable so tests are deterministic.
func New(handles []string, now time.Time, rng *rand.Rand) *Roster {
	r := &Roster{handles: handles, rng: rng}
	if len(handles) == 0 {
		return r
	}
	// Walk backwards from now to fill the window, then flip: generating in
	// reverse keeps the gaps as irregular as the live ones.
	var back []store.Caller
	for at := now; ; {
		at = at.Add(-r.NextInterval())
		if now.Sub(at) > backfill {
			break
		}
		back = append(back, store.Caller{At: at})
	}
	for i := len(back) - 1; i >= 0; i-- {
		r.call(back[i].At)
	}
	return r
}

// NextInterval is how long to wait before the next ghost calls.
func (r *Roster) NextInterval() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return minGap + time.Duration(r.rng.Int63n(int64(maxGap-minGap+1)))
}

// call records one ghost dialling in at the given time.
func (r *Roster) call(at time.Time) {
	if len(r.handles) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	h := r.handles[r.rng.Intn(len(r.handles))]
	// Never the same handle twice running: that reads as a bug, not a
	// coincidence. With a single seeded handle there is nothing to vary.
	if n := len(r.calls); n > 0 && r.calls[n-1].Handle == h && len(r.handles) > 1 {
		h = r.handles[(indexOf(r.handles, h)+1+r.rng.Intn(len(r.handles)-1))%len(r.handles)]
	}
	r.calls = append(r.calls, store.Caller{Handle: h, At: at})
	if len(r.calls) > maxCalls {
		r.calls = r.calls[len(r.calls)-maxCalls:]
	}
}

// Recent returns up to n ghost calls, newest first.
func (r *Roster) Recent(n int) []store.Caller {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]store.Caller, 0, n)
	for i := len(r.calls) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, r.calls[i])
	}
	return out
}

// Run dials a ghost in every minGap–maxGap until ctx is done. The daemon owns
// this goroutine; a board with no seeded callers never starts one.
func (r *Roster) Run(done <-chan struct{}) {
	if len(r.handles) == 0 {
		return
	}
	for {
		t := time.NewTimer(r.NextInterval())
		select {
		case <-done:
			t.Stop()
			return
		case now := <-t.C:
			r.call(now)
		}
	}
}

func indexOf(ss []string, s string) int {
	for i, v := range ss {
		if v == s {
			return i
		}
	}
	return 0
}
