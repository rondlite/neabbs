# NEABBS — SSH BBS Hacking Game (Project Brief)

You are building **NEABBS**, a multiplayer game served over real SSH, styled as a
Dutch 1980s BBS that has "reopened after 40 years of silence". Players
`ssh neabbs.com` and get a Bubble Tea TUI (never a shell).

The world has **two layers**:

1. **NEABBS public BBS** — what every caller lands in. A friendly, innocent,
   menu-driven Dutch bulletin board: public message boards, a file area, online
   user list, one-line chat. Fully usable as a social hangout by people who never
   dig deeper. This layer is also the camouflage.
2. **THIS** ("The Hacker's Information System") — a hidden section buried inside
   the BBS. Its existence is not advertised anywhere; *discovering that it exists
   and getting in is the first puzzle of the game.* Inside, players climb
   clearance levels **THIS-0 → THIS-9** by exploring a scripted node graph,
   reading level-gated message boards, and "hacking" hosts. Nothing is actually
   hackable — everything is a content tree dressed as a system.

The two layers should *feel* different: the public BBS is menu-driven, polite,
numbered choices, lots of hand-holding. THIS drops you into a raw command
prompt with no menus and an incomplete `help`. Crossing that threshold should
feel like stepping through a door.

Read this whole file before writing code. Work in the phases listed at the
bottom, committing after each phase. **The near-term target is v0: a complete,
deployable BBS with the level-gated board engine fully implemented (phases
1–3).** THIS and the hacking layer come after v0 is live. Ask before deviating
from the security requirements.

---

## 1. Tech stack (fixed decisions — do not substitute)

- **Go 1.22+**, single static binary.
- **SSH server:** `github.com/charmbracelet/wish` (on `gliderlabs/ssh`).
- **TUI:** `github.com/charmbracelet/bubbletea` + `lipgloss` via Wish's bubbletea middleware.
- **Persistence:** SQLite via `modernc.org/sqlite` (pure Go, no CGO — binary must
  cross-compile for ARM64). Wrap all storage behind a `Store` interface so Redis
  can be swapped in later. One DB file, WAL mode.
- **Content:** all game content loaded from YAML files in `content/` at startup
  (`gopkg.in/yaml.v3`). Content is data, never code. Hot-reload on SIGHUP is a
  nice-to-have, not required.
- **LLM:** optional OpenAI-compatible chat-completions client pointing at
  `LLM_BASE_URL` / `LLM_MODEL` / `LLM_API_KEY` env vars (this will be a local vLLM
  gateway). The game MUST be fully playable with the LLM disabled
  (`LLM_BASE_URL` unset) — LLM output is flavor only, never on the critical path.
- **Config:** env vars only. `NEABBS_LISTEN` (default `:2222`), `NEABBS_DB`
  (default `./neabbs.db`), `NEABBS_HOSTKEY` (default `./hostkey`),
  `NEABBS_CONTENT` (default `./content`), plus the LLM vars above.

## 2. Security requirements (non-negotiable)

The SSH server is the game. There is **no shell, no exec, no filesystem access**.

- Public-key auth only; **accept any key**. The SHA256 fingerprint of the pubkey
  IS the player identity (auto-creates a player row on first connect). No
  passwords, no keyboard-interactive.
- Explicitly **reject**: `exec` requests, SFTP/SCP subsystems, local and remote
  port forwarding (`direct-tcpip`, `tcpip-forward`), agent forwarding, X11.
  Log every rejected attempt with fingerprint + remote IP.
- Limits: max 3 concurrent sessions per fingerprint, global cap 200 sessions,
  idle timeout 15 min, hard session TTL 4 h, per-session input rate limit
  (~20 events/sec, drop excess), max line length 512 bytes.
