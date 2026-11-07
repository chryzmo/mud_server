#!/usr/bin/env bash
# A local Phase 4 load run: a server pinned to the production droplet's two CPUs, playing the generic load
# world (mudbot world), and guest bots using the full verb mix. Local only: the dedicated mud_load database,
# loopback, nothing is sent anywhere else.
#
#   scripts/load-crowd.sh [COUNT] [spread|feast] [SECONDS] [INTERVAL]
#     COUNT     guests (default 1000)
#     spread    guests walk about and use every verb, spread over copies of the Lobby and Garden
#     feast     guests walk to the Feast Hall (capacity 200) and fill it; the rest talk in the lobby copies
#     SECONDS   play time after everyone has joined and the feast has started (default 150: the Feast starts 90 to 150 s after the server does)
#     INTERVAL  each bot's pause between actions (default 6s, the "realistic" profile)
#
# Prints the bots' report, the server's CPU and peak memory, and its close-reason counts. A failed
# run is kept: the server log and the report stay in the output directory printed at the end.
set -euo pipefail
cd "$(dirname "$0")/.."

count="${1:-1000}"
scenario="${2:-spread}"
seconds="${3:-150}"
interval="${4:-6s}"
case "$scenario" in spread | feast) ;; *) echo "usage: $0 [COUNT] [spread|feast] [SECONDS] [INTERVAL]" >&2; exit 2 ;; esac

out="$(mktemp -d "${TMPDIR:-/tmp}/mud-load-crowd.XXXXXX")"
scripts/db.sh up >/dev/null 2>&1 || echo "load-crowd.sh: could not start the local database; is one already running?" >&2
go build -o "$out/mudbot" ./src/cmd/mudbot
go build -o "$out/server" ./src

export MUD_BOT_PASSWORD="load-fixture-1" # pre-commit: allow-secret (a throwaway password for local fixture accounts)
export MUD_BOT_DATABASE_URL="postgres://mud:mud@127.0.0.1:5432/mud_load"
"$out/mudbot" seed -reset -count 1 >/dev/null
"$out/mudbot" world -dir "$out/world" -feast-in 70s

/usr/bin/time -v -o "$out/server.time" env GOMAXPROCS=2 \
	MUD_DATABASE_URL="$MUD_BOT_DATABASE_URL" MUD_LISTEN_ADDR=127.0.0.1:18080 MUD_WORLD_DIR="$out/world" \
	taskset -c 0,1 "$out/server" >"$out/server.log" 2>&1 &
time_pid=$!
for _ in $(seq 1 100); do
	(exec 3<>/dev/tcp/127.0.0.1/18080) 2>/dev/null && break
	sleep 0.2
done

stay=()
[ "$scenario" = feast ] && stay=(-stay)
set +e
GOMAXPROCS=8 taskset -c 2-15 "$out/mudbot" storm -url ws://127.0.0.1:18080/ws -count "$count" -join-workers 16 \
	-mix verbs "${stay[@]}" -interval "$interval" -cycles 0 -before 0 -between "${seconds}s" \
	-join-timeout 10m >"$out/report.json" 2>"$out/bots.err"
status=$?
set -e

pkill -INT -f "$out/server" || true
wait "$time_pid" 2>/dev/null || true

echo "== report ($scenario, $count guests, $seconds s, interval $interval; bots exit $status)"
cat "$out/report.json"
echo "== server"
grep -E "Elapsed|Maximum resident|User time|System time" "$out/server.time" || true
echo "== close reasons and warnings in the server log"
grep -i -E "close|queue full|panic|recovered" "$out/server.log" | tail -15 || true
echo "== kept in $out"
exit "$status"
