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
	Name     L         `yaml:"name"`
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
	Subject        L      `yaml:"subject"`
	Body           L      `yaml:"body"`
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

// File is one entry in the public file area (bestanden).
type File struct {
	Name         string `yaml:"name"`
	Date         string `yaml:"date"` // display date, freeform period text
	Size         string `yaml:"size"` // display size, freeform ("12K")
	Desc         L      `yaml:"desc"`
	Body         L      `yaml:"body"`
	GrantsFlag   string `yaml:"grants_flag"`   // reading the file sets this flag
	RequiresFlag string `yaml:"requires_flag"` // without it: absent from the list
}

// Effects is what a hidden command or host event does to a player.
type Effects struct {
	SetThisMember bool     `yaml:"set_this_member"`
	GrantFlags    []string `yaml:"grant_flags"`
	GrantLevel    int      `yaml:"grant_level"` // 0 = no promotion
	Broadcast     L        `yaml:"broadcast"`   // {handle} substituted; THIS members only
}

// Host is one node in the THIS world graph (content/hosts/*.yaml).
// Nothing is actually hackable — a host is a content tree dressed as a system.
type Host struct {
	ID           string     `yaml:"id"`
	Address      string     `yaml:"address"` // what the player types after `connect`
	MinLevel     int        `yaml:"min_level"`
	RequiresFlag string     `yaml:"requires_flag"` // set: flag gates visibility instead of level
	Banner       L          `yaml:"banner"`
	Locked       bool       `yaml:"locked"` // requires a successful `crack`
	Crack        *CrackSpec `yaml:"crack"`
	Files        []HostFile `yaml:"files"`
	Mail         []MailMsg  `yaml:"mail"`    // spool read with `mail`
	Netstat      *HostView  `yaml:"netstat"` // connections revealed by `netstat`
	NPC          *NPC       `yaml:"npc"`
	Effects      struct {
		OnFirstCrack *Effects `yaml:"on_first_crack"`
	} `yaml:"effects"`
}

// MailMsg is one message in a host's spool, level-filtered like files.
type MailMsg struct {
	From       string `yaml:"from"`
	Subject    L      `yaml:"subject"`
	Body       L      `yaml:"body"`
	MinLevel   int    `yaml:"min_level"`
	GrantsFlag string `yaml:"grants_flag"` // reading it sets this flag
}

// HostView is a cracked-only readout (e.g. `netstat`): a body plus an
// optional flag it grants, which can make new hosts visible (dynamic graph).
type HostView struct {
	MinLevel   int    `yaml:"min_level"`
	GrantsFlag string `yaml:"grants_flag"`
	Body       L      `yaml:"body"`
}

// NPC is an optional LLM-backed character on a host, reachable via `talk`.
// The NPC may hint toward flags the player holds; a deterministic path to
// every flag must also exist (the LLM is never on the critical path).
type NPC struct {
	Name       string       `yaml:"name"`
	Persona    L            `yaml:"persona"`     // persona prompt (or prompts/ ref via PersonaFile)
	Greeting   L            `yaml:"greeting"`    // shown on `talk`, LLM-free
	Fallback   L            `yaml:"fallback"`    // canned reply when the LLM is unavailable
	KnowsFlags map[string]L `yaml:"knows_flags"` // flag → fact the NPC may reveal if the player holds it
}

// CrackSpec describes how a locked host opens.
type CrackSpec struct {
	Method        string   `yaml:"method"`         // password | wordlist | bluebox | none
	PasswordFlag  string   `yaml:"password_flag"`  // crack succeeds iff player holds this flag
	RequiresFlags []string `yaml:"requires_flags"` // multi-stage: ALL must be held too
	MinLevel      int      `yaml:"min_level"`      // below this: crack refused, names the clearance
	HintOnFail    L        `yaml:"hint_on_fail"`
	TraceSeconds  int      `yaml:"trace_seconds"` // trace timer starts on successful crack
	Sequence      string   `yaml:"sequence"`      // bluebox: canonical MF tone sequence to dial
}

