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
  sysop reset <handle>   wis de THIS-voortgang (playtest; verbreekt sessies)
  sysop gen <board> [n]  laat de LLM n concepten schrijven (wachtrij)
  sysop pending          toon concepten die op review wachten
  sysop ok <id>          publiceer een concept
  sysop nee <id>         verwerp een concept
  sysop help             deze lijst`

const sysopHelpEN = `SYSOP — moderation
  sysop who              all lines, with fingerprint and THIS-presence
  sysop zeg <tekst>      broadcast to everyone (public and THIS)
  sysop wis <nr>         delete message <nr> in the current board
  sysop ban <handle>     ban a player (drops live sessions at once)
  sysop unban <handle>   lift a ban
  sysop reset <handle>   wipe THIS-progress (playtest; drops sessions)
  sysop gen <board> [n]  have the LLM write n drafts (queue)
  sysop pending          show drafts awaiting review
  sysop ok <id>          publish a draft
  sysop nee <id>         reject a draft
  sysop help             this list`

// sysopCmd dispatches the sysop-only verbs. It is only reached for players
// with Admin set (see handleLine), so it never leaks to ordinary callers.
func (m *Model) sysopCmd(line string, fields []string) (tea.Model, tea.Cmd) {
	sub := ""
	if len(fields) > 1 {
		sub = strings.ToLower(fields[1])
	}
	switch sub {
	case "", "help", "?":
		return m, m.out(m.tr(sysopHelp, sysopHelpEN))
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
	case "reset":
		arg := ""
		if len(fields) > 2 {
			arg = fields[2]
		}
		return m.sysopReset(arg)
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
	return m, m.out(m.tr("Onbekend sysop-commando. Probeer: sysop help", "Unknown sysop command. Try: sysop help"))
}

// renderSysopWho is the elevated user list: unlike the public `who`, it shows
// every session in full — handle, line, fingerprint prefix, and whether the
// caller is inside THIS. Sysop-only, so the THIS leak is intentional.
func (m *Model) renderSysopWho() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf(m.tr("SYSOP — ALLE LIJNEN (%d bezet)\n", "SYSOP — ALL LINES (%d busy)\n"), m.deps.Registry.Count()))
	b.WriteString(strings.Repeat("-", 60) + "\n")
	b.WriteString(fmt.Sprintf("  %-4s %-16s %-16s %s\n", m.tr("LIJN", "LINE"), "HANDLE", m.tr("VINGERAFDRUK", "FINGERPRINT"), m.tr("PLEK", "SPOT")))
	for _, s := range m.deps.Registry.All() {
		handle, area, inThis := s.Snapshot()
		if handle == "" {
			handle = m.tr("(inloggen...)", "(logging in...)")
		}
		plek := area
		if plek == "" {
			plek = m.tr("hoofdmenu", "main menu")
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
		return m, m.out(m.tr("Gebruik: sysop zeg <tekst>", "Usage: sysop zeg <tekst>"))
	}
	line := "*** SYSOP: " + msg
	m.deps.Registry.Broadcast(SysopMsg{Line: line}, nil)
	return m, m.out(m.tr("Omgeroepen naar alle lijnen.", "Broadcast to all lines."))
}

// sysopBan bans or unbans a player by handle. A ban takes the ban bit (so
// they cannot reconnect) and immediately kicks any live sessions on that
// fingerprint. Identity is the SSH pubkey, so a determined banned caller can
// still return with a fresh key — this is a speed bump, not a wall.
func (m *Model) sysopBan(handle string, ban bool) (tea.Model, tea.Cmd) {
	if handle == "" {
		return m, m.out(m.tr("Gebruik: sysop ban <handle>", "Usage: sysop ban <handle>"))
	}
	target, err := m.deps.Store.PlayerByHandle(context.Background(), handle)
	if err != nil {
		return m, m.out(m.tr("Geen speler met die naam.", "No player by that name."))
	}
	if ban && target.Fingerprint == m.deps.Player.Fingerprint {
		return m, m.out(m.tr("Je kunt jezelf niet bannen.", "You cannot ban yourself."))
	}
	if err := m.deps.Store.SetBanned(context.Background(), target.Fingerprint, ban); err != nil {
		return m, m.out(m.tr("Actie mislukt.", "Action failed."))
	}
	if !ban {
		return m, m.out(fmt.Sprintf(m.tr("%s is niet langer verbannen.", "%s is no longer banned."), target.Handle))
	}
	// Drop any live sessions on that fingerprint.
	kicked := 0
	for _, s := range m.deps.Registry.All() {
		if s.Fingerprint == target.Fingerprint {
			s.SendMsg(KickMsg{Reason: "TOEGANG INGETROKKEN DOOR DE SYSOP."})
			kicked++
		}
	}
	return m, m.out(fmt.Sprintf(m.tr("%s verbannen. %d live sessie(s) verbroken.", "%s banned. %d live session(s) dropped."), target.Handle, kicked))
}

// sysopReset wipes a player's THIS arc for replay (a playtest tool) and kicks
// any live sessions on that fingerprint so they reconnect into the fresh
// state. Membership goes too: they land back on the public BBS with the door
// shut, and re-walk the discovery chain.
func (m *Model) sysopReset(handle string) (tea.Model, tea.Cmd) {
	if handle == "" {
		return m, m.out(m.tr("Gebruik: sysop reset <handle>", "Usage: sysop reset <handle>"))
	}
	target, err := m.deps.Store.PlayerByHandle(context.Background(), handle)
	if err != nil {
		return m, m.out(m.tr("Geen speler met die naam.", "No player by that name."))
	}
	if err := m.deps.Store.ResetProgress(context.Background(), target.Fingerprint, target.Handle); err != nil {
		return m, m.out(m.tr("Reset mislukt.", "Reset failed."))
	}
	kicked := 0
	for _, s := range m.deps.Registry.All() {
		if s.Fingerprint == target.Fingerprint {
			s.SendMsg(KickMsg{Reason: "SESSIE GERESET DOOR DE SYSOP — log opnieuw in."})
			kicked++
		}
	}
	return m, m.out(fmt.Sprintf(m.tr("%s: THIS-arc gereset (lidmaatschap ingetrokken, niveau 0, vlaggen/hosts/heat/sporen gewist). %d sessie(s) verbroken.", "%s: THIS-arc reset (membership revoked, level 0, flags/hosts/heat/traces wiped). %d session(s) dropped."), target.Handle, kicked))
}

// sysopDelete removes a player-authored post in the current board. YAML-seeded
// content (ID < 10000) is immutable and reported as such.
func (m *Model) sysopDelete(arg string) (tea.Model, tea.Cmd) {
	if m.boardID == "" {
		return m, m.out(m.tr("Open eerst een board: board <id>", "Open a board first: board <id>"))
	}
	nr, err := strconv.Atoi(arg)
	if err != nil {
		return m, m.out(m.tr("Gebruik: sysop wis <nr>", "Usage: sysop wis <nr>"))
	}
	deleted, err := m.deps.Store.DeletePost(context.Background(), m.boardID, nr)
	if err != nil {
		return m, m.out(m.tr("Verwijderen mislukt.", "Delete failed."))
	}
	if !deleted {
		if nr < 10000 {
			return m, m.out(fmt.Sprintf(m.tr("#%d is vaste content — alleen door bellers geplaatste berichten kunnen weg.", "#%d is fixed content — only messages posted by callers can be removed."), nr))
		}
		return m, m.out(fmt.Sprintf(m.tr("Geen bericht #%d in dit board.", "No message #%d in this board."), nr))
	}
	// Reprint the listing so the sysop sees the result.
	l, lerr := m.deps.Boards.Listing(context.Background(), m.boardID, m.viewer())
	out := fmt.Sprintf(m.tr("Bericht #%d verwijderd.\n", "Message #%d deleted.\n"), nr)
	if lerr == nil {
		out += "\n" + renderListing(l, m.lang())
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
		return m, m.out(m.tr("LLM staat uit — genereren kan niet (zet LLM_BASE_URL/MODEL/API_KEY).", "LLM is off — cannot generate (set LLM_BASE_URL/MODEL/API_KEY)."))
	}
	b := m.deps.Content.BoardByID(board)
	if b == nil {
		return m, m.out(m.tr("Gebruik: sysop gen <board> [n] — onbekend board.", "Usage: sysop gen <board> [n] — unknown board."))
	}
	if n < 1 {
		n = 1
	}
	if n > 10 {
		n = 10 // one LLM call, keep it bounded
	}
	base := m.deps.Content.Prompts["genposts"]
	lc, st := m.deps.LLM, m.deps.Store
	lang := m.lang() // captured before the async closure to avoid a data race
	boardID, boardName := b.ID, b.Name.Get(lang)
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
			// Player posts are single-language; store the sysop's active one.
			_, e := st.SavePost(ctx, &store.SavedMessage{
				BoardID:  boardID,
				Author:   author,
				Level:    0, // filler is board texture; review before raising
				Subject:  text.CleanLine(d.Subject.Get(lang)),
				Body:     text.Clean(d.Body.Get(lang)),
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
		m.out(fmt.Sprintf(m.tr("Concepten aanvragen bij de LLM voor %s (%d)… dit kan even duren.", "Requesting drafts from the LLM for %s (%d)… this may take a moment."), boardID, n)),
		gen)
}

// sysopPending lists the review queue across all boards.
func (m *Model) sysopPending() (tea.Model, tea.Cmd) {
	posts, err := m.deps.Store.PendingPosts(context.Background())
	if err != nil {
		return m, m.out(m.tr("Kon de wachtrij niet ophalen.", "Could not fetch the queue."))
	}
	if len(posts) == 0 {
		return m, m.out(m.tr("Geen concepten in de wachtrij.", "No drafts in the queue."))
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf(m.tr("CONCEPTEN OP REVIEW (%d)\n", "DRAFTS AWAITING REVIEW (%d)\n"), len(posts)))
	b.WriteString(strings.Repeat("-", 60) + "\n")
	for _, p := range posts {
		b.WriteString(fmt.Sprintf("  #%-6d %-10s %-12.12s %.32s\n", p.ID, p.BoardID, p.Author, p.Subject))
	}
	b.WriteString(m.tr("\nsysop ok <id> publiceert · sysop nee <id> verwerpt", "\nsysop ok <id> publishes · sysop nee <id> rejects"))
	return m, m.out(b.String())
}

// sysopReview publishes (ok) or discards (nee) a single draft by ID.
func (m *Model) sysopReview(arg string, publish bool) (tea.Model, tea.Cmd) {
	id, err := strconv.Atoi(arg)
	if err != nil {
		if publish {
			return m, m.out(m.tr("Gebruik: sysop ok <id>", "Usage: sysop ok <id>"))
		}
		return m, m.out(m.tr("Gebruik: sysop nee <id>", "Usage: sysop nee <id>"))
	}
	var ok bool
	if publish {
		ok, err = m.deps.Store.PublishPost(context.Background(), id)
	} else {
		ok, err = m.deps.Store.DeletePendingPost(context.Background(), id)
	}
	if err != nil {
		return m, m.out(m.tr("Actie mislukt.", "Action failed."))
	}
	if !ok {
		return m, m.out(fmt.Sprintf(m.tr("Geen concept #%d in de wachtrij.", "No draft #%d in the queue."), id))
	}
	if publish {
		return m, m.out(fmt.Sprintf(m.tr("#%d gepubliceerd.", "#%d published."), id))
	}
	return m, m.out(fmt.Sprintf(m.tr("#%d verworpen.", "#%d rejected."), id))
}
