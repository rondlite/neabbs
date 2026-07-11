# NEABBS Website — Design

**Date:** 2026-07-12
**Status:** approved pending user review

## Goal

Landing site on `https://neabbs.com`, served by the existing `neabbs` binary.
Hybrid tone: in-character CRT terminal front with a short out-of-character
explainer. One job: get the visitor to run `ssh neabbs.com`. The site hints —
never states — that something deeper (THIS) exists.

## Architecture

- New package `internal/web`; an `http.Server` started from `cmd` main
  alongside the SSH server. Web-start failure logs and does not stop the game.
- **TLS:** `golang.org/x/crypto/acme/autocert` (Let's Encrypt). Cert cache on
  the writable volume. Host policy: `neabbs.com`, `www.neabbs.com`. `:443`
  serves the site; `:80` serves the ACME http-01 challenge and 301-redirects
  everything else to https.
- **Env vars** (all optional; web off by default):
  - `NEABBS_WEB` — listen address, empty = disabled. `:443` enables autocert;
    any other port = plain HTTP (local dev, e.g. `:8080`).
  - `NEABBS_WEB_DOMAIN` — default `neabbs.com`.
  - `NEABBS_CERTS` — autocert cache dir, default `./certs` (deploy: `/data/certs`).
- **Static assets:** `index.html`, `style.css`, `site.js` under
  `internal/web/static/`, compiled in via `go:embed`. No framework, no build step.
- **Live stats:** `GET /api/status` →
  `{"callers_online": n, "registered": n, "reopened": "<date>"}`.
  Callers from the existing session tracker, registered = `COUNT(*)` on
  players. No handles, no per-player data. Cache response ~10s to keep DB
  quiet under load.
- **Docker/deploy:** publish 80 + 443 next to 22. The existing root-phase
  ownership fix must cover the certs dir.

## Page (single page, scroll)

1. **Boot-sequence hero** — CRT black, amber phosphor. Typewriter effect:
   modem dial, `CONNECT 1200`, ASCII NEABBS logo, live line
   `er zijn nu N bellers online` (from `/api/status`), blinking cursor with
   `ssh neabbs.com`.
2. **Explainer** (out-of-character) — three short paragraphs: a Dutch 1980s
   BBS reopened after 40 years, reachable over real SSH, a game and a hangout.
   Copy-paste connect block; note that it works on Mac/Linux/Windows 10+.
3. **Board listing** (in-character) — screenshot-style block listing the real
   public boards (ALGEMEEN, COMPUTERS, HULP, MARKT…). **THIS hint:** one extra
   row flickers in for ~200 ms at random intervals — garbled name,
   `[VERWIJDERD DOOR SYSOP]` — then vanishes. Never explained.
4. **Footer** — colofon-style, sysop credit. No social links, no analytics.

## CRT treatment

- Scanlines: CSS overlay (`repeating-linear-gradient`), always on (static, no
  motion).
- Phosphor glow (`text-shadow`), subtle vignette, faint screen-curvature edge.
- Occasional full-screen flicker tick, and the glitch row (§3).
- `prefers-reduced-motion`: typewriter, flicker and glitch row are skipped
  (final text shown immediately; glitch row simply never appears). Static
  scanlines/glow remain.

## Language

- NL/EN toggle top-right. JS swaps text via `data-nl` / `data-en` attributes;
  choice persisted in `localStorage`; default NL.
- In-character blocks (boot sequence, board listing, glitch row) are Dutch
  always; only the explainer and UI chrome translate.

## Error handling

- `/api/status` failure → hero shows the static line without numbers (no
  broken UI); JS treats fetch errors silently.
- Autocert failure (rate limit, DNS) → logged; :80 keeps serving redirect +
  challenge; game unaffected.
- Unknown paths → in-character 404 (`ONBEKEND COMMANDO`), plain text.

## Testing

- Unit: handler tests for `/`, `/api/status` (JSON shape, no player data),
  404, and the :80 redirect.
- Config test: `NEABBS_WEB` empty → no listener started.
- Manual: local `NEABBS_WEB=:8080` run; check scanlines, typewriter, glitch
  row, NL/EN toggle, reduced-motion (devtools emulation), mobile width.

## Out of scope

- Board-activity teasers, player pages, changelog, RSS.
- Analytics of any kind.
- Separate www redirect infra (autocert covers www; redirect www→apex in the
  handler).
