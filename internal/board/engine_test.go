package board

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rondlite/neabbs/internal/content"
	"github.com/rondlite/neabbs/internal/store"
	"github.com/rondlite/neabbs/internal/store/sqlitestore"
)

func boolp(b bool) *bool { return &b }

func testContent() *content.Set {
	set := &content.Set{
		Boards: []content.Board{
			{
				ID: "algemeen", Name: "ALGEMEEN", Area: content.AreaPublic, Writable: true,
				Messages: []content.Message{
					{ID: 1, Author: "sysop", Level: 0, Subject: "welkom", Body: "hallo"},
					{ID: 2, Author: "sysop", Level: 0, Subject: "regels", Body: "wees aardig"},
				},
			},
			{
				ID: "this-board", Name: "THIS BOARD", Area: content.AreaThis, MinLevel: 0, Writable: true,
				Messages: []content.Message{
					{ID: 142, Author: "sysop", Level: 0, Subject: "welkom nieuwe leden", Body: "dit is this"},
					{ID: 150, Author: "route66", Level: 2, Subject: "modempool", Body: "geheim-2", GrantsFlag: "saw_modempool"},
					{ID: 152, Author: "phantom", Level: 6, Subject: "de echte ingang naar node 9", Body: "geheim-6"},
					{ID: 153, Author: "phantom", Level: 7, Subject: "supergeheim", Body: "x",
						SubjectVisible: boolp(false), AuthorVisible: boolp(false)},
					{ID: 154, Author: "ghost", Level: 8, Subject: "bestaat niet", Body: "x", Hidden: true},
				},
			},
			{
				ID: "phreak", Name: "#PHREAK", Area: content.AreaThis, MinLevel: 3, Writable: true,
				Messages: []content.Message{
					{ID: 300, Author: "blueboxer", Level: 3, Subject: "toonfrequenties", Body: "2600Hz"},
				},
			},
		},
	}
	if err := content.Lint(set); err != nil {
		panic(err)
	}
	return set
}

func newEngine(t *testing.T) (*Engine, store.Store) {
	t.Helper()
	st, err := sqlitestore.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return NewEngine(testContent(), st), st
}

var (
	nonMember = Viewer{Fingerprint: "fp-n", Handle: "noob"}
	member0   = Viewer{Fingerprint: "fp-0", Handle: "kilroy", ThisMember: true, Level: 0}
	member2   = Viewer{Fingerprint: "fp-2", Handle: "wodan", ThisMember: true, Level: 2}
	member6   = Viewer{Fingerprint: "fp-6", Handle: "phantom", ThisMember: true, Level: 6}
	member9   = Viewer{Fingerprint: "fp-9", Handle: "root", ThisMember: true, Level: 9}
)

func TestBoardVisibility(t *testing.T) {
	e, _ := newEngine(t)

	ids := func(v Viewer) []string {
		var out []string
		for _, b := range e.VisibleBoards(v) {
			out = append(out, b.ID)
		}
		return out
	}

	// Non-members: zero evidence THIS exists.
	if got := ids(nonMember); strings.Join(got, ",") != "algemeen" {
		t.Fatalf("non-member sees %v", got)
	}
	// Member level 0: this-board yes, phreak (min 3) entirely absent.
	if got := ids(member0); strings.Join(got, ",") != "algemeen,this-board" {
		t.Fatalf("member0 sees %v", got)
	}
	if got := ids(member9); strings.Join(got, ",") != "algemeen,this-board,phreak" {
		t.Fatalf("member9 sees %v", got)
	}

	// Board above level / non-member board: identical ErrNoBoard as gibberish.
	for _, v := range []Viewer{nonMember, member2} {
		_, errA := e.Listing(context.Background(), "phreak", v)
		_, errB := e.Listing(context.Background(), "zzzzz", v)
		if !errors.Is(errA, ErrNoBoard) || !errors.Is(errB, ErrNoBoard) {
			t.Fatalf("hidden board leaks: %v vs %v", errA, errB)
		}
	}
	if _, err := e.Listing(context.Background(), "this-board", nonMember); !errors.Is(err, ErrNoBoard) {
		t.Fatalf("non-member can address this-board: %v", err)
	}
}

func TestListingRedaction(t *testing.T) {
	e, _ := newEngine(t)
	ctx := context.Background()

	l, err := e.Listing(ctx, "this-board", member2)
	if err != nil {
		t.Fatal(err)
	}
	rows := map[int]Row{}
	for _, r := range l.Rows {
		rows[r.ID] = r
	}

	// Readable: normal rows.
	if r := rows[142]; r.Redacted || r.Subject != "welkom nieuwe leden" {
		t.Fatalf("142: %+v", r)
	}
	if r := rows[150]; r.Redacted || r.Author != "route66" {
		t.Fatalf("150 at level 2 should be readable: %+v", r)
	}
	// Above level, subject visible: stub with subject + author, level tag.
	r := rows[152]
	if !r.Redacted || r.Subject != "de echte ingang naar node 9" || r.Author != "phantom" || r.Level != 6 {
		t.Fatalf("152 stub wrong: %+v", r)
	}
	// Above level, subject+author blocked: stub fully blanked.
	r = rows[153]
	if !r.Redacted || strings.Contains(r.Subject, "supergeheim") || strings.Contains(r.Author, "phantom") {
		t.Fatalf("153 leaks: %+v", r)
	}
	// Hidden message: not listed at all, counted.
	if _, ok := rows[154]; ok {
		t.Fatal("hidden message 154 is listed")
	}
	if l.HiddenCount != 1 {
		t.Fatalf("HiddenCount = %d, want 1", l.HiddenCount)
	}

	// At level 9 everything resolves.
	l9, _ := e.Listing(ctx, "this-board", member9)
	if l9.HiddenCount != 0 {
		t.Fatalf("level 9 HiddenCount = %d", l9.HiddenCount)
	}
	for _, r := range l9.Rows {
		if r.Redacted {
			t.Fatalf("level 9 sees redacted row: %+v", r)
		}
	}
}

