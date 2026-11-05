# AGENTS.md — mud_server

Go MUD server. Players connect over a websocket; the worldbuilder uses a small REST API.
Sibling repos: `../mud_frontend` (player client) and `../mud_worldbuilder` (room editor).

## Current goal and plans

The project is being prepared for a weekend event at MAGFest 2027 (mid-January). The plan lives in
`plans/magfest-2027.md`; read it before starting non-trivial work. `plans/` holds plans for this
project and any subsystem, and is gitignored, so it is local working state, not repo history.

- Check the plan's phase and checklist before picking up work; don't jump ahead of phases without asking.
- When work lands, tick the checklist, update the status line, and add a dated entry to the log.
- For a new subsystem, write `plans/<subsystem>.md` and add it to `plans/README.md`.

## Layout

- `src/main.go`: HTTP entry point; routes `/ws`, `/api/nodes`, `/api/edges`.
- `src/internal/mudserver/`: game logic: server, players, rooms, world, commands, items, equipment, NPCs, spawners.
- `src/internal/mudapi/`: REST handlers for the worldbuilder.
- `src/internal/database/`: pgx pool wrapper.
- `src/internal/muddata/`: status and error codes. `src/internal/mudutils/`: ANSI color helpers.
- `src/sql/`: schema and seed scripts, one per table.
- `config/server_config.json`: server, DB and game settings, loaded relative to the working directory.
- `resources/MOTD`: message of the day.

## Build and run

```sh
go build ./src          # build
go vet ./...            # vet
go run ./src            # run from the repo root; config path is relative to the cwd
go test -race ./...     # once tests exist
```

The server needs Postgres with the schema from `src/sql/`. Load order matters because of foreign
keys: `room.sql`, `item.sql`, `equipment.sql`, `baddienpc_generic.sql`, `baddiespawner.sql`,
`mudplayer.sql`. The seed data is currently inconsistent (see the plan's known-bugs list).

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
- Never expose `/api/*` publicly; it has no authentication and allows any origin.
- Commit or push only when asked. Branch off `main` for changes; the history uses one PR per feature.
