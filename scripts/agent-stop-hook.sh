#!/usr/bin/env bash
# Claude Code Stop hook: when an agent finishes a turn that changed this repo,
# run scripts/test.sh. Wired up in .claude/settings.json.
#
# - Skips when the working tree (tracked and untracked files) matches the last
#   tree that passed, so turns that changed nothing here cost nothing.
# - On failure, exits 2 so the test output goes back to the agent to fix.
# - If the agent is already continuing because of this hook and the tests still
#   fail, reports to the user instead of blocking again, so it can't loop.
set -uo pipefail

repo="$(cd "$(dirname "$0")/.." && pwd)"
input="$(cat)"
git_dir="$(git -C "$repo" rev-parse --absolute-git-dir)"
state="$git_dir/agent-tests-passed-tree"

# Snapshot the working tree as a git tree object using a throwaway index.
tmp_index="$(mktemp)"
trap 'rm -f "$tmp_index"' EXIT
cp "$git_dir/index" "$tmp_index" 2>/dev/null || true
GIT_INDEX_FILE="$tmp_index" git -C "$repo" add -A >/dev/null 2>&1
tree="$(GIT_INDEX_FILE="$tmp_index" git -C "$repo" write-tree)"

if [ -f "$state" ]; then
	[ "$tree" = "$(cat "$state")" ] && exit 0
elif [ "$tree" = "$(git -C "$repo" rev-parse 'HEAD^{tree}' 2>/dev/null)" ]; then
	exit 0
fi

name="$(basename "$repo")"
if output="$("$repo/scripts/test.sh" 2>&1)"; then
	echo "$tree" > "$state"
	exit 0
fi

summary="$(printf '%s\n' "$output" | tail -n 60)"
if printf '%s' "$input" | grep -q '"stop_hook_active"[[:space:]]*:[[:space:]]*true'; then
	jq -n --arg msg "$name: scripts/test.sh is still failing after the agent's retry. Last output:
$summary" '{systemMessage: $msg}'
	exit 0
fi

printf '%s: scripts/test.sh failed. Fix the failures (or explain why they are not caused by your change) before finishing.\n\n%s\n' "$name" "$summary" >&2
exit 2
