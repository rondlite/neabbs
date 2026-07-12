// Package presence tracks live sessions: per-fingerprint and global caps,
// phone-line assignment (the 24 lines), and cross-session broadcast.
package presence

import (
	"errors"
	"sync"
)

const (
	// MaxPerFingerprint is the concurrent-session cap per player key.
	MaxPerFingerprint = 3
	// MaxGlobal is the global concurrent-session cap.
	MaxGlobal = 200
	// Lines is how many phone lines the board "has".
	Lines = 24
)

var (
	// ErrTooManySessions: this fingerprint already has MaxPerFingerprint sessions.
	ErrTooManySessions = errors.New("presence: too many sessions for this key")
	// ErrFull: the global session cap is reached.
	ErrFull = errors.New("presence: server full")
)

// Session is one live caller. Send posts a message into the session's
// Bubble Tea program (set by the TUI layer; may be nil early in setup).
type Session struct {
	Fingerprint string
	Handle      string // updated after login/handle pick
	Line        int    // 1-24; 0 means "LIJN ??" (overflow beyond 24)
	Area        string // what the user list shows, e.g. "hoofdmenu"
	InTHIS      bool   // true → user list shows "lijn bezet"
	Send        func(msg any)

	mu sync.Mutex
}

// SetHandle updates the session's handle (after the handle picker).
func (s *Session) SetHandle(h string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Handle = h
}

// SetArea updates what the public user list shows for this session.
func (s *Session) SetArea(area string, inThis bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Area = area
	s.InTHIS = inThis
}

// Snapshot returns a copy of the mutable fields.
func (s *Session) Snapshot() (handle, area string, inThis bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Handle, s.Area, s.InTHIS
}

// Registry tracks all live sessions.
type Registry struct {
	mu       sync.Mutex
	sessions map[*Session]struct{}
	byFP     map[string]int
	lines    [Lines + 1]*Session // index 1..24
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		sessions: make(map[*Session]struct{}),
		byFP:     make(map[string]int),
	}
}

// Add registers a new session for fp, assigning the lowest free line.
// Returns ErrTooManySessions or ErrFull when a cap is hit.
func (r *Registry) Add(fp string) (*Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.sessions) >= MaxGlobal {
		return nil, ErrFull
	}
	if r.byFP[fp] >= MaxPerFingerprint {
		return nil, ErrTooManySessions
	}
	s := &Session{Fingerprint: fp}
	for i := 1; i <= Lines; i++ {
		if r.lines[i] == nil {
			r.lines[i] = s
			s.Line = i
			break
		}
	}
	r.sessions[s] = struct{}{}
	r.byFP[fp]++
	return s, nil
}

// Remove unregisters a session and frees its line.
func (r *Registry) Remove(s *Session) {
	if s == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.sessions[s]; !ok {
		return
	}
	delete(r.sessions, s)
	if r.byFP[s.Fingerprint] <= 1 {
		delete(r.byFP, s.Fingerprint)
	} else {
		r.byFP[s.Fingerprint]--
	}
	if s.Line >= 1 && s.Line <= Lines && r.lines[s.Line] == s {
		r.lines[s.Line] = nil
	}
}

// Count returns the number of live sessions.
func (r *Registry) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.sessions)
}

// LinesBusy returns how many of the 24 numbered lines are occupied.
func (r *Registry) LinesBusy() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for i := 1; i <= Lines; i++ {
		if r.lines[i] != nil {
			n++
		}
	}
	return n
}

// All returns a snapshot slice of live sessions, line order first (numbered
// lines ascending, then overflow sessions).
func (r *Registry) All() []*Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*Session, 0, len(r.sessions))
	seen := make(map[*Session]bool, len(r.sessions))
	for i := 1; i <= Lines; i++ {
		if r.lines[i] != nil {
			out = append(out, r.lines[i])
			seen[r.lines[i]] = true
		}
	}
	for s := range r.sessions {
		if !seen[s] {
			out = append(out, s)
		}
	}
	return out
}

// Broadcast sends msg to every live session (with a non-nil Send),
// optionally excluding one session. Delivery happens on a fresh goroutine:
// Program.Send blocks when called from inside the sending session's own
// Update loop, so broadcasts must never run synchronously there.
func (r *Registry) Broadcast(msg any, except *Session) {
	sessions := r.All()
	go func() {
		for _, s := range sessions {
			if s != except {
				s.SendMsg(msg)
			}
		}
	}()
}

// SendTo delivers msg to every live session belonging to fp and reports how
// many there were. Delivery happens on a fresh goroutine for the same reason
// Broadcast does it: Program.Send blocks when called from inside the target's
// own Update loop, and a sysop acting on themselves (refilling their own time,
// resetting their own arc) is exactly that case — done synchronously it
// deadlocks the session instead of messaging it.
func (r *Registry) SendTo(fp string, msg any) int {
	var targets []*Session
	for _, s := range r.All() {
		if s.Fingerprint == fp {
			targets = append(targets, s)
		}
	}
	go func() {
		for _, s := range targets {
			s.SendMsg(msg)
		}
	}()
	return len(targets)
}

// SetSend installs the Bubble Tea bridge for a session.
func (s *Session) SetSend(f func(any)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Send = f
}

// SendMsg delivers a message into the session's Bubble Tea program, if the
// bridge is installed.
func (s *Session) SendMsg(msg any) {
	s.mu.Lock()
	f := s.Send
	s.mu.Unlock()
	if f != nil {
		f(msg)
	}
}
