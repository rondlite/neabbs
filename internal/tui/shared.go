package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rondlite/neabbs/internal/text"
)

// renderLeaderboard is the notoriety board: THIS members ranked by clearance
// then by hosts cracked. The shared world made visible as a scoreboard.
func (m *Model) renderLeaderboard() string {
	rows, err := m.deps.Store.Leaderboard(context.Background(), 15)
	if err != nil {
		return "roem: kon de lijst niet ophalen."
	}
	if len(rows) == 0 {
		return "roem: nog niemand op het bord. wees de eerste."
	}
	var b strings.Builder
	b.WriteString("ROEM — de beruchtste operators\n")
	b.WriteString(strings.Repeat("-", 44) + "\n")
	b.WriteString(fmt.Sprintf("  %-3s %-16s %-7s %s\n", "#", "HANDLE", "NIVEAU", "HOSTS"))
	for i, r := range rows {
		mark := ""
		if r.Handle == m.deps.Player.Handle {
			mark = " «"
		}
		b.WriteString(fmt.Sprintf("  %-3d %-16s THIS-%-2d %d%s\n", i+1, r.Handle, r.Level, r.Breaches, mark))
	}
	return b.String()
}

// whisper sends a private line to a named operator who is currently inside
// THIS. Nothing reaches the public BBS, and offline/handle-unknown targets
// answer identically — presence is never confirmed to non-members elsewhere.
func (m *Model) whisper(rest string) (tea.Model, tea.Cmd) {
	fields := strings.SplitN(strings.TrimSpace(rest), " ", 2)
	if len(fields) < 2 || strings.TrimSpace(fields[1]) == "" {
		return m, m.out("gebruik: fluister <handle> <tekst>")
	}
	target := fields[0]
	line := text.CleanLine(fields[1])
	if line == "" {
		return m, m.out("gebruik: fluister <handle> <tekst>")
	}
	if strings.EqualFold(target, m.deps.Player.Handle) {
		return m, m.out("fluister: tegen jezelf praten telt niet.")
	}
	delivered := false
	for _, s := range m.deps.Registry.All() {
		handle, _, inThis := s.Snapshot()
		if inThis && strings.EqualFold(handle, target) {
			s.SendMsg(WhisperMsg{From: m.deps.Player.Handle, Line: line})
			delivered = true
		}
	}
	if !delivered {
		return m, m.out("fluister: die operator is nu niet in het systeem.")
	}
	return m, m.out(green.Render(fmt.Sprintf("» aan %s: %s", target, line)))
}

// renderGhosts lists the other operators currently inside THIS — the living
// presence of the shared world, distinct from the persistent breach trail.
func (m *Model) renderGhosts() string {
	var others []string
	for _, s := range m.deps.Registry.All() {
		if s == m.deps.Sess {
			continue
		}
		handle, area, inThis := s.Snapshot()
		if !inThis || handle == "" {
			continue
		}
		where := area
		if where == "" {
			where = "ergens in het systeem"
		}
		others = append(others, fmt.Sprintf("  %-16s %s", handle, where))
	}
	if len(others) == 0 {
		return "je bent nu de enige die binnen is. het is stil op de lijnen."
	}
	return "OPERATORS IN HET SYSTEEM\n" + strings.Repeat("-", 44) + "\n" +
		strings.Join(others, "\n") + "\n(fluister <handle> <tekst> om er een aan te spreken)"
}
