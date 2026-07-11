package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
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
