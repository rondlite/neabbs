// Package world is the THIS node graph: hosts the player can scan, connect
// to, and read files on. Everything comes from YAML; nothing is actually
// hackable — it is a clearance-filtered content tree dressed as a system.
package world

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rondlite/neabbs/internal/board"
	"github.com/rondlite/neabbs/internal/content"
	"github.com/rondlite/neabbs/internal/store"
)

var (
	// ErrNoRoute is returned for unknown addresses — and, identically, for
	// hosts the viewer may not know exist. Never confirm existence.
	ErrNoRoute = errors.New("world: no route to host")
	// ErrNoFile is returned for file names that don't exist on the host.
	ErrNoFile = errors.New("world: no such file")
	// ErrLocked is returned when reading files on a locked, uncracked host.
	ErrLocked = errors.New("world: host is locked")
)

// ErrClearance means the file exists but needs a higher THIS level.
type ErrClearance struct{ Need int }

func (e ErrClearance) Error() string { return fmt.Sprintf("world: THIS-%d required", e.Need) }

// FileRow is one `ls` line: files above the viewer's level are listed as
// redacted rows with the required clearance (level-filtered like messages).
type FileRow struct {
	Name     string
	Level    int
	Redacted bool
	Hidden   bool // dotfile: only shown by `ls -a`
}

// MailRow is one spool line for `mail`.
type MailRow struct {
	Index    int // 1-based
	From     string
	Subject  string
	Level    int
	Redacted bool
}

// Engine resolves the host graph against a viewer's clearance.
type Engine struct {
	content *content.Set
	store   store.Store
}

// NewEngine builds the world engine.
func NewEngine(c *content.Set, s store.Store) *Engine {
	return &Engine{content: c, store: s}
}

// hostVisible: hosts with a requires_flag are granted by that flag alone;
// all others by min_level. Invisible hosts are not even listed by scan.
func hostVisible(h *content.Host, v board.Viewer, has func(string) bool) bool {
	if !v.ThisMember {
		return false
	}
	if h.RequiresFlag != "" {
		return has(h.RequiresFlag)
	}
	return h.MinLevel <= v.Level
}

// Scan lists the hosts reachable for the viewer's level and flags.
func (e *Engine) Scan(v board.Viewer, has func(string) bool) []*content.Host {
	var out []*content.Host
	for i := range e.content.Hosts {
		if hostVisible(&e.content.Hosts[i], v, has) {
			out = append(out, &e.content.Hosts[i])
		}
	}
	return out
}

// Connect resolves an address. Unknown and invisible hosts return the same
// ErrNoRoute.
func (e *Engine) Connect(addr string, v board.Viewer, has func(string) bool) (*content.Host, error) {
	h := e.content.HostByAddress(addr)
	if h == nil || !hostVisible(h, v, has) {
		return nil, ErrNoRoute
	}
	return h, nil
}

// Unlocked reports whether the viewer may read files on the host now:
// open hosts always, locked hosts only while cracked (a trace kick clears
// the cracked bit again).
func (e *Engine) Unlocked(ctx context.Context, h *content.Host, fp string) (bool, error) {
	if !h.Locked {
		return true, nil
	}
	hs, err := e.store.HostState(ctx, fp, h.ID)
	if err != nil {
		return false, err
	}
	return hs.Cracked, nil
}

// CrackResult is the outcome of a crack attempt.
type CrackResult struct {
	Success      bool
	First        bool   // first successful crack ever → effects fired
	Msg          string // refusal hint or flavor; specific, never generic
	TraceSeconds int    // >0: the trace timer starts now
	Effects      *content.Effects
}

// Crack attempts to unlock the current host. The "lookup wearing a ski
// mask" mechanic: password cracks succeed iff the player already holds the
// password_flag — acquiring that flag elsewhere IS the puzzle.
func (e *Engine) Crack(ctx context.Context, h *content.Host, v board.Viewer, has func(string) bool) (CrackResult, error) {
	if !h.Locked {
		return CrackResult{Msg: "crack: dit systeem staat gewoon open."}, nil
	}
	hs, err := e.store.HostState(ctx, v.Fingerprint, h.ID)
	if err != nil {
		return CrackResult{}, err
	}
	if hs.Cracked {
		return CrackResult{Msg: "crack: al open. rustig maar."}, nil
	}
	if until := hs.CooldownUntil; !until.IsZero() && time.Now().Before(until) {
		rem := time.Until(until).Round(time.Minute)
		if rem < time.Minute {
			rem = time.Minute
		}
		return CrackResult{Msg: fmt.Sprintf("crack: dit systeem herkent je nog van daarnet. probeer het over %s weer.", rem)}, nil
	}
	spec := h.Crack
	if spec == nil {
		return CrackResult{Msg: "crack: geen bekende ingang."}, nil
	}
	fail := func() CrackResult {
		if spec.HintOnFail != "" {
			return CrackResult{Msg: spec.HintOnFail}
		}
		return CrackResult{Msg: "TOEGANG GEWEIGERD."}
	}
	if v.Level < spec.MinLevel {
		return fail(), nil
	}
	switch spec.Method {
	case "none":
		return fail(), nil
	case "password", "wordlist":
		if spec.PasswordFlag != "" && !has(spec.PasswordFlag) {
			return fail(), nil
		}
	default:
		return fail(), nil
	}
	// Multi-stage: every additional prerequisite must be held too (e.g. the
	// captured hash AND the wordlist that breaks it).
	for _, f := range spec.RequiresFlags {
		if !has(f) {
			return fail(), nil
		}
	}
	first, err := e.store.SetHostCracked(ctx, v.Fingerprint, h.ID, true)
	if err != nil {
		return CrackResult{}, err
	}
	res := CrackResult{
		Success:      true,
		First:        first,
		TraceSeconds: spec.TraceSeconds,
	}
	if first {
		res.Effects = h.Effects.OnFirstCrack
	}
	return res, nil
}

