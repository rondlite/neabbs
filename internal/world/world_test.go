package world

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rondlite/neabbs/internal/board"
	"github.com/rondlite/neabbs/internal/content"
	"github.com/rondlite/neabbs/internal/store/sqlitestore"
)

func testSet() *content.Set {
	pw := &content.Host{
		ID: "kraakbaar", Address: "kraak.this.nl", MinLevel: 0, Locked: true,
		Crack: &content.CrackSpec{Method: "password", PasswordFlag: "kraak_pw",
			HintOnFail: content.L{NL: "wachtwoord vereist (hint: board #1)"}, TraceSeconds: 90},
		Files: []content.HostFile{{Name: "buit.txt", MinLevel: 0}},
	}
	pw.Effects.OnFirstCrack = &content.Effects{
		GrantLevel: 1, GrantFlags: []string{"gekraakt"},
		Broadcast: content.L{NL: "{handle} is binnengedrongen bij kraak.this.nl"},
	}
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
				Crack: &content.CrackSpec{Method: "none", MinLevel: 5, HintOnFail: content.L{NL: "THIS-5 vereist"}}},
			{ID: "sys", Address: "sys.this.nl", MinLevel: 0,
				Files: []content.HostFile{
					{Name: "motd", MinLevel: 0},
					{Name: ".secret", MinLevel: 0, GrantsFlag: "saw_secret"},
				},
				Mail: []content.MailMsg{
					{From: "root", Subject: content.L{NL: "hallo"}, MinLevel: 0},
					{From: "chef", Subject: content.L{NL: "geheim"}, MinLevel: 3, GrantsFlag: "mail_flag"},
				},
				Netstat: &content.HostView{MinLevel: 0, GrantsFlag: "net_flag", Body: content.L{NL: "conn"}}},
			{ID: "multi", Address: "multi.this.nl", MinLevel: 0, Locked: true,
				Crack: &content.CrackSpec{Method: "password", PasswordFlag: "pw",
					RequiresFlags: []string{"hash", "wordlist"}, HintOnFail: content.L{NL: "meer nodig"}}},
			{ID: "exch", Address: "exch.this.nl", MinLevel: 3, Locked: true,
				Crack: &content.CrackSpec{Method: "bluebox",
					Sequence:     "2600 1700+1100 700+900 pauze 2600",
					HintOnFail:   content.L{NL: "GEEN ANTWOORD."},
					TraceSeconds: 75}},
			*pw,
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
	rows, err := e.Ls(ctx, h, v)
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
	if _, err := e.Ls(ctx, d, v); !errors.Is(err, ErrLocked) {
		t.Fatalf("ls on locked: %v", err)
	}
}

func TestHiddenFilesAndReadouts(t *testing.T) {
	e := newEngine(t)
	ctx := context.Background()
	if _, err := e.store.CreatePlayer(ctx, "fp"); err != nil {
		t.Fatal(err)
	}
	v, has := viewer(0)
	h, _ := e.Connect("sys.this.nl", v, has)

	rows, err := e.Ls(ctx, h, v)
	if err != nil {
		t.Fatal(err)
	}
	var motd, secret *FileRow
	for i := range rows {
		switch rows[i].Name {
		case "motd":
			motd = &rows[i]
		case ".secret":
			secret = &rows[i]
		}
	}
	if motd == nil || motd.Hidden {
		t.Fatalf("motd should be visible: %+v", motd)
	}
	if secret == nil || !secret.Hidden {
		t.Fatalf(".secret should be marked hidden: %+v", secret)
	}

	// Mail: level-filtered, indexed, grants on read.
	mrows, err := e.Mail(ctx, h, v)
	if err != nil || len(mrows) != 2 || mrows[0].Redacted || !mrows[1].Redacted {
		t.Fatalf("mail rows: %v %+v", err, mrows)
	}
	if _, err := e.ReadMail(ctx, h, 1, v); err != nil {
		t.Fatalf("read mail 1: %v", err)
	}
	var ec ErrClearance
	if _, err := e.ReadMail(ctx, h, 2, v); !errors.As(err, &ec) || ec.Need != 3 {
		t.Fatalf("read above-level mail: %v", err)
	}
	if _, err := e.ReadMail(ctx, h, 9, v); !errors.Is(err, ErrNoFile) {
		t.Fatalf("read missing mail: %v", err)
	}

	// Netstat readout present; a host without one returns ErrNoFile.
	if _, err := e.Netstat(ctx, h, v); err != nil {
		t.Fatalf("netstat: %v", err)
	}
	open, _ := e.Connect("open.this.nl", v, has)
	if _, err := e.Netstat(ctx, open, v); !errors.Is(err, ErrNoFile) {
		t.Fatalf("netstat on host without one: %v", err)
	}
}

func TestMultiStageCrack(t *testing.T) {
	e := newEngine(t)
	ctx := context.Background()
	h := e.content.HostByAddress("multi.this.nl")

	// Password flag alone is not enough — every requires_flags must be held.
	for _, flags := range [][]string{{"pw"}, {"pw", "hash"}} {
		v, has := viewer(0, flags...)
		res, err := e.Crack(ctx, h, v, has)
		if err != nil || res.Success {
			t.Fatalf("crack with %v should fail: success=%v err=%v", flags, res.Success, err)
		}
	}
	// All prerequisites held → open.
	v, has := viewer(0, "pw", "hash", "wordlist")
	res, err := e.Crack(ctx, h, v, has)
	if err != nil || !res.Success {
		t.Fatalf("full-stack crack should succeed: %+v err=%v", res, err)
	}
}

