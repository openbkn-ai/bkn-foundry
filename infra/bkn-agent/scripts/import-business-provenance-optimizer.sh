#!/usr/bin/env bash
set -euo pipefail

# 导入或更新业务溯源优化 Agent。脚本不保存任何凭据；调用方显式提供自己的
# BKN Agent 管理端点与身份头，适用于本地、测试和正式环境。
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
