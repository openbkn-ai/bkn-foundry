#!/usr/bin/env bash
# Validate every shipped dataset against the dataset contract.
# Thin wrapper: all checks live in the bkn-eval command, not here.
set -euo pipefail
cd "$(dirname "$0")/.."
shopt -s nullglob
datasets=(datasets/*/dataset.json)
if [ ${#datasets[@]} -eq 0 ]; then
  echo "no datasets found under datasets/" >&2
  exit 1
fi
go run . validate "${datasets[@]}"
