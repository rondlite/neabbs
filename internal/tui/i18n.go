package tui

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rondlite/neabbs/internal/content"
)

// lang is the player's normalized display language ("nl"/"en").
func (m *Model) lang() string { return content.NormalizeLang(m.deps.Player.Lang) }

// toggleLang switches the player's display language (persisted). `taal` with
// no argument flips it; `taal en` / `taal nl` set it explicitly. Confirmation
// is shown in the newly-selected language.
func (m *Model) toggleLang(arg string) (tea.Model, tea.Cmd) {
	var target string
	switch strings.ToLower(strings.TrimSpace(arg)) {
	case "":
		if m.lang() == content.LangEN {
			target = content.LangNL
		} else {
			target = content.LangEN
		}
	case "nl", "dutch", "nederlands":
		target = content.LangNL
	case "en", "english", "engels":
		target = content.LangEN
	default:
		return m, m.out(m.tr("gebruik: taal nl|en", "usage: language nl|en"))
	}
	if err := m.deps.Store.SetLang(context.Background(), m.deps.Player.Fingerprint, target); err != nil {
		return m, m.out(m.tr("kon taal niet opslaan.", "could not save language."))
	}
	m.deps.Player.Lang = target
	m.printer.lang = target
	if target == content.LangEN {
		return m, m.out("Language set to English. Type 'language nl' to switch back.")
	}
	return m, m.out("Taal ingesteld op Nederlands. Tik 'taal en' voor Engels.")
}

// tr picks the Dutch or English variant of a UI string for the current
// player. English falls back to Dutch when a translation isn't supplied yet,
// so partial translation never renders blank.
func (m *Model) tr(nl, en string) string {
	return trl(m.lang(), nl, en)
}

// trl is the lang-parameterized form for free render helpers that receive a
// language string instead of the Model. English falls back to Dutch when a
// translation isn't supplied yet.
func trl(lang, nl, en string) string {
	if content.NormalizeLang(lang) == content.LangEN && en != "" {
		return en
	}
	return nl
}
