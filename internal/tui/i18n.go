package tui

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rondlite/neabbs/internal/content"
)

// lang is the player's normalized display language ("nl"/"en").
func (m *Model) lang() string { return content.NormalizeLang(m.deps.Player.Lang) }

// setLang persists the display language and syncs the in-memory copies the
// renderer and the pager each hold.
func (m *Model) setLang(target string) error {
	if err := m.deps.Store.SetLang(context.Background(), m.deps.Player.Fingerprint, target); err != nil {
		return err
	}
	m.deps.Player.Lang = target
	m.printer.lang = target
	return nil
}

// parseLangChoice reads an answer to the first-login language prompt. It
// accepts the menu numbers, the language codes and their spelled-out names in
// either language. An empty answer takes the Dutch default: this is a Dutch
// board, and the caller can still switch from the menu afterwards.
func parseLangChoice(s string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "1", "nl", "dutch", "nederlands":
		return content.LangNL, true
	case "2", "en", "english", "engels":
		return content.LangEN, true
	}
	return "", false
}

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
	if err := m.setLang(target); err != nil {
		return m, m.out(m.tr("kon taal niet opslaan.", "could not save language."))
	}
	// The prompt is part of the language too: refresh the one on screen.
	switch m.state {
	case stateMenu:
		m.input.Prompt = m.tr("Keuze: ", "Choice: ")
	case stateFiles:
		m.input.Prompt = m.tr("Bestand> ", "File> ")
	}
	confirm := "Taal ingesteld op Nederlands. Tik 'taal en' voor Engels."
	if target == content.LangEN {
		confirm = "Language set to English. Type 'language nl' to switch back."
	}
	// Re-draw the menu in the new language: leaving the old-language menu on
	// screen is the confusing half-state this whole feature exists to avoid.
	if m.state == stateMenu {
		return m, tea.Batch(m.out(confirm), m.print(m.renderMenu()))
	}
	return m, m.out(confirm)
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
