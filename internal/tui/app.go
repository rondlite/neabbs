// Package tui is the Bubble Tea session UI: the period-authentic public BBS.
// Inline (non-altscreen) scrolling teletype output with baud emulation,
// the call ritual, a hotkey main menu, boards, file area, and Babbel chat.
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
	"github.com/rondlite/neabbs/internal/chat"
	"github.com/rondlite/neabbs/internal/config"
	"github.com/rondlite/neabbs/internal/content"
	"github.com/rondlite/neabbs/internal/presence"
	"github.com/rondlite/neabbs/internal/store"
	"github.com/rondlite/neabbs/internal/text"
)

const (
	maxLineBytes  = 512 // hard cap on one input line
	rateLimitPerS = 20  // sustained input events per second; excess dropped
	rateBurst     = 256 // bucket size: pastes burst, sustained floods don't

	dailyMinutes = 120 // time-limit theater budget
)

// Deps is everything a session UI needs.
type Deps struct {
	Cfg      config.Config
	Store    store.Store
	Registry *presence.Registry
	Sess     *presence.Session
	Player   *store.Player
	Boards   *board.Engine
	Content  *content.Set
	Chat     *chat.Room
}

type state int

const (
	stateRitual state = iota
	stateHandle
	stateMenu
	stateBoards
	stateFiles
	stateChat
	stateComposeSubject
	stateComposeBody
	stateComposeLevel
	stateDone
)

// ritual steps, in call order.
type ritualStep int

const (
	ritConnect ritualStep = iota
	ritUsername
	ritPassword
	ritGranted
	ritBulletins
	ritCallers
	ritUnread
	ritMenu
)

var (
	amber  = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	dimmed = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
)

// PraatMsg is a one-line shout delivered to every session.
type PraatMsg struct{ Line string }

// Model is the root session model.
type Model struct {
	deps    Deps
	state   state
	step    ritualStep
	input   textinput.Model
	printer printer
	start   time.Time
	width   int

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

	// chat + praat rate limiting (token buckets refilled per minute)
	chatBudget  int
	praatBudget int
	lastMinute  time.Time

	minutesLeft int
	pagedSysop  int // pages this session (cap 2)
}

// promoTickMsg drives the live-repaint poll while viewing a board.
type promoTickMsg struct{}

// minuteTickMsg drives real time accounting.
type minuteTickMsg struct{}

// ritualDelayMsg advances the login theater after a beat of delay.
type ritualDelayMsg struct{ next ritualStep }

func promoTick() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return promoTickMsg{} })
}

func minuteTick() tea.Cmd {
	return tea.Tick(time.Minute, func(time.Time) tea.Msg { return minuteTickMsg{} })
}

func delay(d time.Duration, next ritualStep) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return ritualDelayMsg{next: next} })
}

func today() string { return time.Now().Format("2006-01-02") }

// New builds the root model for a session.
func New(deps Deps) *Model {
	ti := textinput.New()
	ti.CharLimit = maxLineBytes
	ti.Prompt = "> "
	ti.Focus()
	m := &Model{
		deps:        deps,
		input:       ti,
		start:       time.Now(),
		width:       80,
		tokens:      rateBurst,
		lastRefill:  time.Now(),
		chatBudget:  6,
		praatBudget: 1,
		lastMinute:  time.Now(),
		state:       stateRitual,
		step:        ritConnect,
		minutesLeft: dailyMinutes,
	}
	m.printer.cps = m.playerCPS()
	return m
}

// playerCPS maps the player's stored modem speed to reveal chars/sec.
func (m *Model) playerCPS() int {
	if m.deps.Cfg.BaudOff {
		return 0
	}
	speed := m.deps.Player.Speed
	if speed <= 0 {
		speed = 1200
	}
	return speed / 10 // 1200 baud ≈ 120 chars/sec
}

// print pushes a block through the baud-emulated printer.
func (m *Model) print(s string) tea.Cmd { return m.printer.enqueue(s) }

