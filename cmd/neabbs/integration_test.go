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
	srv, err := newServer(cfg, st, registry, board.NewEngine(cset, st), cset, chat.NewRoom(), world.NewEngine(cset, st))
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
		// (it just re-prints the current menu/listing).
		if idx := strings.LastIndex(got, "-- Meer?"); idx != -1 && idx >= c.answeredAt {
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
	c.waitFor("CONNECT 1200")
	c.waitFor("Nieuwe beller gedetecteerd")
	c.send("tester\r")
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

func TestTwoCallersChatAndPraat(t *testing.T) {
	addr := startServer(t)

	a := dialBBS(t, addr)
	a.waitFor("Nieuwe beller gedetecteerd")
	a.send("alice\r")
	a.waitFor("HOOFDMENU")

	b := dialBBS(t, addr)
	b.waitFor("Nieuwe beller gedetecteerd")
	b.send("bob\r")
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
	a.waitFor("Nieuwe beller")
	a.send("carol\r")
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
	b.waitFor("Nieuwe beller")
	b.send("dave\r")
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
	c.waitFor("Nieuwe beller")
	c.send("hacker\r")
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
	d.waitFor("Nieuwe beller")
	d.send("normaal\r")
	d.waitFor("HOOFDMENU")
	d.send("w")
	d.waitFor("WIE IS ER OP DE LIJNEN")
	d.waitFor("lijn bezet")
	if strings.Contains(d.snapshot(), "this-board") {
		t.Fatal("THIS board leaked to non-member")
	}

	// The world: scan lists the open host and the locked teaser only.
	c.send("scan\r")
	c.waitFor("archief.this.nl")
	c.waitFor("nacht.centrale.ptt.nl")
	c.waitFor("[vergrendeld]")

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

	// Locked teaser responds specifically, naming its clearance.
	c.send("connect nacht.centrale.ptt.nl\r")
	c.waitFor("NACHTCENTRALE")
	c.waitFor("THIS-5 sleutels")
	c.send("ls\r")
	c.waitFor("THIS-5 sleutels") // ls refused with the same specific hint
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
