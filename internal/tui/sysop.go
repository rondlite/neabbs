package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rondlite/neabbs/internal/llm"
	"github.com/rondlite/neabbs/internal/store"
	"github.com/rondlite/neabbs/internal/text"
)

// sysopSurface reports whether the current state is a command surface where
// the `sysop` verb should be intercepted (never mid-compose or handle-pick,
// where the typed line is content, not a command).
func (m *Model) sysopSurface() bool {
	switch m.state {
	case stateMenu, stateBoards, stateFiles, stateThis:
		return true
	}
	return false
}

const sysopHelp = `SYSOP — moderatie
  sysop who              alle lijnen, met vingerafdruk en THIS-aanwezigheid
  sysop zeg <tekst>      omroep naar iedereen (publiek én THIS)
  sysop wis <nr>         verwijder bericht <nr> in het huidige board
  sysop ban <handle>     verban een speler (verbreekt live sessies direct)
  sysop unban <handle>   hef een verbanning op
  sysop gen <board> [n]  laat de LLM n concepten schrijven (wachtrij)
  sysop pending          toon concepten die op review wachten
  sysop ok <id>          publiceer een concept
  sysop nee <id>         verwerp een concept
  sysop help             deze lijst`

// sysopCmd dispatches the sysop-only verbs. It is only reached for players
// with Admin set (see handleLine), so it never leaks to ordinary callers.
func (m *Model) sysopCmd(line string, fields []string) (tea.Model, tea.Cmd) {
	sub := ""
	if len(fields) > 1 {
		sub = strings.ToLower(fields[1])
	}
	switch sub {
	case "", "help", "?":
		return m, m.out(sysopHelp)
	case "who", "wie":
		return m, m.out(m.renderSysopWho())
	case "zeg", "say", "omroep":
		rest := ""
		if idx := strings.Index(line, " "); idx > 0 {
			if idx2 := strings.Index(line[idx+1:], " "); idx2 >= 0 {
				rest = strings.TrimSpace(line[idx+1+idx2:])
			}
		}
		return m.sysopBroadcast(rest)
	case "wis", "del", "delete":
		arg := ""
		if len(fields) > 2 {
			arg = fields[2]
		}
		return m.sysopDelete(arg)
	case "ban", "unban":
		arg := ""
		if len(fields) > 2 {
			arg = fields[2]
		}
		return m.sysopBan(arg, sub == "ban")
	case "gen", "genereer":
		board := ""
		if len(fields) > 2 {
			board = strings.ToLower(fields[2])
		}
		n := 5
		if len(fields) > 3 {
			if v, err := strconv.Atoi(fields[3]); err == nil {
				n = v
			}
		}
		return m.sysopGen(board, n)
	case "pending", "wachtrij":
		return m.sysopPending()
	case "ok", "publiceer":
		arg := ""
		if len(fields) > 2 {
			arg = fields[2]
		}
		return m.sysopReview(arg, true)
	case "nee", "verwerp":
		arg := ""
		if len(fields) > 2 {
			arg = fields[2]
		}
		return m.sysopReview(arg, false)
	}
	return m, m.out("Onbekend sysop-commando. Probeer: sysop help")
}

// renderSysopWho is the elevated user list: unlike the public `who`, it shows
// every session in full — handle, line, fingerprint prefix, and whether the
// caller is inside THIS. Sysop-only, so the THIS leak is intentional.
func (m *Model) renderSysopWho() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("SYSOP — ALLE LIJNEN (%d bezet)\n", m.deps.Registry.Count()))
	b.WriteString(strings.Repeat("-", 60) + "\n")
	b.WriteString(fmt.Sprintf("  %-4s %-16s %-16s %s\n", "LIJN", "HANDLE", "VINGERAFDRUK", "PLEK"))
	for _, s := range m.deps.Registry.All() {
		handle, area, inThis := s.Snapshot()
		if handle == "" {
			handle = "(inloggen...)"
		}
		plek := area
		if plek == "" {
			plek = "hoofdmenu"
		}
		if inThis {
			plek = "THIS » " + plek
		}
		b.WriteString(fmt.Sprintf("  %-4s %-16s %-16s %s\n",
			lineLabel(s.Line), handle, fpShort(s.Fingerprint), plek))
	}
	return b.String()
}