- Treat all player input as hostile: strip/reject control characters except the
  ones Bubble Tea handles; never echo raw input into ANSI output unescaped
  (terminal-escape injection into other players' screens via board posts or
  handles is the #1 bug class here — sanitize handles and post bodies on write).
- Player handles: 3–16 chars, `[a-z0-9_-]`, unique, chosen on first login.
- The binary must run fine as non-root with a read-only rootfs (DB and hostkey
  paths are the only writes). Provide a distroless Dockerfile (multi-stage,
  `gcr.io/distroless/static`, nonroot user).

## 3. Core domain model

### Player
`fingerprint (pk)`, `handle`, `this_member (bool, default false)`,
`level (0–9, meaningful only when this_member)`, `created_at`, `last_seen`,
`flags` (JSON set of string flags, e.g. `found_node9_hint`), `banned (bool)`.

Everyone is a full citizen of the public BBS from first login. `this_member`
flips permanently when the player finds the entrance (see below); level starts
at 0 at that moment. Non-members must see **zero** evidence in the UI that THIS
exists: no THIS-level in the status bar, no THIS boards listed, no redacted
stubs on public boards, no THIS commands recognized.

### The public BBS layer
Menu-driven areas, defined in `content/areas/*.yaml`. The main menu offers
numbered choices (classic BBS style): message boards, file area (`bestanden`),
online users, chat, colophon, logout. Public boards use the same board/message
model as THIS boards but with `area: public` — they are writable social spaces
and are **not** level-gated (all messages level 0; the redaction machinery
simply never triggers here).

The **file area** is a flat list of downloadable-looking text files (`lees <nr>`
displays them in a pager). Files can `grants_flag` on read, exactly like host
files — this is where the THIS discovery chain lives.

**The entrance to THIS:** hidden command(s) at the main menu, defined in YAML:

```yaml
# content/areas/main.yaml
hidden_commands:
  - input: "this"                  # what the player must type at the menu
    requires_flag: "this_invite"   # without it: play dumb ("Onbekende keuze.")
    effects:
      set_this_member: true
      grant_flags: ["entered_this"]
    response: |
      ... even geduld ...
      DOORVERBINDEN NAAR: THIS
```

Two ways in, both must work: (a) the in-game clue chain that grants
`this_invite`, and (b) word-of-mouth — a player who is told "type `this` at the
menu" still needs the flag, so the chain can't be skipped entirely, but the
*final* clue should be findable quickly once you know what you're looking for.
Typing the hidden command without the flag returns the exact same error as any
gibberish input. Never confirm existence.

### Node graph (THIS only)
Hosts exist only inside THIS. The world is a graph of **hosts** the player can
`connect` to. Each host has gated content. Everything comes from YAML:

```yaml
# content/hosts/gemeente.yaml
id: gemeente-vax
address: "vax.gemeente.nl"        # what the player types after `connect`
min_level: 1                       # below this: host not even listed by `scan`
banner: |
  GEMEENTE AMSTERDAM — VAX/VMS V4.7
  Onbevoegd gebruik is strafbaar.
locked: true                       # requires a successful `crack`
crack:
  method: password                 # password | wordlist | none
  password_flag: "gemeente_pw"     # succeeds only if player has this flag
  hint_on_fail: "TOEGANG GEWEIGERD — wachtwoord vereist (hint: zie board #147)"
  trace_seconds: 90                # trace timer starts on successful crack
files:
  - name: "personeel.txt"
    min_level: 1
    body: |
      ...plain text, may contain clues...
  - name: "modemlijst.dat"
    min_level: 3
    grants_flag: "found_modemlist" # reading it sets a flag
    body: "..."
effects:
  on_first_crack:
    grant_level: 2                 # promotions come from effects like this
    grant_flags: ["cracked_gemeente"]
    broadcast: "{handle} is binnengedrongen bij vax.gemeente.nl"
```

Rules:
- `scan` lists only hosts with `min_level <= player.level` (plus any granted by
  flags via an optional `requires_flag` field).
- `crack` on a `password` host succeeds iff the player has `password_flag`;
  the *acquisition* of that flag happens elsewhere (a file, a board post read,
  an LLM puzzle). This is the "lookup wearing a ski mask" mechanic.
- Promotions are **only** granted via `effects` — never hardcoded.
- Every locked thing must respond specifically, not generically (e.g. name the
  required clearance). Dead ends should teach.

### Trace timer
On successful `crack` of a host with `trace_seconds`, start a countdown rendered
in the TUI status bar (`TRACE ACTIEF — 0:47`). If it hits zero before the player
runs `disconnect`: kick to home node, apply cooldown (host locked again for that
player for 10 min), flavor message ("VERBINDING VERBROKEN — je bent bijna
getraceerd"). No level loss in v1. Implement as a per-session goroutine posting
tick messages into the Bubble Tea program; must be cancelled cleanly on
disconnect/session end.

### Boards & messages (the signature mechanic)
**Build this engine first and build it once** — it is the core of the game and
it ships in v0 with the public BBS, before THIS exists. Every board runs on the
same level-aware engine; public boards simply operate with everything at
level 0, so the redaction machinery never triggers there in normal play.

Boards carry an `area` (`public` or `this`). THIS boards are level-gated, and
**individual messages carry their own level**. Public boards reuse the same
model with everything at level 0 and are visible to all players; THIS boards
are visible only to members.

To keep the mechanic honest in v0 (not just dormant code), include one
`area: this` test board in seed content from day one — even before the THIS
world exists, it exercises the full machinery (membership visibility, per-
message levels, redacted stubs, hidden count, post-level rules) behind the
membership flag, and `neabbs admin promote` lets you playtest every level of
the rendering without the game around it.

```yaml
# content/boards/this.yaml
id: this-board
name: "THIS BOARD"
area: this
min_level: 0
messages:
  - id: 142
    author: "sysop"
    level: 0
    subject: "welkom nieuwe leden"
    body: "..."
  - id: 152
    author: "phantom"
    level: 6
    subject: "de echte ingang naar node 9"
    body: "..."
    grants_flag: "node9_hint"      # reading it (once eligible) sets a flag
```

Rendering rules (this is the heart of the game — get it right):
- List view shows ALL messages the player's level lets them *see exist*:
  readable ones normally; ones above their level as a **redacted stub** —
  subject may be shown or `████`-blocked per message (`subject_visible: bool`,
  default true), author blocked if `author_visible: false`, and a `[THIS-N]`
  tag showing the required level.
- Below the list: `N berichten verborgen boven jouw niveau` counting fully
  hidden ones (boards the player can't see don't exist at all in `boards` list).
- **Player posting:** players can post and reply on boards with
  `writable: true`. A post's level = the author's level at post time (author
  may lower it, never raise it). This makes spoilers structurally unable to
  flow downward.
- Replies to messages above your level are allowed (`reply 152`) — you see only
  the subject, and your reply threads under it. (Emergent paranoia feature.)
- On promotion, if the player is viewing a board, repaint it live so redacted
  posts visibly resolve. This unlock moment is the game's core dopamine hit —
  if feasible, animate the reveal (brief frame-by-frame un-redaction).

### Sub-boards
Same YAML shape, higher `min_level`. Boards above the player's level are
entirely absent from listings — discovering that `#phreak` exists is itself
content (mention it in files/posts).

## 4. Interaction model (v1)

Two distinct modes, deliberately different in feel:

### Public BBS mode (period-authentic, scrolling)
**Not a full-screen TUI.** The public BBS runs in Bubble Tea *inline* mode: a
scrolling teletype-style stream, exactly like a real 80s board. No status bar.
Menus print and scroll away; long output pauses on `-- Meer? (J/n) --` pager
prompts. Menus are **single-keystroke hotkeys** (press `B` for boards, no
Enter), with a numbered fallback.

**Baud emulation (required, core to the vibe):** all public-BBS output is
throttled to ~120 chars/sec (1200 baud), implemented as a rate-limited writer
between renderer and session. Pressing any key mid-draw skips to the end of
the current output (authentic — everyone did this). A discoverable command
`2400` doubles a player's speed permanently ("modem-upgrade" as an unlockable;
store on player). Hard-off env var `NEABBS_BAUD=0` for development.

**The call ritual, in order:**
1. `CONNECT 1200` banner (with the player's stored speed).
2. Login theater. Auth is really the SSH key, but print `Gebruikersnaam:` and
   echo the known handle, then a fake `Wachtwoord:` that accepts anything with
   a beat of delay (first-time callers get the handle picker instead).
3. **Bulletins** — 1–2 news screens (`content/bulletins/*.txt`, sysop-dated),
   paged, skippable.
4. **Laatste bellers** — the last 10 callers with timestamps (store per login).
5. **Nieuwe berichten sinds uw laatste bezoek** — per-player high-water mark
   per board; show counts, offer `Q` quickscan that walks all unread messages
   across boards. This is the core loop of a real BBS; per-player last-read
   pointers are a first-class DB concern, not an afterthought.
6. Main menu.

Also on the menu:
- `P` **Page de sysop** — rings the operator. If an admin session is attached,
  break into live chat; otherwise, after a period-appropriate wait: "De sysop
  is niet aanwezig." (Later phase: LLM answers *sometimes*, as the sysop,
  grumpy about the hour. Rate-limit 2 pages/day/player.)
- Daily time-limit theater: session shows "U heeft nog NN minuten vandaag"
  at login and in prompts occasionally. Generous (120 min/day), decremented
  for real, but in v1 hitting 0 only triggers increasingly pointed sysop
  warnings — never an actual kick.
- **De lijnen (24):** the real NEABBS ran 24 phone lines — multi-user was its
  identity, so the live social layer is heritage, not compromise. Login
  assigns the lowest free line number 1–24; the banner shows
  `LIJN 7 — 12 van 24 lijnen bezet`. If more than 24 callers are on, extra
  sessions still connect but get `LIJN ??` and a grumbling sysop notice about
  the phone company ("PTT belooft al maanden extra lijnen"). The user list is
  titled "wie is er op de lijnen" and shows line number, handle, and what
  area they're in (board names only — never reveals THIS presence; members
  inside THIS show as "lijn bezet").
