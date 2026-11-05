# AGENTS.md — mud_server

Go MUD server. Players connect over a websocket. Sibling repo: `../mud_frontend` (player client).
`../mud_worldbuilder` (a room editor) is retired; world content will move into data files here.

## Current goal and plans

The project is being prepared for a weekend event at MAGFest 2027 (mid-January). The plan lives in
`plans/magfest-2027.md`; read it before starting non-trivial work. `plans/` holds plans for this
project and any subsystem, and is gitignored, so it is local working state, not repo history.

- Check the plan's phase and checklist before picking up work; don't jump ahead of phases without asking.
- When work lands, tick the checklist, update the status line, and add a dated entry to the log.
- For a new subsystem, write `plans/<subsystem>.md` and add it to `plans/README.md`.

## Layout

- `src/main.go`: HTTP entry point; routes `/ws` (and `/`, a plain-text liveness check).
- `src/internal/mudserver/`: game logic: server, players, rooms, world, commands, items, equipment, NPCs, spawners.
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
- **Commit through the pre-commit hook.** Run `scripts/install-git-hooks.sh` once per clone. The
  hook (`.githooks/pre-commit`) blocks files that usually hold secrets, secrets in the staged
  changes, merge-conflict markers and files over 1 MB, then runs `scripts/test.sh` (skipped if
  that exact tree already passed). Don't use `git commit --no-verify` unless the user asks. If the
  secret scan flags something safe, mark that line with a `pre-commit: allow-secret` comment.
- In Claude Code, `.claude/settings.json` adds a Stop hook (`scripts/agent-stop-hook.sh`) that runs
  `scripts/test.sh` when a turn changed the repo and sends failures back to you. Other agents:
  run it yourself before finishing.

Integration tests (`src/main_test.go`) run a real server and talk to it over a websocket; they
drop and reseed `mud_test` before each test, and skip when `MUD_TEST_DATABASE_URL` is unset.
Prefer that style for gameplay behavior: send the command a player would type, assert on what
comes back. Don't weaken `-race` or skip tests under it; a race report is a real bug.

CI runs on 2-CPU GitHub runners. For anything timing-sensitive (floods, timeouts, concurrency),
also run `GOMAXPROCS=2 taskset -c 0,1 scripts/test.sh` before opening a PR; a test that only
passes on a many-core machine will fail on `main` and block the deploy.

## Deploying

- **Deploy only when the user asks.** `deploy/deploy.sh` changes the live game; never run it,
  `rollback`, or anything over SSH on the Droplet on your own initiative.
- **Merging to `main` deploys.** CI (`.github/workflows/`) deploys every push to `main` in this repo
  or `mud_frontend` after the tests pass, while the `AUTO_DEPLOY` repository variable is `true`. So
  never merge a PR, or push to `main`, unless the user asks; that is a production deploy.
- `deploy/deploy.sh` refuses uncommitted changes, runs both repos' `scripts/test.sh`, builds the
  images locally, ships only images the host lacks, keeps the previous images for `rollback`, and
  restarts only what changed. `deploy/deploy.sh status` is read-only.
- Base images in `deploy/Containerfile.*` are pinned by digest and built with `--timestamp 0` so
  unchanged images keep their IDs and aren't re-uploaded. Change a pin deliberately.
- The `admin` user has sudo, for maintenance. Production runs as rootless Podman under the
  `deploy` user (no sudo; CI logs in as it), managed by systemd Quadlet units
  (`deploy/quadlet/`); logs are in `journalctl --user -u mud-server` on the host. Secrets live on the
  host in `~/mud/secrets.env` and `~/mud/caddy.env`, never in git.
- The domain is proxied through Cloudflare: SSH to the Droplet's IP (`deploy/deploy.local.env`), and
  remember Caddy sees Cloudflare's IPs unless trusted-proxy handling is configured.

## How the code works today

- **One game loop owns all game state** (`MudServer.run` in `mudserver.go`): online players,
  rooms, items, NPCs, spawners. It runs queued events one at a time, a world tick
  (`RoomTickDurationInMS`, 250 ms) that drives NPC turns and spawners, the periodic save, and
  shutdown. Game code needs no locks because nothing else touches game state.
- **Never touch game state from another goroutine.** From outside the loop, use `enqueue` (fire and
  forget) or `onLoop` (run and wait). The only exception is `World.RoomMap`, which doesn't change
  after startup.
- `ProcessInput` (one goroutine per connection) handles `/LOGIN` and `/REGISTER` itself (database
  lookups and bcrypt stay off the loop), then `admit` places the player on the loop. After that it
  queues each command with `enqueue`, and the player's disconnect when the connection ends.
- Timed behavior is a timestamp checked on the tick (`nextAttackAt`, `nextSpawnAt`), not a goroutine
  or `time.Ticker`.
- Each connection is a `Session` (`session.go`): sends go into its bounded queue and one writer
  goroutine drains it, with a write timeout and keepalive pings. `SendMessage` never blocks; a client
  whose queue fills is disconnected. Never write to the websocket directly.
- **Saving is batched and off the loop** (`persist.go`). Code that changes a player sets
  `player.dirty = true`; code that changes a room's contents sets `room.dirty = true`. Every
  `SaveIntervalMS` (5 s) the loop snapshots whatever is dirty and the saver goroutine writes it in
  one transaction. Disconnects save at once, and a login under the same name waits for that save.
  Never write game state to the database from a command; mark it dirty. A new kind of state change
  needs its dirty mark and a test that it reaches the database (see `src/persist_test.go`).
- Some commands still *read* the database on the loop: `EQUIP` looks up the equipment catalog and
  NPC kills look up the gold item. Prefer not to add more; a catalog cache would remove these.
- Output is plain text with ANSI colors. The client depends on those colors (red = NPC,
  blue = player, green = item, magenta = whisper) and on `STATUPDATE:` lines. Changing message text
  or colors can break `../mud_frontend`.

## Conventions

- Match the surrounding code: each type has a `FetchFromDB` / `WriteToDB` pair, commands are
  `MudCommand` entries registered in `GenerateCommandList`, and DB writes use `pgx.NamedArgs`.
- Files aren't uniformly `gofmt`ed. Format files you substantially edit, but put formatting-only
  changes in their own commit so diffs stay reviewable.
- There is no REST API. Anything added beside `/ws` is public through Caddy unless the Caddyfile
  blocks it, so admin or GM endpoints need authentication from the start.
- Commit or push only when asked. Branch off `main` for changes; the history uses one PR per feature.
