package content

import "gopkg.in/yaml.v3"

// Language codes. Dutch is the source language and the fallback.
const (
	LangNL = "nl"
	LangEN = "en"
)

// NormalizeLang maps arbitrary input to a supported code, defaulting to NL.
func NormalizeLang(s string) string {
	if s == LangEN {
		return LangEN
	}
	return LangNL
}

// L is a localized string. In YAML it accepts EITHER a plain scalar (treated
// as the Dutch source text) OR a mapping {nl: ..., en: ...}. That keeps every
// existing monolingual YAML file valid unchanged: a bare string just becomes
// the NL text with no EN, and Get falls back to NL.
type L struct {
	NL string
	EN string
}

// UnmarshalYAML accepts a scalar (→ NL) or a {nl, en} mapping.
func (l *L) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		l.NL = value.Value
		return nil
	}
	var m struct {
		NL string `yaml:"nl"`
		EN string `yaml:"en"`
	}
	if err := value.Decode(&m); err != nil {
		return err
	}
	l.NL, l.EN = m.NL, m.EN
	return nil
}

// Get returns the text for lang, falling back to NL when the translation is
// missing (so English mode never renders blank).
func (l L) Get(lang string) string {
	if lang == LangEN && l.EN != "" {
		return l.EN
	}
	return l.NL
}

// String is the raw Dutch source (used by the content lint, which scans
// source text for cross-references regardless of display language).
func (l L) String() string { return l.NL }

// AllText returns every language's text joined, so the lint's cross-reference
// scan catches a leaked THIS reference in any translation, not just Dutch.
func (l L) AllText() string { return l.NL + "\n" + l.EN }