- `praat <tekst>` — one-line message to all lines (rate-limit 1/min), kept
  for drive-by shouts.
- `B` **Babbel** — a proper multi-user chat room (this was the marquee
  feature of a 24-line board; it deserves first-class treatment). One shared
  room in v1: scrolling live messages, join/leave notices with line numbers
  ("* lijn 4 (wodan) komt binnen"), input line at the bottom, ESC to leave.
  Rate-limit 6 msgs/min/player. Messages are ephemeral (not stored beyond a
  200-line in-memory scrollback). Sanitization rules from §2 apply in full.
- Logout prints a goodbye screen, then `NO CARRIER`. Always `NO CARRIER`.

Unknown input: a polite "Onbekende keuze." — identical whether the input is
gibberish or a hidden command the player isn't eligible for.

### THIS mode (command prompt, full-screen)
Entered via the hidden command; thereafter members get a `THIS` option on the
main menu (the door stays discovered once found). **Full-screen** Bubble Tea
altscreen with status bar — the in-fiction justification is that THIS runs
"custom terminal software" the old hackers built, so the interface generation-
jump is itself lore. No baud throttle inside THIS (their software was better).
Raw prompt, no menus. Case-insensitive commands, Dutch-flavored aliases
welcome. Unknown commands get period-appropriate errors, occasionally snarky.

