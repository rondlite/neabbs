// Package board is the level-gated board engine — the game's signature
// mechanic. Every board (public and THIS) runs on this engine; public boards
// simply operate with everything at level 0 so redaction never triggers.
package board

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/rondlite/neabbs/internal/content"
	"github.com/rondlite/neabbs/internal/store"
	"github.com/rondlite/neabbs/internal/text"
)

var (
	// ErrNoBoard is returned for boards that don't exist — and, identically,
	// for boards the viewer is not allowed to know exist.
	ErrNoBoard = errors.New("board: no such board")
	// ErrNoMessage is returned for message IDs that don't exist on the board.
	ErrNoMessage = errors.New("board: no such message")
	// ErrNotWritable is returned when posting to a read-only board.
	ErrNotWritable = errors.New("board: board is read-only")
	// ErrBadPost is returned for empty/invalid post content.
	ErrBadPost = errors.New("board: empty subject or body")
)

// ErrClearance means the message exists but needs a higher THIS level.
// Locked things respond specifically: the UI names the required clearance.
type ErrClearance struct{ Need int }

func (e ErrClearance) Error() string { return fmt.Sprintf("board: THIS-%d required", e.Need) }

// Viewer is the clearance identity used for filtering.
type Viewer struct {
	Fingerprint string
	Handle      string
	ThisMember  bool
	Level       int
}

// Msg is the unified view of a message (YAML seed or player post).
type Msg struct {
	ID           int
	Author       string
	Level        int
	Subject      string
	Body         string
	SubjectShown bool // may the subject appear in a redacted stub?
	AuthorShown  bool // may the author appear in a redacted stub?
	Hidden       bool // above-level: fully invisible, counted not listed
	GrantsFlag   string
	ReplyTo      int
	Date         string
}

// Row is one line in a board listing.
type Row struct {
	ID       int
	Author   string // already blanked when blocked
	Subject  string // already blanked when blocked
	Level    int
	Redacted bool // true → render [THIS-N] tag
	Date     string
}

// Listing is a clearance-filtered board view.
type Listing struct {
	Board       *content.Board
	Rows        []Row
	HiddenCount int // fully hidden messages above the viewer's level
}

// Engine merges YAML seed content with player posts and applies clearance.
type Engine struct {
	content *content.Set
	store   store.Store
}

// NewEngine builds the engine.
func NewEngine(c *content.Set, s store.Store) *Engine {
	return &Engine{content: c, store: s}
}

// effectiveLevel is the viewer's level within a board's area: in the public
// area everyone is level 0 (and everything is level 0), in THIS it is the
// member's clearance.
func effectiveLevel(b *content.Board, v Viewer) int {
	if b.Area == content.AreaPublic {
		return 0
	}
	return v.Level
}

// boardVisible reports whether the viewer may know this board exists.
func boardVisible(b *content.Board, v Viewer) bool {
	if b.Area == content.AreaPublic {
		return true
	}
	return v.ThisMember && b.MinLevel <= v.Level
}

// VisibleBoards lists boards the viewer may see, in content order.
// Boards above the viewer's level are entirely absent.
func (e *Engine) VisibleBoards(v Viewer) []*content.Board {
	var out []*content.Board
	for i := range e.content.Boards {
		b := &e.content.Boards[i]
		if boardVisible(b, v) {
			out = append(out, b)
		}
	}
	return out
}

// visibleBoard resolves a board id, returning ErrNoBoard identically for
// nonexistent boards and boards above the viewer's clearance.
func (e *Engine) visibleBoard(id string, v Viewer) (*content.Board, error) {
	b := e.content.BoardByID(id)
	if b == nil || !boardVisible(b, v) {
		return nil, ErrNoBoard
	}
	return b, nil
}

// VisibleBoardByID resolves a board id, or nil if it doesn't exist or the
// viewer may not know it exists.
func (e *Engine) VisibleBoardByID(id string, v Viewer) *content.Board {
	b, err := e.visibleBoard(id, v)
	if err != nil {
		return nil
	}
	return b
}

