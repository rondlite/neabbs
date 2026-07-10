package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/rondlite/neabbs/internal/board"
	"github.com/rondlite/neabbs/internal/content"
)

var redactStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

// renderBoardList formats the visible boards for the `boards` command.
func renderBoardList(boards []*content.Board) string {
	if len(boards) == 0 {
		return "Geen boards beschikbaar."
	}
	var b strings.Builder
	b.WriteString("BOARDS\n")
	for _, bd := range boards {
		b.WriteString(fmt.Sprintf("  %-12s %s\n", bd.ID, bd.Name))
	}
	b.WriteString("Gebruik: board <id>")
	return b.String()
}

// renderListing formats one board's clearance-filtered message list.
// 80-column friendly. Redacted rows carry the [THIS-N] clearance tag.
func renderListing(l *board.Listing) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s — %s\n", strings.ToUpper(l.Board.ID), l.Board.Name))
	b.WriteString(strings.Repeat("-", 78) + "\n")
	if len(l.Rows) == 0 {
		b.WriteString("(geen berichten)\n")
	}
	for _, r := range l.Rows {
		line := fmt.Sprintf("%5d  %-12.12s  %-44.44s", r.ID, r.Author, r.Subject)
		if r.Redacted {
			line += fmt.Sprintf("  [THIS-%d]", r.Level)
			line = redactStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}
	if l.HiddenCount > 0 {
		b.WriteString(fmt.Sprintf("\n%d bericht(en) verborgen boven jouw niveau\n", l.HiddenCount))
	}
	b.WriteString("\nGebruik: read <nr>")
	if l.Board.Writable {
		b.WriteString(", post, reply <nr>")
	}
	return b.String()
}

// renderFileList formats the public file area (bestanden).
func renderFileList(files []content.File) string {
	var b strings.Builder
	b.WriteString("BESTANDEN\n")
	b.WriteString(strings.Repeat("-", 70) + "\n")
	if len(files) == 0 {
		b.WriteString("(leeg — de sysop is nog aan het inpakken)\n")
	}
	for i, f := range files {
		size := f.Size
		if size == "" {
			size = fmt.Sprintf("%dK", max(1, len(f.Body)/1024))
		}
		b.WriteString(fmt.Sprintf("%3d  %-20.20s %6s  %-8s %s\n", i+1, f.Name, size, f.Date, f.Desc))
	}
	b.WriteString("\nGebruik: lees <nr>, terug")
	return b.String()
}

// renderFile formats one file for the pager.
func renderFile(f *content.File) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("=== %s ===\n\n", f.Name))
	b.WriteString(strings.TrimRight(f.Body, "\n"))
	return b.String()
}

// renderMessage formats one readable message.
func renderMessage(boardID string, m *board.Msg) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Bericht #%d op %s\n", m.ID, strings.ToUpper(boardID)))
	b.WriteString(fmt.Sprintf("Van      : %s\n", m.Author))
	if m.Date != "" {
		b.WriteString(fmt.Sprintf("Datum    : %s\n", m.Date))
	}
	if m.ReplyTo != 0 {
		b.WriteString(fmt.Sprintf("Antwoord : op #%d\n", m.ReplyTo))
	}
	b.WriteString(fmt.Sprintf("Onderwerp: %s\n", m.Subject))
	b.WriteString(strings.Repeat("-", 78) + "\n")
	b.WriteString(strings.TrimRight(m.Body, "\n"))
	return b.String()
}
