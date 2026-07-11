package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Amber-family palette for the public BBS (amber-on-black, per spec).
var (
	amberBright = lipgloss.NewStyle().Foreground(lipgloss.Color("220")) // headings / accents
	amberDeep   = lipgloss.NewStyle().Foreground(lipgloss.Color("208")) // logo shadow
	menuBorder  = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	menuHot     = lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
)

// logoArt is the NEABBS wordmark (ANSI Shadow block font). Rendered in amber;
// falls back to a plain string if the terminal is narrow.
const logoArt = `███╗   ██╗███████╗ █████╗ ██████╗ ██████╗ ███████╗
████╗  ██║██╔════╝██╔══██╗██╔══██╗██╔══██╗██╔════╝
██╔██╗ ██║█████╗  ███████║██████╔╝██████╔╝███████╗
██║╚██╗██║██╔══╝  ██╔══██║██╔══██╗██╔══██╗╚════██║
██║ ╚████║███████╗██║  ██║██████╔╝██████╔╝███████║
╚═╝  ╚═══╝╚══════╝╚═╝  ╚═╝╚═════╝ ╚═════╝ ╚══════╝`

// logo returns the coloured wordmark, or a compact fallback on narrow ttys.
// Content authors can override it with content/login-banner.txt.
func (m *Model) logo() string {
	if custom := m.deps.Content.LoginBanner; custom != "" {
		return amber.Render(strings.TrimRight(custom, "\n"))
	}
	if m.width > 0 && m.width < 52 {
		return amberBright.Render("= NEABBS =")
	}
	// Two-tone: the block glyphs amber, the ╚═╝ shadow row a shade deeper.
	lines := strings.Split(logoArt, "\n")
	for i, ln := range lines {
		if i == len(lines)-1 {
			lines[i] = amberDeep.Render(ln)
		} else {
			lines[i] = amber.Render(ln)
		}
	}
	return strings.Join(lines, "\n")
}

// rule draws a full-width single rule in amber (public BBS chrome).
func rule(width int) string {
	if width <= 0 || width > 78 {
		width = 62
	}
	return menuBorder.Render(strings.Repeat("─", width))
}
