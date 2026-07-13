# `bluebox` command — design

**Date:** 2026-07-13
**Status:** Approved, pending implementation plan

## Problem

The `#phreak` arc gates the PTT district exchange (`ptt-wijkcentrale`,
`wijk.centrale.ptt.nl`) behind a blue-box tone sequence posted on `#phreak`
(post #206). Today that sequence is pure flavor: reading post #206
auto-grants the `toon_reeks` flag, and the host's `crack` command
(`method: wordlist`, `password_flag: toon_reeks`) checks the held flag.

The player therefore runs a *password wordlist* against a *telephone
exchange* and never dials the tones. That is thematically wrong: blue boxing
is not password cracking. You seize a trunk with a 2600 Hz supervisory tone,
then key routing digits in **MF** (multi-frequency inter-office signaling) —
not DTMF (the subscriber keypad tones) and not a wordlist.

## Goal

Introduce a `bluebox` command where the player types the actual tone
sequence. The sequence posted on `#phreak` becomes a genuine puzzle input:
you must read the board, learn the tones, and dial them. Acquiring the
knowledge is the puzzle.

## Non-goals

- No audio. Tones are text tokens, as today.
- No interactive stateful "box mode" sub-prompt. `bluebox` is a one-shot
  verb that takes the whole sequence on one line.
- No new trace / heat mechanics. Reuse the existing trace timer and effects.

## Design

### 1. New crack method `bluebox` (generic, reusable)

Extend `CrackSpec` (`internal/content/content.go`) with:

- `method: bluebox`
- `sequence: string` — the canonical expected tone sequence, e.g.
  `"2600 1700+1100 700+900 pauze 2600"`.

Reuse the existing `CrackSpec` machinery: `MinLevel`, `HintOnFail`,
`TraceSeconds`, `RequiresFlags`, and the `on_first_crack` effects all apply
unchanged. `nacht.centrale.ptt.nl` (THIS-5, teased in post #202) is also a
phone switch and can reuse `method: bluebox` for free later.

Content validation (`content.go` validate pass) must require a non-empty
`sequence` when `method == bluebox`, and reject `sequence` on other methods.

### 2. Knowledge is the key — drop `toon_reeks`

Reading post #206 no longer grants a flag; it just *shows* the sequence.
Remove `grants_flag: "toon_reeks"` from `content/boards/phreak.yaml` post
#206. Remove `password_flag: "toon_reeks"` from the wijkcentrale crack spec.

The host stays gated by `requires_flag: "phreak_invite"` (visibility) and
`min_level: 3`, and `#phreak` itself is THIS-3 gated, so the sequence is only
reachable through `#phreak`. A returning player who already knows the
sequence may dial it directly — realistic, and it cannot be brute-forced
given the format.

### 3. Input + match

Dispatch (`thisLine`, `internal/tui/app.go`): add verbs `bluebox` and
`blauwedoos`. Grab the rest of the line after the verb (like `wall` does),
not just the lowercased `arg` field — the sequence contains spaces.

New `Engine.Bluebox(ctx, h, v, has, input)` in `internal/world/world.go`
mirrors `Crack`'s guards (not locked / already open / cooldown / min level /
`RequiresFlags`) then compares a **normalized** input against the normalized
`sequence`:

- lowercase
- collapse whitespace and `·` separators to single spaces between tokens
- keep `+` as the intra-pair joiner (`1700+1100`)
- accept `pause` and `pauze` as equivalent
- order matters; exact normalized token match required

Wrong or short sequence → `hint_on_fail` (same refusal path as `crack`).
Correct → success: fire trace timer (`trace_seconds: 75`), `on_first_crack`
effects, breach record, and the "trunk seized" output — identical
consequences to today's crack success.

New TUI handler `blueboxHost` parallels `crackHost`/`runCrack`: a seize/key
animation (`seizing trunk... 2600Hz` / `keying MF...`) then the outcome.

### 4. Cross-verb flavor

- `crack` on a `bluebox`-method host → refuse with a themed hint:
  NL "dit is een telefoonschakelaar, geen computer. probeer een blue box."
  EN "this is a phone switch, not a computer. try a blue box."
- `bluebox` on a non-`bluebox` host → refuse:
  NL "geen trunk om over te nemen hier."
  EN "no trunk to seize here."

### 5. Copy fixes (MF, not DTMF/wordlist)

- `content/hosts/wijkcentrale.yaml`: header comment and banner/hint copy
  reference MF tones and trunk seizure, not "toonkiezer als woordenlijst."
- `content/boards/phreak.yaml` post #206: "dial it like a word list on the
  tone dialer" → key it on the blue box after the 2600 Hz seize. Keep the
  literal sequence line intact (it is now the real input).

## Affected units

- `internal/content/content.go` — `CrackSpec.Sequence` field + validation.
- `internal/world/world.go` — `Engine.Bluebox`, normalization helper; guard
  `Crack` to redirect on `bluebox` hosts.
- `internal/tui/app.go` — `bluebox`/`blauwedoos` dispatch, `blueboxHost`
  handler, cross-verb refusals.
- `content/hosts/wijkcentrale.yaml` — crack spec method/sequence + copy.
- `content/boards/phreak.yaml` — drop `toon_reeks` grant + copy.
- `internal/world/world_test.go` — Bluebox success/fail/normalization tests.

## Testing

- Bluebox success with exact sequence; with alternate separators (`·`,
  extra spaces, `pause` vs `pauze`); failure on wrong/short sequence.
- Guards: not-visible / min-level / cooldown behave like `Crack`.
- `crack` on a bluebox host returns the redirect, not a wordlist run.
- Content validation rejects `method: bluebox` with empty `sequence`.
- Integration: player reads #phreak, dials tones, gains THIS-4.
