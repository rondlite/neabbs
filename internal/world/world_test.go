package world

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/rondlite/neabbs/internal/board"
	"github.com/rondlite/neabbs/internal/content"
	"github.com/rondlite/neabbs/internal/store/sqlitestore"
)

func testSet() *content.Set {
	return &content.Set{
		Hosts: []content.Host{
			{ID: "open0", Address: "open.this.nl", MinLevel: 0,
				Files: []content.HostFile{
					{Name: "readme.1st", MinLevel: 0},
					{Name: "geheim.dat", MinLevel: 3, GrantsFlag: "saw_geheim"},
				}},
			{ID: "hoog", Address: "hoog.this.nl", MinLevel: 4},
			{ID: "vip", Address: "vip.this.nl", MinLevel: 9, RequiresFlag: "vip_pas"},
			{ID: "dicht", Address: "dicht.this.nl", MinLevel: 0, Locked: true,
				Crack: &content.CrackSpec{Method: "none", MinLevel: 5, HintOnFail: "THIS-5 vereist"}},
		},
	}
}

func newEngine(t *testing.T) *Engine {
	t.Helper()
	st, err := sqlitestore.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return NewEngine(testSet(), st)
}

func viewer(level int, flags ...string) (board.Viewer, func(string) bool) {
	set := map[string]bool{}
	for _, f := range flags {
		set[f] = true
	}
	return board.Viewer{Fingerprint: "fp", Handle: "h", ThisMember: true, Level: level},
		func(f string) bool { return set[f] }
}

func TestScanFilters(t *testing.T) {
	e := newEngine(t)

	v, has := viewer(0)
	got := map[string]bool{}
	for _, h := range e.Scan(v, has) {
		got[h.ID] = true
	}
	if !got["open0"] || !got["dicht"] || got["hoog"] || got["vip"] {
		t.Fatalf("level 0 scan: %v", got)
	}

	// requires_flag grants visibility regardless of level.
	v, has = viewer(0, "vip_pas")
	found := false
	for _, h := range e.Scan(v, has) {
		if h.ID == "vip" {
			found = true
		}
	}
	if !found {
		t.Fatal("flag-granted host missing from scan")
	}

	// Non-members see nothing.
	nm := board.Viewer{Fingerprint: "fp2", Handle: "n"}
	if hosts := e.Scan(nm, func(string) bool { return false }); len(hosts) != 0 {
		t.Fatalf("non-member scan: %d hosts", len(hosts))
	}
}

func TestConnectNeverConfirms(t *testing.T) {
	e := newEngine(t)
	v, has := viewer(0)

	if _, err := e.Connect("open.this.nl", v, has); err != nil {
		t.Fatal(err)
	}
	_, errHidden := e.Connect("hoog.this.nl", v, has) // exists, above level
	_, errNone := e.Connect("bestaat.niet.nl", v, has)
	if !errors.Is(errHidden, ErrNoRoute) || !errors.Is(errNone, ErrNoRoute) {
		t.Fatalf("connect leaks existence: %v vs %v", errHidden, errNone)
	}
}

func TestLsAndCat(t *testing.T) {
	e := newEngine(t)
	ctx := context.Background()
	v, has := viewer(0)

	h, _ := e.Connect("open.this.nl", v, has)
	rows, err := e.Ls(h, v)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Redacted || !rows[1].Redacted || rows[1].Level != 3 {
		t.Fatalf("ls rows: %+v", rows)
	}

	if _, err := e.Cat(ctx, h, "readme.1st", v); err != nil {
		t.Fatal(err)
	}
	var ec ErrClearance
	if _, err := e.Cat(ctx, h, "geheim.dat", v); !errors.As(err, &ec) || ec.Need != 3 {
		t.Fatalf("cat above level: %v", err)
	}
	if _, err := e.Cat(ctx, h, "nope.txt", v); !errors.Is(err, ErrNoFile) {
		t.Fatalf("cat missing: %v", err)
	}

	// Locked host refuses ls/cat.
	d, _ := e.Connect("dicht.this.nl", v, has)
	if _, err := e.Ls(d, v); !errors.Is(err, ErrLocked) {
		t.Fatalf("ls on locked: %v", err)
	}
}

func TestCatGrantsFlag(t *testing.T) {
	e := newEngine(t)
	ctx := context.Background()
	e.store.(*sqlitestore.Store).CreatePlayer(ctx, "fp")
	v, has := viewer(3)
	h, _ := e.Connect("open.this.nl", v, has)
	if _, err := e.Cat(ctx, h, "geheim.dat", v); err != nil {
		t.Fatal(err)
	}
	p, _ := e.store.PlayerByFingerprint(ctx, "fp")
	if !p.HasFlag("saw_geheim") {
		t.Fatal("grants_flag not applied on cat")
	}
}
