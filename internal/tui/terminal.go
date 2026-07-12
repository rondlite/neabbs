package tui

import (
	"context"
	"sort"
	"strings"
)

// This file gives THIS its terminal manners: a command history on the arrow
// keys and Tab completion. The public BBS stays menu-driven on purpose — the
// difference in feel between the two surfaces is the point.

// ─── history ───────────────────────────────────────────────────────────────

// history is a session-scoped command history. pos indexes lines; pos ==
// len(lines) means "on a fresh, empty line" (below the newest entry).
type history struct {
	lines []string
	pos   int
}

// push records a command. Blanks and immediate duplicates are dropped, as in
// any shell worth the name, and the cursor returns to the fresh line.
func (h *history) push(line string) {
	line = strings.TrimSpace(line)
	if line != "" && (len(h.lines) == 0 || h.lines[len(h.lines)-1] != line) {
		h.lines = append(h.lines, line)
	}
	h.pos = len(h.lines)
}

// prev walks back (arrow up), stopping at the oldest entry.
func (h *history) prev() string {
	if len(h.lines) == 0 {
		return ""
	}
	if h.pos > 0 {
		h.pos--
	}
	return h.lines[h.pos]
}

// next walks forward (arrow down); past the newest entry you land on a fresh
// empty line.
func (h *history) next() string {
	if h.pos >= len(h.lines) {
		return ""
	}
	h.pos++
	if h.pos >= len(h.lines) {
		return ""
	}
	return h.lines[h.pos]
}

// ─── completion ────────────────────────────────────────────────────────────

// completeSources is everything Tab is allowed to know about. Each slice is
// supplied by the caller from the SAME visibility-filtered lists the screen
// renders, which is what keeps completion from leaking:
//   - verbs:  only commands this player has already learned (see knownVerbs)
//   - hosts:  only what `scan` shows at their clearance
//   - files:  only what `ls` shows on the connected host
//   - boards: only boards visible at their level
type completeSources struct {
	verbs  []string
	hosts  []string
	files  []string
	boards []string
}

// argPool returns the completion pool for a verb's argument, or nil for verbs
// that take no completable argument.
func (s completeSources) argPool(verb string) []string {
	switch verb {
	case "connect", "route", "bounce":
		return s.hosts
	case "cat", "type", "mail", "spool":
		return s.files
	case "board":
		return s.boards
	}
	return nil
}

// completeLine applies Tab to line. It returns the (possibly) extended line
// and, when the match is ambiguous, the candidates to show the player.
//
// Shell semantics: a unique verb completes with a trailing space; an ambiguous
// match extends to the longest common prefix and lists the candidates; no
// match leaves the line untouched.
func completeLine(line string, src completeSources) (string, []string) {
	trimmed := strings.TrimLeft(line, " ")
	fields := strings.Fields(trimmed)
	endsInSpace := strings.HasSuffix(line, " ")

	// Completing the verb: nothing typed yet beyond the first word.
	if len(fields) <= 1 && !endsInSpace {
		prefix := ""
		if len(fields) == 1 {
			prefix = fields[0]
		}
		completion, cands, ok := match(prefix, src.verbs)
		if !ok {
			return line, nil
		}
		if len(cands) == 0 {
			return completion + " ", nil // unique: ready for its argument
		}
		return completion, cands
	}

	// Completing an argument: only the first one, and only for verbs that have
	// a pool. Anything else is left alone.
	pool := src.argPool(strings.ToLower(fields[0]))
	if pool == nil || len(fields) > 2 || (len(fields) == 2 && endsInSpace) {
		return line, nil
	}
	prefix := ""
	if len(fields) == 2 {
		prefix = fields[1]
	}
	completion, cands, ok := match(prefix, pool)
	if !ok {
		return line, nil
	}
	return fields[0] + " " + completion, cands
}

// match returns the completion for prefix against pool: the single match, or
// the longest common prefix of several (with those candidates listed). ok is
// false only when nothing matched — an empty completion with candidates is a
// real result (`cat <TAB>` on files that share no prefix still lists them).
func match(prefix string, pool []string) (completion string, cands []string, ok bool) {
	var hits []string
	for _, c := range pool {
		if strings.HasPrefix(strings.ToLower(c), strings.ToLower(prefix)) {
			hits = append(hits, c)
		}
	}
	switch len(hits) {
	case 0:
		return "", nil, false
	case 1:
		return hits[0], nil, true
	}
	sort.Strings(hits)
	return commonPrefix(hits), hits, true
}

// commonPrefix is the longest prefix shared by every string in ss.
func commonPrefix(ss []string) string {
	p := ss[0]
	for _, s := range ss[1:] {
		for !strings.HasPrefix(strings.ToLower(s), strings.ToLower(p)) {
			p = p[:len(p)-1]
			if p == "" {
				return ""
			}
		}
	}
	return p
}

// ─── learned verbs ─────────────────────────────────────────────────────────

// baseVerbs are the commands `help` already lists, so completing them reveals
// nothing. Every other THIS command has to be discovered in play — Tab only
// starts completing it once the player has used it (see learnVerb).
var baseVerbs = []string{"help", "scan", "connect", "boards", "read", "exit"}

// thisVerbs is every command thisLine dispatches, aliases included. It is the
// set a typed word must belong to before it counts as "learned" — the pool Tab
// draws from is the player's learned subset, never this whole table.
var thisVerbs = map[string]bool{
	"help": true, "?": true, "scan": true, "connect": true, "disconnect": true,
	"ls": true, "dir": true, "cat": true, "type": true, "mail": true,
	"spool": true, "netstat": true, "ps": true, "route": true, "bounce": true,
	"crack": true, "kraak": true, "wipe": true, "wis": true, "talk": true,
	"praat": true, "taal": true, "language": true, "lang": true, "who": true,
	"wie": true, "ghosts": true, "spoken": true, "roem": true, "toplijst": true,
	"wall": true, "fluister": true, "whisper": true, "boards": true,
	"board": true, "read": true, "lees": true, "post": true, "reply": true,
	"status": true, "exit": true, "terug": true, "logout": true,
}

// learnVerb records a command the player has actually used, making it
// completable for the rest of the session. Reaching here means the word was a
// real command, so Tab never teaches anything the player didn't already know.
func (m *Model) learnVerb(verb string) {
	if !thisVerbs[verb] {
		return
	}
	if m.knownVerbs == nil {
		m.knownVerbs = map[string]bool{}
	}
	m.knownVerbs[verb] = true
}

// thisSources gathers what Tab may complete right now, reading the same
// filtered views the player can already see on screen.
func (m *Model) thisSources() completeSources {
	verbs := append([]string{}, baseVerbs...)
	for v := range m.knownVerbs {
		if !contains(verbs, v) {
			verbs = append(verbs, v)
		}
	}
	sort.Strings(verbs)

	src := completeSources{verbs: verbs}
	for _, h := range m.deps.World.Scan(m.viewer(), m.hasFlag) {
		src.hosts = append(src.hosts, h.Address)
	}
	for _, b := range m.deps.Boards.VisibleBoards(m.viewer()) {
		src.boards = append(src.boards, b.ID)
	}
	// Files only exist in the context of a connected host, and only the ones
	// `ls` would print: hidden entries stay hidden.
	if h := m.currentHost(); h != nil {
		if rows, err := m.deps.World.Ls(context.Background(), h, m.viewer()); err == nil {
			for _, r := range rows {
				if !r.Hidden {
					src.files = append(src.files, r.Name)
				}
			}
		}
	}
	return src
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
