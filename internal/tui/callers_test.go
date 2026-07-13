package tui

import (
	"testing"
	"time"

	"github.com/rondlite/neabbs/internal/content"
	"github.com/rondlite/neabbs/internal/store"
)

func at(min int) time.Time {
	return time.Date(2026, 7, 13, 2, 0, 0, 0, time.UTC).Add(time.Duration(-min) * time.Minute)
}

var oldPad = []content.SeedCaller{
	{Handle: "wodan", Date: "11-03-86 23:41"},
	{Handle: "blueboxer", Date: "10-03-86 04:44"},
	{Handle: "de_krab", Date: "10-03-86 01:30"},
	{Handle: "zilvervis", Date: "09-03-86 23:59"},
	{Handle: "kermit", Date: "09-03-86 22:22"},
}

func TestMergeCallersNewestFirst(t *testing.T) {
	real := []store.Caller{{Handle: "ron", At: at(5)}}
	ghost := []store.Caller{{Handle: "phantom", At: at(30)}, {Handle: "kermit", At: at(1)}}

	rows := mergeCallers(real, ghost, oldPad)

	if rows[0].handle != "kermit" || rows[1].handle != "ron" || rows[2].handle != "phantom" {
		t.Fatalf("wrong order: %v", handles(rows))
	}
}

// The 40-year gap is the whole artifact: ghosts must never crowd the 1986
// entries out of the list entirely.
func TestMergeCallersAlwaysKeepsTheOldFloor(t *testing.T) {
	// Distinct callers, more than the list can hold — the roster never repeats
	// a handle back to back, so this is the shape production actually produces.
	names := []string{"phantom", "route66", "de_specht", "turbo_ted", "mainframe", "kermit", "de_krab", "zilvervis"}
	var ghost []store.Caller
	for i, n := range names {
		ghost = append(ghost, store.Caller{Handle: n, At: at(i)})
	}

	rows := mergeCallers(nil, ghost, oldPad)

	if len(rows) != callerRows {
		t.Fatalf("got %d rows, want %d", len(rows), callerRows)
	}
	old := 0
	for _, r := range rows {
		if r.old {
			old++
		}
	}
	if old < oldFloor {
		t.Errorf("only %d rows from 1986, want at least %d: the silence must stay visible", old, oldFloor)
	}
}

// A board nobody has called yet looks exactly as it did before this feature.
func TestMergeCallersEmptyIsAllSeed(t *testing.T) {
	rows := mergeCallers(nil, nil, oldPad)
	for _, r := range rows {
		if !r.old {
			t.Fatalf("unexpected recent row on a silent board: %+v", r)
		}
	}
	if len(rows) != len(oldPad) {
		t.Errorf("got %d rows, want all %d seeded", len(rows), len(oldPad))
	}
}

// A caller who dials in repeatedly must not fill the list with themselves: the
// list names who called, not how often.
func TestMergeCallersOneRowPerHandle(t *testing.T) {
	real := []store.Caller{
		{Handle: "ron", At: at(1)},
		{Handle: "ron", At: at(9)},
		{Handle: "ron", At: at(20)},
	}
	ghost := []store.Caller{{Handle: "phantom", At: at(15)}}

	rows := mergeCallers(real, ghost, oldPad)

	rons := 0
	for _, r := range rows {
		if r.handle == "ron" {
			rons++
		}
	}
	if rons != 1 {
		t.Errorf("ron appears %d times, want once (newest call only)", rons)
	}
	if rows[0].handle != "ron" || rows[0].when != at(1).Format("02-01-06 15:04") {
		t.Errorf("first row = %+v, want ron's newest call", rows[0])
	}
	// And the ghost still gets a row rather than being crowded out.
	if rows[1].handle != "phantom" {
		t.Errorf("second row = %q, want phantom", rows[1].handle)
	}
}

func TestMergeCallersRecentRowsAreCapped(t *testing.T) {
	names := []string{"phantom", "route66", "de_specht", "turbo_ted", "mainframe", "kermit", "de_krab", "zilvervis"}
	var ghost []store.Caller
	for i, n := range names {
		ghost = append(ghost, store.Caller{Handle: n, At: at(i)})
	}
	rows := mergeCallers(nil, ghost, oldPad)
	recent := 0
	for _, r := range rows {
		if !r.old {
			recent++
		}
	}
	if recent > callerRows-oldFloor {
		t.Errorf("%d recent rows, want at most %d", recent, callerRows-oldFloor)
	}
}

func handles(rows []callerRow) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.handle)
	}
	return out
}
