#!/usr/bin/env bash
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
workspace="$(mktemp -d)"
trap 'rm -rf "$workspace"' EXIT

(
  cd "$workspace"
  go work init "$repo_root/adp/bkn/bkn-backend/server" "$repo_root/vega/vega-backend/server"
)

cd "$repo_root/adp/bkn/bkn-backend/server"
GOWORK="$workspace/go.work" I18N_MODE_UT=true \
  go test -tags integration ./logics/cypher -run '^TestBKNVegaLikeContract$' -count=1
