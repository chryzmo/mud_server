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
comes back. Don't weaken `-race`; a race report is a real bug. The only sanctioned exception is
`skipUntilGameLoop` in `src/gameplay_test.go`, for the known races listed in `plans/game-loop.md`
that step 3 removes; don't add tests to it for new races.

## Deploying

- **Deploy only when the user asks.** `deploy/deploy.sh` changes the live game; never run it,
  `rollback`, or anything over SSH on the Droplet on your own initiative.
- `deploy/deploy.sh` refuses uncommitted changes, runs both repos' `scripts/test.sh`, builds the
  images locally, ships only images the host lacks, keeps the previous images for `rollback`, and
  restarts only what changed. `deploy/deploy.sh status` is read-only.
- Production runs as rootless Podman under the `deploy` user, managed by systemd Quadlet units
  (`deploy/quadlet/`); logs are in `journalctl --user -u mud-server` on the host. Secrets live on the
  host in `~/mud/secrets.env` and `~/mud/caddy.env`, never in git.
- The domain is proxied through Cloudflare: SSH to the Droplet's IP (`deploy/deploy.local.env`), and
  remember Caddy sees Cloudflare's IPs unless trusted-proxy handling is configured.

## How the code works today

- `ProcessInput` (one goroutine per connection) reads frames, handles `/LOGIN` and `/REGISTER`
  itself, and queues everything else on `MudServer.Broadcast`.
- `HandleMessages` (one goroutine) applies aliases and runs the command from `GenerateCommandList`.
- Each room runs its own `ProcessBroadcast` and `RunTicker` goroutines; each spawner runs a ticker.
- Output is plain text with ANSI colors. The client depends on those colors (red = NPC,
  blue = player, green = item, magenta = whisper) and on `STATUPDATE:` lines. Changing message text
  or colors can break `../mud_frontend`.

Shared state is not consistently synchronized. Don't add new cross-goroutine access to maps or
player fields; the plan replaces this with a single game loop.

## Conventions

- Match the surrounding code: each type has a `FetchFromDB` / `WriteToDB` pair, commands are
  `MudCommand` entries registered in `GenerateCommandList`, and DB writes use `pgx.NamedArgs`.
- Files aren't uniformly `gofmt`ed. Format files you substantially edit, but put formatting-only
  changes in their own commit so diffs stay reviewable.
- There is no REST API. Anything added beside `/ws` is public through Caddy unless the Caddyfile
  blocks it, so admin or GM endpoints need authentication from the start.
- Commit or push only when asked. Branch off `main` for changes; the history uses one PR per feature.
