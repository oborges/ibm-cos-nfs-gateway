#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

MOUNT_POINT="${MOUNT_POINT:-/mnt/cos-nfs}"
PROFILE="${PROFILE:-standard}"
RESULTS_ROOT="${RESULTS_ROOT:-benchmark-results}"

if ! command -v python3 >/dev/null 2>&1; then
  echo "python3 is required to run the benchmark suite" >&2
  exit 1
fi

exec python3 scripts/benchmark_suite.py \
  --mount "$MOUNT_POINT" \
  --profile "$PROFILE" \
  --results-root "$RESULTS_ROOT" \
  "$@"
