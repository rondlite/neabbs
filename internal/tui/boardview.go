package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/rondlite/neabbs/internal/board"
	"github.com/rondlite/neabbs/internal/content"
)

var redactStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

// headerStyle bolds section titles without setting a colour, so it reads
// correctly in both the amber public BBS and the green THIS mode.
var headerStyle = lipgloss.NewStyle().Bold(true)

// renderBoardList formats the visible boards for the `boards` command.
func renderBoardList(boards []*content.Board, lang string) string {
	if len(boards) == 0 {
		return trl(lang, "Geen boards beschikbaar.", "No boards available.")
	}
	var b strings.Builder
	b.WriteString(headerStyle.Render("BOARDS") + "\n")
	for _, bd := range boards {
		b.WriteString(fmt.Sprintf("  %-12s %s\n", bd.ID, bd.Name.Get(lang)))
	}
	b.WriteString(trl(lang, "Gebruik: board <id>", "Usage: board <id>"))
	return b.String()
}

// renderListing formats one board's clearance-filtered message list.
// 80-column friendly. Redacted rows carry the [THIS-N] clearance tag.
func renderListing(l *board.Listing, lang string) string {
	var b strings.Builder
	b.WriteString(headerStyle.Render(fmt.Sprintf("%s — %s", strings.ToUpper(l.Board.ID), l.Board.Name.Get(lang))) + "\n")
	b.WriteString(strings.Repeat("─", 78) + "\n")
	if len(l.Rows) == 0 {
		b.WriteString(trl(lang, "(geen berichten)", "(no messages)") + "\n")
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
		b.WriteString(fmt.Sprintf(trl(lang, "\n%d bericht(en) verborgen boven jouw niveau\n", "\n%d message(s) hidden above your clearance\n"), l.HiddenCount))
	}
	b.WriteString(trl(lang, "\nGebruik: read <nr>", "\nUsage: read <nr>"))
	if l.Board.Writable {
		b.WriteString(", post, reply <nr>")
	}
	return b.String()
}

// renderFileList formats the public file area (bestanden).
func renderFileList(files []content.File, lang string) string {
	var b strings.Builder
	b.WriteString(headerStyle.Render(trl(lang, "BESTANDEN", "FILES")) + "\n")
	b.WriteString(strings.Repeat("─", 70) + "\n")
	if len(files) == 0 {
		b.WriteString(trl(lang, "(leeg — de sysop is nog aan het inpakken)", "(empty — the sysop is still packing)") + "\n")
	}
	for i, f := range files {
		size := f.Size
		if size == "" {
			size = fmt.Sprintf("%dK", max(1, len(f.Body.Get(lang))/1024))
		}
		b.WriteString(fmt.Sprintf("%3d  %-20.20s %6s  %-8s %s\n", i+1, f.Name, size, f.Date, f.Desc.Get(lang)))
	}
	b.WriteString(trl(lang, "\nGebruik: lees <nr>, terug", "\nUsage: read <nr>, back"))
	return b.String()
}

// renderFile formats one file for the pager.
func renderFile(f *content.File, lang string) string {
	var b strings.Builder
	b.WriteString(headerStyle.Render(fmt.Sprintf("═══ %s ═══", f.Name)) + "\n\n")
	b.WriteString(strings.TrimRight(f.Body.Get(lang), "\n"))
	return b.String()
}

// renderMessage formats one readable message.
func renderMessage(boardID string, m *board.Msg, lang string) string {
	var b strings.Builder
	b.WriteString(headerStyle.Render(fmt.Sprintf(trl(lang, "Bericht #%d op %s", "Message #%d on %s"), m.ID, strings.ToUpper(boardID))) + "\n")
	b.WriteString(fmt.Sprintf(trl(lang, "Van      : %s\n", "From     : %s\n"), m.Author))
	if m.Date != "" {
		b.WriteString(fmt.Sprintf(trl(lang, "Datum    : %s\n", "Date     : %s\n"), m.Date))
	}
	if m.ReplyTo != 0 {
		b.WriteString(fmt.Sprintf(trl(lang, "Antwoord : op #%d\n", "Reply    : to #%d\n"), m.ReplyTo))
	}
	b.WriteString(fmt.Sprintf(trl(lang, "Onderwerp: %s\n", "Subject  : %s\n"), m.Subject))
	b.WriteString(strings.Repeat("─", 78) + "\n")
	b.WriteString(strings.TrimRight(m.Body, "\n"))
	return b.String()
}