- `help` — deliberately incomplete; lists ~6 basic commands. Advanced commands
  (`crack`, `trace`, `talk`) are *discovered* in files and posts, not documented.
- `scan` — list reachable hosts for your level/flags.
- `connect <address>` / `disconnect`
- `ls` / `cat <file>` — files on the current host (level-filtered like messages).
- `crack` — attempt to unlock current host.
- `boards` / `board <id>` / `read <msg-id>` / `post` / `reply <msg-id>`
  (post/reply open a minimal multi-line composer; ESC cancels, ctrl-d submits).
- `who` — handles + THIS-levels of *members* currently online (inside THIS,
  you see members; the public user list never shows who is a member).
- `wall <text>` — one-line shout to members currently inside THIS (1/min).
- `status` — your handle, level, flags count, uptime.
- `talk` — only on hosts with an `npc` block; opens LLM chat (see §5).
- `exit` — back to the public BBS menu. `logout` — disconnect entirely.

Rendering: public mode is inline/scrolling with the baud-throttled writer,
amber-on-black. THIS mode is full-screen altscreen, green-on-black, top status
bar (handle, THIS-level, current host, trace timer when active), main pane +
command input line. Keep everything 80-column friendly. Support terminal
resize in THIS mode; in public mode just wrap at 80.

