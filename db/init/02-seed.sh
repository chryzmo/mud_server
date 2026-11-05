#!/usr/bin/env bash
# Runs once, when the Postgres container first initializes its data volume.
# Loads the schema and seed data into the dev database in dependency order.
set -euo pipefail
grep -v '^#' /sql/load_order.txt | while read -r f; do
	[ -n "$f" ] || continue
	echo "Loading $f"
	psql -v ON_ERROR_STOP=1 -q -U "$POSTGRES_USER" -d "$POSTGRES_DB" -f "/sql/$f" > /dev/null
done