func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.ritual(ritConnect), minuteTick())
}

// ─── ritual ────────────────────────────────────────────────────────────────

// ritual runs one step of the call ritual and schedules the next.
func (m *Model) ritual(step ritualStep) tea.Cmd {
	m.step = step
	p := m.deps.Player
	switch step {
	case ritConnect:
		speed := p.Speed
		if speed <= 0 {
			speed = 1200
		}
		busy := m.deps.Registry.LinesBusy()
		banner := fmt.Sprintf("CONNECT %d\n\n%s\n%s\n\n%s",
			speed,
			amber.Render("NEABBS — heropend na 40 jaar stilte"),
			dimmed.Render("Amsterdam · sinds 1984 · 24 lijnen"),
			fmt.Sprintf("LIJN %s — %d van %d lijnen bezet", lineLabel(m.deps.Sess.Line), busy, presence.Lines))
		return tea.Batch(m.print(banner), delay(700*time.Millisecond, ritUsername))
	case ritUsername:
		if p.Handle == "" {
			m.state = stateHandle
			m.input.Prompt = "Gebruikersnaam (nieuw): "
			return m.print("Nieuwe beller gedetecteerd.\nKies een gebruikersnaam (3-16 tekens, a-z 0-9 _ -).")
		}
		return tea.Batch(
			m.print(fmt.Sprintf("Gebruikersnaam: %s", p.Handle)),
			delay(600*time.Millisecond, ritPassword))
	case ritPassword:
		// Login theater: auth is really the SSH key.
		return tea.Batch(m.print("Wachtwoord: ········"), delay(900*time.Millisecond, ritGranted))
	case ritGranted:
		_ = m.deps.Store.RecordCall(context.Background(), p.Handle, time.Now())
		used, err := m.deps.Store.AddMinutes(context.Background(), p.Fingerprint, today(), 0)
		if err == nil {
			m.minutesLeft = dailyMinutes - used
		}
		lines := []string{"Toegang verleend.", ""}
		if m.minutesLeft > 0 {
			lines = append(lines, fmt.Sprintf("U heeft nog %d minuten vandaag.", m.minutesLeft))
		} else {
			lines = append(lines, "U heeft uw beltegoed voor vandaag verbruikt. De sysop kijkt toe.")
		}
		return tea.Batch(m.print(strings.Join(lines, "\n")), delay(500*time.Millisecond, ritBulletins))
	case ritBulletins:
		if len(m.deps.Content.Bulletins) == 0 {
			return m.ritual(ritCallers)
		}
		var b strings.Builder
		for i, bl := range m.deps.Content.Bulletins {
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString(strings.TrimRight(bl.Body, "\n") + "\n")
		}
		return tea.Batch(m.print(b.String()), delay(300*time.Millisecond, ritCallers))
	case ritCallers:
		return tea.Batch(m.print(m.renderCallers()), delay(300*time.Millisecond, ritUnread))
	case ritUnread:
		return tea.Batch(m.print(m.renderUnread()), delay(300*time.Millisecond, ritMenu))
	case ritMenu:
		m.state = stateMenu
		m.input.Prompt = "Keuze: "
		return m.print(m.renderMenu())
	}
	return nil
}

// renderCallers merges real calls (newest first) with the seeded 1980s list.
func (m *Model) renderCallers() string {
	var b strings.Builder
	b.WriteString("LAATSTE BELLERS\n")
	b.WriteString(strings.Repeat("-", 40) + "\n")
	n := 0
	real, _ := m.deps.Store.LastCallers(context.Background(), 10)
	for _, c := range real {
		b.WriteString(fmt.Sprintf("  %-16s %s\n", c.Handle, c.At.Format("02-01-06 15:04")))
		n++
	}
	for _, c := range m.deps.Content.SeedCallers {
		if n >= 10 {
			break
		}
		b.WriteString(fmt.Sprintf("  %-16s %s\n", c.Handle, c.Date))
		n++
	}
	return b.String()
}