// fpShort trims a "SHA256:base64…" fingerprint to a glanceable prefix.
func fpShort(fp string) string {
	fp = strings.TrimPrefix(fp, "SHA256:")
	if len(fp) > 12 {
		return fp[:12]
	}
	return fp
}

// sysopBroadcast shouts a sysop announcement to every session.
func (m *Model) sysopBroadcast(msg string) (tea.Model, tea.Cmd) {
	if msg == "" {
		return m, m.out("Gebruik: sysop zeg <tekst>")
	}
	line := "*** SYSOP: " + msg
	m.deps.Registry.Broadcast(SysopMsg{Line: line}, nil)
	return m, m.out("Omgeroepen naar alle lijnen.")
}

// sysopBan bans or unbans a player by handle. A ban takes the ban bit (so
// they cannot reconnect) and immediately kicks any live sessions on that
// fingerprint. Identity is the SSH pubkey, so a determined banned caller can
// still return with a fresh key — this is a speed bump, not a wall.
func (m *Model) sysopBan(handle string, ban bool) (tea.Model, tea.Cmd) {
	if handle == "" {
		return m, m.out("Gebruik: sysop ban <handle>")
	}
	target, err := m.deps.Store.PlayerByHandle(context.Background(), handle)
	if err != nil {
		return m, m.out("Geen speler met die naam.")
	}
	if ban && target.Fingerprint == m.deps.Player.Fingerprint {
		return m, m.out("Je kunt jezelf niet bannen.")
	}
	if err := m.deps.Store.SetBanned(context.Background(), target.Fingerprint, ban); err != nil {
		return m, m.out("Actie mislukt.")
	}
	if !ban {
		return m, m.out(fmt.Sprintf("%s is niet langer verbannen.", target.Handle))
	}
	// Drop any live sessions on that fingerprint.
	kicked := 0
	for _, s := range m.deps.Registry.All() {
		if s.Fingerprint == target.Fingerprint {
			s.SendMsg(KickMsg{Reason: "TOEGANG INGETROKKEN DOOR DE SYSOP."})
			kicked++
		}
	}
	return m, m.out(fmt.Sprintf("%s verbannen. %d live sessie(s) verbroken.", target.Handle, kicked))
}

// sysopDelete removes a player-authored post in the current board. YAML-seeded
// content (ID < 10000) is immutable and reported as such.
func (m *Model) sysopDelete(arg string) (tea.Model, tea.Cmd) {
	if m.boardID == "" {
		return m, m.out("Open eerst een board: board <id>")
	}
	nr, err := strconv.Atoi(arg)
	if err != nil {
		return m, m.out("Gebruik: sysop wis <nr>")
	}
	deleted, err := m.deps.Store.DeletePost(context.Background(), m.boardID, nr)
	if err != nil {
		return m, m.out("Verwijderen mislukt.")
	}
	if !deleted {
		if nr < 10000 {
			return m, m.out(fmt.Sprintf("#%d is vaste content — alleen door bellers geplaatste berichten kunnen weg.", nr))
		}
		return m, m.out(fmt.Sprintf("Geen bericht #%d in dit board.", nr))
	}
	// Reprint the listing so the sysop sees the result.
	l, lerr := m.deps.Boards.Listing(context.Background(), m.boardID, m.viewer())
	out := fmt.Sprintf("Bericht #%d verwijderd.\n", nr)
	if lerr == nil {
		out += "\n" + renderListing(l)
	}
	return m, m.out(out)
}

// genDraftedMsg reports the outcome of an async draft generation.
type genDraftedMsg struct {
	board string
	n     int
	err   error
}

