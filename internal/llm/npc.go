package llm

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/rondlite/neabbs/internal/content"
)

// Turn caps per the spec.
const (
	MaxTurnsPerSession = 20
	MaxTurnsPerDay     = 60
)

// BuildSystemPrompt assembles an NPC's system prompt from a base template,
// the persona, and the facts the NPC may reveal for flags the player holds.
// base may be empty (the persona alone is enough).
func BuildSystemPrompt(base string, npc *content.NPC, hasFlag func(string) bool, lang string) string {
	var b strings.Builder
	if base != "" {
		b.WriteString(strings.TrimRight(base, "\n"))
		b.WriteString("\n\n")
	}
	fmt.Fprintf(&b, "Je bent %s.\n%s\n", npc.Name, strings.TrimRight(npc.Persona.Get(lang), "\n"))

	// Only reveal facts gated by flags the player actually holds. Sorted for
	// deterministic prompts.
	var facts []string
	keys := make([]string, 0, len(npc.KnowsFlags))
	for f := range npc.KnowsFlags {
		keys = append(keys, f)
	}
	sort.Strings(keys)
	for _, f := range keys {
		if hasFlag(f) {
			facts = append(facts, "- "+npc.KnowsFlags[f].Get(lang))
		}
	}
	if len(facts) > 0 {
		b.WriteString("\nJe mag deze dingen prijsgeven als het gesprek erom vraagt:\n")
		b.WriteString(strings.Join(facts, "\n"))
		b.WriteString("\n")
	} else {
		b.WriteString("\nDe beller heeft nog niets waarmee jij hem verder kunt helpen. Blijf vaag.\n")
	}
	if lang == content.LangEN {
		b.WriteString("\nAntwoord kort, in het ENGELS, in karakter. Nooit meer dan een paar zinnen.")
	} else {
		b.WriteString("\nAntwoord kort, in het Nederlands, in karakter. Nooit meer dan een paar zinnen.")
	}
	return b.String()
}

// Reply runs one NPC turn, or returns the NPC's canned fallback (and false)
// when the LLM is unavailable or errors — never blocking, never fatal.
func (c *Client) Reply(ctx context.Context, system string, history []Message, user, fallback string) (string, bool) {
	if !c.Enabled() {
		return fallback, false
	}
	msgs := make([]Message, 0, len(history)+2)
	msgs = append(msgs, Message{Role: "system", Content: system})
	msgs = append(msgs, history...)
	msgs = append(msgs, Message{Role: "user", Content: user})
	out, err := c.Chat(ctx, msgs)
	if err != nil || strings.TrimSpace(out) == "" {
		return fallback, false
	}
	return strings.TrimSpace(out), true
}
