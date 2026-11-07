# AGENTS.md — mud_server

Go MUD server. Players connect over a websocket. Sibling repo: `../mud_frontend` (player client).
`../mud_worldbuilder` (a room editor) is retired; world content will move into data files here.

## Current goal and plans

The project is being prepared for a weekend event at MAGFest 2027 (mid-January). The plan lives in
`plans/magfest-2027.md`; read it before starting non-trivial work. `plans/` holds plans for this
project and any subsystem, and is gitignored, so it is local working state, not repo history. In a
linked git worktree `plans` is a symlink to the main checkout's folder; `.gitignore` covers it, but
stage files by name anyway.

- Check the plan's phase and checklist before picking up work; don't jump ahead of phases without asking.
- When work lands, tick the checklist, update the status line, and add a dated entry to the log.
- For a new subsystem, write `plans/<subsystem>.md` and add it to `plans/README.md`.

## Agent handoff (Pi and Claude Code)

- This file is the shared source of project rules; `CLAUDE.md` points here. Keep rules here,
  not in separate tool-specific copies.
- At startup or when resuming, read in order: this file, `../mud_frontend/AGENTS.md`,
  `plans/README.md`, `plans/magfest-2027.md`, the active subsystem plan, then
  `plans/handoff.md` if present. If the handoff is missing, reconstruct state from the plans
  and git, and ask about unclear authorization before changing anything.
- Verify each affected repo's branch, HEAD and working-tree changes. Handoff notes are a
  snapshot, not proof that git, tests, CI or production still match. Preserve other agents'
  unfinished work; don't reset, stash, discard or overwrite it without permission.
- Before switching agents, update `plans/handoff.md`: task and explicit authorization,
  repo/branch/HEAD, unfinished changes, decisions, exact verification commands and results,
  outstanding checks, PR/CI status, and concrete next actions. Identify what commit or working
  tree was tested; don't describe tests as current after subsequent changes.
- Keep the handoff as one current snapshot, not another log. Completed work and durable
  decisions still go in the plans' status/checklists and dated logs. The handoff is gitignored
  local state; explicitly transfer it if moving machines.
- Proposed next actions are not permission to commit, push, open a PR, merge, deploy or SSH.
  Record the user's actual authorization and ask when unclear. Never copy secrets into notes;
  refer to their file paths instead.
- Claude Code's hooks are tool-specific. In Pi, run the required checks explicitly; do not
  assume another tool's hooks ran. Report anything not verified.

## Layout

- `src/main.go`: HTTP entry point; routes `/ws` (and `/`, a plain-text liveness check).
- `src/cmd/mudbot/`: opt-in websocket load client; `src/internal/mudbot/` holds its runner
  and test-account seeder. See README.md for local usage and measurement limitations.
- `src/internal/mudserver/gm.go`: the game master: the `GM` command (gmOnly commands are invisible to others), `AddGM` and
  `mud_server -add-gm NAME`, mutes (`muteduntil`) and the `gm_audit` table. One GM, the owner; guests are never GMs.
- `src/internal/worldfile/` and `world-sample/`: the world content format (YAML: rooms, items, NPCs) and its
  validator, `mud_server -validate-world [-strict] DIR` (no database, no server), and `-render-world DIR` (a Markdown
  overview with a Mermaid map, for review). `world-sample/` is the small
  public sample used by tests and `scripts/test.sh`; the real world lives in the private `mud_content` repository
  (plans/event-systems.md). Keys are lower-case words and dashes, global per kind. `mudserver/content.go` applies it at startup
  when `World.ContentDir` (env `MUD_WORLD_DIR`) is set: upsert by `contentkey`, adopting same-name rows, never resetting
  runtime state; the world then loads only content rooms and takes its start room and areas from the content.
  Rooms may have `features` (examinable scenery; `mudserver/examine.go`: EXAMINE, aliases, prefixes, ambiguity).
  Rooms may have `people` (friendly characters with greeting and topics; `mudserver/people.go`: TALK and ASK).
  Rooms may have `rules` and `gates` (puzzles; `mudserver/rules.go`: verbs, conditions, effects, per-player exits) and players carry
  `state` (flags, counters, sequences, gates; `mudserver/state.go`, persisted through a queue to `player_state`).
  `puzzles:` and `region` feed JOURNAL, HINT and MAP (`mudserver/guide.go`; rooms entered are `visited` state).
