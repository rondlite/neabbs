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

## Ontwikkelen

```sh
make test         # unit- en integratietests
make vet
make docker       # distroless image, nonroot, read-only rootfs
```

Admin-CLI (tegen het DB-bestand, server mag draaien):

```sh
./bin/neabbs admin inspect
./bin/neabbs admin promote <handle> <0-9>
./bin/neabbs admin ban <handle>
```

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
