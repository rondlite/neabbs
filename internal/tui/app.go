// Package tui holds the Bubble Tea session UI. Phase 1: banner, handle
// picker, a minimal prompt with status/logout. The public BBS call ritual
// replaces the prompt in phase 3.
package tui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/rondlite/neabbs/internal/board"
	"github.com/rondlite/neabbs/internal/config"
	"github.com/rondlite/neabbs/internal/content"
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
	Boards   *board.Engine
}

type state int

const (
	stateHandle state = iota
	stateMain
	stateComposeSubject
	stateComposeBody
	stateComposeLevel
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

	// board context: set while the player is "in" a board
	boardID string

	// composer state (post/reply)
	compose struct {
		replyTo int
		subject string
		lines   []string
	}
}

// promoTickMsg drives the live-repaint poll while viewing a board.
type promoTickMsg struct{}

func promoTick() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return promoTickMsg{} })
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
	lines = append(lines, dimmed.Render("Typ 'boards', 'status' of 'logout'."))
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
	case promoTickMsg:
		return m.checkPromotion()
	case tea.KeyMsg:
		if !m.allow() {
			return m, nil
		}
		switch msg.Type {
		case tea.KeyCtrlC:
			return m.quit()
		case tea.KeyCtrlD:
			if m.state == stateComposeBody {
				return m.finishBody()
			}
			return m.quit()
		case tea.KeyEsc:
			if m.composing() {
				m.resetCompose()
				return m, tea.Println("Geannuleerd.")
			}
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

func (m *Model) composing() bool {
	return m.state == stateComposeSubject || m.state == stateComposeBody || m.state == stateComposeLevel
}

func (m *Model) resetCompose() {
	m.compose.replyTo = 0
	m.compose.subject = ""
	m.compose.lines = nil
	m.state = stateMain
	m.input.Prompt = "> "
}

func (m *Model) handleLine(line string) (tea.Model, tea.Cmd) {
	switch m.state {
	case stateHandle:
		return m.pickHandle(line)
	case stateMain:
		return m.command(line)
	case stateComposeSubject:
		return m.composeSubject(line)
	case stateComposeBody:
		return m.composeBody(line)
	case stateComposeLevel:
		return m.composeLevel(line)
	}
	return m, nil
}

// viewer builds the clearance identity from the current player state.
func (m *Model) viewer() board.Viewer {
	p := m.deps.Player
	return board.Viewer{
		Fingerprint: p.Fingerprint,
		Handle:      p.Handle,
		ThisMember:  p.ThisMember,
		Level:       p.Level,
	}
}

// checkPromotion reloads the player row and, on a level/membership change
// while a board is open, repaints the listing live so redacted posts
// visibly resolve — the game's core dopamine hit.
func (m *Model) checkPromotion() (tea.Model, tea.Cmd) {
	if m.boardID == "" || m.state == stateDone {
		return m, nil
	}
	fresh, err := m.deps.Store.PlayerByFingerprint(context.Background(), m.deps.Player.Fingerprint)
	if err != nil {
		return m, promoTick()
	}
	oldLevel, oldMember := m.deps.Player.Level, m.deps.Player.ThisMember
	changed := fresh.Level != oldLevel || fresh.ThisMember != oldMember
	*m.deps.Player = *fresh
	if !changed {
		return m, promoTick()
	}
	var out []string
	if fresh.Level > oldLevel {
		out = append(out, amber.Render(fmt.Sprintf("*** PROMOTIE — THIS-%d toegekend ***", fresh.Level)))
	}
	l, err := m.deps.Boards.Listing(context.Background(), m.boardID, m.viewer())
	if err != nil {
		// Board may have vanished for this viewer (demotion): drop context.
		m.boardID = ""
		return m, promoTick()
	}
	out = append(out, renderListing(l))
	return m, tea.Batch(tea.Println(strings.Join(out, "\n")), promoTick())
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
	return m, tea.Println(fmt.Sprintf("Aangenaam, %s.\n%s", h, dimmed.Render("Typ 'boards', 'status' of 'logout'.")))
}

func (m *Model) command(line string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return m, nil
	}
	cmd := strings.ToLower(fields[0])
	arg := ""
	if len(fields) > 1 {
		arg = strings.ToLower(fields[1])
	}
	switch cmd {
	case "boards":
		return m, tea.Println(renderBoardList(m.deps.Boards.VisibleBoards(m.viewer())))
	case "board":
		if arg == "" {
			return m, tea.Println("Gebruik: board <id>")
		}
		l, err := m.deps.Boards.Listing(context.Background(), arg, m.viewer())
		if err != nil {
			return m, tea.Println("Onbekend board.")
		}
		firstOpen := m.boardID == ""
		m.boardID = arg
		cmds := []tea.Cmd{tea.Println(renderListing(l))}
		if firstOpen {
			cmds = append(cmds, promoTick()) // start the live-repaint poll
		}
		return m, tea.Batch(cmds...)
	case "read":
		return m.readMessage(arg)
	case "post":
		return m.startCompose(0)
	case "reply":
		nr, err := strconv.Atoi(arg)
		if err != nil {
			return m, tea.Println("Gebruik: reply <nr>")
		}
		return m.startCompose(nr)
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

// readMessage handles `read <nr>` in the current board context.
func (m *Model) readMessage(arg string) (tea.Model, tea.Cmd) {
	if m.boardID == "" {
		return m, tea.Println("Open eerst een board: board <id>")
	}
	nr, err := strconv.Atoi(arg)
	if err != nil {
		return m, tea.Println("Gebruik: read <nr>")
	}
	msg, err := m.deps.Boards.Read(context.Background(), m.boardID, nr, m.viewer())
	var ec board.ErrClearance
	switch {
	case errors.As(err, &ec):
		// Locked things respond specifically: name the required clearance.
		return m, tea.Println(fmt.Sprintf("TOEGANG GEWEIGERD — THIS-%d vereist.", ec.Need))
	case err != nil:
		return m, tea.Println("Geen bericht met dat nummer.")
	}
	return m, tea.Println(renderMessage(m.boardID, msg))
}

// startCompose begins the post/reply composer (ESC cancels).
func (m *Model) startCompose(replyTo int) (tea.Model, tea.Cmd) {
	if m.boardID == "" {
		return m, tea.Println("Open eerst een board: board <id>")
	}
	b := m.deps.Boards.VisibleBoardByID(m.boardID, m.viewer())
	if b == nil {
		m.boardID = ""
		return m, tea.Println("Onbekend board.")
	}
	if !b.Writable {
		return m, tea.Println("Dit board is alleen-lezen.")
	}
	m.compose.replyTo = replyTo
	m.state = stateComposeSubject
	m.input.Prompt = "Onderwerp: "
	return m, tea.Println("Nieuw bericht. ESC annuleert.")
}

func (m *Model) composeSubject(line string) (tea.Model, tea.Cmd) {
	if line == "" {
		return m, tea.Println("Onderwerp mag niet leeg zijn.")
	}
	m.compose.subject = line
	m.state = stateComposeBody
	m.input.Prompt = "| "
	return m, tea.Println("Tekst. Sluit af met '.' op een eigen regel (of ctrl-d).")
}

func (m *Model) composeBody(line string) (tea.Model, tea.Cmd) {
	if line == "." {
		return m.finishBody()
	}
	m.compose.lines = append(m.compose.lines, line)
	return m, nil
}

// finishBody moves to the level prompt (THIS boards for leveled members)
// or submits directly.
func (m *Model) finishBody() (tea.Model, tea.Cmd) {
	if len(m.compose.lines) == 0 {
		m.resetCompose()
		return m, tea.Println("Leeg bericht, geannuleerd.")
	}
	b := m.deps.Boards.VisibleBoardByID(m.boardID, m.viewer())
	if b != nil && b.Area == content.AreaThis && m.deps.Player.Level > 0 {
		m.state = stateComposeLevel
		m.input.Prompt = fmt.Sprintf("Niveau (0-%d, enter=%d): ", m.deps.Player.Level, m.deps.Player.Level)
		return m, nil
	}
	return m.submitPost(-1)
}

func (m *Model) composeLevel(line string) (tea.Model, tea.Cmd) {
	if line == "" {
		return m.submitPost(-1)
	}
	lvl, err := strconv.Atoi(line)
	if err != nil || lvl < 0 || lvl > m.deps.Player.Level {
		return m, tea.Println(fmt.Sprintf("Kies een niveau van 0 t/m %d.", m.deps.Player.Level))
	}
	return m.submitPost(lvl)
}

func (m *Model) submitPost(level int) (tea.Model, tea.Cmd) {
	id, err := m.deps.Boards.Post(context.Background(), m.boardID, m.viewer(),
		m.compose.subject, strings.Join(m.compose.lines, "\n"), level, m.compose.replyTo)
	m.resetCompose()
	if err != nil {
		if errors.Is(err, board.ErrNoMessage) {
			return m, tea.Println("Geen bericht met dat nummer.")
		}
		return m, tea.Println("Plaatsen mislukt.")
	}
	return m, tea.Println(fmt.Sprintf("Geplaatst als bericht #%d.", id))
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