// TraceExpired applies the trace consequences: the host locks again for
// this player for 10 minutes. No level loss in v1.
func (e *Engine) TraceExpired(ctx context.Context, h *content.Host, fp string) error {
	return e.store.SetHostCooldown(ctx, fp, h.ID, time.Now().Add(10*time.Minute))
}

// Ls lists the host's files for the viewer: readable ones normally,
// above-level ones as redacted rows naming their clearance.
func (e *Engine) Ls(ctx context.Context, h *content.Host, v board.Viewer) ([]FileRow, error) {
	if ok, err := e.Unlocked(ctx, h, v.Fingerprint); err != nil {
		return nil, err
	} else if !ok {
		return nil, ErrLocked
	}
	rows := make([]FileRow, 0, len(h.Files))
	for i := range h.Files {
		f := &h.Files[i]
		rows = append(rows, FileRow{
			Name:     f.Name,
			Level:    f.MinLevel,
			Redacted: f.MinLevel > v.Level,
			Hidden:   strings.HasPrefix(f.Name, "."),
		})
	}
	return rows, nil
}

// Mail lists a host's spool for the viewer (cracked-gated, level-filtered).
func (e *Engine) Mail(ctx context.Context, h *content.Host, v board.Viewer) ([]MailRow, error) {
	if ok, err := e.Unlocked(ctx, h, v.Fingerprint); err != nil {
		return nil, err
	} else if !ok {
		return nil, ErrLocked
	}
	rows := make([]MailRow, 0, len(h.Mail))
	for i := range h.Mail {
		msg := &h.Mail[i]
		rows = append(rows, MailRow{
			Index:    i + 1,
			From:     msg.From,
			Subject:  msg.Subject,
			Level:    msg.MinLevel,
			Redacted: msg.MinLevel > v.Level,
		})
	}
	return rows, nil
}

// ReadMail returns one spool message (1-based), applying its grants_flag.
func (e *Engine) ReadMail(ctx context.Context, h *content.Host, idx int, v board.Viewer) (*content.MailMsg, error) {
	if ok, err := e.Unlocked(ctx, h, v.Fingerprint); err != nil {
		return nil, err
	} else if !ok {
		return nil, ErrLocked
	}
	if idx < 1 || idx > len(h.Mail) {
		return nil, ErrNoFile
	}
	msg := &h.Mail[idx-1]
	if msg.MinLevel > v.Level {
		return nil, ErrClearance{Need: msg.MinLevel}
	}
	if msg.GrantsFlag != "" {
		if err := e.store.GrantFlags(ctx, v.Fingerprint, msg.GrantsFlag); err != nil {
			return nil, err
		}
	}
	return msg, nil
}

// Netstat returns a host's connection readout (cracked-gated), applying its
// grants_flag — which can make new hosts visible (the graph grows).
func (e *Engine) Netstat(ctx context.Context, h *content.Host, v board.Viewer) (*content.HostView, error) {
	if ok, err := e.Unlocked(ctx, h, v.Fingerprint); err != nil {
		return nil, err
	} else if !ok {
		return nil, ErrLocked
	}
	if h.Netstat == nil {
		return nil, ErrNoFile
	}
	if h.Netstat.MinLevel > v.Level {
		return nil, ErrClearance{Need: h.Netstat.MinLevel}
	}
	if h.Netstat.GrantsFlag != "" {
		if err := e.store.GrantFlags(ctx, v.Fingerprint, h.Netstat.GrantsFlag); err != nil {
			return nil, err
		}
	}
	return h.Netstat, nil
}

// Cat returns a file body, applying its grants_flag as a side effect.
// Above-level files return ErrClearance naming the required level.
func (e *Engine) Cat(ctx context.Context, h *content.Host, name string, v board.Viewer) (*content.HostFile, error) {
	if ok, err := e.Unlocked(ctx, h, v.Fingerprint); err != nil {
		return nil, err
	} else if !ok {
		return nil, ErrLocked
	}
	for i := range h.Files {
		f := &h.Files[i]
		if f.Name != name {
			continue
		}
		if f.MinLevel > v.Level {
			return nil, ErrClearance{Need: f.MinLevel}
		}
		if f.GrantsFlag != "" {
			if err := e.store.GrantFlags(ctx, v.Fingerprint, f.GrantsFlag); err != nil {
				return nil, err
			}
		}
		return f, nil
	}
	return nil, ErrNoFile
}