- `src/internal/mudserver/`: game logic. The server and its loop (`mudserver.go`), players (`mudplayer.go`),
  rooms and world (`room.go`, `world.go`), commands (`mudcommand.go`), items, equipment, NPCs and spawners.
  The wire protocol (`protocol.go`) and connections (`session.go`, `closestats.go`). Guests and their
  lifecycle: `guest.go`, `names.go` (+ `blocked_words.txt`), `resume.go`, `recover.go`, `idle.go`. Crowd
  control: `crowd.go` (area copies, caps), `ratelimit.go` (join limit), `clientip.go` (client addresses).
  Saves: `persist.go`. Config: `serverconfig.go`; every key and default is in README.md.
- `deploy/`: the Droplet deployment: `Caddyfile` and `cloudflare-ips.caddy`, Quadlet units, container
  files, `deploy.sh`, host setup scripts, and `test-client-ip.sh` (proves the client-IP header handling
  with the real Caddy image).
- `src/internal/database/`: pgx pool wrapper.
- `src/internal/muddata/`: status and error codes. `src/internal/mudutils/`: ANSI color helpers.
- `src/sql/`: schema and seed scripts, one per table.
- `config/server_config.json`: server, DB and game settings, loaded relative to the working directory.
- `resources/MOTD`: message of the day.

## Build and run

```sh
scripts/db.sh up        # start local Postgres 17 (podman-compose or docker compose); seeds `mud` on first start
go build ./src          # build
go run ./src            # run from the repo root; config path is relative to the cwd
scripts/test.sh         # go vet + all tests with -race against the `mud_test` database
scripts/db.sh reset     # wipe the database volume and reseed
```

Config comes from `config/server_config.json`; `MUD_CONFIG`, `MUD_LISTEN_ADDR` and
`MUD_DATABASE_URL` override it. Schema and seed scripts are in `src/sql/`, loaded in the order
listed in `src/sql/load_order.txt` (foreign keys depend on it). If you add a table, add its
script there.

## Tests and commits (required)

- **New features ship with tests.** Every new feature or behavior change includes tests that cover
  it, in the same PR. A bug fix includes a test that fails without the fix. If something can't
  reasonably be tested automatically, say why in the PR description.
- **The local suite must pass before you open or update a PR.** Run `scripts/test.sh` and make
  sure it passes, then report it in the PR's testing section. Don't open a PR with failing or
  skipped checks; if a failure isn't caused by your change, raise it with the user first.
- **Every PR gets a real description.** Say what changed and why, the deploy note (merging deploys while
  `AUTO_DEPLOY` is true; say whether players see a change), what was tested and what was not verified.
  Pass it with `gh pr create --body-file <file>` (or `--body "<text>"`), never `--body -`: `gh` takes that
  as the literal text "-" and the PR is left blank. After creating or editing, check it with
  `gh pr view <n> --json body -q '.body|length'`.
- **Commit through the pre-commit hook.** Run `scripts/install-git-hooks.sh` once per clone. The
  hook (`.githooks/pre-commit`) blocks files that usually hold secrets, secrets in the staged
  changes, merge-conflict markers and files over 1 MB, then runs `scripts/test.sh` (skipped if
  that exact tree already passed). Don't use `git commit --no-verify` unless the user asks. If the
  secret scan flags something safe, mark that line with a `pre-commit: allow-secret` comment.
- Tests do not run automatically at the end of an agent turn. Run `scripts/test.sh` once before committing
  or pushing (the pre-commit hook runs it again unless that exact tree already passed), and during
  development when it makes sense.

- **Protocol changes regenerate the wire transcripts.** `TestWireTranscripts` (`src/transcripts_test.go`)
  plays a reference client through join, resume, gap, replaced, recover, idle and the limits against the real
  server and compares the frames with `src/testdata/transcripts.json`. After an intended change run
  `UPDATE_TRANSCRIPTS=1 go test ./src -run TestWireTranscripts`, then `scripts/check-transcripts.sh --update`
  to copy it to `../mud_frontend/src/net/testdata/transcripts.json`, and change the client's parser and tests
  in the same coordinated pair of PRs. `scripts/check-transcripts.sh` fails if the copies differ.

