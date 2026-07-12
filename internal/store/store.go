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
	Admin       bool   // sysop: may moderate (delete posts, elevated who/broadcast)
	Speed       int    // baud emulation chars/sec class: 1200 or 2400
	MinutesUsed int    // minutes used today (time-limit theater)
	MinutesDay  string // YYYY-MM-DD the MinutesUsed counter belongs to
	Heat        int    // THIS "heat": raw value at HeatAt, decays over time
	HeatAt      time.Time
	Lang        string // display language: "nl" (default) or "en"
	CreatedAt   time.Time
	LastSeen    time.Time
}

// HasFlag reports whether the player holds the named flag.
func (p *Player) HasFlag(f string) bool { return p.Flags[f] }

// Heat policy: getting caught (a trace expiring) raises heat; it decays with
// time and can be actively scrubbed with `wipe`. Enough heat locks THIS.
const (
	HeatMax         = 50
	HeatCaught      = 15 // a trace expiring
	HeatWipe        = 20 // one `wipe` scrub
	HeatDecayPerMin = 1
	HeatWarn        = 15 // status bar starts warning
	HeatHot         = 30 // NPCs turn wary; status bar goes hot
	HeatLockout     = 40 // THIS refuses entry until it decays below this
)

// DecayedHeat is raw heat aged from `since` to `now`, floored at zero.
func DecayedHeat(raw int, since, now time.Time) int {
	if raw <= 0 || since.IsZero() {
		if raw < 0 {
			return 0
		}
		return raw
	}
	mins := int(now.Sub(since).Minutes())
	if mins < 0 {
		mins = 0
	}
	if v := raw - mins*HeatDecayPerMin; v > 0 {
		return v
	}
	return 0
}

// CurrentHeat is the player's decayed heat as of now.
func (p *Player) CurrentHeat(now time.Time) int { return DecayedHeat(p.Heat, p.HeatAt, now) }

// Caller is one entry in the "laatste bellers" log.
type Caller struct {
	Handle string
	At     time.Time
}

// BreachInfo is the shared trail a host carries: how many distinct operators
// have cracked it, and who got there first.
type BreachInfo struct {
	Count       int
	FirstHandle string
	FirstAt     time.Time
}

// Notoriety is one operator's standing on the leaderboard.
type Notoriety struct {
	Handle   string
	Level    int
	Breaches int // distinct hosts cracked
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
	// SetAdmin flips the sysop bit.
	SetAdmin(ctx context.Context, fp string, admin bool) error
	// AddHeat decays the stored heat to now, adds delta (clamped to
	// [0, HeatMax]), persists it, and returns the new value.
	AddHeat(ctx context.Context, fp string, delta int) (int, error)
	// ResetProgress wipes a player's THIS arc for replay: membership revoked,
	// level→0, flags cleared, heat→0, and all host_state and breach rows
	// removed. The door shuts with everything else — the player re-walks the
	// discovery chain from the public BBS. handle is needed to clear the
	// (handle-keyed) breach trail.
	ResetProgress(ctx context.Context, fp, handle string) error
	// GrantFlags adds flags to the player's flag set.
	GrantFlags(ctx context.Context, fp string, flags ...string) error
	// SetLevel sets the THIS clearance level (0-9).
	SetLevel(ctx context.Context, fp string, level int) error
	// SetThisMember flips THIS membership (permanent in practice).
	SetThisMember(ctx context.Context, fp string, member bool) error
	// SetSpeed stores the baud class (2400/9600).
	SetSpeed(ctx context.Context, fp string, speed int) error
	// SetLang stores the display language ("nl"/"en").
	SetLang(ctx context.Context, fp, lang string) error
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
	// DeletePost removes a player-authored post by board and ID. It reports
	// whether a row was actually deleted (false = no such player post; the ID
	// may be a YAML-seeded message, which is immutable).
	DeletePost(ctx context.Context, boardID string, id int) (bool, error)
	// PendingPosts returns all sysop-review drafts (any board), oldest first.
	PendingPosts(ctx context.Context) ([]SavedMessage, error)
	// PublishPost clears the pending flag on a draft, making it live. Reports
	// whether a pending post with that ID existed.
	PublishPost(ctx context.Context, id int) (bool, error)
	// DeletePendingPost discards a draft. It only ever removes a pending row,
	// so it can never delete a live post by mistake.
	DeletePendingPost(ctx context.Context, id int) (bool, error)

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

	// RecordBreach notes that handle has cracked hostID (idempotent per
	// operator — the first time is kept, so it never overwrites the pioneer).
	RecordBreach(ctx context.Context, hostID, handle string, at time.Time) error
	// BreachInfo returns the shared trail for one host.
	BreachInfo(ctx context.Context, hostID string) (BreachInfo, error)
	// Leaderboard returns operators ranked by THIS level then hosts cracked.
	Leaderboard(ctx context.Context, limit int) ([]Notoriety, error)

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
	ReplyTo  int  // 0 = top-level
	Pending  bool // true = sysop-review draft, hidden until published
	PostedAt time.Time
}
