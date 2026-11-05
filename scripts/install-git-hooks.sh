#!/usr/bin/env bash
# Point git at the tracked hooks in .githooks/ (pre-commit: secret scan,
# conflict markers, large files, tests). Run once per clone.
set -euo pipefail
cd "$(dirname "$0")/.."
git config core.hooksPath .githooks
echo "Git hooks enabled from .githooks/"
