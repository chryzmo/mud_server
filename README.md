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