## 5. LLM integration (flavor only, never blocking)

All LLM calls: 10 s timeout, graceful degradation to canned fallback text,
never gate progression on an LLM response.

1. **NPC `talk`** — hosts may define an `npc` block (name, persona prompt,
   `knows_flags: {flag: "fact the NPC may reveal"}`). Chat turns go to the LLM
   with a system prompt built from persona + the facts for flags the player
   holds. Hard cap 20 turns/session, per-player 60 turns/day. The NPC can
   *hint* toward flags but a deterministic path to every flag must also exist.
2. **Board texture generator** — a CLI subcommand (`neabbs genposts --board X
   --level N --count 20`) that calls the LLM to draft atmospheric filler posts
   as YAML for human review. Offline authoring tool, never runs in-game.
3. **Nightly news** (stretch) — cron-style goroutine posting one LLM-written
   news item to the public board per day, seeded with which nodes opened
   recently. Behind env flag `NEABBS_NEWS=1`.

System prompts live in `content/prompts/*.txt`, not in Go code.

## 6. Seed content (author this too — in Dutch, period-authentic)

Write an initial `content/` set: a welcoming public BBS, a discovery chain of
~15–20 min, then a complete THIS-0 → THIS-3 arc of ~45–60 min.

**Public layer:**
- Login banner: "NEABBS — heropend na 40 jaar stilte" framing, `CONNECT 1200`
  opener, login theater, first-run handle picker.
- Two seed bulletins (sysop-voiced: re-opening announcement + house rules),
  a seeded "laatste bellers" list with fictional 1980s handles and dates that
  make the 40-year gap visible in the timestamps (last real call: 1986).
- Four public boards: `ALGEMEEN` (welcome/chatter), `COMPUTERS & TECHNIEK`,
  `MARKT` (retro for-sale posts: modems, een MSX-2, "z.g.a.n."), `HULP`.
  ~8–10 seeded posts each; boards writable so the public layer works as a
  real hangout. Nothing here may reference THIS by name.
- File area with ~10 period texts (modem handleidingen, ASCII art, a zine
  fragment, a "netiquette" file) — most are pure atmosphere.
- Colophon (fan tribute to the original Neabbs/THIS, no affiliation, credits).

**The discovery chain (public → THIS), three beats:**
1. A file in the file area is subtly *misfiled* — e.g. `sysop-notities.txt`
   clearly not meant to be public, mentioning "de oude sectie" being sealed
   after an incident, and a name.
2. That name posts once, cryptically, on `ALGEMEEN` — replying to their thread
   (or reading a follow-up) grants a flag and points at one more file.
3. The final file spells out the ritual — what to type at the main menu — and
   grants `this_invite`. Total: two flags plus the hidden command. Solvable
   solo; fast when word-of-mouth tells you where to look.

**THIS layer (unchanged in spirit):**
- Arrival text: short, cold, different register from the public BBS.
- **THIS BOARD** with ~15 messages: welcome posts, flavor, two real clues,
  plus 6–8 redacted high-level stubs (some subjects visible and tantalizing,
  some fully blocked) so the iceberg is felt from minute one.