// unreadCounts returns visible boards with their unread message counts.
func (m *Model) unreadCounts() []struct {
	Board  *content.Board
	Unread []board.Msg
} {
	ctx := context.Background()
	var out []struct {
		Board  *content.Board
		Unread []board.Msg
	}
	v := m.viewer()
	for _, b := range m.deps.Boards.VisibleBoards(v) {
		last, err := m.deps.Store.LastRead(ctx, v.Fingerprint, b.ID)
		if err != nil {
			continue
		}
		msgs, err := m.deps.Boards.Messages(ctx, b)
		if err != nil {
			continue
		}
		lvl := 0
		if b.Area == content.AreaThis {
			lvl = v.Level
		}
		var unread []board.Msg
		for _, msg := range msgs {
			if msg.ID > last && msg.Level <= lvl {
				unread = append(unread, msg)
			}
		}
		if len(unread) > 0 {
			out = append(out, struct {
				Board  *content.Board
				Unread []board.Msg
			}{b, unread})
		}
	}
	return out
}

func (m *Model) renderUnread() string {
	counts := m.unreadCounts()
	if len(counts) == 0 {
		return "Geen nieuwe berichten sinds uw laatste bezoek."
	}
	var b strings.Builder
	b.WriteString("NIEUWE BERICHTEN SINDS UW LAATSTE BEZOEK\n")
	b.WriteString(strings.Repeat("-", 40) + "\n")
	for _, c := range counts {
		b.WriteString(fmt.Sprintf("  %-12s %3d nieuw\n", strings.ToUpper(c.Board.ID), len(c.Unread)))
	}
	b.WriteString("\nDruk Q in het menu voor een quickscan.")
	return b.String()
}

// ─── main menu ─────────────────────────────────────────────────────────────

func (m *Model) renderMenu() string {
	now := time.Now().Format("15:04")
	var b strings.Builder
	b.WriteString("\n" + strings.Repeat("=", 62) + "\n")
	b.WriteString(fmt.Sprintf(" NEABBS HOOFDMENU%38s\n", fmt.Sprintf("LIJN %s · %s", lineLabel(m.deps.Sess.Line), now)))
	b.WriteString(strings.Repeat("=", 62) + "\n")
	b.WriteString(" [B] Berichtenboards      [W] Wie is er op de lijnen\n")
	b.WriteString(" [F] Bestanden            [C] Babbelbox\n")
	b.WriteString(" [S] Sysop oproepen       [I] Colofon\n")
	b.WriteString(" [Q] Quickscan nieuwe berichten\n")
	b.WriteString(" [U] Uitloggen\n")
	b.WriteString(dimmed.Render(" Ook: praat <tekst> — roep iets naar alle lijnen") + "\n")
	if m.minutesLeft <= 0 {
		b.WriteString(dimmed.Render(" De sysop tikt op zijn horloge. Uw beltijd is om.") + "\n")
	}
	return b.String()
}

// menuAction executes a main-menu choice (hotkey or number).
func (m *Model) menuAction(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "b", "1":
		m.state = stateBoards
		m.input.Prompt = "Board> "
		m.deps.Sess.SetArea("berichtenboards", false)
		return m, tea.Batch(
			m.print(renderBoardList(m.deps.Boards.VisibleBoards(m.viewer()))),
			promoTick())
	case "f", "2":
		m.state = stateFiles
		m.input.Prompt = "Bestand> "
		m.deps.Sess.SetArea("bestanden", false)
		return m, m.print(renderFileList(m.deps.Content.Files))
	case "w", "3":
		return m, m.print(m.renderWho())
	case "c", "4":
		return m.enterChat()
	case "s", "5":
		return m.pageSysop()
	case "i", "6":
		col := m.deps.Content.Colofon
		if col == "" {
			col = "NEABBS — een eerbetoon. Geen enkele band met het origineel."
		}
		return m, m.print(col)
	case "q", "7":
		return m, m.print(m.quickscan())
	case "u", "8":
		return m.quit()
	}
	return m, nil
}

