# NEABBS

Een Nederlandse jaren-80 BBS, heropend na 40 jaar stilte. Geserveerd over
echte SSH; spelers krijgen een Bubble Tea TUI, nooit een shell.

## Lokaal spelen

```sh
make run          # bouwt en start op :2222
ssh -p 2222 localhost
```

Je SSH-sleutel is je identiteit (elke sleutel wordt geaccepteerd; de
SHA256-fingerprint is het account). Bij de eerste keer inbellen kies je een
gebruikersnaam. Twee spelers testen: verbind twee keer met verschillende
sleutels (`ssh -i andere_sleutel -p 2222 localhost`).

## Configuratie (alleen env vars)

| Variabele        | Default      | Betekenis                          |
|------------------|--------------|------------------------------------|
| `NEABBS_LISTEN`  | `:2222`      | SSH listen-adres                   |
| `NEABBS_DB`      | `./neabbs.db`| SQLite database (WAL)              |
| `NEABBS_HOSTKEY` | `./hostkey`  | SSH hostkey (auto-gegenereerd)     |
| `NEABBS_CONTENT` | `./content`  | Content-directory (YAML/tekst)     |
| `NEABBS_BAUD`    | (aan)        | `0` schakelt baud-emulatie uit     |
| `LLM_BASE_URL`   | (leeg = uit) | OpenAI-compatible chat endpoint    |
| `LLM_MODEL`      | (leeg)       | modelnaam voor de LLM              |
| `LLM_API_KEY`    | (leeg)       | bearer-token voor de LLM           |
| `NEABBS_WEB`     | (leeg = uit) | Website-adres; `:443` = Let's Encrypt |
| `NEABBS_WEB_DOMAIN` | `neabbs.com` | Domein voor TLS-certificaten     |
| `NEABBS_CERTS`   | `./certs`    | Cache-map Let's Encrypt-certificaten |

## Deployen (volume & rechten)

De container heeft één schrijfbaar pad nodig: het volume met de DB en de
hostkey (default `/data`). Veel platforms (Fly, Railway, Coolify, k8s, …)
koppelen zo'n volume als `root`, waardoor een strikt non-root proces er
niet in kan schrijven (`unable to open database file (14)`).

Daarom start de daemon als `root` **uitsluitend** om de eigenaar van de
schrijfbare mappen recht te zetten, en zakt daarna permanent naar uid/gid
`65532` — vóór de DB wordt geopend. Serveren gebeurt dus nooit als root.

- Mount het volume op `/data` (of zet `NEABBS_DB`/`NEABBS_HOSTKEY` naar een
  pad binnen je mount).
- Doel-uid/gid overschrijven kan met `NEABBS_UID` / `NEABBS_GID`.
- Draai je met `--user 65532` en is het volume al schrijfbaar voor die uid,
  dan wordt de root-fase overgeslagen en draait alles direct als non-root.
- Om de website mee te serveren: publiceer poorten 80 en 443 naast 22 en zet
  `NEABBS_WEB=:443`; certificaten komen in `/data/certs`.
- Non-root binden aan poorten 80/443 werkt standaard onder Docker (≥20.10);
  op k8s/podman kan `CAP_NET_BIND_SERVICE` of een sysctl
  (`net.ipv4.ip_unprivileged_port_start`) nodig zijn.

## Ontwikkelen

```sh
make test         # unit- en integratietests
make vet
make docker       # distroless image, nonroot, read-only rootfs
```

Admin-CLI (tegen het DB-bestand, server mag draaien):

```sh
./bin/neabbs admin inspect                 # alle spelers, of: inspect <handle>
./bin/neabbs admin promote <handle> <0-9>  # THIS-niveau (impliceert lidmaatschap)
./bin/neabbs admin member <handle> on|off
./bin/neabbs admin ban <handle>            # / unban <handle>
./bin/neabbs admin sysop <handle> on|off   # in-game moderatie aan/uit
./bin/neabbs admin flag <handle> <flag>
```

