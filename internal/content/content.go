// Package content loads all game content from YAML at startup.
// Content is data, never code.
package content

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Area distinguishes the public BBS from THIS.
const (
	AreaPublic = "public"
	AreaThis   = "this"
)

// MaxYAMLMessageID is the exclusive upper bound for YAML message IDs;
// player posts are assigned IDs from 10000 up.
const MaxYAMLMessageID = 10000

// Board is one message board as authored in content/boards/*.yaml.
type Board struct {
	ID       string    `yaml:"id"`
	Name     string    `yaml:"name"`
	Area     string    `yaml:"area"`
	MinLevel int       `yaml:"min_level"`
	Writable bool      `yaml:"writable"`
	Messages []Message `yaml:"messages"`
}

// Message is one seeded board message.
type Message struct {
	ID             int    `yaml:"id"`
	Author         string `yaml:"author"`
	Level          int    `yaml:"level"`
	Subject        string `yaml:"subject"`
	Body           string `yaml:"body"`
	SubjectVisible *bool  `yaml:"subject_visible"` // default true
	AuthorVisible  *bool  `yaml:"author_visible"`  // default true
	Hidden         bool   `yaml:"hidden"`          // above-level: no stub, count only
	GrantsFlag     string `yaml:"grants_flag"`
	ReplyTo        int    `yaml:"reply_to"`
	Date           string `yaml:"date"` // display date, freeform period text
}

// SubjectShown reports whether the subject may appear in a redacted stub.
func (m *Message) SubjectShown() bool { return m.SubjectVisible == nil || *m.SubjectVisible }

// AuthorShown reports whether the author may appear in a redacted stub.
func (m *Message) AuthorShown() bool { return m.AuthorVisible == nil || *m.AuthorVisible }

// Set is all loaded content.
type Set struct {
	Boards []Board // sorted: public first, then by min_level, then id
}

// BoardByID returns the board or nil.
func (s *Set) BoardByID(id string) *Board {
	for i := range s.Boards {
		if s.Boards[i].ID == id {
			return &s.Boards[i]
		}
	}
	return nil
}

// Load reads and lints all content under dir.
func Load(dir string) (*Set, error) {
	set := &Set{}
	boardsDir := filepath.Join(dir, "boards")
	entries, err := os.ReadDir(boardsDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read %s: %w", boardsDir, err)
	}
	for _, e := range entries {
		if e.IsDir() || (!strings.HasSuffix(e.Name(), ".yaml") && !strings.HasSuffix(e.Name(), ".yml")) {
			continue
		}
		path := filepath.Join(boardsDir, e.Name())
		buf, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var b Board
		if err := yaml.Unmarshal(buf, &b); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		set.Boards = append(set.Boards, b)
	}
	sort.Slice(set.Boards, func(i, j int) bool {
		a, b := &set.Boards[i], &set.Boards[j]
		if a.Area != b.Area {
			return a.Area == AreaPublic
		}
		if a.MinLevel != b.MinLevel {
			return a.MinLevel < b.MinLevel
		}
		return a.ID < b.ID
	})
	if err := Lint(set); err != nil {
		return nil, err
	}
	return set, nil
}

// Lint fails fast on content errors. Grows with each content type.
func Lint(s *Set) error {
	var errs []string
	fail := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }

	boardIDs := map[string]bool{}
	for i := range s.Boards {
		b := &s.Boards[i]
		if b.ID == "" {
			fail("board #%d: missing id", i)
			continue
		}
		if boardIDs[b.ID] {
			fail("board %s: duplicate id", b.ID)
		}
		boardIDs[b.ID] = true
		if b.Area != AreaPublic && b.Area != AreaThis {
			fail("board %s: area %q must be %q or %q", b.ID, b.Area, AreaPublic, AreaThis)
		}
		if b.MinLevel < 0 || b.MinLevel > 9 {
			fail("board %s: min_level %d out of range", b.ID, b.MinLevel)
		}
		msgIDs := map[int]bool{}
		for j := range b.Messages {
			m := &b.Messages[j]
			if m.ID <= 0 || m.ID >= MaxYAMLMessageID {
				fail("board %s msg #%d: id %d out of range 1-%d", b.ID, j, m.ID, MaxYAMLMessageID-1)
			}
			if msgIDs[m.ID] {
				fail("board %s: duplicate message id %d", b.ID, m.ID)
			}
			msgIDs[m.ID] = true
			if m.Level < 0 || m.Level > 9 {
				fail("board %s msg %d: level %d out of range", b.ID, m.ID, m.Level)
			}
			if b.Area == AreaPublic && m.Level != 0 {
				fail("board %s msg %d: public-area messages must be level 0", b.ID, m.ID)
			}
			if m.ReplyTo != 0 && !msgIDs[m.ReplyTo] {
				fail("board %s msg %d: reply_to %d does not reference an earlier message", b.ID, m.ID, m.ReplyTo)
			}
			if m.Author == "" {
				fail("board %s msg %d: missing author", b.ID, m.ID)
			}
		}
		// Public content must never reference THIS by name.
		if b.Area == AreaPublic {
			for j := range b.Messages {
				m := &b.Messages[j]
				lower := strings.ToLower(m.Subject + "\n" + m.Body)
				for _, other := range s.Boards {
					if other.Area == AreaThis && strings.Contains(lower, strings.ToLower(other.ID)) {
						fail("board %s msg %d: public content references THIS board id %q", b.ID, m.ID, other.ID)
					}
				}
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("content lint:\n  %s", strings.Join(errs, "\n  "))
	}
	return nil
}