// menuLine handles a typed line at the main menu (numbered fallback,
// praat, speed upgrade, and — later — hidden commands from YAML).
func (m *Model) menuLine(line string) (tea.Model, tea.Cmd) {
	lower := strings.ToLower(line)
	fields := strings.Fields(lower)
	if len(fields) == 0 {
		return m, m.print(m.renderMenu())
	}
	switch fields[0] {
	case "b", "f", "w", "c", "s", "i", "q", "u", "1", "2", "3", "4", "5", "6", "7", "8":
		if len(fields) == 1 {
			return m.menuAction(fields[0])
		}
	case "praat":
		rest := strings.TrimSpace(line[len("praat"):])
		return m.praat(rest)
	case "2400":
		return m.upgradeSpeed()
	case "logout", "uitloggen":
		return m.quit()
	}
	// Unknown input — identical whether gibberish or a hidden command the
	// player isn't eligible for. Never confirm existence.
	return m, m.print("Onbekende keuze.")
}

// renderWho lists the lines: number, handle, area. Sessions inside THIS
// show only as "lijn bezet" — membership never leaks here.
func (m *Model) renderWho() string {
	var b strings.Builder
	b.WriteString("WIE IS ER OP DE LIJNEN\n")
	b.WriteString(strings.Repeat("-", 44) + "\n")
	for _, s := range m.deps.Registry.All() {
		handle, area, inThis := s.Snapshot()
		switch {
		case inThis:
			b.WriteString(fmt.Sprintf("  LIJN %-3s lijn bezet\n", lineLabel(s.Line)))
		default:
			if handle == "" {
				handle = "(inloggen...)"
			}
			if area == "" {
				area = "hoofdmenu"
			}
			b.WriteString(fmt.Sprintf("  LIJN %-3s %-16s %s\n", lineLabel(s.Line), handle, area))
		}
	}
	return b.String()
}

// praat shouts one line to every session (rate-limit 1/min).
func (m *Model) praat(msg string) (tea.Model, tea.Cmd) {
	msg = text.CleanLine(msg)
	if msg == "" {
		return m, m.print("Gebruik: praat <tekst>")
	}
	m.refillMinuteBudgets()
	if m.praatBudget <= 0 {
		return m, m.print("Rustig aan — één praatje per minuut.")
	}
	m.praatBudget--
	line := fmt.Sprintf("»» %s (lijn %s): %s", m.deps.Player.Handle, lineLabel(m.deps.Sess.Line), msg)
	m.deps.Registry.Broadcast(PraatMsg{Line: line}, nil)
	return m, nil
}

func (m *Model) refillMinuteBudgets() {
	if time.Since(m.lastMinute) >= time.Minute {
		m.chatBudget = 6
		m.praatBudget = 1
		m.lastMinute = time.Now()
	}
}

// upgradeSpeed is the discoverable modem-upgrade command.
func (m *Model) upgradeSpeed() (tea.Model, tea.Cmd) {
	if m.deps.Player.Speed >= 2400 {
		return m, m.print("Uw modem loopt al op 2400 baud.")
	}
	if err := m.deps.Store.SetSpeed(context.Background(), m.deps.Player.Fingerprint, 2400); err != nil {
		return m, m.print("Er knettert iets op de lijn. Probeer later opnieuw.")
	}
	m.deps.Player.Speed = 2400
	m.printer.cps = m.playerCPS()
	return m, m.print("+++ CARRIER RENEGOTIATED +++\nCONNECT 2400\n\nUw modem-upgrade is permanent geregistreerd.")
}

// pageSysop rings the operator. v0: nobody home (period-appropriate wait).
func (m *Model) pageSysop() (tea.Model, tea.Cmd) {
	if m.pagedSysop >= 2 {
		return m, m.print("De bel doet het niet meer. (Max 2 oproepen per sessie.)")
	}
	m.pagedSysop++
	return m, tea.Batch(
		m.print("De sysop wordt opgeroepen... tring... tring..."),
		tea.Tick(3*time.Second, func(time.Time) tea.Msg { return sysopNoAnswerMsg{} }))
}

