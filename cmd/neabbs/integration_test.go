package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"github.com/rondlite/neabbs/internal/board"
	"github.com/rondlite/neabbs/internal/chat"
	"github.com/rondlite/neabbs/internal/config"
	"github.com/rondlite/neabbs/internal/content"
	"github.com/rondlite/neabbs/internal/llm"
	"github.com/rondlite/neabbs/internal/presence"
	"github.com/rondlite/neabbs/internal/store/sqlitestore"
	"github.com/rondlite/neabbs/internal/world"
)

// startServer boots the full Wish server on a random port.
func startServer(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Config{
		Listen:     "127.0.0.1:0",
		DBPath:     filepath.Join(dir, "test.db"),
		HostKey:    filepath.Join(dir, "hostkey"),
		ContentDir: "../../content",
		BaudOff:    true,
	}
	st, err := sqlitestore.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	cset, err := content.Load(cfg.ContentDir)
	if err != nil {
		t.Fatal(err)
	}
	registry := presence.NewRegistry()
	srv, err := newServer(cfg, st, registry, board.NewEngine(cset, st), cset, chat.NewRoom(), world.NewEngine(cset, st), llm.New(cfg))
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return ln.Addr().String()
}

// client is a scripted SSH session against the BBS.
type client struct {
	t    *testing.T
	sess *gossh.Session
	in   io.WriteCloser

	mu         sync.Mutex
	buf        bytes.Buffer
	answeredAt int // buffer offset up to which pager prompts were answered
}