// Messages returns all messages on a board (seed + player posts), ID order.
// No clearance filtering here — callers filter.
func (e *Engine) Messages(ctx context.Context, b *content.Board) ([]Msg, error) {
	msgs := make([]Msg, 0, len(b.Messages))
	for i := range b.Messages {
		m := &b.Messages[i]
		msgs = append(msgs, Msg{
			ID:           m.ID,
			Author:       m.Author,
			Level:        m.Level,
			Subject:      m.Subject,
			Body:         m.Body,
			SubjectShown: m.SubjectShown(),
			AuthorShown:  m.AuthorShown(),
			Hidden:       m.Hidden,
			GrantsFlag:   m.GrantsFlag,
			ReplyTo:      m.ReplyTo,
			Date:         m.Date,
		})
	}
	posts, err := e.store.PostsForBoard(ctx, b.ID)
	if err != nil {
		return nil, err
	}
	for _, p := range posts {
		msgs = append(msgs, Msg{
			ID:           p.ID,
			Author:       p.Author,
			Level:        p.Level,
			Subject:      p.Subject,
			Body:         p.Body,
			SubjectShown: true,
			AuthorShown:  true,
			ReplyTo:      p.ReplyTo,
			Date:         p.PostedAt.Format("02-01-06"),
		})
	}
	sort.Slice(msgs, func(i, j int) bool { return msgs[i].ID < msgs[j].ID })
	return msgs, nil
}

const blocked = "████████"

// Listing builds the clearance-filtered view of a board: readable messages
// normally, above-level ones as redacted stubs, hidden ones counted only.
func (e *Engine) Listing(ctx context.Context, boardID string, v Viewer) (*Listing, error) {
	b, err := e.visibleBoard(boardID, v)
	if err != nil {
		return nil, err
	}
	msgs, err := e.Messages(ctx, b)
	if err != nil {
		return nil, err
	}
	lvl := effectiveLevel(b, v)
	l := &Listing{Board: b}
	for _, m := range msgs {
		if m.Level <= lvl {
			l.Rows = append(l.Rows, Row{
				ID: m.ID, Author: m.Author, Subject: m.Subject, Level: m.Level, Date: m.Date,
			})
			continue
		}
		if m.Hidden {
			l.HiddenCount++
			continue
		}
		r := Row{ID: m.ID, Level: m.Level, Redacted: true, Date: m.Date,
			Author: blocked, Subject: blocked}
		if m.SubjectShown {
			r.Subject = m.Subject
		}
		if m.AuthorShown {
			r.Author = m.Author
		}
		l.Rows = append(l.Rows, r)
	}
	return l, nil
}

// Read returns a readable message, applying its grants_flag as a side effect.
// Above-level messages return ErrClearance naming the required level.
func (e *Engine) Read(ctx context.Context, boardID string, msgID int, v Viewer) (*Msg, error) {
	b, err := e.visibleBoard(boardID, v)
	if err != nil {
		return nil, err
	}
	msgs, err := e.Messages(ctx, b)
	if err != nil {
		return nil, err
	}
	for i := range msgs {
		m := &msgs[i]
		if m.ID != msgID {
			continue
		}
		lvl := effectiveLevel(b, v)
		if m.Level > lvl {
			// Fully hidden messages must not confirm their existence.
			if m.Hidden {
				return nil, ErrNoMessage
			}
			return nil, ErrClearance{Need: m.Level}
		}
		if m.GrantsFlag != "" {
			if err := e.store.GrantFlags(ctx, v.Fingerprint, m.GrantsFlag); err != nil {
				return nil, err
			}
		}
		return m, nil
	}
	return nil, ErrNoMessage
}

// Post writes a player message. The post's level is the author's level at
// post time; the author may lower it, never raise it (spoilers structurally
// cannot flow downward). Public-area posts are always level 0. replyTo may
// reference a message above the author's level (you see only its subject).
func (e *Engine) Post(ctx context.Context, boardID string, v Viewer, subject, body string, level, replyTo int) (int, error) {
	b, err := e.visibleBoard(boardID, v)
	if err != nil {
		return 0, err
	}
	if !b.Writable {
		return 0, ErrNotWritable
	}
	if b.Area == content.AreaPublic {
		level = 0
	} else if level < 0 || level > v.Level {
		level = v.Level
	}
	subject = text.CleanLine(subject)
	body = text.Clean(body)
	if subject == "" || body == "" {
		return 0, ErrBadPost
	}
	if replyTo != 0 {
		msgs, err := e.Messages(ctx, b)
		if err != nil {
			return 0, err
		}
		found := false
		for _, m := range msgs {
			if m.ID == replyTo {
				// Replies to hidden messages would confirm their existence.
				if m.Hidden && m.Level > effectiveLevel(b, v) {
					return 0, ErrNoMessage
				}
				found = true
				break
			}
		}
		if !found {
			return 0, ErrNoMessage
		}
	}
	return e.store.SavePost(ctx, &store.SavedMessage{
		BoardID:  b.ID,
		Author:   v.Handle,
		Level:    level,
		Subject:  subject,
		Body:     body,
		ReplyTo:  replyTo,
		PostedAt: time.Now(),
	})
}
