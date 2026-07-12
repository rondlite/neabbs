package llm

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/rondlite/neabbs/internal/content"
)

// Draft is one LLM-generated filler post, before any review. Subject and Body
// are localized: the model is asked for both Dutch and English, and each
// field parses from a {nl, en} mapping (or a bare scalar, treated as NL).
type Draft struct {
	Author  string    `yaml:"author"`
	Level   int       `yaml:"level"`
	Subject content.L `yaml:"subject"`
	Body    content.L `yaml:"body"`
}

// GenpostSystemPrompt builds the shared system prompt for board filler drafts,
// used by both the offline `genposts` CLI and the in-game sysop generator so
// the two never drift.
func GenpostSystemPrompt(base, boardID, boardName string, level, count int) string {
	if strings.TrimSpace(base) == "" {
		base = "Je schrijft sfeervolle, korte filler-berichten voor een Nederlands 1980s BBS. Periode-echt, geen anachronismen."
	}
	return fmt.Sprintf(`%s

Board: %s (%s)
Niveau: THIS-%d
Schrijf %d korte berichten. Schrijf elk bericht in het Nederlands EN een
getrouwe Engelse vertaling met dezelfde betekenis en toon (periode-echt, geen
anachronismen; eigennamen als Max Keizer, Amsterdam, PTT blijven staan).
Antwoord UITSLUITEND als YAML in dit formaat, niets eromheen:

messages:
  - author: "handle"
    level: %d
    subject:
      nl: "onderwerp"
      en: "subject"
    body:
      nl: |
        tekst
      en: |
        text
`, strings.TrimRight(base, "\n"), boardID, boardName, level, count, level)
}

// ParseDrafts extracts the drafts from an LLM YAML reply, tolerating stray
// prose or ```yaml fences around the block.
func ParseDrafts(out string) ([]Draft, error) {
	out = strings.TrimSpace(out)
	// Strip a leading/trailing markdown code fence if present.
	if strings.HasPrefix(out, "```") {
		if i := strings.IndexByte(out, '\n'); i >= 0 {
			out = out[i+1:]
		}
		out = strings.TrimSuffix(strings.TrimRight(out, "\n"), "```")
	}
	// Trim anything before the first "messages:" key.
	if i := strings.Index(out, "messages:"); i > 0 {
		out = out[i:]
	}
	var doc struct {
		Messages []Draft `yaml:"messages"`
	}
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		return nil, fmt.Errorf("parse drafts: %w", err)
	}
	return doc.Messages, nil
}
