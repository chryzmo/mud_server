# mud_server

Go server for the MAGFest 2027 weekend MUD: a guest-only, phone-first text game over websockets,
built for up to 5,000 players on one small server. A player joins with a name and nothing else, and
a dropped connection resumes the same character. The browser client is the sibling repo
[mud_frontend](https://github.com/chryzmo/mud_frontend). Project rules for contributors and agents
are in `AGENTS.md`; the working plans live in a local, gitignored `plans/` folder.

## World content

The world (rooms, items, NPCs, areas) can be described in YAML files; the format and its checks are in
`src/internal/worldfile`, and `world-sample/` is a small example. Check a directory with
`go run ./src -validate-world [-strict] DIR`: it reads every `.yaml`/`.yml` file under `DIR`, reports errors
(unknown fields, bad keys, exits to missing rooms, items or NPCs that do not exist, a missing start room) and
warnings (one-way exits, rooms unreachable from the start), and exits 1 on errors (or on warnings with `-strict`).

`go run ./src -render-world DIR` prints a readable Markdown overview of the world instead: a map (a Mermaid diagram, which GitHub
draws), every room with its description, exits, floor items and spawners in the order a player would meet them, and the
item and NPC tables, with any validation problems listed first. The content repository's CI publishes it for review.

A room may list `features`: scenery (a clock, a banner) with a list of names (the first is what it is called, the rest are aliases)
and the text `EXAMINE` shows; `unlisted: true` keeps one out of LOOK's "You notice: ..." line so players have to find it. `EXAMINE <thing>`
(`X`, or `LOOK AT <thing>`) looks at a feature, an item in the room or the pack, a player or a monster; names match whole or by a unique
prefix of three letters or more, and when several fit the server asks which (`EXAMINE MY <item>` looks only in the pack). Features
are stored with the room (`room.featuresjson`) and are shared by its crowd copies.

A room may also list `people`: friendly characters (not monsters; they never fight or move) with `names`, `examine` text, a `greeting`, an
optional `shrug` (the answer to an unknown topic) and `topics`, each with `keywords` and what they `say`. `TALK <person>` shows the greeting
and lists the topics (just `TALK` if there is only one person), and `ASK <person> ABOUT <topic>` answers one; people are named in LOOK's
"Also here" line and can be examined. Every word is authored. People are stored with the room (`room.peoplejson`) and shared by copies.

Rooms may also have `rules` and `gates` (puzzles; see plans in the world design): a rule says `on` a verb (TURN, PULL, PUSH, PRESS, RING, LIGHT, OPEN, SEARCH, READ, USE, GIVE, PUT, THROW, SET), a `say`, or `enter`, with optional `if`/`unless` conditions and a list of `do` effects (flags, counters, sequences, gates, items, text, teleport, `run` of a named rule). The first matching rule runs. A `gate` makes an exit hidden or locked until a rule opens it for the player (or for everyone in the room). Progress is stored per player in the `player_state` table and survives restarts; `bound: true` items are puzzle tokens that cannot be dropped or stacked. `mud_server -validate-world` checks that every flag, gate and token is reachable and `-render-world` summarises rules in plain language.

A top-level `puzzles:` block declares what `JOURNAL` and `HINT` show: for each puzzle a `title`, ordered `steps` (a `flag` a rule sets and the journal `line` it earns), a `solved` flag and line, and up to five `hints` from a nudge to nearly the answer (`HINT` gives the next one each time it is asked; asking twice within two seconds repeats the same hint). A top-level `codes:` block holds sticker codes: each has a `hash` (the code's HMAC-SHA256 under the host secret `MUD_CODE_SECRET`; content never holds a code) and what it grants (`color`, `badge`, `item`, `flag`, `message`; never points). `MUD_CODE_SECRET=... mud_server -new-codes N` prints N fresh codes with their hashes: the codes go on stickers and are never committed, the hashes go into content. Players type `REDEEM <code>` (once per code per player; five wrong tries a minute start a doubling cooldown; without the secret nothing redeems), `COSMETIC [name|OFF]` and `PROGRESS`. **Ambient life:** a room may list `ambient:` entries (`every: [least, most]` seconds, 15 to 3600; `lines:`; optional `when:` with `time_of_day` (`dawn` 06-08, `day` 08-18, `dusk` 18-20, `night` 20-06, Eastern) and `players_at_least`) and `day_notes:` (a line per time of day that `LOOK` adds). A person may list `idle:` lines (`idle_every` defaults to `[120, 300]`). Lines go only to rooms with players in them (an empty room keeps no schedule), the first one after a gap rather than on arrival, and one world-wide budget (20 a second, burst 40) caps them all. At most 6 entries per room, 12 lines of 200 characters per entry. **Guided first steps:** an `onboarding:` block (`onboarding: {}` is enough) turns on a walkthrough for brand-new players: on arrival they get your `welcome` line and the nudge for the first step, and each step done (LOOK, a move, EXAMINE, TALK, SAY) brings the next nudge from `steps:` (keys `look`, `move`, `examine`, `talk`, `say`); a `finish` line closes it. Every line is in your voice and anything left out takes a plain default. It runs only on the player's own flags (nothing on a timer), a step done early is ticked off quietly, failed attempts do not count, and `TOUR SKIP` and `TOUR START` end and restart it. Without the block there is no walkthrough and `TOUR` says so. **Cosmetic rewards:** a rule effect `unlock: {badge: crown}` (or `{color: gold}`) grants a name badge or colour from the fixed palette (colours `gold crimson azure emerald violet amber rose silver`, badges `star crown key lantern bell gear quill flame`): the player is told, wears it at once, and can change it with `COSMETIC`. Granting one a player already owns changes nothing, so a rule that runs again does not repeat the message or undo their choice. `-render-world` lists every quest that grants one under "Cosmetic rewards". **Small mobs:** an npc may set `aggressive` (default false: it fights only those who attack it, the latest attacker first, and ignores everyone else; true attacks whoever is in its room) and `xp` (what each fighter is paid for a kill; 0 means the progression kill XP). A kill pays every player who hit it and is still in the room the full amount, gold loot goes to the killer, and dying costs nothing (the player wakes at their spawn point with full health). A room spawner (`count`, `tick_ms`) works only while a player is in the room, its first monster coming one interval after the first player arrives, and the world holds at most 300 live monsters. Monsters appear in the room frame as `things` with the verb `ATTACK`, so the client can make them tappable. Two monsters never share a name: a second `RAT` is `RAT 2`. A world without content keeps the seeded orc, which stays aggressive. **Event rooms:** a room may set `capacity` (1 to 200; the most players it holds, where movement in is refused; without it the server hard cap of 60 applies) and `quiet_at` (the audience at which the room goes quiet; with a `capacity` and no `quiet_at` it is 30, or the capacity if smaller; a room with neither never goes quiet). In a quiet room SAY, emotes and socials are limited to one line every 10 seconds per player, except for game masters and performers given the stage voice with `GM STAGE <name> ON|OFF`; they use `STAGE <text>`, which the whole room hears. `GM RAFFLE [ALL]` draws one guest (never a game master) from your room or from everyone online, tells that audience the winner and awards nothing by itself; every draw is in the GM audit log. An optional `progression:` block tunes XP and levels (`levels`: the total XP at which each level starts, the first 0; `per_level`: `max_hp`, `attack`, `defense` added per level; `xp`: `room` and `feature` (exploration, the first time only) and `kill` (combat)); anything left out takes the default, and with no block the defaults apply. `RANK` shows your level, XP and place. `GET /api/leaderboard` serves a snapshot rebuilt every 10 s away from the game loop (`updated`, `total`, and the top 50 of `overall`, `explorers`, `fighters`, each entry `name`, `level`, `xp`, `explore`, `combat`, `color`, `badge`); it leaves out game masters and `GM HIDE`-n names, and the Caddy route for it is deploy configuration (see `deploy/Caddyfile`). A room's optional `region` groups it for `MAP`, which names only the rooms the player has been in. The validator checks that every step flag is set by some rule; `-render-world` lists the puzzles.

A top-level `events:` block schedules things that happen on their own: an event has a `title`, an `at` start ("2027-01-07 12:00 America/New_York", a wall-clock time in a named zone), an optional `repeat` (`every: 24h`, `count: 3`), a `duration`, and timed `steps` that announce to everyone, tell a room something, or set or clear a world flag (`finish` steps run when it ends, by the clock or by `GM EVENT STOP`). A rule may test `world_flag`, and a gate with `open_when: <flag>` is open for everyone while that flag is set, so an event can open a door. Progress is saved: after a restart a running event resumes, a step that was due while the server was down still sets its flags but says nothing, and an occurrence whose window passed is skipped and logged, never played late. `EVENTS` tells players what is on and what is next (events marked `hidden: true` are left out). The validator checks times, zones, rooms and limits and that every world flag a rule or gate reads is set by some event; it does not reject a start time in the past, so a content fix during the con cannot stop the server starting.

Global chat is limited three ways, all under `Chat` in the config: per player (`PerPlayerSeconds` 5 with a `PlayerBurst` of 3), overall (`OverallPerSecond` 30, excess is dropped with a "busy" notice to the sender), and a wait before a new guest may speak (`GuestMinAgeSeconds` 60; -1 for none; password accounts and GMs never wait). Lines are at most `MaxLength` (200) characters. Words on the blocked list (`blocked_words.txt`, the one that screens names) are masked with `*`, not refused; it catches look-alike digits and separators inside a word (`sh1t`, `s_h_i_t`) but not spaced-out letters. Muted players cannot use it. **Automatic moderation** (a mute, never a kick): the same message (ignoring case, spacing and trailing punctuation; lines under three characters do not count) four times within a minute, in `SAY` or `GLOBAL`, or eight refusals by the rate limit within 30 seconds, mutes the player for 2 minutes, then 10, then 30 (a quiet hour forgets earlier strikes); they are told why, GMs see an `[Auto]` line, `gm_audit` logs it as `system`, and a GM is never struck. **Quiet hours:** from 01:00 to 08:00 Eastern global chat has a 30-second slow mode and new guests wait five minutes, until a GM sets `GM CHAT ON|OFF` or `GM SLOW` by hand, which turns these defaults off for the rest of that night. **Socials:** twelve ready-made verbs (`WAVE BOW CHEER DANCE HUG SHRUG NOD LAUGH CLAP SALUTE HIGHFIVE FACEPALM`), alone or aimed at someone in the room (`WAVE`, `WAVE BOB`), and `EMOTE <text>` (`/me` in the client; at most 100 characters, blocked words masked, counted by the repeat rule); they share `SAY`'s rate limit and refuse muted players. `WHO` shows how many players are online, a count per map region and the names in your own room only (counts are cached for 5 seconds). `IGNORE [name]` hides a player's `SAY`, whispers, emotes, socials and global chat from you for the session (not saved; at most 20; a GM cannot be ignored; the ignored player is not told), `UNIGNORE <name|ALL>` undoes it.

Set `World.ContentDir` (or `MUD_WORLD_DIR`) and the server validates the directory at startup, refuses to start if
it has errors, applies it to the database in one transaction, and builds the world from the rooms it defines.
Rows are matched by a stable key (`contentkey`), so a deploy updates names, descriptions, exits, stats and spawners
in place and never changes an id. The first time, a row with no key and the same name is adopted, so a database seeded
from `src/sql` is taken over without moving players or inventories. Items on a room's floor are placed when the room is
created and never reset. Content removed from the files is not deleted: it just leaves the world, and a player saved
in a room that left starts at the start room. With no `World.ContentDir` the world is whatever the database holds, as
before. The real world lives in the private `mud_content` repository.

## Game master

There is one game master (GM), the owner. A GM is a password account with the `gm` flag, created on the host with
`mud_server -add-gm NAME` (it prompts for a password of at least 12 characters, twice, without echo; piped input reads one line;
an existing password account is promoted and its password replaced; a guest's name is refused). On the Droplet run it inside
the container: `ssh deploy@<host> podman exec -it mud-server /app/mud_server -add-gm NAME`. The GM logs in with `login` (the client's
hidden staff route, planned) and plays as an ordinary character; password accounts are never idle-kicked.

`GM` is one command with sub-commands, and for anyone else it does not exist ("Unknown command.", and `HELP` does not list it):

| Command | Effect |
|---|---|
| `GM WHO [prefix]` | counts (online, guests, accounts, away, whether visitors are locked), then names with rooms |
| `GM MUTE <name> [minutes]` / `GM UNMUTE <name>` | refuse `SAY` and `WHISPER` (default 15 minutes, at most 1440); stored, so it survives a restart; a GM cannot be muted |
| `GM REPORTS [number]` / `GM REPORTS CLOSE <number\|ALL>` | player reports sent with `REPORT <player> [reason]` (three an hour each; stored with the last lines of global chat and room talk); online GMs are told as they arrive; when three different players report the same person within ten minutes they are muted for ten (a mute, never a kick; logged in `gm_audit` as `system`; GMs are never auto-muted) |
| `GM TELEPORT <name>` / `GM TELEPORT ROOM <text>` | go to a player, or to the room whose name starts with the text (ignores crowd caps) |
| `GM SUMMON <name>` | bring a player to the GM's room |
| `GM ANNOUNCE <text>` | a `[GM] text` line to every player, once encoded |
| `GM KICK <name> [minutes]` | remove a character from the world for a break (default 10, at most 1440): the client gets `error kicked` and is closed, and resume, recover and login for that name are refused (`auth` code `kicked`) until it ends; the character and its token are kept. A GM cannot be kicked. The break is in memory (a restart ends it) and is by name, so a determined player can rejoin under another name: `GM LOCK` is the answer to that |
| `GM EVENT [START\|STOP\|SKIP <event>]` | list the scheduled events and where each is (hidden ones too), or run one now, end it now (its `finish` steps run), or skip its next occurrence |
| `GM CHAT OFF\|ON` | switch global chat off for everyone but GMs, or back on (in memory: a restart turns it on) |
| `GM SLOW [seconds]` | slow mode: the least gap between one player's global messages (0 or no number turns it off; in memory) |
| `GM FLAG [name [on\|off]]` | show the world flags, or set or clear one (rules and gates read them) |
| `GM LOCK` / `GM UNLOCK` | refuse or allow new guests (`auth` code `joins_locked`); players already in can reconnect |

Every use is logged and written to the `gm_audit` table (who, what, arguments, result). More commands (flags, events, kick,
reports, chat controls) arrive with their features; see the event-systems plan.

## Local development

Requires Go 1.24+ and either `podman-compose` or `docker compose`.

```sh
scripts/db.sh up      # start Postgres 17 on 127.0.0.1:5432 (seeds the `mud` database on first start)
go run ./src          # run the server on 127.0.0.1:8080, from the repo root
scripts/test.sh       # go vet + all tests with -race, against the `mud_test` database
scripts/db.sh reset   # wipe the database volume and reseed
```

Config comes from `config/server_config.json`. Environment variables override it:
`MUD_CONFIG` (path to the config file), `MUD_LISTEN_ADDR`, `MUD_DATABASE_URL`.

Run `scripts/install-git-hooks.sh` once per clone to enable the pre-commit hook (secret scan, conflict markers, large files, tests).

## Protocol (`mud.v1`)

Connect to `/ws` offering the websocket subprotocol `mud.v1` (the protocol version; a client that does
not gets one `bad_version` error and is closed). Every frame, both ways, is a flat JSON object with a
type `t`. `src/internal/mudserver/protocol.go` is the source of truth, with exact shapes pinned by tests.

| Client sends | Fields | Meaning |
|---|---|---|
| `join` | `name` | new guest; the reply carries a one-time resume `token` and recovery `recovery` code |
| `resume` | `token`, `received` | reattach after a drop; `received` is how many counted frames the client has processed; leave it out (a freshly loaded page) and you get a gap and a fresh look |
| `recover` | `name`, `code` | take a character back with the recovery code; issues a new token and code |
| `cmd` | `line` | a game command, such as `MOVE N` or `SAY hello` |
| `login` | `username`, `password` | password accounts, for game masters added to the database by hand. There is no `register`: players are guests (`join`), and a `register` frame gets `bad_request` |

| Server sends | Fields | Meaning |
|---|---|---|
| `hello` | `message` | greeting on connect |
| `auth` | `ok`, `code`, `message`, and for success `token`, `recovery`, `resumed`, `gap`, `replayed`, `base` | answer to join, resume, recover, login |
| `error` | `code`, `message`, `retry_ms` | protocol or server problem; codes include `rate_limited` (wait `retry_ms`; without `retry_ms` it is only a slow-down notice), `idle`, `replaced` (another connection took the character over; do not reconnect by itself), `kicked` (a GM removed the character for a break, said before the close; do not reconnect by itself; resume, recover and login are then refused with `auth` code `kicked` until the break ends), `unknown_command`, `restarting`, `internal` |
| `text` | `text` | prose, with ANSI colour codes the client styles per line |
| `say`, `arrive`, `leave`, `whisper` | `who`, `text`, `from`, `to`, `out`, `color`, `badge` | room and whisper events; the client words and colours them. `color` and `badge` are the sender's active cosmetic keys (see `cosmetics`), left out when none |
| `chat` | `who`, `text`, `ch` (`global`), `gm`, `color`, `badge` | a global chat line (`GLOBAL <text>`, short `G`); the sender gets their own line back the same way, so there is no separate echo. Masked words are already `*` |
| `announce` | `text`, `from` (`gm` or `event`) | a broadcast from `GM ANNOUNCE` or a scheduled event: show a banner and a log line |
| `cosmetics` | `colors`, `badges`, `color`, `badge` | the player's own unlocked cosmetic keys and the ones worn (after `REDEEM`, `COSMETIC`, and at login for a player who has any; absent means none). Keys come from a fixed set: colours `gold crimson azure emerald violet amber rose silver`, badges `star crown key lantern bell gear quill flame` |
| `events` | `now` (`title`, `ends`), `next` (`title`, `at`) | the schedule for the banner: events on now and the next five to start, as Unix milliseconds so the client counts down on its own clock; hidden events are left out. Sent at login when anything is visible, and to everyone when an event starts, ends or is skipped; a world with no visible events sends none |
| `room`, `stats`, `inventory` | name, exits, and what is here (`locked` exits, `things` with their `verbs`, `people`, `items`); vitals plus `level`, total `xp` and `next` (the total XP at which the next level starts; absent at the top); listing | state for the UI; sent again when an item moves or a gate opens |

**Frame counting.** Every frame except `hello`, `auth` and `error` is *counted*, by the server as it
queues it and by the client as it processes it. After a drop the client sends its count in `resume`;
the server replays what the client missed from a short per-player ring, or answers `gap: true` with a
`base` count when it cannot, followed by a fresh look. No sequence number travels in frames, because a
broadcast shares one encoding among all its recipients.

**Reconnecting.** A dropped guest stays in the world for `ResumeGraceMS` (90 s) with no announcement. An
`idle` error means the server disconnected a guest who had sent no command for 30 minutes: the client
should wait for a tap, not reconnect by itself. After a `rate_limited` reply, wait `retry_ms` rather than
backing off exponentially. Design notes: `plans/json-protocol.md`, `plans/guest-sessions.md`.

## Configuration

`config/server_config.json`, with `MUD_CONFIG`, `MUD_LISTEN_ADDR` and `MUD_DATABASE_URL` overriding as
above. Zero or absent means the default; `-1` where noted turns a limit off. The server refuses to
start on an invalid `TrustedProxies` list.

| Key | Default | Meaning |
|---|---|---|
| `Server.SaveIntervalMS` | 5000 | how often changed players and rooms are written, in one transaction |
| `Server.TrustedProxies` | loopback | peers allowed to report a client's address (CIDRs); an empty list trusts nobody |
| `Server.ClientIPHeader` | `X-Client-IP` | header Caddy uses to pass the client's address |
| `Server.LogClientIPs` | true | log the address with joins and recovery attempts |
| `Connection.ReadLimitBytes` | 4096 | largest message, including fragments |
| `Connection.CommandsPerSecond`, `CommandBurst` | 10, 20 | per-connection command bucket |
| `Connection.SayPerSecond`, `SayBurst` | 1, 5 | per-player `SAY` bucket |
| `Connection.OutboundQueueSize` | 256 | frames queued per connection before it is dropped as too slow |
| `Connection.WriteTimeoutMS`, `PingIntervalMS`, `PongTimeoutMS` | 10000, 30000, 60000 | write deadline and keepalive |
| `Connection.ResumeGraceMS` | 90000 | how long a dropped guest stays in the world |
| `Connection.ReplayFrames` | 100 | counted frames kept per guest for replay (clamped under the queue size) |
| `Connection.RecoveryLockMS` | 900000 | a name refuses recovery this long after 5 wrong codes |
| `Connection.JoinPerSecond`, `JoinBurst` | 1000, 5000 | one bucket for every join, resume, recover and login; `-1` for no limit |
| `Connection.MaxJoinsPerMinutePerIP` | 0 (off) | optional per-address ceiling; a flood breaker, not a fairness rule |
| `Connection.IdleTimeoutMS` | 1800000 | a guest with no command this long is disconnected; `-1` never |
| `World.ContentDir` | none | directory of world YAML files applied at startup (env `MUD_WORLD_DIR`); empty means the world comes from the database as seeded |
| `Database.*` | see file | connection settings, `MaxConnections` (10), `StartupWaitSeconds` (30) |
| `Game.*` | see file | tick length and a new player's starting stats |
| `Crowd.SoftCap`, `HardCap`, `MaxCopies` | 50, 60, 200 | arrivals fill a copy of an area up to the soft cap; movement into a room is refused at the hard cap |
| `Crowd.Areas` | none | rooms that copy themselves when crowded, by database ID; the shipped config copies the three seeded rooms |

The server logs session-close totals by reason (`queue_full`, `write_error`, `read_timeout`, `idle`,
`replaced`, ...) every 10 seconds while they change; use them to see why connections dropped under load.

## Bot client and local load testing

`src/cmd/mudbot` uses the public websocket protocol (JSON frames, subprotocol `mud.v1`): login with seeded accounts,
MOVE, SAY, ATTACK, and WHISPER. It continuously reads every connection. The ordinary
`scripts/test.sh` suite includes 200 bots playing for 10 seconds under `-race`.

### Reconnect storms

`mudbot storm` joins guests by name, plays lightly, then cuts a share of their connections at once; each bot
reconnects after a random delay (growing on failure) and resumes with its token and frame count. It reports,
per storm, how many came back with the character kept or with a gap, frames replayed, retries and the time to
resume. `-cycles 0 -between 30s` just plays, so you can restart the server from outside and read the
"unplanned_reconnects" section. Names are prefix plus five digits (prefix at most 11 characters), and guests
persist in the database, so use a fresh prefix or a reset local database for each run. The target must be
loopback unless `-allow-remote` is given.

For a manual run, **stop any previous load server first** and use the dedicated local
`mud_load` database, never the development or production database. From the repo root:

```sh
scripts/db.sh up
go build -o /tmp/mudbot ./src/cmd/mudbot
go build -o /tmp/mud-load-server ./src
read -rsp 'Local fixture password: ' MUD_BOT_PASSWORD; echo; export MUD_BOT_PASSWORD
export MUD_BOT_DATABASE_URL='postgres://mud:mud@127.0.0.1:5432/mud_load'
/tmp/mudbot seed -reset -count 5000

# Terminal 1: separate server process, pinned to the target's two-CPU budget.
# Keep time's output outside the repo; Ctrl-C after the bots finish saves/shuts down.
/usr/bin/time -v -o /tmp/mud-load-server.time env GOMAXPROCS=2 \
  MUD_DATABASE_URL="$MUD_BOT_DATABASE_URL" MUD_LISTEN_ADDR=127.0.0.1:18080 \
  taskset -c 0,1 /tmp/mud-load-server

# Terminal 2 (same MUD_BOT_PASSWORD): use other CPUs for the generator if available.
/tmp/mudbot -url ws://127.0.0.1:18080/ws -count 5000 -duration 30s
```

`seed -reset` creates `mud_load` if missing and replaces its schema using `src/sql`.
It refuses non-loopback hosts, other database names and connection query overrides.
Fixture accounts share a bcrypt-cost-4 hash; normal login still verifies it. This measures
steady-state gameplay, **not production-cost login/reconnect storms**.
Supply passwords only through `MUD_BOT_PASSWORD`, not command-line flags. `-prefix` defaults
to `loadbot`; account names append a zero-based five-digit index.

The generator refuses remote hosts unless explicitly opted in with `-allow-remote`;
that flag is not permission to load-test production. Admission has bounded concurrency
(`-login-workers`, default 8) and a separate deadline (`-join-timeout`, default 2m).
Gameplay begins after admission, with staggered actions. Each bot waits for a response
before pausing `-interval` (default 1s), so this is a closed-loop workload, not fixed-rate
traffic. Every action is followed by a unique outgoing-whisper echo used as a completion
fence; RTT includes the action and fence, not delivery to every broadcast recipient.

JSON reports admission/gameplay time in nanoseconds; p50/p95/p99 are nearest-rank, rounded
up to 1ms. Latency includes only completed fences; uncompleted actions, timeouts, failed
joins, server errors, rate-limit notices, and unexpected disconnects are separate counters.
Active-at-start/end are concurrent counts; joined is cumulative. Read counts cover post-auth
traffic, including admission broadcasts. Exit status is nonzero if the requested load was
not sustained. A low survivor-only p95 is not a successful capacity test: inspect all counts.
Record server RSS/CPU separately from the generator, report workload and world occupancy,
and retain failed measurements too. The 5,000-player target remains unproven until sustained
through the full stack; the current small world can expose known room fan-out bottlenecks.

### Phase 4 crowd runs

`scripts/load-crowd.sh [COUNT] [spread|feast] [SECONDS] [INTERVAL]` is a local run of the features added for the con: a server pinned to two CPUs plays a generic load world (`mudbot world` writes it: a cloneable Lobby and Garden, and a Feast Hall with capacity 200, a person, a lamp, ambient lines and a Feast event) while guests use the verb mix (`mudbot storm -mix verbs`: LOOK, EXAMINE, TALK, EMOTE, a social, WHO, SAY and a walk). `spread` lets guests wander over copies of the Lobby; `feast` (`-stay`) sends them all to the hall, which fills to its capacity and turns the rest back. One extra guest asks for STATS once a second and the report gives its p50/p95/p99/max, which is how long a command waits behind the crowd. The report also counts frames and bytes received, announcements, `events` frames, moves refused because a room was full and lines refused because it was quiet. It uses the dedicated local `mud_load` database and loopback only, and keeps the server log and report in the directory it prints.

## Deploying

Production is a DigitalOcean Droplet (Fedora) running the game server, Postgres and Caddy as
rootless Podman containers managed by systemd (Quadlet units in `deploy/quadlet/`). Caddy
serves the frontend, proxies `/ws`, and handles TLS.

```sh
deploy/deploy.sh            # test, build images locally, ship them, restart what changed
deploy/deploy.sh status     # service status and recent server logs
deploy/deploy.sh rollback   # switch back to the previous images
```

Put `MUD_DEPLOY_HOST=deploy@<droplet IP>` in `deploy/deploy.local.env` (gitignored). A new host
is set up with `deploy/bootstrap-host.sh`, then `deploy/harden-ssh.sh`; see the comments in each.

### Client addresses behind Cloudflare

Caddy trusts `CF-Connecting-IP` only for connections from Cloudflare's published ranges, kept in
`deploy/cloudflare-ips.caddy`, and passes the result to the game server as `X-Client-IP`, which the
server believes only from its trusted proxies (loopback inside the pod). Run
`deploy/update-cloudflare-ips.sh` (or `--check`) before an event to refresh the ranges, and
`deploy/test-client-ip.sh` to prove the behaviour with the real pinned Caddy image (needs podman).
After a deploy, join once and check that the logged address is yours:
`journalctl --user -u mud-server | grep 'Guest joined'`. If it is a Cloudflare address, the ranges are
stale. Do not enable `MaxJoinsPerMinutePerIP` before that check.

### CI and auto-deploy

GitHub Actions runs the tests on every PR. **Merging to `main` in this repo or `mud_frontend`
deploys to production** once the tests pass, while the repository variable `AUTO_DEPLOY` is
`true` (Settings → Secrets and variables → Actions → Variables). Set it to `false` in both repos to
freeze deploys, for example during the con. The Deploy workflow can also be run by hand from the
Actions tab to deploy `main` or roll back. One-time setup: `deploy/setup-github-ci.sh <droplet IP> [owner/repo ...]` (no repositories means this one and `mud_frontend`; name `chryzmo/mud_content` to set up the content repository).

**World content.** A deploy also takes the private `mud_content` repository: the Deploy workflow checks out its `main` (or the commit that triggered the deploy) into `world/` with the read-only `CONTENT_READ_TOKEN` secret of the `production` environment, and the server image build validates it (errors fail the build, so a broken world never reaches the host), copies it to `/app/world` and sets `MUD_WORLD_DIR`. A push to `mud_content` deploys like a push here, gated on its own validation and on its own `AUTO_DEPLOY` variable. `world/` is gitignored: this repository is public and must never hold the content. If the new server does not answer within 60 seconds after a deploy, `deploy/deploy.sh` rolls back the images it changed and exits 1. Rolling back by hand does not need the content token. Set `AUTO_DEPLOY` to `false` in all three repositories to freeze deploys.