Integration tests (`src/main_test.go`) run a real server and talk to it over a websocket; they
drop and reseed `mud_test` before each test, and skip when `MUD_TEST_DATABASE_URL` is unset.
Prefer that style for gameplay behavior: send the command a player would type, assert on what
comes back. Helpers: `startTestServerWithConfig` (build a `Configuration` in the test; defaults apply),
`passwordPlayer`/`createAccount`/`send`/`expect` for a password player with text-style assertions (`flatten` renders frames the
way a player once saw them), `joinGuest` and its `guest` type (`resume_test.go`: a reference client that
counts frames, drops, reconnects and resumes), `readMessage`/`expectMessage` for exact frames, and
`waitForClose` for session-close reasons. A gorilla connection that hits a read timeout is dead, so wait
for a message instead of probing for silence. `deploy/test-client-ip.sh` (needs podman) checks the Caddy
side. The suite includes 200 bots playing for 10 s, with fixture-only bcrypt cost 4
and a durable NPC; it exercises real login and gameplay, not production-cost login storms.
The manual load fixture uses only a dedicated local `mud_load` database; never point the
seeder at the development or production DB, or load-test production without authorization.
Don't weaken `-race` or skip tests under it; a race report is a real bug.

CI runs on 2-CPU GitHub runners. For anything timing-sensitive (floods, timeouts, concurrency),
also run `GOMAXPROCS=2 taskset -c 0,1 scripts/test.sh` before opening a PR; a test that only
passes on a many-core machine will fail on `main` and block the deploy.

## AI assistance attribution

- End AI-assisted commit messages and PR descriptions with exactly one trailer:
  `AI-Assisted-By: <agent>`. Use `ChatGPT` for ChatGPT in Pi or `Claude Code` for Claude Code;
  use an equivalent agent/product name for other tools, never a model name.
- Do not use AI `Co-Authored-By` trailers or otherwise imply AI authorship. Preserve the user's
  normal git author identity. Do not include model names or Pi/Claude session information
  (IDs, URLs, paths, or generated-with/session-link footers) in commits or PR descriptions.
- This replaces earlier requests for environment-provided attribution/session lines. Do not
  rewrite existing commits or published history without explicit permission.
- User-wide copies live in `~/.pi/agent/AGENTS.md` and `~/.claude/CLAUDE.md`; this repo copy
  carries the rule to other machines. Claude Code's user settings also set its commit/PR
  attribution to `AI-Assisted-By: Claude Code` rather than the default co-author footer.

## Deploying

- **Deploy only when the user asks.** `deploy/deploy.sh` changes the live game; never run it,
  `rollback`, or anything over SSH on the Droplet on your own initiative.
- **Merging to `main` deploys.** CI (`.github/workflows/`) deploys every push to `main` in this repo
  or `mud_frontend` after the tests pass, while the `AUTO_DEPLOY` repository variable is `true`. So
  never merge a PR, or push to `main`, unless the user asks; that is a production deploy.
- `deploy/deploy.sh` refuses uncommitted changes, runs both repos' `scripts/test.sh`, builds the
  images locally, ships only images the host lacks, keeps the previous images for `rollback`, and
  restarts only what changed. `deploy/deploy.sh status` is read-only.
- The world content (private `mud_content`) is checked out into `world/` by the Deploy workflow (token: `CONTENT_READ_TOKEN`,
  `production` environment of each calling repo) and baked into the server image, which validates it at build time.
  `world/` is gitignored and must never be committed here: this repository is public. A deploy whose server does not
  answer within 60 s is rolled back (the images it changed) by `deploy.sh`.
- Base images in `deploy/Containerfile.*` are pinned by digest and built with `--timestamp 0` so
  unchanged images keep their IDs and aren't re-uploaded. Change a pin deliberately.
- The `admin` user has sudo, for maintenance. Production runs as rootless Podman under the
  `deploy` user (no sudo; CI logs in as it), managed by systemd Quadlet units
  (`deploy/quadlet/`); logs are in `journalctl --user -u mud-server` on the host. Secrets live on the
  host in `~/mud/secrets.env` and `~/mud/caddy.env`, never in git.
- The domain is proxied through Cloudflare: SSH to the Droplet's IP (`deploy/deploy.local.env`). Caddy
  trusts `CF-Connecting-IP` only from the ranges in `deploy/cloudflare-ips.caddy` (refresh with
  `deploy/update-cloudflare-ips.sh` before an event) and passes `X-Client-IP` to the game server. After any
  change there, join once on the live site and confirm the logged address is yours, not Cloudflare's.