// sysopGen kicks off async LLM generation of n filler drafts for a board. The
// LLM call and DB writes run in a tea.Cmd (off the Update goroutine) so the
// session never blocks — drafts land in the pending queue for review, never
// live. Honours the "review before use" rule the offline genposts tool set.
func (m *Model) sysopGen(board string, n int) (tea.Model, tea.Cmd) {
	if !m.deps.LLM.Enabled() {
		return m, m.out("LLM staat uit — genereren kan niet (zet LLM_BASE_URL/MODEL/API_KEY).")
	}
	b := m.deps.Content.BoardByID(board)
	if b == nil {
		return m, m.out("Gebruik: sysop gen <board> [n] — onbekend board.")
	}
	if n < 1 {
		n = 1
	}
	if n > 10 {
		n = 10 // one LLM call, keep it bounded
	}
	base := m.deps.Content.Prompts["genposts"]
	lc, st := m.deps.LLM, m.deps.Store
	boardID, boardName := b.ID, b.Name
	gen := func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		sys := llm.GenpostSystemPrompt(base, boardID, boardName, 0, n)
		out, err := lc.Chat(ctx, []llm.Message{
			{Role: "system", Content: sys},
			{Role: "user", Content: fmt.Sprintf("Genereer %d berichten voor %s.", n, boardName)},
		})
		if err != nil {
			return genDraftedMsg{err: err}
		}
		drafts, err := llm.ParseDrafts(out)
		if err != nil {
			return genDraftedMsg{err: err}
		}
		saved := 0
		for _, d := range drafts {
			author := text.CleanLine(d.Author)
			if author == "" {
				author = "anoniem"
			}
			_, e := st.SavePost(ctx, &store.SavedMessage{
				BoardID:  boardID,
				Author:   author,
				Level:    0, // filler is board texture; review before raising
				Subject:  text.CleanLine(d.Subject),
				Body:     text.Clean(d.Body),
				Pending:  true,
				PostedAt: time.Now(),
			})
			if e == nil {
				saved++
			}
		}
		return genDraftedMsg{board: boardID, n: saved}
	}
	return m, tea.Batch(
		m.out(fmt.Sprintf("Concepten aanvragen bij de LLM voor %s (%d)… dit kan even duren.", boardID, n)),
		gen)
}

// sysopPending lists the review queue across all boards.
func (m *Model) sysopPending() (tea.Model, tea.Cmd) {
	posts, err := m.deps.Store.PendingPosts(context.Background())
	if err != nil {
		return m, m.out("Kon de wachtrij niet ophalen.")
	}
	if len(posts) == 0 {
		return m, m.out("Geen concepten in de wachtrij.")
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("CONCEPTEN OP REVIEW (%d)\n", len(posts)))
	b.WriteString(strings.Repeat("-", 60) + "\n")
	for _, p := range posts {
		b.WriteString(fmt.Sprintf("  #%-6d %-10s %-12.12s %.32s\n", p.ID, p.BoardID, p.Author, p.Subject))
	}
	b.WriteString("\nsysop ok <id> publiceert · sysop nee <id> verwerpt")
	return m, m.out(b.String())
}

// sysopReview publishes (ok) or discards (nee) a single draft by ID.
func (m *Model) sysopReview(arg string, publish bool) (tea.Model, tea.Cmd) {
	id, err := strconv.Atoi(arg)
	if err != nil {
		if publish {
			return m, m.out("Gebruik: sysop ok <id>")
		}
		return m, m.out("Gebruik: sysop nee <id>")
	}
	var ok bool
	if publish {
		ok, err = m.deps.Store.PublishPost(context.Background(), id)
	} else {
		ok, err = m.deps.Store.DeletePendingPost(context.Background(), id)
	}
	if err != nil {
		return m, m.out("Actie mislukt.")
	}
	if !ok {
		return m, m.out(fmt.Sprintf("Geen concept #%d in de wachtrij.", id))
	}
	if publish {
		return m, m.out(fmt.Sprintf("#%d gepubliceerd.", id))
	}
	return m, m.out(fmt.Sprintf("#%d verworpen.", id))
}