- 5 hosts: one open (tutorial: teaches `ls`/`cat`, contains the word `crack`
  in a readme), two password-gated (clue chains via files ↔ board posts),
  one with an NPC (old sysadmin "beheerder", grumpy, knows one password), one
  visible-but-uncrackable teaser gated at THIS-5 that names its clearance.
- `#phreak` sub-board at THIS-3 as the reward for finishing the arc, containing
  hooks/foreshadowing for THIS-4+ content that doesn't exist yet.

**Content-lint at startup:** fail fast on unreachable flags, promotion gaps
(any level with no path to reach it), dangling `password_flag`s, message ID
collisions, and — new — any `area: public` content that references THIS
boards/hosts by id, and any path where `this_invite` is unreachable.

## 7. Testing & acceptance

- Unit tests: clearance filter (messages + files + hosts), crack/flag logic,
  promotion effects, handle sanitization (include ANSI-escape injection cases),
  YAML loader + content lint.
- Integration test: spin up the Wish server on a random port, connect with
  `golang.org/x/crypto/ssh` as a client, script the full path: public menu →
  discovery chain → enter THIS → THIS-0→THIS-1.
- Non-member invisibility test: a fresh player must get byte-identical error
  output for the hidden command vs. gibberish, see no THIS boards, and see no
  membership info leak via the public user list.
- Manual acceptance: `ssh -p 2222 localhost` twice with two different keys →
  two players chat via the public board and `praat`; one completes the
  discovery chain and enters THIS while the other (non-member) sees nothing
  change; the member's promotion repaints their THIS board live.
- `make run`, `make test`, `make docker`. README with local-play instructions.

## 8. Explicitly OUT of scope for v1

Real vulnerabilities of any kind, password auth, web UI, telnet, minigames
(wordlist cracking animation is fine, actual puzzles later), PvP, economy,
federation, per-player LLM-generated passwords, admin TUI (a `neabbs admin`
CLI subcommand for ban/promote/inspect against the DB file is enough).

## 9. Build order (commit after each phase)

**Primary goal: a complete, deployable BBS at the end of phase 3 (v0).** The
level-gated board engine — the game's signature mechanic — is built in full
during v0, not deferred. THIS is added on top afterward without touching the
engine.

1. **Skeleton** — Wish server, pubkey identity, session/channel-request
   rejections, limits, SQLite store, handle picker, `status`/`logout`.
2. **Board engine (complete)** — the full level-aware boards/messages model
   from §3: areas, per-message levels, membership visibility, redacted-stub
   rendering, hidden-count line, post/reply with post-level rules, live
   repaint on level change. Plus `neabbs admin` (ban/promote/inspect) so the
   mechanic can be playtested end-to-end via the hidden test board. Unit
   tests for the clearance filter land here, not later.
3. **v0 BBS** — inline/scrolling mode + baud-throttled writer + skip-on-key,
   the full call ritual (login theater, bulletins, laatste bellers, unread
   scan with per-player last-read pointers, quickscan), hotkey main menu +
   areas loader, public boards on the engine, file area + `-- Meer? --`
   pager, line assignment (24 lines) + `praat` + Babbel chat room,
   page-sysop (offline message in v0),
   time-limit theater, `NO CARRIER` logout, public seed content from §6,
   Dockerfile. **Milestone: deploy to neabbs.com. The BBS is live and usable
   as a hangout while the rest is built.**
4. **The door** — hidden-command mechanism, `this_member` flip, discovery-
   chain content (flags via file/board reads), THIS mode switch + theme
   shift, THIS BOARD seed content on the existing engine.
5. **THIS world** — hosts loader + lint, `scan`/`connect`/`ls`/`cat`, level
   filters, `who`/`wall` (member-scoped).
6. **Hacking loop** — `crack`, flags, effects/promotions, trace timer,
   broadcast lines, then the full THIS-0→THIS-3 arc content + playtest pass.
7. **LLM layer** — NPC talk + genposts tool, fallbacks, README,
   integration test.

Start with phase 1. Keep the daemon logic boring and the content weird.
