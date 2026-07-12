package tui

import (
	"reflect"
	"testing"
)

func testSources() completeSources {
	return completeSources{
		// What the player has learned: the help baseline, plus `crack` which
		// they discovered in play. `wipe` exists in the game but is NOT here.
		verbs:  []string{"help", "scan", "connect", "boards", "read", "exit", "crack"},
		hosts:  []string{"vax.gemeente.nl", "cyber.sara.nl", "hydra.uva.nl"},
		files:  []string{"modemlijst.dat", "notulen-jan86.txt"},
		boards: []string{"this-board", "phreak"},
	}
}

func TestCompleteVerbUnique(t *testing.T) {
	line, cands := completeLine("con", testSources())
	if line != "connect " {
		t.Errorf("line = %q, want %q (unique verb completes with trailing space)", line, "connect ")
	}
	if len(cands) != 0 {
		t.Errorf("candidates = %v, want none for a unique match", cands)
	}
}

func TestCompleteVerbCommonPrefixAndCandidates(t *testing.T) {
	src := testSources()
	src.verbs = []string{"scan", "scram"}
	line, cands := completeLine("sc", src)
	if line != "scan" && line != "scr" {
		// longest common prefix of scan/scram is "sc" — nothing to add
		if line != "sc" {
			t.Errorf("line = %q, want the longest common prefix %q", line, "sc")
		}
	}
	if !reflect.DeepEqual(cands, []string{"scan", "scram"}) {
		t.Errorf("candidates = %v, want both matches listed", cands)
	}
}

func TestCompleteVerbNoMatchLeavesLine(t *testing.T) {
	line, cands := completeLine("zzz", testSources())
	if line != "zzz" || len(cands) != 0 {
		t.Errorf("got (%q, %v), want the line untouched and no candidates", line, cands)
	}
}

// The discovery mechanic: Tab must never hand the player a verb they have not
// already learned. `wipe` is a real command, absent from this player's verbs.
func TestCompleteNeverRevealsUnlearnedVerb(t *testing.T) {
	line, cands := completeLine("wi", testSources())
	if line != "wi" || len(cands) != 0 {
		t.Errorf("got (%q, %v): Tab leaked an undiscovered command", line, cands)
	}
}

func TestCompleteHostArgument(t *testing.T) {
	line, _ := completeLine("connect vax.", testSources())
	if line != "connect vax.gemeente.nl" {
		t.Errorf("line = %q, want the host completed", line)
	}
}

// Completion reads the same level-filtered slices the screen does, so a host
// out of the player's clearance is simply not in the source and cannot appear.
func TestCompleteNeverRevealsHostOutOfClearance(t *testing.T) {
	src := testSources()
	src.hosts = []string{"vax.gemeente.nl"} // keizer.* is above this player
	line, cands := completeLine("connect kei", src)
	if line != "connect kei" || len(cands) != 0 {
		t.Errorf("got (%q, %v): Tab leaked a host above the player's clearance", line, cands)
	}
}

func TestCompleteFileArgument(t *testing.T) {
	line, _ := completeLine("cat mod", testSources())
	if line != "cat modemlijst.dat" {
		t.Errorf("line = %q, want the filename completed", line)
	}
}

func TestCompleteEmptyArgListsAllCandidates(t *testing.T) {
	_, cands := completeLine("cat ", testSources())
	if !reflect.DeepEqual(cands, []string{"modemlijst.dat", "notulen-jan86.txt"}) {
		t.Errorf("candidates = %v, want every file on the host listed", cands)
	}
}

func TestCompleteBoardArgument(t *testing.T) {
	line, _ := completeLine("board phr", testSources())
	if line != "board phreak" {
		t.Errorf("line = %q, want the board id completed", line)
	}
}

func TestCompleteUnknownVerbTakesNoArguments(t *testing.T) {
	line, cands := completeLine("status vax.", testSources())
	if line != "status vax." || len(cands) != 0 {
		t.Errorf("got (%q, %v), want no argument pool for a verb that takes none", line, cands)
	}
}

func TestHistoryWalk(t *testing.T) {
	var h history
	h.push("scan")
	h.push("connect vax.gemeente.nl")

	if got := h.prev(); got != "connect vax.gemeente.nl" {
		t.Errorf("first up = %q, want the newest entry", got)
	}
	if got := h.prev(); got != "scan" {
		t.Errorf("second up = %q, want the older entry", got)
	}
	if got := h.prev(); got != "scan" {
		t.Errorf("up past the oldest = %q, want to stay on the oldest", got)
	}
	if got := h.next(); got != "connect vax.gemeente.nl" {
		t.Errorf("down = %q, want the newer entry", got)
	}
	if got := h.next(); got != "" {
		t.Errorf("down past the newest = %q, want an empty line", got)
	}
}

func TestHistorySkipsBlanksAndDuplicates(t *testing.T) {
	var h history
	h.push("scan")
	h.push("scan") // immediate duplicate: not recorded twice
	h.push("")     // blank: never recorded
	if len(h.lines) != 1 {
		t.Fatalf("lines = %v, want just one entry", h.lines)
	}
	if got := h.prev(); got != "scan" {
		t.Errorf("up = %q, want scan", got)
	}
}

// A fresh push after browsing resets the cursor, so the next Up starts from
// the newest line again rather than wherever the player had scrolled to.
func TestHistoryPushResetsCursor(t *testing.T) {
	var h history
	h.push("scan")
	h.push("ls")
	h.prev()
	h.prev() // scrolled back to "scan"
	h.push("crack")
	if got := h.prev(); got != "crack" {
		t.Errorf("up after a new command = %q, want the newest entry %q", got, "crack")
	}
}
