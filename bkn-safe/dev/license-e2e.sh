#!/usr/bin/env bash
# license-e2e.sh — end-to-end test of the bkn-safe license hub against a REAL
# license-server, started locally from source (never a shared issuer).
#
# Flow: build issuer → keygen → two reviewer accounts → serve (email
# verification off, loopback only) → register a customer → survey → apply
# community (auto-issued) + professional (dual approval) → hand the unbound
# .lic files to `go test -tags e2e`, which drives internal/license.Service
# through true activation, first-wins conflicts, copied-cert rejection and
# renewal binding checks.
#
# Usage:
#   dev/license-e2e.sh                 # LS_SRC defaults to ../../license-server (sibling checkout)
#   LS_SRC=/path/to/license-server dev/license-e2e.sh
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
SERVER_DIR="$HERE/../server"
LS_SRC="${LS_SRC:-$(cd "$HERE/../.." && pwd)/../license-server}"
# Worktree checkouts nest the repo under .claude/worktrees/...; fall back to
# the conventional sibling of the main checkout.
if [[ ! -d "$LS_SRC/server" ]]; then
  LS_SRC="$HOME/Work/openbkn/license-server"
fi
if [[ ! -d "$LS_SRC/server" ]]; then
  echo "license-server source not found — set LS_SRC=/path/to/license-server" >&2
  exit 1
fi

PORT="${PORT:-18341}"
ISSUER="http://127.0.0.1:$PORT"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/license-e2e.XXXXXX")"
CJ_CUST="$WORK/cookies-customer.txt"
CJ_REV1="$WORK/cookies-rev1.txt"
CJ_REV2="$WORK/cookies-rev2.txt"
SERVER_PID=""
cleanup() {
  [[ -n "$SERVER_PID" ]] && kill "$SERVER_PID" 2>/dev/null || true
  rm -rf "$WORK"
}
trap cleanup EXIT

say() { printf '\n== %s\n' "$*"; }

# jq is required for response plumbing.
command -v jq >/dev/null || { echo "jq is required" >&2; exit 1; }

say "build license-server from $LS_SRC"
(cd "$LS_SRC/server" && go build -o "$WORK/license-server" .)

say "keygen + reviewer accounts"
(cd "$WORK" && ./license-server keygen -dir ./keys -kid e2e-01)
(cd "$WORK" && LS_NEW_PASSWORD='E2e-pass-1!' ./license-server useradd -email rev1@e2e.local -system-admin -reviewer)
(cd "$WORK" && LS_NEW_PASSWORD='E2e-pass-2!' ./license-server useradd -email rev2@e2e.local -reviewer)

say "serve on $ISSUER (email verification OFF, loopback only)"
# exec so $SERVER_PID is the server itself, not a wrapper subshell — otherwise
# the trap kills the wrapper and a stale issuer keeps squatting on the port.
(cd "$WORK" && exec env LS_ADDR="127.0.0.1:$PORT" LS_DB="$WORK/data/license.db" LS_KEYS_DIR="$WORK/keys" \
  LS_REQUIRE_EMAIL_VERIFICATION=0 ./license-server serve >"$WORK/server.log" 2>&1) &
SERVER_PID=$!
for _ in $(seq 1 50); do
  curl -sf "$ISSUER/api/healthz" >/dev/null && break
  sleep 0.2
done
curl -sf "$ISSUER/api/healthz" >/dev/null || { echo "issuer did not come up"; tail -20 "$WORK/server.log"; exit 1; }

api() { # api <curl-args...> — fails the script on HTTP >= 400
  local out
  out="$(curl -sS -w '\n%{http_code}' "$@")"
  local code="${out##*$'\n'}"
  local body="${out%$'\n'*}"
  if [[ "$code" -ge 400 ]]; then
    echo "HTTP $code: $body" >&2
    return 1
  fi
  printf '%s' "$body"
}

say "register + login customer"
api -X POST "$ISSUER/api/register" -H 'Content-Type: application/json' \
  -d '{"email":"customer@e2e.local","password":"E2e-pass-c!","name":"e2e customer","company":"e2e"}' >/dev/null
api -c "$CJ_CUST" -X POST "$ISSUER/api/auth/login" -H 'Content-Type: application/json' \
  -d '{"email":"customer@e2e.local","password":"E2e-pass-c!"}' >/dev/null

say "survey (application gate)"
api -b "$CJ_CUST" -X PUT "$ISSUER/api/portal/survey" -H 'Content-Type: application/json' -d '{
  "answers": {
    "role": "企事业单位技术人员",
    "phone": "13800000000",
    "llms": ["Claude"],
    "scenarios": ["企业知识助手"],
    "usage_plan": "技术研究和学习",
    "project_stage": "有，正在规划",
    "agent_platforms": ["LangChain"],
    "data_platforms": ["数据仓库 / 湖仓"]
  }
}' >/dev/null

say "apply community (auto-issued)"
COMMUNITY_JSON="$(api -b "$CJ_CUST" -X POST "$ISSUER/api/portal/requests" -H 'Content-Type: application/json' \
  -d '{"edition":"community","project":"e2e-community"}')"
echo "$COMMUNITY_JSON" | jq -er '.license.license' >"$WORK/community.lic"

say "apply professional (dual review)"
REQ_ID="$(api -b "$CJ_CUST" -X POST "$ISSUER/api/portal/requests" -H 'Content-Type: application/json' \
  -d '{"edition":"professional","project":"e2e-pro"}' | jq -er '.id')"

api -c "$CJ_REV1" -X POST "$ISSUER/api/auth/login" -H 'Content-Type: application/json' \
  -d '{"email":"rev1@e2e.local","password":"E2e-pass-1!"}' >/dev/null
api -c "$CJ_REV2" -X POST "$ISSUER/api/auth/login" -H 'Content-Type: application/json' \
  -d '{"email":"rev2@e2e.local","password":"E2e-pass-2!"}' >/dev/null
api -b "$CJ_REV1" -X POST "$ISSUER/api/admin/requests/$REQ_ID/approve" >/dev/null
api -b "$CJ_REV2" -X POST "$ISSUER/api/admin/requests/$REQ_ID/approve" >/dev/null

api -b "$CJ_CUST" "$ISSUER/api/portal/licenses" \
  | jq -er '[.licenses[] | select(.edition=="professional")][0].text' >"$WORK/pro.lic"

say "run go test -tags e2e"
(cd "$SERVER_DIR" && \
  LICENSE_E2E_ISSUER="$ISSUER" \
  LICENSE_E2E_COMMUNITY="$WORK/community.lic" \
  LICENSE_E2E_PRO="$WORK/pro.lic" \
  go test -tags e2e -count=1 -v ./internal/license/ -run 'TestE2E')

say "e2e OK"
