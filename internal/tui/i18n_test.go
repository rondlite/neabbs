package tui

import (
	"testing"

	"github.com/rondlite/neabbs/internal/content"
)

func TestParseLangChoice(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"1", content.LangNL, true},
		{"2", content.LangEN, true},
		{"nl", content.LangNL, true},
		{"NL", content.LangNL, true},
		{" nederlands ", content.LangNL, true},
		{"dutch", content.LangNL, true},
		{"en", content.LangEN, true},
		{"English", content.LangEN, true},
		{"engels", content.LangEN, true},
		{"", content.LangNL, true}, // empty accepts the Dutch default
		{"3", "", false},
		{"deutsch", "", false},
		{"nl en", "", false},
	}
	for _, c := range cases {
		got, ok := parseLangChoice(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("parseLangChoice(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestNextAfterConnect(t *testing.T) {
	if got := nextAfterConnect(""); got != ritLang {
		t.Errorf("new caller (no handle): next = %v, want ritLang", got)
	}
	if got := nextAfterConnect("ron"); got != ritUsername {
		t.Errorf("returning caller: next = %v, want ritUsername (never re-asked)", got)
	}
}
