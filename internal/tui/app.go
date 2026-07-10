// Package tui holds the Bubble Tea session UI. Phase 1: banner, handle
// picker, a minimal prompt with status/logout. The public BBS call ritual
// replaces the prompt in phase 3.
package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/rondlite/neabbs/internal/config"
	"github.com/rondlite/neabbs/internal/presence"
	"github.com/rondlite/neabbs/internal/store"
	"github.com/rondlite/neabbs/internal/text"
)

const (
	maxLineBytes  = 512 // hard cap on one input line
	rateLimitPerS = 20  // input events per second; excess dropped
)

// Deps is everything a session UI needs.
type Deps struct {
	Cfg      config.Config
	Store    store.Store
	Registry *presence.Registry
	Sess     *presence.Session
	Player   *store.Player
}

type state int

const (
	stateHandle state = iota
	stateMain
	stateDone
)

var (
	amber  = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	dimmed = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
)

// Model is the root session model.
type Model struct {
	deps  Deps
	state state
	input textinput.Model
	start time.Time
	width int

	// input rate limiting: token bucket
	tokens     int
	lastRefill time.Time
}

// New builds the root model for a session.
func New(deps Deps) *Model {
	ti := textinput.New()
	ti.CharLimit = maxLineBytes
	ti.Prompt = "> "
	ti.Focus()
	m := &Model{
		deps:       deps,
		input:      ti,
		start:      time.Now(),
		width:      80,
		tokens:     rateLimitPerS,
		lastRefill: time.Now(),
	}
	if deps.Player.Handle == "" {
		m.state = stateHandle
		m.input.Prompt = "Gebruikersnaam: "
	} else {
		m.state = stateMain
	}
	return m
}

func (m *Model) Init() tea.Cmd {
	lines := []string{
		amber.Render("NEABBS — heropend na 40 jaar stilte"),
		"",
	}
	if m.state == stateHandle {
		lines = append(lines,
			"Nieuwe beller gedetecteerd.",
			"Kies een gebruikersnaam (3-16 tekens, a-z 0-9 _ -).")
	} else {
		lines = append(lines, fmt.Sprintf("Welkom terug, %s.", m.deps.Player.Handle))
	}
	lines = append(lines, dimmed.Render("Typ 'status' of 'logout'."))
	return tea.Println(strings.Join(lines, "\n"))
}

// allow implements the per-session input rate limit (drop excess events).
func (m *Model) allow() bool {
	now := time.Now()
	elapsed := now.Sub(m.lastRefill)
	refill := int(elapsed / (time.Second / rateLimitPerS))
	if refill > 0 {
		m.tokens = min(rateLimitPerS, m.tokens+refill)
		m.lastRefill = now
	}
	if m.tokens <= 0 {
		return false
	}
	m.tokens--
	return true
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil
	case tea.KeyMsg:
		if !m.allow() {
			return m, nil
		}
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyCtrlD:
			return m.quit()
		case tea.KeyEnter:
			line := text.CleanLine(m.input.Value())
			m.input.SetValue("")
			return m.handleLine(line)
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *Model) handleLine(line string) (tea.Model, tea.Cmd) {
	switch m.state {
	case stateHandle:
		return m.pickHandle(line)
	case stateMain:
		return m.command(strings.ToLower(line))
	}
	return m, nil
}

func (m *Model) pickHandle(h string) (tea.Model, tea.Cmd) {
	if !text.ValidHandle(h) {
		return m, tea.Println("Ongeldige naam. 3-16 tekens, alleen a-z 0-9 _ -. Probeer opnieuw.")
	}
	err := m.deps.Store.SetHandle(context.Background(), m.deps.Player.Fingerprint, h)
	if errors.Is(err, store.ErrHandleTaken) {
		return m, tea.Println("Die naam is al bezet. Probeer een andere.")
	}
	if err != nil {
		return m, tea.Println("Opslaan mislukt, probeer opnieuw.")
	}
	m.deps.Player.Handle = h
	m.deps.Sess.SetHandle(h)
	m.state = stateMain
	m.input.Prompt = "> "
	return m, tea.Println(fmt.Sprintf("Aangenaam, %s.\n%s", h, dimmed.Render("Typ 'status' of 'logout'.")))
}

func (m *Model) command(cmd string) (tea.Model, tea.Cmd) {
	switch cmd {
	case "":
		return m, nil
	case "status":
		p := m.deps.Player
		up := time.Since(m.start).Round(time.Second)
		out := []string{
			fmt.Sprintf("Gebruiker : %s", p.Handle),
			fmt.Sprintf("Lid sinds : %s", p.CreatedAt.Format("02-01-2006")),
			fmt.Sprintf("Online    : %s", up),
			fmt.Sprintf("Lijn      : %s", lineLabel(m.deps.Sess.Line)),
		}
		// THIS clearance is only ever shown to members (non-members must see
		// zero evidence THIS exists).
		if p.ThisMember {
			out = append(out, fmt.Sprintf("THIS      : niveau %d, %d vlaggen", p.Level, len(p.Flags)))
		}
		return m, tea.Println(strings.Join(out, "\n"))
	case "logout", "quit", "exit":
		return m.quit()
	default:
		return m, tea.Println("Onbekende keuze.")
	}
}

func lineLabel(n int) string {
	if n == 0 {
		return "??"
	}
	return fmt.Sprintf("%d", n)
}

func (m *Model) quit() (tea.Model, tea.Cmd) {
	m.state = stateDone
	return m, tea.Sequence(tea.Println("\nTot ziens.\n\nNO CARRIER"), tea.Quit)
}

func (m *Model) View() string {
	if m.state == stateDone {
		return ""
	}
	return m.input.View()
}