func TestReadClearanceAndFlags(t *testing.T) {
	e, st := newEngine(t)
	ctx := context.Background()
	st.(*sqlitestore.Store).CreatePlayer(ctx, "fp-2")

	// Readable message grants its flag.
	m, err := e.Read(ctx, "this-board", 150, member2)
	if err != nil || m.Body != "geheim-2" {
		t.Fatalf("read 150: %v %+v", err, m)
	}
	p, _ := st.PlayerByFingerprint(ctx, "fp-2")
	if !p.HasFlag("saw_modempool") {
		t.Fatal("grants_flag not applied on read")
	}

	// Above level: specific refusal naming the required clearance.
	_, err = e.Read(ctx, "this-board", 152, member2)
	var ec ErrClearance
	if !errors.As(err, &ec) || ec.Need != 6 {
		t.Fatalf("read 152: %v", err)
	}

	// Hidden message must not confirm existence: same error as nonexistent.
	_, errHidden := e.Read(ctx, "this-board", 154, member2)
	_, errNo := e.Read(ctx, "this-board", 999, member2)
	if !errors.Is(errHidden, ErrNoMessage) || !errors.Is(errNo, ErrNoMessage) {
		t.Fatalf("hidden read leaks: %v vs %v", errHidden, errNo)
	}
}

func TestPostLevelRules(t *testing.T) {
	e, _ := newEngine(t)
	ctx := context.Background()

	// THIS post defaults to author's level; author may lower, never raise.
	id, err := e.Post(ctx, "this-board", member6, "test", "body", -1, 0)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := e.Read(ctx, "this-board", id, member6)
	if m.Level != 6 {
		t.Fatalf("default post level = %d, want 6", m.Level)
	}
	id, _ = e.Post(ctx, "this-board", member6, "lager", "body", 1, 0)
	m, _ = e.Read(ctx, "this-board", id, member6)
	if m.Level != 1 {
		t.Fatalf("lowered post level = %d, want 1", m.Level)
	}
	id, _ = e.Post(ctx, "this-board", member6, "hoger", "body", 9, 0)
	m, _ = e.Read(ctx, "this-board", id, member6)
	if m.Level != 6 {
		t.Fatalf("raise attempt gave level %d, want clamp to 6", m.Level)
	}

	// Public posts always level 0, whatever the author's clearance.
	id, err = e.Post(ctx, "algemeen", member6, "hoi", "body", 6, 0)
	if err != nil {
		t.Fatal(err)
	}
	m, _ = e.Read(ctx, "algemeen", id, nonMember)
	if m == nil || m.Level != 0 {
		t.Fatalf("public post: %+v", m)
	}

	// Reply to a message above your level is allowed (paranoia feature)...
	if _, err := e.Post(ctx, "this-board", member2, "re: ingang", "wat is dit?", -1, 152); err != nil {
		t.Fatalf("reply above level: %v", err)
	}
	// ...but replying to a hidden message must not confirm it exists.
	if _, err := e.Post(ctx, "this-board", member2, "re: x", "x", -1, 154); !errors.Is(err, ErrNoMessage) {
		t.Fatalf("reply to hidden: %v", err)
	}
	// Reply to nonexistent: same error.
	if _, err := e.Post(ctx, "this-board", member2, "re: x", "x", -1, 999); !errors.Is(err, ErrNoMessage) {
		t.Fatalf("reply to missing: %v", err)
	}
}

func TestPostSanitization(t *testing.T) {
	e, _ := newEngine(t)
	ctx := context.Background()

	id, err := e.Post(ctx, "algemeen", member0, "hoi\x1b[31m", "body\x1b[2Jmet escape", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := e.Read(ctx, "algemeen", id, member0)
	if strings.ContainsRune(m.Subject+m.Body, 0x1b) {
		t.Fatalf("escape survived write: %q %q", m.Subject, m.Body)
	}

	if _, err := e.Post(ctx, "algemeen", member0, "\x1b\x07", "\x00", 0, 0); !errors.Is(err, ErrBadPost) {
		t.Fatalf("all-control post accepted: %v", err)
	}
}

func TestReadOnlyBoard(t *testing.T) {
	e, _ := newEngine(t)
	e.content.Boards[0].Writable = false
	if _, err := e.Post(context.Background(), "algemeen", member0, "x", "y", 0, 0); !errors.Is(err, ErrNotWritable) {
		t.Fatalf("posted to read-only board: %v", err)
	}
}
