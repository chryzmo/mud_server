# mud_server
Golang MUD Server using websockets

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

## Bot client and local load testing

`src/cmd/mudbot` uses the public websocket protocol: login (or optional registration),
MOVE, SAY, ATTACK, and WHISPER. It continuously reads every connection. The ordinary
`scripts/test.sh` suite includes 200 bots playing for 10 seconds under `-race`.

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
steady-state gameplay, **not production-cost login/reconnect storms**. `-register` instead
creates real accounts with normal hashing, and requires a fresh prefix on subsequent runs.
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

### CI and auto-deploy

GitHub Actions runs the tests on every PR. **Merging to `main` in this repo or `mud_frontend`
deploys to production** once the tests pass, while the repository variable `AUTO_DEPLOY` is
`true` (Settings → Secrets and variables → Actions → Variables). Set it to `false` in both repos to
freeze deploys, for example during the con. The Deploy workflow can also be run by hand from the
Actions tab to deploy `main` or roll back. One-time setup: `deploy/setup-github-ci.sh <droplet IP>`.