// HostFile is one readable file on a host, level-filtered like messages.
type HostFile struct {
	Name       string `yaml:"name"`
	MinLevel   int    `yaml:"min_level"`
	GrantsFlag string `yaml:"grants_flag"`
	Body       L      `yaml:"body"`
}

// HiddenCommand is a secret input at the main menu. Typing it without the
// required flag yields the exact same error as gibberish — never confirm
// existence.
type HiddenCommand struct {
	Input        string  `yaml:"input"`
	RequiresFlag string  `yaml:"requires_flag"`
	Effects      Effects `yaml:"effects"`
	Response     L       `yaml:"response"`
}

// SeedCaller is one fictional entry padding the "laatste bellers" list.
type SeedCaller struct {
	Handle string `yaml:"handle"`
	Date   string `yaml:"date"` // display string, e.g. "03-11-86 23:41"
}

// Bulletin is one login news screen.
type Bulletin struct {
	Name string
	Body L
}

// Set is all loaded content.
type Set struct {
	Boards         []Board // sorted: public first, then by min_level, then id
	Files          []File
	SeedCallers    []SeedCaller
	Bulletins      []Bulletin // sorted by filename
	Colofon        L
	Goodbye        L
	LoginBanner    string            // content/login-banner.txt (ANSI wordmark, language-neutral)
	HiddenCommands []HiddenCommand   // content/areas/main.yaml
	ThisArrival    L                 // content/this-arrival.txt
	Hosts          []Host            // content/hosts/*.yaml, sorted by min_level then id
	Prompts        map[string]string // content/prompts/*.txt, keyed by basename without extension
}