## How the code works today

- **One game loop owns all game state** (`MudServer.run` in `mudserver.go`): online players,
  rooms, items, NPCs, spawners. It runs queued events one at a time, a world tick
  (`RoomTickDurationInMS`, 250 ms) that drives NPC turns and spawners, the periodic save, and
  shutdown. Game code needs no locks because nothing else touches game state. Queued events,
  ticks and periodic saves recover independently from panics; a panicking command notifies its
  player and the loop continues. Recovery cannot roll back partially changed state.
- **Never touch game state from another goroutine.** From outside the loop, use `enqueue` (fire and
  forget) or `onLoop` (run and wait). There are no exceptions: `World.RoomMap` gains cloned rooms as
  crowds arrive, so a connection reader must not read it.
- Area copies (`crowd.go`): login records only where a player was saved (`savedRoomID`) and `admit` places
  them on the loop (`World.place`, fullest copy below `SoftCap`). Copies are never saved; persisted room IDs
  always name the template (`Room.persistID`). Movement goes through `World.resolveMove`: a room at
  `HardCap` refuses (`ERR_ROOM_FULL`), and crossing an area boundary is a placement that prefers the
  player's last copy of that area (`MudPlayer.lastCopy`).
- Every websocket frame, both ways, is a flat JSON object with a type, `{"t":"say","who":"bob","text":"hi"}`
  (`protocol.go`; design in `plans/json-protocol.md`). The version is the websocket subprotocol `mud.v1`,
  checked once per connection. Send through `SendMessage`/`SendAuth`/`SendError`/`SendLook`/etc., never
  raw bytes. Fan-out and chatter use typed events (`say`, `arrive`, `leave`, `whisper`), not coloured
  text; encode once (`encodeSay`, `BroadcastFrame`) and queue the same slice to everyone.
- `ProcessInput` (one goroutine per connection) handles `join`, `resume`, `recover` and `login`
  itself (database lookups and bcrypt stay off the loop), then `admit` or `attach` puts the player in the
  world on the loop. After that it queues each command with `enqueue`, and what happens to the player
  (dormant for a guest, removed otherwise) when the connection ends.
- Guests (`guest.go`, `names.go`, plans/guest-sessions.md): `join {name}` creates a `player` row with `guest=true`,
  returns a resume token and a recovery code once, and stores only their SHA-256 hashes. Names are 3-16 of
  `[A-Za-z0-9_-]`, unique ignoring case (unique index on `lower(username)`), checked against a reserved list and
  `blocked_words.txt` (compiled in; format in the file). Schema changes for older databases go in `EnsureSchema`.
- Resume (`resume.go`): a dropped guest goes dormant for `ResumeGraceMS` (default 90 s), still in its room with
  no announcement, and `resume {token, received}` reattaches a new connection and replays what it missed;
  after the grace period, or a restart, it is an ordinary arrival (`gap`). Replay needs no sequence number in
  frames: every frame except `hello`, `auth` and `error` is *counted* (server: `MudPlayer.sendFrame`; client:
  as it processes them) and a ring of shared references keeps the last `ReplayFrames`. So send counted frames
  only through `sendFrame` (or `SendMessage` and friends), and replies to the reader's own session only through
  `Session.sendAuth/sendError`. Reader goroutines keep their own `session` and never read `player.Session`
  after admission: a resume can hand the player to a newer connection, and only the loop may change it.
- Recovery (`recover.go`): `recover {name, code}` takes a character back with the recovery code shown at join. It
  verifies the hash, rotates token and code in one compare-and-swap `UPDATE` (so two recoveries with the same
  code cannot both win), and attaches like a resume with no frames remembered (`noFramesSeen`, so a gap and a
  fresh look). Five wrong codes lock the name for `RecoveryLockMS` (15 min); answers never reveal whether a name
  exists. Any new path that changes a guest's token must update `ms.byToken` and `player.tokenHash` on the loop.
- Idle reclaim (`idle.go`): a guest with no command for `IdleTimeoutMS` (default 30 min, -1 never) gets an `idle`
  error and is closed (reason `idle`), then goes through the normal dormant grace period. Activity is commands,
  admission and resume, not keepalives; password accounts are never kicked. A client should treat `idle` as
  "wait for a tap" and not reconnect by itself, or it holds the slot again.
