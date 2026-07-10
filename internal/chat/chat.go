// Package chat is the Babbel room: one shared multi-user chat room with
// join/leave notices and an ephemeral in-memory scrollback (nothing stored).
package chat

import (
	"fmt"
	"sync"

	"github.com/rondlite/neabbs/internal/presence"
)

// ScrollbackLines is the maximum kept history.
const ScrollbackLines = 200

// Event is one chat line delivered to member sessions via Session.Send.
type Event struct{ Line string }

// Room is the shared Babbel room.
type Room struct {
	mu         sync.Mutex
	members    map[*presence.Session]bool
	scrollback []string
}

// NewRoom builds an empty room.
func NewRoom() *Room {
	return &Room{members: make(map[*presence.Session]bool)}
}

func lineLabel(n int) string {
	if n == 0 {
		return "??"
	}
	return fmt.Sprintf("%d", n)
}

// Join adds a session, announces it, and returns recent scrollback.
func (r *Room) Join(s *presence.Session) []string {
	handle, _, _ := s.Snapshot()
	r.mu.Lock()
	r.members[s] = true
	recent := make([]string, len(r.scrollback))
	copy(recent, r.scrollback)
	r.mu.Unlock()
	r.post(fmt.Sprintf("* lijn %s (%s) komt binnen", lineLabel(s.Line), handle), nil)
	return recent
}

// Leave removes a session and announces it.
func (r *Room) Leave(s *presence.Session) {
	r.mu.Lock()
	if !r.members[s] {
		r.mu.Unlock()
		return
	}
	delete(r.members, s)
	r.mu.Unlock()
	handle, _, _ := s.Snapshot()
	r.post(fmt.Sprintf("* lijn %s (%s) vertrekt", lineLabel(s.Line), handle), nil)
}

// Say posts a chat message from a member. Text must already be sanitized.
func (r *Room) Say(s *presence.Session, text string) {
	handle, _, _ := s.Snapshot()
	r.post(fmt.Sprintf("<%s> %s", handle, text), nil)
}

// Count returns the number of members.
func (r *Room) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.members)
}

// post appends to scrollback and delivers to all members (except skip).
func (r *Room) post(line string, skip *presence.Session) {
	r.mu.Lock()
	r.scrollback = append(r.scrollback, line)
	if len(r.scrollback) > ScrollbackLines {
		r.scrollback = r.scrollback[len(r.scrollback)-ScrollbackLines:]
	}
	members := make([]*presence.Session, 0, len(r.members))
	for m := range r.members {
		if m != skip {
			members = append(members, m)
		}
	}
	r.mu.Unlock()
	// Deliver on a fresh goroutine: Program.Send blocks when called from
	// inside the posting session's own Update loop.
	go func() {
		for _, m := range members {
			m.SendMsg(Event{Line: line})
		}
	}()
}