type sysopNoAnswerMsg struct{}

// quickscan walks all unread messages across boards and marks them read.
func (m *Model) quickscan() string {
	counts := m.unreadCounts()
	if len(counts) == 0 {
		return "Geen nieuwe berichten."
	}
	ctx := context.Background()
	var b strings.Builder
	b.WriteString("QUICKSCAN — alle nieuwe berichten\n")
	for _, c := range counts {
		b.WriteString("\n" + strings.Repeat("=", 62) + "\n")
		b.WriteString(fmt.Sprintf("%s — %s\n", strings.ToUpper(c.Board.ID), c.Board.Name))
		for i := range c.Unread {
			msg := &c.Unread[i]
			b.WriteString(strings.Repeat("-", 62) + "\n")
			b.WriteString(fmt.Sprintf("#%d van %s: %s\n\n", msg.ID, msg.Author, msg.Subject))
			b.WriteString(strings.TrimRight(msg.Body, "\n") + "\n")
			_ = m.deps.Store.SetLastRead(ctx, m.deps.Player.Fingerprint, c.Board.ID, msg.ID)
			if msg.GrantsFlag != "" {
				_, _ = m.deps.Boards.Read(ctx, c.Board.ID, msg.ID, m.viewer())
			}
		}
	}
	b.WriteString("\nEinde quickscan.")
	return b.String()
}

// ─── update loop ───────────────────────────────────────────────────────────