- Join limit (`ratelimit.go`): one token bucket for every join, resume, recover and login, checked by
  the reader before any database work (`MudServer.allowJoin`; requests that do no work, such as one from
  someone already in the game or with a missing field, do not spend it). It does not look at who is asking:
  venue WiFi and carrier NAT put many players behind one address. Refusals are `error rate_limited` with a
  `retry_ms` hint that includes jitter; clients should wait that long, not back off exponentially. Defaults
  (`JoinPerSecond` 1000, `JoinBurst` 5000, -1 off) come from the storm measurements; see the file comment.
- Client IPs (`clientip.go`, `deploy/Caddyfile`): Cloudflare sets `CF-Connecting-IP`; Caddy honours it only for
  connections from Cloudflare's published ranges (`deploy/cloudflare-ips.caddy`, regenerate with
  `deploy/update-cloudflare-ips.sh` before an event) and passes the result to the game server as `X-Client-IP`;
  the game server believes that header only from `Server.TrustedProxies` (default: loopback, where Caddy runs).
  `deploy/test-client-ip.sh` proves it with the real pinned Caddy image. The address is for logs and
  abuse forensics (`LogClientIPs`) and an optional ceiling (`MaxJoinsPerMinutePerIP`, off by default, never a
  fairness rule: venue WiFi and carrier NAT put many players behind one address). If Caddy ever stops
  trusting Cloudflare (stale ranges), every client looks like a Cloudflare address: do not turn the ceiling on
  without checking a live join's logged address first.
- Every session close has a reason (`Session.CloseWith`, `closestats.go`): `queue_full`, `write_error`,
  `ping_error`, `client_closed`, `read_timeout`, `read_limit`, `read_error`, `shutdown`, `server`. The first
  reason wins, totals are logged every 10 s while they change, and `MudServer.SessionCloseCounts()` exposes
  them. Use it when drops happen under load; add a reason rather than a bare `Close()` for new close paths.
- Timed behavior is a timestamp checked on the tick (`nextAttackAt`, `nextSpawnAt`), not a goroutine
  or `time.Ticker`.
- Each connection is a `Session` (`session.go`): sends go into its bounded queue and one writer
  goroutine drains it, with a write timeout and keepalive pings. `SendMessage` never blocks; a client
  whose queue fills is disconnected. Never write to the websocket directly. Incoming messages
  are limited to 4096 bytes (including fragments), with a per-session token bucket of 10 commands/s,
  burst 20. Excess input is dropped before parsing or authentication; slow-down notices are limited
  to once per second. These limits are configurable under `Connection` (`ReadLimitBytes`,
  `CommandsPerSecond`, `CommandBurst`); pre-login input and blank messages count too.
- **Saving is batched and off the loop** (`persist.go`). Code that changes a player sets
  `player.dirty = true`; code that changes a room's contents sets `room.dirty = true`. Every
  `SaveIntervalMS` (5 s) the loop snapshots whatever is dirty and the saver goroutine writes it in
  one transaction. Disconnects save at once, and a login under the same name waits for that save.
  Never write game state to the database from a command; mark it dirty. A new kind of state change
  needs its dirty mark and a test that it reaches the database (see `src/persist_test.go`).
- Some commands still *read* the database on the loop: `EQUIP` looks up the equipment catalog and
  NPC kills look up the gold item. Prefer not to add more; a catalog cache would remove these.
- Prose is `text` with ANSI colours (red = NPC, blue = player, green = item) that the client styles per
  line. Changing a message type or field, or the ANSI in `text`, can break `../mud_frontend`: change both
  sides together, and keep the exact-shape and size tests in `protocol_test.go` current. What a client
  must do with each message is in `../mud_frontend/AGENTS.md`.

## Conventions

- Match the surrounding code: each type has a `FetchFromDB` / `WriteToDB` pair, commands are
  `MudCommand` entries registered in `GenerateCommandList`, and DB writes use `pgx.NamedArgs`.
- Files aren't uniformly `gofmt`ed. Format files you substantially edit, but put formatting-only
  changes in their own commit so diffs stay reviewable.
- There is no REST API. Anything added beside `/ws` is public through Caddy unless the Caddyfile
  blocks it, so admin or GM endpoints need authentication from the start.
- Commit or push only when asked. Branch off `main` for changes; the history uses one PR per feature.
