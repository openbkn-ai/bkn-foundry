#!/usr/bin/env bash
set -euo pipefail

# Import or update the business-provenance optimization agent. This script
# stores no credentials; callers explicitly provide their own BKN Agent
# management endpoint and identity headers for local, test, or production use.
: "${BKN_AGENT_URL:?set BKN_AGENT_URL, for example http://127.0.0.1:30800/api/bkn-agent/v1}"
: "${BKN_ACCOUNT_ID:?set BKN_ACCOUNT_ID}"

command -v jq >/dev/null || { echo "jq is required to wrap the BKN Agent import package" >&2; exit 1; }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PACKAGE="$SCRIPT_DIR/../deploy/agents/business-provenance-optimizer.json"

curl --fail-with-body --silent --show-error \
  -X POST "$BKN_AGENT_URL/import" \
  -H "Content-Type: application/json" \
  -H "x-account-id: $BKN_ACCOUNT_ID" \
  -H "x-account-type: ${BKN_ACCOUNT_TYPE:-user}" \
  --data-binary "$(jq -c '{package: .}' "$PACKAGE")"
