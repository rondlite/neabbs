// Package store defines the persistence interface. All storage sits behind
// Store so the SQLite implementation can be swapped (e.g. for Redis) later.
package store

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrNotFound is returned when a requested row does not exist.
	ErrNotFound = errors.New("store: not found")
	// ErrHandleTaken is returned when a handle is already claimed.
	ErrHandleTaken = errors.New("store: handle already taken")
)

// Player is one caller, keyed by the SHA256 fingerprint of their SSH pubkey.
type Player struct {
	Fingerprint string
	Handle      string // empty until chosen on first login
	ThisMember  bool
	Level       int // 0-9, meaningful only when ThisMember
	Flags       map[string]bool
	Banned      bool
	Speed       int    // baud emulation chars/sec class: 1200 or 2400
	MinutesUsed int    // minutes used today (time-limit theater)
	MinutesDay  string // YYYY-MM-DD the MinutesUsed counter belongs to
	CreatedAt   time.Time
	LastSeen    time.Time
}

// HasFlag reports whether the player holds the named flag.
func (p *Player) HasFlag(f string) bool { return p.Flags[f] }

// Caller is one entry in the "laatste bellers" log.
type Caller struct {
	Handle string
	At     time.Time
}

// Store is the persistence boundary.
type Store interface {
	// PlayerByFingerprint returns the player, or ErrNotFound.
	PlayerByFingerprint(ctx context.Context, fp string) (*Player, error)
	// CreatePlayer inserts a new player row for fp with no handle yet.
	CreatePlayer(ctx context.Context, fp string) (*Player, error)
	// SetHandle claims a handle for fp; ErrHandleTaken if in use.
	SetHandle(ctx context.Context, fp, handle string) error
	// TouchLastSeen updates last_seen to now.
	TouchLastSeen(ctx context.Context, fp string) error
	// SetBanned flips the ban bit.
	SetBanned(ctx context.Context, fp string, banned bool) error
	// GrantFlags adds flags to the player's flag set.
	GrantFlags(ctx context.Context, fp string, flags ...string) error
	// SetLevel sets the THIS clearance level (0-9).
	SetLevel(ctx context.Context, fp string, level int) error
	// SetThisMember flips THIS membership (permanent in practice).
	SetThisMember(ctx context.Context, fp string, member bool) error
	// SetSpeed stores the baud class (2400/9600).
	SetSpeed(ctx context.Context, fp string, speed int) error
	// AddMinutes adds n to today's used-minutes counter and returns the new
	// total for day (YYYY-MM-DD). A day rollover resets the counter.
	AddMinutes(ctx context.Context, fp string, day string, n int) (int, error)

	// RecordCall appends to the callers log.
	RecordCall(ctx context.Context, handle string, at time.Time) error
	// LastCallers returns the most recent n callers, newest first.
	LastCallers(ctx context.Context, n int) ([]Caller, error)

	// LastRead returns the per-player high-water mark for a board (0 if none).
	LastRead(ctx context.Context, fp, boardID string) (int, error)
	// SetLastRead raises the high-water mark for a board (never lowers it).
	SetLastRead(ctx context.Context, fp, boardID string, msgID int) error

	// SavePost persists a player-authored message and returns its assigned ID.
	SavePost(ctx context.Context, m *SavedMessage) (int, error)
	// PostsForBoard returns player-authored messages for a board, ID order.
	PostsForBoard(ctx context.Context, boardID string) ([]SavedMessage, error)

	// HostState returns per-player host state: cracked now, whether the
	// first-crack effects ever fired, and any lockout deadline.
	HostState(ctx context.Context, fp, hostID string) (HostState, error)
	// SetHostCracked flips the cracked bit; on the first crack ever it also
	// marks first_cracked and reports it.
	SetHostCracked(ctx context.Context, fp, hostID string, cracked bool) (first bool, err error)
	// SetHostCooldown locks the host for this player until the deadline.
	SetHostCooldown(ctx context.Context, fp, hostID string, until time.Time) error

	// AddNPCTurns adds n to today's NPC-turn counter (per-player, all NPCs)
	// and returns the new total for day (YYYY-MM-DD); a day rollover resets.
	AddNPCTurns(ctx context.Context, fp, day string, n int) (int, error)

	// AllPlayers returns every player (admin inspect).
	AllPlayers(ctx context.Context) ([]Player, error)
	// PlayerByHandle returns the player with the given handle, or ErrNotFound.
	PlayerByHandle(ctx context.Context, handle string) (*Player, error)

	Close() error
}

// HostState is a player's relationship with one host.
type HostState struct {
	Cracked       bool
	FirstCracked  bool      // first-crack effects already fired once
	CooldownUntil time.Time // zero = no lockout
}

// SavedMessage is a player-authored board message as persisted.
type SavedMessage struct {
	ID       int
	BoardID  string
	Author   string
	Level    int
	Subject  string
	Body     string
	ReplyTo  int // 0 = top-level
	PostedAt time.Time
}
