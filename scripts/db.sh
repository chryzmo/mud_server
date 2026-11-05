#!/usr/bin/env bash
# Manage the local Postgres container.
#   scripts/db.sh up      start it and wait until it accepts connections
#   scripts/db.sh down    stop it (data is kept)
#   scripts/db.sh reset   delete the data volume and start fresh (reseeds the dev database)
#   scripts/db.sh psql    open psql on the dev database
set -euo pipefail
cd "$(dirname "$0")/.."

if command -v podman-compose > /dev/null; then
	compose=(podman-compose)
elif command -v docker > /dev/null && docker compose version > /dev/null 2>&1; then
	compose=(docker compose)
else
	echo "db.sh: need podman-compose or docker compose" >&2
	exit 1
fi

wait_ready() {
	# Check over TCP inside the container: during first-run init Postgres only
	# listens on its unix socket, so this waits for the seed scripts to finish.
	for _ in $(seq 1 60); do
		if "${compose[@]}" exec -T db psql -h 127.0.0.1 -U mud -d mud_test -c 'select 1' > /dev/null 2>&1; then
			return 0
		fi
		sleep 1
	done
	echo "db.sh: Postgres did not become ready in 60s" >&2
	"${compose[@]}" logs db | tail -20 >&2
	exit 1
}

case "${1:-up}" in
	up)
		"${compose[@]}" up -d db > /dev/null
		wait_ready
		;;
	down)
		"${compose[@]}" down
		;;
	reset)
		"${compose[@]}" down -v
		"${compose[@]}" up -d db > /dev/null
		wait_ready
		;;
	psql)
		exec "${compose[@]}" exec db psql -U mud -d mud
		;;
	*)
		echo "usage: scripts/db.sh [up|down|reset|psql]" >&2
		exit 2
		;;
esac