In Docker draai je de CLI in de container, als de doel-uid, tegen dezelfde DB:

```sh
docker exec -u 65532 neabbs /neabbs admin inspect
docker exec -u 65532 neabbs /neabbs admin sysop <jouw-handle> on
```

### Sysop-commando's in de BBS zelf

`admin sysop <handle> on` geeft een speler de sysop-vlag. Na opnieuw inloggen
heeft die speler extra commando's, bruikbaar vanaf elk menu (publiek én THIS);
voor niet-sysops bestaan ze niet (geen enkel spoor):

```
sysop who            alle lijnen: handle, vingerafdruk, en THIS-aanwezigheid
sysop zeg <tekst>    omroep naar iedereen (publiek én THIS)
sysop wis <nr>       verwijder bericht <nr> in het huidige board
sysop ban <handle>   verban (verbreekt live sessies direct) · unban <handle>
sysop gen <board> [n]  laat de LLM n concepten schrijven (naar de wachtrij)
sysop pending        toon concepten die op review wachten
sysop ok <id>        publiceer een concept · sysop nee <id> verwerpt
sysop help
```

`sysop wis` verwijdert alleen door bellers geplaatste berichten (id ≥ 10000);
YAML-content is vast en blijft staan. Live sessie-info (`who`, omroep, ban)
komt uit de draaiende daemon, niet de CLI — daarom zijn dit in-game commando's.

**Gegenereerde berichten reviewen.** Er zijn twee routes:

1. *Offline (curated, permanent).* `neabbs genposts --board <id> [--level N]
   [--count N]` roept de LLM aan en print YAML-concepten naar stdout — het
   schrijft niets weg. Je leest ze na, snoeit, en plakt de goede `messages:`
   in `content/boards/<id>.yaml`; na herstart laadt de content-lint ze als
   vaste content (id < 10000, niet in-game te wissen). De review ben jij.
2. *In-game (sysop, review-wachtrij).* `sysop gen <board> [n]` laat de LLM
   async n concepten schrijven; die belanden **pending** in de wachtrij en
   zijn voor niemand zichtbaar. `sysop pending` toont ze, `sysop ok <id>`
   publiceert er één (wordt een gewone spelerspost, id ≥ 10000, later met
   `sysop wis` te verwijderen), `sysop nee <id>` gooit hem weg. Niets gaat
   ongezien live. De LLM draait nooit op het kritieke pad: de generatie is
   async met een timeout, en mislukt hij, dan gebeurt er simpelweg niets.

Content is data: boards in `content/boards/*.yaml`, bulletins in
`content/bulletins/*.txt`, bestandensectie in `content/files.yaml`, hosts in
`content/hosts/*.yaml`, en systeem-prompts in `content/prompts/*.txt`.
De content-lint draait bij het opstarten en weigert kapotte content
(onbereikbare vlaggen, promotie-gaten, dubbele id's, publieke content die
naar THIS verwijst, en meer).

## LLM (optioneel, alleen flavour)

De game is volledig speelbaar met de LLM uit (`LLM_BASE_URL` niet gezet).
LLM-uitvoer is nooit op het kritieke pad: elke call heeft een timeout van
10 s en valt terug op vaste tekst. Zet de drie `LLM_*` env vars om een
OpenAI-compatible endpoint (bijv. een lokale vLLM-gateway) te gebruiken.

- **NPC's** — sommige hosts hebben een `npc`-blok; `talk` opent een gesprek.
  De NPC hint naar dingen die de speler al ontsloten heeft (`knows_flags`),
  maar een deterministisch pad naar elke vlag bestaat altijd ook.
  Limieten: 20 beurten per sessie, 60 per dag.
- **genposts** — offline hulpmiddel dat sfeer-berichten als YAML voordraft
  voor menselijke review; draait nooit in-game:

  ```sh
  LLM_BASE_URL=... LLM_MODEL=... ./bin/neabbs genposts --board algemeen --level 0 --count 20
  ```