func TestCrackLoop(t *testing.T) {
	e := newEngine(t)
	ctx := context.Background()
	e.store.(*sqlitestore.Store).CreatePlayer(ctx, "fp")

	v, has := viewer(0)
	h, _ := e.Connect("kraak.this.nl", v, has)

	// Without the password flag: specific refusal, still locked.
	res, err := e.Crack(ctx, h, v, has)
	if err != nil || res.Success || !strings.Contains(res.Msg, "wachtwoord vereist") {
		t.Fatalf("crack without flag: %+v %v", res, err)
	}
	if ok, _ := e.Unlocked(ctx, h, "fp"); ok {
		t.Fatal("locked host unlocked after failed crack")
	}

	// With the flag: success, first-crack effects, trace starts.
	v, has = viewer(0, "kraak_pw")
	res, err = e.Crack(ctx, h, v, has)
	if err != nil || !res.Success || !res.First || res.TraceSeconds != 90 {
		t.Fatalf("crack: %+v %v", res, err)
	}
	if res.Effects == nil || res.Effects.GrantLevel != 1 {
		t.Fatalf("first-crack effects missing: %+v", res.Effects)
	}
	if ok, _ := e.Unlocked(ctx, h, "fp"); !ok {
		t.Fatal("cracked host still locked")
	}

	// Re-crack: no-op message, never re-fires effects.
	res, _ = e.Crack(ctx, h, v, has)
	if res.Success || !strings.Contains(res.Msg, "al open") {
		t.Fatalf("re-crack: %+v", res)
	}

	// Trace expiry: locked again, cooldown blocks the next attempt,
	// and a later successful crack is not "first" anymore.
	if err := e.TraceExpired(ctx, h, "fp"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := e.Unlocked(ctx, h, "fp"); ok {
		t.Fatal("host still unlocked after trace kick")
	}
	res, _ = e.Crack(ctx, h, v, has)
	if res.Success || !strings.Contains(res.Msg, "herkent je nog") {
		t.Fatalf("crack during cooldown: %+v", res)
	}
	// Simulate cooldown expiry.
	e.store.SetHostCooldown(ctx, "fp", h.ID, time.Now().Add(-time.Minute))
	res, _ = e.Crack(ctx, h, v, has)
	if !res.Success || res.First {
		t.Fatalf("re-crack after cooldown: %+v", res)
	}
}

func TestCrackClearanceGate(t *testing.T) {
	e := newEngine(t)
	ctx := context.Background()
	v, has := viewer(0)
	h, _ := e.Connect("dicht.this.nl", v, has)
	res, _ := e.Crack(ctx, h, v, has)
	if res.Success || !strings.Contains(res.Msg, "THIS-5") {
		t.Fatalf("teaser crack must name its clearance: %+v", res)
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

func TestNormalizeTones(t *testing.T) {
	cases := map[string]string{
		"2600 1700+1100 700+900 pauze 2600":         "2600 1700+1100 700+900 pause 2600",
		"2600 · 1700+1100 · 700+900 · pause · 2600": "2600 1700+1100 700+900 pause 2600",
		"  2600   1700+1100  700+900 PAUZE 2600 ":   "2600 1700+1100 700+900 pause 2600",
	}
	for in, want := range cases {
		if got := normalizeTones(in); got != want {
			t.Errorf("normalizeTones(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBlueboxSuccess(t *testing.T) {
	ctx := context.Background()
	e := newEngine(t)
	e.store.(*sqlitestore.Store).CreatePlayer(ctx, "fp")
	h := e.content.HostByAddress("exch.this.nl")
	v, _ := viewer(3)
	res, err := e.Bluebox(ctx, h, v, func(string) bool { return false }, "2600 · 1700+1100 · 700+900 · pauze · 2600")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Success || !res.First || res.TraceSeconds != 75 {
		t.Fatalf("bad result: %+v", res)
	}
}

func TestBlueboxWrongSequence(t *testing.T) {
	ctx := context.Background()
	e := newEngine(t)
	e.store.(*sqlitestore.Store).CreatePlayer(ctx, "fp")
	h := e.content.HostByAddress("exch.this.nl")
	v, has := viewer(3)
	res, err := e.Bluebox(ctx, h, v, has, "2600 700+900 2600")
	if err != nil {
		t.Fatal(err)
	}
	if res.Success {
		t.Fatal("wrong sequence should not succeed")
	}
	if res.Msg == "" {
		t.Fatal("expected hint_on_fail message")
	}
}

func TestCrackOnBlueboxHostRedirects(t *testing.T) {
	ctx := context.Background()
	e := newEngine(t)
	e.store.(*sqlitestore.Store).CreatePlayer(ctx, "fp")
	h := e.content.HostByAddress("exch.this.nl")
	v, has := viewer(3)
	res, err := e.Crack(ctx, h, v, has)
	if err != nil {
		t.Fatal(err)
	}
	if res.Success || !strings.Contains(res.Msg, "blue box") {
		t.Fatalf("expected blue-box redirect, got %+v", res)
	}
}