// allow implements the per-session input rate limit (drop excess events).
func (m *Model) allow() bool {
	now := time.Now()
	elapsed := now.Sub(m.lastRefill)
	refill := int(elapsed / (time.Second / rateLimitPerS))
	if refill > 0 {
		m.tokens = min(rateBurst, m.tokens+refill)
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
	case printTickMsg:
		return m, m.printer.tick()
	case ritualDelayMsg:
		if m.state == stateRitual {
			return m, m.ritual(msg.next)
		}
		return m, nil
	case promoTickMsg:
		return m.checkPromotion()
	case minuteTickMsg:
		used, err := m.deps.Store.AddMinutes(context.Background(), m.deps.Player.Fingerprint, today(), 1)
		if err == nil {
			m.minutesLeft = dailyMinutes - used
		}
		return m, minuteTick()
	case sysopNoAnswerMsg:
		return m, m.print("Geen antwoord. De sysop is niet aanwezig.\nLaat een bericht achter op het HULP board.")
	case PraatMsg:
		return m, tea.Println(dimmed.Render(msg.Line))
	case chat.Event:
		if m.state == stateChat {
			return m, tea.Println(msg.Line)
		}
		return m, nil
	case tea.KeyMsg:
		if !m.allow() {
			return m, nil
		}
		return m.key(msg)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *Model) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// The printer owns the screen: any key skips the current block or
	// answers the pager prompt.
	if m.printer.more {
		return m, m.printer.moreKey(msg.String())
	}
	if len(m.printer.current) > 0 {
		return m, m.printer.skip()
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
		switch {
		case m.composing():
			m.resetCompose()
			return m, tea.Println("Geannuleerd.")
		case m.state == stateChat:
			return m.leaveChat()
		case m.state == stateBoards || m.state == stateFiles:
			return m.backToMenu()
		}
		m.input.SetValue("")
		return m, nil
	case tea.KeyEnter:
		line := text.CleanLine(m.input.Value())
		m.input.SetValue("")
		return m.handleLine(line)
	}

	// Main menu: single-keystroke hotkeys when the buffer is empty.
	// Letters that start typed commands (praat, 2400, this, ...) are not
	// hotkeys, so multi-char commands remain typable.
	if m.state == stateMenu && m.input.Value() == "" && len(msg.Runes) == 1 {
		switch r := strings.ToLower(string(msg.Runes[0])); r {
		case "b", "f", "w", "c", "s", "i", "q", "u":
			return m.menuAction(r)
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
	m.state = stateBoards
	m.input.Prompt = "Board> "
}

func (m *Model) backToMenu() (tea.Model, tea.Cmd) {
	m.state = stateMenu
	m.boardID = ""
	m.input.Prompt = "Keuze: "
	m.deps.Sess.SetArea("hoofdmenu", false)
	return m, m.print(m.renderMenu())
}

func (m *Model) handleLine(line string) (tea.Model, tea.Cmd) {
	switch m.state {
	case stateHandle:
		return m.pickHandle(line)
	case stateMenu:
		return m.menuLine(line)
	case stateBoards:
		return m.boardsLine(line)
	case stateFiles:
		return m.filesLine(line)
	case stateChat:
		return m.chatLine(line)
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
	if m.state == stateDone || (m.state != stateBoards && !m.composing()) {
		return m, nil
	}
	fresh, err := m.deps.Store.PlayerByFingerprint(context.Background(), m.deps.Player.Fingerprint)
	if err != nil {
		return m, promoTick()
	}
	oldLevel, oldMember := m.deps.Player.Level, m.deps.Player.ThisMember
	changed := fresh.Level != oldLevel || fresh.ThisMember != oldMember
	*m.deps.Player = *fresh
	if !changed || m.boardID == "" {
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

// ─── handle picker ─────────────────────────────────────────────────────────

func (m *Model) pickHandle(h string) (tea.Model, tea.Cmd) {
	if !text.ValidHandle(h) {
		return m, m.print("Ongeldige naam. 3-16 tekens, alleen a-z 0-9 _ -. Probeer opnieuw.")
	}
	err := m.deps.Store.SetHandle(context.Background(), m.deps.Player.Fingerprint, h)
	if errors.Is(err, store.ErrHandleTaken) {
		return m, m.print("Die naam is al bezet. Probeer een andere.")
	}
	if err != nil {
		return m, m.print("Opslaan mislukt, probeer opnieuw.")
	}
	m.deps.Player.Handle = h
	m.deps.Sess.SetHandle(h)
	m.state = stateRitual
	m.input.Prompt = "> "
	return m, tea.Batch(m.print(fmt.Sprintf("Aangenaam, %s.", h)), delay(500*time.Millisecond, ritPassword))
}

// ─── boards area ───────────────────────────────────────────────────────────

func (m *Model) boardsLine(line string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		if m.boardID != "" {
			l, err := m.deps.Boards.Listing(context.Background(), m.boardID, m.viewer())
			if err == nil {
				return m, m.print(renderListing(l))
			}
		}
		return m, m.print(renderBoardList(m.deps.Boards.VisibleBoards(m.viewer())))
	}
	cmd := strings.ToLower(fields[0])
	arg := ""
	if len(fields) > 1 {
		arg = strings.ToLower(fields[1])
	}
	switch cmd {
	case "terug", "menu":
		return m.backToMenu()
	case "boards":
		return m, m.print(renderBoardList(m.deps.Boards.VisibleBoards(m.viewer())))
	case "board":
		return m.openBoard(arg)
	case "read", "lees":
		return m.readMessage(arg)
	case "post":
		return m.startCompose(0)
	case "reply":
		nr, err := strconv.Atoi(arg)
		if err != nil {
			return m, m.print("Gebruik: reply <nr>")
		}
		return m.startCompose(nr)
	case "status":
		return m, m.print(m.renderStatus())
	case "logout":
		return m.quit()
	}
	// Bare board id opens that board.
	if len(fields) == 1 {
		return m.openBoard(cmd)
	}
	return m, m.print("Onbekende keuze.")
}

func (m *Model) openBoard(id string) (tea.Model, tea.Cmd) {
	if id == "" {
		return m, m.print("Gebruik: board <id>")
	}
	l, err := m.deps.Boards.Listing(context.Background(), id, m.viewer())
	if err != nil {
		return m, m.print("Onbekend board.")
	}
	m.boardID = id
	m.deps.Sess.SetArea("board "+strings.ToUpper(id), l.Board.Area == content.AreaThis)
	return m, m.print(renderListing(l))
}

func (m *Model) renderStatus() string {
	p := m.deps.Player
	up := time.Since(m.start).Round(time.Second)
	out := []string{
		fmt.Sprintf("Gebruiker : %s", p.Handle),
		fmt.Sprintf("Lid sinds : %s", p.CreatedAt.Format("02-01-2006")),
		fmt.Sprintf("Online    : %s", up),
		fmt.Sprintf("Lijn      : %s", lineLabel(m.deps.Sess.Line)),
		fmt.Sprintf("Beltijd   : nog %d minuten vandaag", max(0, m.minutesLeft)),
	}
	// THIS clearance is only ever shown to members (non-members must see
	// zero evidence THIS exists).
	if p.ThisMember {
		out = append(out, fmt.Sprintf("THIS      : niveau %d, %d vlaggen", p.Level, len(p.Flags)))
	}
	return strings.Join(out, "\n")
}

// readMessage handles `read <nr>` in the current board context.
func (m *Model) readMessage(arg string) (tea.Model, tea.Cmd) {
	if m.boardID == "" {
		return m, m.print("Open eerst een board: board <id>")
	}
	nr, err := strconv.Atoi(arg)
	if err != nil {
		return m, m.print("Gebruik: read <nr>")
	}
	msg, err := m.deps.Boards.Read(context.Background(), m.boardID, nr, m.viewer())
	var ec board.ErrClearance
	switch {
	case errors.As(err, &ec):
		// Locked things respond specifically: name the required clearance.
		return m, m.print(fmt.Sprintf("TOEGANG GEWEIGERD — THIS-%d vereist.", ec.Need))
	case err != nil:
		return m, m.print("Geen bericht met dat nummer.")
	}
	_ = m.deps.Store.SetLastRead(context.Background(), m.deps.Player.Fingerprint, m.boardID, msg.ID)
	return m, m.print(renderMessage(m.boardID, msg))
}

// startCompose begins the post/reply composer (ESC cancels).
func (m *Model) startCompose(replyTo int) (tea.Model, tea.Cmd) {
	if m.boardID == "" {
		return m, m.print("Open eerst een board: board <id>")
	}
	b := m.deps.Boards.VisibleBoardByID(m.boardID, m.viewer())
	if b == nil {
		m.boardID = ""
		return m, m.print("Onbekend board.")
	}
	if !b.Writable {
		return m, m.print("Dit board is alleen-lezen.")
	}
	m.compose.replyTo = replyTo
	m.state = stateComposeSubject
	m.input.Prompt = "Onderwerp: "
	return m, m.print("Nieuw bericht. ESC annuleert.")
}

func (m *Model) composeSubject(line string) (tea.Model, tea.Cmd) {
	if line == "" {
		return m, m.print("Onderwerp mag niet leeg zijn.")
	}
	m.compose.subject = line
	m.state = stateComposeBody
	m.input.Prompt = "| "
	return m, m.print("Tekst. Sluit af met '.' op een eigen regel (of ctrl-d).")
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
		return m, m.print("Leeg bericht, geannuleerd.")
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
		return m, m.print(fmt.Sprintf("Kies een niveau van 0 t/m %d.", m.deps.Player.Level))
	}
	return m.submitPost(lvl)
}

func (m *Model) submitPost(level int) (tea.Model, tea.Cmd) {
	id, err := m.deps.Boards.Post(context.Background(), m.boardID, m.viewer(),
		m.compose.subject, strings.Join(m.compose.lines, "\n"), level, m.compose.replyTo)
	m.resetCompose()
	if err != nil {
		if errors.Is(err, board.ErrNoMessage) {
			return m, m.print("Geen bericht met dat nummer.")
		}
		return m, m.print("Plaatsen mislukt.")
	}
	_ = m.deps.Store.SetLastRead(context.Background(), m.deps.Player.Fingerprint, m.boardID, id)
	return m, m.print(fmt.Sprintf("Geplaatst als bericht #%d.", id))
}

// ─── file area ─────────────────────────────────────────────────────────────

func (m *Model) filesLine(line string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(strings.ToLower(line))
	if len(fields) == 0 {
		return m, m.print(renderFileList(m.deps.Content.Files))
	}
	switch fields[0] {
	case "terug", "menu":
		return m.backToMenu()
	case "lees", "read":
		if len(fields) < 2 {
			return m, m.print("Gebruik: lees <nr>")
		}
		nr, err := strconv.Atoi(fields[1])
		if err != nil || nr < 1 || nr > len(m.deps.Content.Files) {
			return m, m.print("Geen bestand met dat nummer.")
		}
		f := &m.deps.Content.Files[nr-1]
		if f.GrantsFlag != "" {
			_ = m.deps.Store.GrantFlags(context.Background(), m.deps.Player.Fingerprint, f.GrantsFlag)
			if fresh, err := m.deps.Store.PlayerByFingerprint(context.Background(), m.deps.Player.Fingerprint); err == nil {
				*m.deps.Player = *fresh
			}
		}
		return m, m.print(renderFile(f))
	case "logout":
		return m.quit()
	}
	return m, m.print("Onbekende keuze.")
}

// ─── babbel (chat) ─────────────────────────────────────────────────────────

func (m *Model) enterChat() (tea.Model, tea.Cmd) {
	m.state = stateChat
	m.input.Prompt = "Babbel> "
	m.deps.Sess.SetArea("babbelbox", false)
	recent := m.deps.Chat.Join(m.deps.Sess)
	var b strings.Builder
	b.WriteString("DE BABBELBOX — praat met alle lijnen. ESC om te vertrekken.\n")
	b.WriteString(strings.Repeat("-", 62) + "\n")
	tail := recent
	if len(tail) > 15 {
		tail = tail[len(tail)-15:]
	}
	for _, l := range tail {
		b.WriteString(l + "\n")
	}
	return m, m.print(strings.TrimRight(b.String(), "\n"))
}

func (m *Model) leaveChat() (tea.Model, tea.Cmd) {
	m.deps.Chat.Leave(m.deps.Sess)
	return m.backToMenu()
}

func (m *Model) chatLine(line string) (tea.Model, tea.Cmd) {
	if line == "" {
		return m, nil
	}
	if strings.EqualFold(line, "/weg") || strings.EqualFold(line, "terug") {
		return m.leaveChat()
	}
	m.refillMinuteBudgets()
	if m.chatBudget <= 0 {
		return m, tea.Println(dimmed.Render("* rustig aan — max 6 berichten per minuut"))
	}
	m.chatBudget--
	m.deps.Chat.Say(m.deps.Sess, line)
	return m, nil
}

// ─── misc ──────────────────────────────────────────────────────────────────

func lineLabel(n int) string {
	if n == 0 {
		return "??"
	}
	return fmt.Sprintf("%d", n)
}

func (m *Model) quit() (tea.Model, tea.Cmd) {
	if m.state == stateChat {
		m.deps.Chat.Leave(m.deps.Sess)
	}
	m.state = stateDone
	bye := m.deps.Content.Goodbye
	if bye == "" {
		bye = "Tot ziens. NEABBS wacht wel weer 40 jaar."
	}
	return m, tea.Sequence(tea.Println("\n"+strings.TrimRight(bye, "\n")+"\n\nNO CARRIER"), tea.Quit)
}

func (m *Model) View() string {
	if m.state == stateDone {
		return ""
	}
	if pv := m.printer.view(); pv != "" {
		return pv
	}
	if m.state == stateRitual {
		return ""
	}
	return m.input.View()
}
