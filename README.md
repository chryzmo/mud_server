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
