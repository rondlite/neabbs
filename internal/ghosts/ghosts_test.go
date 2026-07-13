package ghosts

import (
	"math/rand"
	"testing"
	"time"
)

var seed = []string{"phantom", "route66", "de_specht", "wodan", "turbo_ted"}

func fixedNow() time.Time { return time.Date(2026, 7, 13, 2, 0, 0, 0, time.UTC) }

func newTestRoster() *Roster {
	return New(seed, fixedNow(), rand.New(rand.NewSource(1)))
}

func TestBackfillCoversTheLastDay(t *testing.T) {
	r := newTestRoster()
	calls := r.Recent(100)
	if len(calls) == 0 {
		t.Fatal("no back-filled calls: a freshly booted board would look dead")
	}
	now := fixedNow()
	for _, c := range calls {
		if c.At.After(now) {
			t.Errorf("%s calls in the future: %s", c.Handle, c.At)
		}
		if c.At.Before(now.Add(-backfill)) {
			t.Errorf("%s is older than the back-fill window: %s", c.Handle, c.At)
		}
	}
}

func TestRecentIsNewestFirst(t *testing.T) {
	calls := newTestRoster().Recent(10)
	for i := 1; i < len(calls); i++ {
		if calls[i].At.After(calls[i-1].At) {
			t.Fatalf("not newest-first at %d: %s then %s", i, calls[i-1].At, calls[i].At)
		}
	}
}

func TestRecentLimits(t *testing.T) {
	if got := len(newTestRoster().Recent(3)); got != 3 {
		t.Errorf("Recent(3) returned %d", got)
	}
}

// phantom twice in a row reads as a bug, not a coincidence.
func TestNoAdjacentDuplicateHandles(t *testing.T) {
	r := newTestRoster()
	for i := 0; i < 50; i++ {
		r.call(fixedNow().Add(time.Duration(i) * time.Minute))
	}
	calls := r.Recent(200)
	for i := 1; i < len(calls); i++ {
		if calls[i].Handle == calls[i-1].Handle {
			t.Fatalf("%s called twice in a row", calls[i].Handle)
		}
	}
}

func TestRingIsCapped(t *testing.T) {
	r := newTestRoster()
	for i := 0; i < maxCalls*3; i++ {
		r.call(fixedNow().Add(time.Duration(i) * time.Minute))
	}
	if got := len(r.Recent(1000)); got > maxCalls {
		t.Errorf("ring holds %d calls, want at most %d", got, maxCalls)
	}
}

func TestIntervalStaysInRange(t *testing.T) {
	r := newTestRoster()
	for i := 0; i < 200; i++ {
		d := r.NextInterval()
		if d < minGap || d > maxGap {
			t.Fatalf("interval %s outside [%s, %s]", d, minGap, maxGap)
		}
	}
}

// A board with no seeded callers has no ghosts — and must not panic.
func TestEmptySeedIsInert(t *testing.T) {
	r := New(nil, fixedNow(), rand.New(rand.NewSource(1)))
	r.call(fixedNow())
	if got := r.Recent(10); len(got) != 0 {
		t.Errorf("got %d calls from an empty seed", len(got))
	}
}

// Every session reads the same roster, so two callers comparing notes agree.
func TestCallIsVisibleToLaterReads(t *testing.T) {
	r := newTestRoster()
	before := len(r.Recent(1000))
	r.call(fixedNow().Add(time.Hour))
	if after := len(r.Recent(1000)); after != before+1 && before < maxCalls {
		t.Errorf("call not recorded: %d then %d", before, after)
	}
	if newest := r.Recent(1)[0]; !newest.At.Equal(fixedNow().Add(time.Hour)) {
		t.Errorf("newest call is %s, want the one just recorded", newest.At)
	}
}