// HostByAddress returns the host with the given address (case-insensitive),
// or nil.
func (s *Set) HostByAddress(addr string) *Host {
	for i := range s.Hosts {
		if strings.EqualFold(s.Hosts[i].Address, addr) {
			return &s.Hosts[i]
		}
	}
	return nil
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
	// File area.
	if buf, err := os.ReadFile(filepath.Join(dir, "files.yaml")); err == nil {
		var wrapper struct {
			Files []File `yaml:"files"`
		}
		if err := yaml.Unmarshal(buf, &wrapper); err != nil {
			return nil, fmt.Errorf("files.yaml: %w", err)
		}
		set.Files = wrapper.Files
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	// Seeded "laatste bellers".
	if buf, err := os.ReadFile(filepath.Join(dir, "callers.yaml")); err == nil {
		var wrapper struct {
			Callers []SeedCaller `yaml:"callers"`
		}
		if err := yaml.Unmarshal(buf, &wrapper); err != nil {
			return nil, fmt.Errorf("callers.yaml: %w", err)
		}
		set.SeedCallers = wrapper.Callers
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	// Bulletins: content/bulletins/*.txt in filename order.
	bulletinsDir := filepath.Join(dir, "bulletins")
	if entries, err := os.ReadDir(bulletinsDir); err == nil {
		names := []string{}
		for _, e := range entries {
			// Skip the .en.txt siblings; they're paired with their NL base below.
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".txt") && !strings.HasSuffix(e.Name(), ".en.txt") {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		for _, n := range names {
			buf, err := os.ReadFile(filepath.Join(bulletinsDir, n))
			if err != nil {
				return nil, err
			}
			body := L{NL: string(buf)}
			// content/bulletins/foo.en.txt is the English translation of foo.txt.
			enName := strings.TrimSuffix(n, ".txt") + ".en.txt"
			if enBuf, err := os.ReadFile(filepath.Join(bulletinsDir, enName)); err == nil {
				body.EN = string(enBuf)
			}
			set.Bulletins = append(set.Bulletins, Bulletin{Name: n, Body: body})
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	// Hosts: content/hosts/*.yaml, one host per file.
	hostsDir := filepath.Join(dir, "hosts")
	if entries, err := os.ReadDir(hostsDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || (!strings.HasSuffix(e.Name(), ".yaml") && !strings.HasSuffix(e.Name(), ".yml")) {
				continue
			}
			path := filepath.Join(hostsDir, e.Name())
			buf, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			var h Host
			if err := yaml.Unmarshal(buf, &h); err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			set.Hosts = append(set.Hosts, h)
		}
		sort.Slice(set.Hosts, func(i, j int) bool {
			if set.Hosts[i].MinLevel != set.Hosts[j].MinLevel {
				return set.Hosts[i].MinLevel < set.Hosts[j].MinLevel
			}
			return set.Hosts[i].ID < set.Hosts[j].ID
		})
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	// System prompts: content/prompts/*.txt (LLM system prompts live in
	// content, not Go code).
	set.Prompts = map[string]string{}
	promptsDir := filepath.Join(dir, "prompts")
	if entries, err := os.ReadDir(promptsDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".txt") {
				continue
			}
			buf, err := os.ReadFile(filepath.Join(promptsDir, e.Name()))
			if err != nil {
				return nil, err
			}
			key := strings.TrimSuffix(e.Name(), ".txt")
			set.Prompts[key] = string(buf)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	// Main-menu area definition (hidden commands).
	if buf, err := os.ReadFile(filepath.Join(dir, "areas", "main.yaml")); err == nil {
		var wrapper struct {
			HiddenCommands []HiddenCommand `yaml:"hidden_commands"`
		}
		if err := yaml.Unmarshal(buf, &wrapper); err != nil {
			return nil, fmt.Errorf("areas/main.yaml: %w", err)
		}
		set.HiddenCommands = wrapper.HiddenCommands
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	// Single-text screens. Localized ones read a foo.en.txt sibling for EN.
	loadL := func(base string, dst *L) error {
		buf, err := os.ReadFile(filepath.Join(dir, base+".txt"))
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		dst.NL = string(buf)
		if enBuf, err := os.ReadFile(filepath.Join(dir, base+".en.txt")); err == nil {
			dst.EN = string(enBuf)
		}
		return nil
	}
	for _, t := range []struct {
		base string
		dst  *L
	}{
		{"colofon", &set.Colofon},
		{"goodbye", &set.Goodbye},
		{"this-arrival", &set.ThisArrival},
	} {
		if err := loadL(t.base, t.dst); err != nil {
			return nil, err
		}
	}
	// The login banner is a language-neutral ANSI wordmark.
	if buf, err := os.ReadFile(filepath.Join(dir, "login-banner.txt")); err == nil {
		set.LoginBanner = string(buf)
	} else if !os.IsNotExist(err) {
		return nil, err
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
				lower := strings.ToLower(m.Subject.AllText() + "\n" + m.Body.AllText())
				for _, other := range s.Boards {
					if other.Area == AreaThis && strings.Contains(lower, strings.ToLower(other.ID)) {
						fail("board %s msg %d: public content references THIS board id %q", b.ID, m.ID, other.ID)
					}
				}
			}
		}
	}
	// File area: names must be present and unique; public files must not
	// reference THIS boards by id.
	fileNames := map[string]bool{}
	for i := range s.Files {
		f := &s.Files[i]
		if f.Name == "" {
			fail("files.yaml entry #%d: missing name", i)
			continue
		}
		if fileNames[f.Name] {
			fail("files.yaml: duplicate file name %q", f.Name)
		}
		fileNames[f.Name] = true
		lower := strings.ToLower(f.Desc.AllText() + "\n" + f.Body.AllText())
		for _, b := range s.Boards {
			if b.Area == AreaThis && strings.Contains(lower, strings.ToLower(b.ID)) {
				fail("files.yaml %s: public file references THIS board id %q", f.Name, b.ID)
			}
		}
	}
	// Hosts: unique ids/addresses, sane levels, valid crack specs.
	hostIDs := map[string]bool{}
	hostAddrs := map[string]bool{}
	for i := range s.Hosts {
		h := &s.Hosts[i]
		if h.ID == "" || h.Address == "" {
			fail("host #%d: missing id or address", i)
			continue
		}
		if hostIDs[h.ID] {
			fail("host %s: duplicate id", h.ID)
		}
		hostIDs[h.ID] = true
		addr := strings.ToLower(h.Address)
		if hostAddrs[addr] {
			fail("host %s: duplicate address %q", h.ID, h.Address)
		}
		hostAddrs[addr] = true
		if h.MinLevel < 0 || h.MinLevel > 9 {
			fail("host %s: min_level %d out of range", h.ID, h.MinLevel)
		}
		if h.Locked && h.Crack == nil {
			fail("host %s: locked without a crack spec (permanently dead content)", h.ID)
		}
		if h.Crack != nil {
			switch h.Crack.Method {
			case "password", "wordlist", "bluebox", "none":
			default:
				fail("host %s: crack method %q (want password|wordlist|bluebox|none)", h.ID, h.Crack.Method)
			}
			if h.Crack.Method == "password" && h.Crack.PasswordFlag == "" {
				fail("host %s: password crack without password_flag", h.ID)
			}
			if h.Crack.Method == "bluebox" && h.Crack.Sequence == "" {
				fail("host %s: bluebox crack without sequence", h.ID)
			}
			if h.Crack.Method != "bluebox" && h.Crack.Sequence != "" {
				fail("host %s: sequence set on non-bluebox crack method %q", h.ID, h.Crack.Method)
			}
			if h.Crack.MinLevel < 0 || h.Crack.MinLevel > 9 {
				fail("host %s: crack min_level %d out of range", h.ID, h.Crack.MinLevel)
			}
		}
		fileNames := map[string]bool{}
		for j := range h.Files {
			f := &h.Files[j]
			if f.Name == "" {
				fail("host %s file #%d: missing name", h.ID, j)
				continue
			}
			if fileNames[f.Name] {
				fail("host %s: duplicate file %q", h.ID, f.Name)
			}
			fileNames[f.Name] = true
			if f.MinLevel < 0 || f.MinLevel > 9 {
				fail("host %s file %s: min_level %d out of range", h.ID, f.Name, f.MinLevel)
			}
		}
		for j := range h.Mail {
			if h.Mail[j].MinLevel < 0 || h.Mail[j].MinLevel > 9 {
				fail("host %s mail #%d: min_level %d out of range", h.ID, j, h.Mail[j].MinLevel)
			}
		}
		if ns := h.Netstat; ns != nil && (ns.MinLevel < 0 || ns.MinLevel > 9) {
			fail("host %s netstat: min_level %d out of range", h.ID, ns.MinLevel)
		}
	}

	// Public content must never reference THIS hosts by id or address.
	for i := range s.Boards {
		b := &s.Boards[i]
		if b.Area != AreaPublic {
			continue
		}
		for j := range b.Messages {
			lower := strings.ToLower(b.Messages[j].Subject.AllText() + "\n" + b.Messages[j].Body.AllText())
			for k := range s.Hosts {
				if strings.Contains(lower, strings.ToLower(s.Hosts[k].ID)) ||
					strings.Contains(lower, strings.ToLower(s.Hosts[k].Address)) {
					fail("board %s msg %d: public content references THIS host %q", b.ID, b.Messages[j].ID, s.Hosts[k].ID)
				}
			}
		}
	}
	for i := range s.Files {
		lower := strings.ToLower(s.Files[i].Desc.AllText() + "\n" + s.Files[i].Body.AllText())
		for k := range s.Hosts {
			if strings.Contains(lower, strings.ToLower(s.Hosts[k].ID)) ||
				strings.Contains(lower, strings.ToLower(s.Hosts[k].Address)) {
				fail("files.yaml %s: public file references THIS host %q", s.Files[i].Name, s.Hosts[k].ID)
			}
		}
	}

	// Flag reachability: every required flag must be grantable somewhere
	// (a file read, a board message read, a host file read, a crack effect,
	// or a hidden command's effects).
	grantable := map[string]bool{}
	for i := range s.Files {
		if f := s.Files[i].GrantsFlag; f != "" {
			grantable[f] = true
		}
	}
	for i := range s.Boards {
		for j := range s.Boards[i].Messages {
			if f := s.Boards[i].Messages[j].GrantsFlag; f != "" {
				grantable[f] = true
			}
		}
	}
	for i := range s.HiddenCommands {
		for _, f := range s.HiddenCommands[i].Effects.GrantFlags {
			grantable[f] = true
		}
	}
	for i := range s.Hosts {
		for j := range s.Hosts[i].Files {
			if f := s.Hosts[i].Files[j].GrantsFlag; f != "" {
				grantable[f] = true
			}
		}
		for j := range s.Hosts[i].Mail {
			if f := s.Hosts[i].Mail[j].GrantsFlag; f != "" {
				grantable[f] = true
			}
		}
		if ns := s.Hosts[i].Netstat; ns != nil && ns.GrantsFlag != "" {
			grantable[ns.GrantsFlag] = true
		}
		if fc := s.Hosts[i].Effects.OnFirstCrack; fc != nil {
			for _, f := range fc.GrantFlags {
				grantable[f] = true
			}
		}
	}
	for i := range s.Files {
		if f := s.Files[i].RequiresFlag; f != "" && !grantable[f] {
			fail("files.yaml %s: requires_flag %q is unreachable (nothing grants it)", s.Files[i].Name, f)
		}
	}
	for i := range s.HiddenCommands {
		hc := &s.HiddenCommands[i]
		if hc.Input == "" {
			fail("areas/main.yaml hidden_command #%d: missing input", i)
		}
		if f := hc.RequiresFlag; f != "" && !grantable[f] {
			fail("areas/main.yaml %q: requires_flag %q is unreachable (this_invite chain broken?)", hc.Input, f)
		}
		if lvl := hc.Effects.GrantLevel; lvl < 0 || lvl > 9 {
			fail("areas/main.yaml %q: grant_level %d out of range", hc.Input, lvl)
		}
	}
	for i := range s.Hosts {
		h := &s.Hosts[i]
		if f := h.RequiresFlag; f != "" && !grantable[f] {
			fail("host %s: requires_flag %q is unreachable", h.ID, f)
		}
		if h.Crack != nil && h.Crack.PasswordFlag != "" && !grantable[h.Crack.PasswordFlag] {
			fail("host %s: password_flag %q is dangling (nothing grants it)", h.ID, h.Crack.PasswordFlag)
		}
		if h.Crack != nil {
			for _, f := range h.Crack.RequiresFlags {
				if !grantable[f] {
					fail("host %s: crack requires_flags %q is dangling (nothing grants it)", h.ID, f)
				}
			}
		}
		if fc := h.Effects.OnFirstCrack; fc != nil && (fc.GrantLevel < 0 || fc.GrantLevel > 9) {
			fail("host %s: on_first_crack grant_level %d out of range", h.ID, fc.GrantLevel)
		}
	}

	// Promotion gaps: promotions come only from effects. Every level from 1
	// up to the highest granted level must be reachable by some effect,
	// otherwise content above the gap is dead.
	granted := map[int]bool{}
	for i := range s.HiddenCommands {
		if lvl := s.HiddenCommands[i].Effects.GrantLevel; lvl > 0 {
			granted[lvl] = true
		}
	}
	maxLevel := 0
	for i := range s.Hosts {
		if fc := s.Hosts[i].Effects.OnFirstCrack; fc != nil && fc.GrantLevel > 0 {
			granted[fc.GrantLevel] = true
		}
	}
	for lvl := range granted {
		if lvl > maxLevel {
			maxLevel = lvl
		}
	}
	for lvl := 1; lvl <= maxLevel; lvl++ {
		if !granted[lvl] {
			fail("promotion gap: no effect grants level %d (but level %d exists)", lvl, maxLevel)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("content lint:\n  %s", strings.Join(errs, "\n  "))
	}
	return nil
}
