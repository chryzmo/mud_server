#!/usr/bin/env bash
# Compares the server's wire transcripts with the copy the browser client tests replay.
# The two repos have separate CI, so this is run by whoever changes the protocol:
#   scripts/check-transcripts.sh            check; exit 1 if the copy is missing or differs
#   scripts/check-transcripts.sh --update   copy the server's file over the frontend's
# Regenerate the server's file first after a protocol change:
#   UPDATE_TRANSCRIPTS=1 go test ./src -run TestWireTranscripts
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
server="$root/src/testdata/transcripts.json"
client="${FRONTEND_TRANSCRIPTS:-$root/../mud_frontend/src/net/testdata/transcripts.json}"

if [[ "${1:-}" == "--update" ]]; then
  mkdir -p "$(dirname "$client")"
  cp "$server" "$client"
  echo "copied to $client"
  exit 0
fi
if [[ ! -f "$client" ]]; then
  echo "missing: $client (the frontend has no copy yet; run with --update)" >&2
  exit 1
fi
if ! cmp -s "$server" "$client"; then
  echo "the frontend's transcripts differ from the server's:" >&2
  echo "  $server" >&2
  echo "  $client" >&2
  echo "Update the frontend's parser and tests with the copy (--update), in the same change." >&2
  exit 1
fi
echo "transcripts match"