func dialBBS(t *testing.T, addr string) *client {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := gossh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := gossh.Dial("tcp", addr, &gossh.ClientConfig{
		User:            "caller",
		Auth:            []gossh.AuthMethod{gossh.PublicKeys(signer)},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	sess, err := conn.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.RequestPty("xterm-256color", 24, 80, gossh.TerminalModes{}); err != nil {
		t.Fatal(err)
	}
	in, err := sess.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := sess.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	c := &client{t: t, sess: sess, in: in}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := out.Read(buf)
			if n > 0 {
				c.mu.Lock()
				c.buf.Write(buf[:n])
				c.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	if err := sess.Shell(); err != nil {
		t.Fatal(err)
	}
	return c
}

func (c *client) send(s string) {
	c.t.Helper()
	// Wait for output to settle first: any key during a draw is (by design)
	// swallowed as skip-to-end, so racing a mid-draw screen would eat keys.
	deadline := time.Now().Add(5 * time.Second)
	stable, last := 0, -1
	for stable < 4 && time.Now().Before(deadline) {
		c.mu.Lock()
		n := c.buf.Len()
		c.mu.Unlock()
		if n == last {
			stable++
		} else {
			stable, last = 0, n
		}
		time.Sleep(25 * time.Millisecond)
	}
	if _, err := io.WriteString(c.in, s); err != nil {
		c.t.Fatalf("send %q: %v", s, err)
	}
}

// waitFor blocks until substr shows up in the output (or times out),
// answering any -- Meer? -- pager prompts along the way.
func (c *client) waitFor(substr string) {
	c.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		got := c.buf.String()
		c.mu.Unlock()
		if strings.Contains(got, substr) {
			return
		}
		// Answer pager prompts with Enter: continues paging, and if the
		// prompt was already gone (repaint race) a stray Enter is harmless
		// (it just re-prints the current menu/listing). The prompt is
		// localized, so watch for either language.
		idx := strings.LastIndex(got, "-- Meer?")
		if i := strings.LastIndex(got, "-- More?"); i > idx {
			idx = i
		}
		if idx != -1 && idx >= c.answeredAt {
			c.answeredAt = len(got)
			c.send("\r")
		}
		time.Sleep(25 * time.Millisecond)
	}
	c.mu.Lock()
	got := c.buf.String()
	c.mu.Unlock()
	c.t.Fatalf("timeout waiting for %q; output:\n%s", substr, got)
}

// register walks a brand-new caller through first login: the bilingual
// language prompt (Dutch here; TestFirstLoginEnglish covers the other branch)
// and then the handle picker.
func (c *client) register(handle string) {
	c.t.Helper()
	c.waitFor("Taal / Language")
	c.send("1\r")
	c.waitFor("Nieuwe beller gedetecteerd")
	c.send(handle + "\r")
}

// snapshot returns everything received so far.
func (c *client) snapshot() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

func TestFullCallRitualAndInvisibility(t *testing.T) {
	addr := startServer(t)
	c := dialBBS(t, addr)

	// Call ritual: connect banner → handle picker → theater → menu.
	c.waitFor("CONNECT 2400")
	c.register("tester")
	c.waitFor("Aangenaam, tester")
	c.waitFor("Toegang verleend")
	c.waitFor("LAATSTE BELLERS")
	c.waitFor("phantom") // seeded 1986 caller visible
	c.waitFor("HOOFDMENU")

	// Non-member invisibility: board list must not contain the THIS board.
	c.send("b")
	c.waitFor("ALGEMEEN")
	if strings.Contains(c.snapshot(), "testkelder") {
		t.Fatal("THIS board leaked to non-member")
	}
	// Addressing it directly: same error as gibberish.
	c.send("testkelder\r")
	c.waitFor("Onbekend board.")
	c.send("terug\r")
	c.waitFor("HOOFDMENU")

	// Unknown menu input vs. future hidden command: identical response.
	c.send("xyzzy\r")
	c.waitFor("Onbekende keuze.")

	// File area works.
	c.send("f")
	c.waitFor("BESTANDEN")
	c.waitFor("modem-abc.txt")
	c.send("lees 2\r")
	c.waitFor("HET MODEM-ABC")
	c.send("terug\r")
	c.waitFor("HOOFDMENU")

	// Logout always ends in NO CARRIER.
	c.send("u")
	c.waitFor("NO CARRIER")
}

// TestFirstLoginEnglish: an English caller picks [2] at the bilingual prompt
// and the rest of the ritual — and the menu — arrives in English, with the
// [L] entry still readable in both languages.
func TestFirstLoginEnglish(t *testing.T) {
	addr := startServer(t)
	c := dialBBS(t, addr)

	c.waitFor("Taal / Language")
	c.send("2\r")
	c.waitFor("New caller detected")
	c.send("brit\r")
	c.waitFor("Pleased to meet you, brit")
	c.waitFor("Access granted")
	c.waitFor("MAIN MENU")
	c.waitFor("[N] Nederlands")
	c.waitFor("[E] English")

	// The menu hotkeys switch either way, in either language.
	c.send("n")
	c.waitFor("Taal ingesteld op Nederlands")
	c.waitFor("HOOFDMENU")
	c.send("e")
	c.waitFor("Language set to English")
	c.waitFor("MAIN MENU")
}

// TestDutchCallerSeesEnglishEscapeHatch: the language row is the one thing a
// caller stranded in the wrong language must be able to read, so it stays
// bilingual even for a Dutch caller.
func TestDutchCallerSeesEnglishEscapeHatch(t *testing.T) {
	addr := startServer(t)
	c := dialBBS(t, addr)
	c.register("hollander")
	c.waitFor("HOOFDMENU")
	c.waitFor("[E] English")
}

func TestTwoCallersChatAndPraat(t *testing.T) {
	addr := startServer(t)

	a := dialBBS(t, addr)
	a.register("alice")
	a.waitFor("HOOFDMENU")

	b := dialBBS(t, addr)
	b.register("bob")
	b.waitFor("HOOFDMENU")

	// praat reaches the other line.
	a.send("praat hallo allemaal\r")
	b.waitFor("alice")
	b.waitFor("hallo allemaal")

	// Babbel: both join, messages flow, join notice visible.
	a.send("c")
	a.waitFor("BABBELBOX")
	b.send("c")
	b.waitFor("BABBELBOX")
	a.waitFor("(bob) komt binnen")
	b.send("goedenavond\r")
	a.waitFor("<bob> goedenavond")

	// who: both lines listed with area. (/weg leaves chat; a lone ESC would
	// be ambiguous with an alt-prefix over a scripted pipe.)
	a.send("/weg\r")
	a.waitFor("HOOFDMENU")
	a.send("w")
	a.waitFor("WIE IS ER OP DE LIJNEN")
	a.waitFor("babbelbox")
}

func TestBoardPostVisibleToOtherCaller(t *testing.T) {
	addr := startServer(t)

	a := dialBBS(t, addr)
	a.register("carol")
	a.waitFor("HOOFDMENU")
	a.send("b")
	a.waitFor("Gebruik: board <id>")
	a.send("algemeen\r")
	a.waitFor("NEABBS is terug")
	a.send("post\r")
	a.waitFor("Onderwerp:")
	a.send("integratietest\r")
	a.waitFor("Sluit af met")
	a.send("dit is een testbericht\r")
	a.send(".\r")
	a.waitFor("Geplaatst als bericht #10000")

	b := dialBBS(t, addr)
	b.register("dave")
	b.waitFor("HOOFDMENU")
	b.send("b")
	b.waitFor("Gebruik: board <id>")
	b.send("algemeen\r")
	b.waitFor("integratietest")
	b.send("read 10000\r")
	b.waitFor("dit is een testbericht")
}

// TestDiscoveryChainIntoTHIS walks the full public→THIS path: misfiled file
// → phantom's thread → gated final file → hidden command → THIS mode.
func TestDiscoveryChainIntoTHIS(t *testing.T) {
	addr := startServer(t)
	c := dialBBS(t, addr)
	c.register("hacker")
	c.waitFor("HOOFDMENU")

	// Door without flag: identical to gibberish, and the gated file is
	// absent from the list.
	c.send("this\r")
	c.waitFor("Onbekende keuze.")
	c.send("f")
	c.waitFor("BESTANDEN")
	if strings.Contains(c.snapshot(), "herstel-log.txt") {
		t.Fatal("gated file visible before flag")
	}

	// Beat 1: the misfiled sysop notes name phantom and band 7.
	c.send("lees 9\r")
	c.waitFor("NOTITIES BIJ HET HERSTEL")
	c.send("terug\r")
	c.waitFor("HOOFDMENU")

	// Beat 2: phantom's follow-up on ALGEMEEN grants the spoor flag.
	c.send("b")
	c.waitFor("Gebruik: board <id>")
	c.send("algemeen\r")
	c.waitFor("40 jaar is niks")
	c.send("read 11\r")
	c.waitFor("die dit wél leest")
	c.send("terug\r")
	c.waitFor("HOOFDMENU")

	// Beat 3: the final file is now listed and grants this_invite.
	c.send("f")
	c.waitFor("herstel-log.txt")
	c.send("lees 12\r")
	c.waitFor("HERSTEL-LOGBOEK")
	c.send("terug\r")
	c.waitFor("HOOFDMENU")

	// The ritual: type the four letters at the main menu.
	c.send("this\r")
	c.waitFor("DOORVERBINDEN NAAR: THIS")
	c.waitFor("VERBINDING OMGELEGD")
	c.waitFor("THIS-0")
	// Crossing the threshold switches to the alt screen: THIS is a full-screen
	// surface with a pinned status bar, not scrollback like the public BBS.
	if !strings.Contains(c.snapshot(), "\x1b[?1049h") {
		t.Fatal("THIS did not enter the alt screen: the status bar cannot stay pinned")
	}

	// THIS boards: iceberg visible from minute one.
	c.send("boards\r")
	c.waitFor("this-board")
	c.send("board this-board\r")
	c.waitFor("lees dit eerst")
	c.waitFor("de echte ingang naar node 9") // tantalizing stub subject
	c.waitFor("[THIS-6]")
	c.waitFor("verborgen boven jouw niveau")
	c.send("read 112\r")
	c.waitFor("TOEGANG GEWEIGERD — THIS-6 vereist.")
	c.send("read 101\r")
	c.waitFor("niveau krijg je van het systeem")

	// A second, fresh caller sees nothing: no THIS boards, and the member
	// inside THIS shows only as a busy line.
	d := dialBBS(t, addr)
	d.register("normaal")
	d.waitFor("HOOFDMENU")
	d.send("w")
	d.waitFor("WIE IS ER OP DE LIJNEN")
	d.waitFor("lijn bezet")
	if strings.Contains(d.snapshot(), "this-board") {
		t.Fatal("THIS board leaked to non-member")
	}

	// The world: scan lists the open tutorial host and the first locked
	// host. Deeper hosts stay invisible until their flags are earned.
	c.send("scan\r")
	c.waitFor("archief.this.nl")
	c.waitFor("vax.gemeente.nl")
	c.waitFor("[vergrendeld]")
	c.send("connect phantom.thuis.nl\r")
	c.waitFor("geen route naar host.") // deep arc host invisible at THIS-0

	// Tutorial host: connect → banner, ls shows redacted rows, cat works,
	// above-level cat names the clearance.
	c.send("connect archief.this.nl\r")
	c.waitFor("THIS ARCHIEF")
	c.send("ls\r")
	c.waitFor("readme.1st")
	c.waitFor("[THIS-2]")
	c.send("cat readme.1st\r")
	c.waitFor("crack") // the word is discovered here
	c.send("cat modemlijst-oud.dat\r")
	c.waitFor("TOEGANG GEWEIGERD — THIS-2 vereist.")

	// A locked host on the THIS-0 map responds specifically: connecting shows
	// its banner, and cracking without the password names what's missing
	// (the puzzle) rather than failing generically.
	c.send("connect vax.gemeente.nl\r")
	c.waitFor("GEMEENTE AMSTERDAM")
	c.send("crack\r")
	c.waitFor("wachtwoord vereist")
	c.send("disconnect\r")
	c.waitFor("verbroken")

	// Unknown vs. above-clearance address: identical no-route error.
	c.send("connect bestaat.niet.nl\r")
	c.waitFor("geen route naar host.")

	// who shows members with levels; wall reaches THIS members.
	c.send("who\r")
	c.waitFor("hacker")
	c.waitFor("THIS-0")
	c.send("wall test van de muur\r")
	c.waitFor("*** hacker: test van de muur")

	// Back through the door; the menu now shows the THIS entry.
	c.send("exit\r")
	c.waitFor("[T] THIS")
	c.send("u")
	c.waitFor("NO CARRIER")
}

// TestHackingArc walks the full THIS-0→THIS-3 crack chain: read the board
// clue → crack gemeente (promote to 1, trace) → read files → crack SARA
// (2) → crack UvA (3) → #phreak unlocks.
func TestHackingArc(t *testing.T) {
	addr := startServer(t)
	c := dialBBS(t, addr)
	c.register("kraker")
	c.waitFor("HOOFDMENU")

	// Shortcut into THIS: grant the invite via a synthetic run of the chain
	// is long, so drive the door directly by reading the real chain files.
	c.send("f")
	c.waitFor("BESTANDEN")
	c.send("lees 9\r")
	c.waitFor("NOTITIES")
	c.send("terug\r")
	c.waitFor("HOOFDMENU")
	c.send("b")
	c.waitFor("Gebruik: board <id>")
	c.send("algemeen\r")
	c.waitFor("40 jaar is niks")
	c.send("read 11\r")
	c.waitFor("die dit wél leest")
	c.send("terug\r")
	c.waitFor("HOOFDMENU")
	c.send("f")
	c.waitFor("herstel-log.txt")
	c.send("lees 12\r")
	c.waitFor("HERSTEL-LOGBOEK")
	c.send("terug\r")
	c.waitFor("HOOFDMENU")
	c.send("this\r")
	c.waitFor("THIS-0")

	// Read the board clue → grants gemeente_pw.
	c.send("board this-board\r")
	c.waitFor("de gemeente slaapt")
	c.send("read 106\r")
	c.waitFor("nachtdienst")

	// #phreak (THIS-3) must be invisible at THIS-0.
	c.send("boards\r")
	if strings.Contains(c.snapshot(), "phreak") {
		t.Fatal("#phreak visible below THIS-3")
	}

	// Crack gemeente → promote to THIS-1, trace starts.
	c.send("scan\r")
	c.waitFor("vax.gemeente.nl")
	c.send("connect vax.gemeente.nl\r")
	c.waitFor("VAX/VMS")
	c.send("crack\r")
	c.waitFor("TOEGANG VERLEEND")
	c.waitFor("PROMOTIE — THIS-1")
	// The status bar is the always-visible clearance readout: it must track the
	// promotion, not stay pinned at the level you entered with.
	c.waitFor("kraker · THIS-1")
	c.waitFor("TRACE ACTIEF")
	c.send("ls\r")
	c.waitFor("modemlijst.dat")
	c.send("cat modemlijst.dat\r") // grants found_modemlist → SARA + beheerder visible
	c.waitFor("SARA")
	c.send("cat notulen-jan86.txt\r") // grants sara_testaccount
	c.waitFor("koffie86")
	c.send("disconnect\r")
	c.waitFor("trace afgebroken")

	// NPC talk with the LLM disabled must degrade to the canned fallback,
	// never block, and respect 'weg'.
	c.send("connect beheerder.sara.nl\r")
	c.waitFor("beheerder")
	c.send("talk\r")
	c.waitFor("gesprek met beheerder")
	c.send("hallo, wie ben jij?\r")
	c.waitFor("gromt iets onverstaanbaars") // fallback text, LLM off
	c.send("weg\r")
	c.waitFor("verbreekt het gesprek")
	c.send("disconnect\r")
	c.waitFor("verbroken")

	// Crack SARA → THIS-2.
	c.send("connect rekencentrum.sara.nl\r")
	c.waitFor("CYBER")
	c.send("crack\r")
	c.waitFor("TOEGANG VERLEEND")
	c.waitFor("PROMOTIE — THIS-2")
	c.send("cat gebruikers.dir\r") // grants sara_userlist → UvA visible
	c.waitFor("PHANTOM")
	c.send("cat phantom-account.txt\r") // grants phantom_account
	c.waitFor("hydra")
	c.send("disconnect\r")
	c.waitFor("verbroken")

	// Crack UvA → THIS-3, #phreak opens.
	c.send("connect hydra.uva.nl\r")
	c.waitFor("HYDRA")
	c.send("crack\r")
	c.waitFor("TOEGANG VERLEEND")
	c.waitFor("PROMOTIE — THIS-3")
	c.send("disconnect\r")
	c.waitFor("verbroken")

	// Leaving and re-entering THIS greets the operator with their real
	// clearance: the arrival banner used to hardcode THIS-0, which read as a
	// demotion to anyone who had climbed.
	c.send("exit\r")
	c.waitFor("HOOFDMENU")
	c.send("this\r")
	c.waitFor("toegang: THIS-3.")

	// #phreak now exists and is readable.
	c.send("boards\r")
	c.waitFor("phreak")
	c.send("board phreak\r")
	c.waitFor("je bent er")
	c.send("read 201\r")
	c.waitFor("de hele keten gelopen")

	c.send("logout\r")
	c.waitFor("NO CARRIER")
}
