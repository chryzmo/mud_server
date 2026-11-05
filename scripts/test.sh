#!/usr/bin/env bash
# Run every server check: go vet, then all tests with the race detector.
# Starts the local Postgres container for the integration tests unless
# MUD_TEST_DATABASE_URL is already set (e.g. to point at another database).
set -euo pipefail
cd "$(dirname "$0")/.."

if [ -z "${MUD_TEST_DATABASE_URL:-}" ]; then
	if scripts/db.sh up; then
		export MUD_TEST_DATABASE_URL="postgres://mud:mud@127.0.0.1:5432/mud_test"
	else
		echo "test.sh: could not start the test database; integration tests will be skipped" >&2
	fi
fi

go vet ./...
go test -race -count=1 ./...
