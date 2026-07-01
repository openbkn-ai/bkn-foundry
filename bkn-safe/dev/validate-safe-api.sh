#!/usr/bin/env bash
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

# Functional test of bkn-safe's API surface, split by trust boundary:
#
#   - internal (tokenless, ClusterIP): authz check/operations/policies, directory
#     names/departments/search-org. Always run, using the seeded super-admin
#     accessor (266c6a42…) so no user creation is needed.
#   - admin (token-gated /api/safe/v1/admin): user create + role binding + the
#     seeded role-grant matrix. Run ONLY when ADMIN_TOKEN is provided — the gate
#     (RequireAdmin) needs a bearer token whose subject is a super-admin. Obtain
#     one out-of-band (openbkn login / device flow) and export it; this script
#     deliberately does NOT grant any automation client admin rights.
#
#   SAFE=http://127.0.0.1:3000 ./validate-safe-api.sh                 # internal only
#   SAFE=http://127.0.0.1:3000 ADMIN_TOKEN=<super-admin token> ./validate-safe-api.sh
set -euo pipefail
SAFE="${SAFE:-http://127.0.0.1:3000}"
ADMIN_TOKEN="${ADMIN_TOKEN:-}"
SUPER="266c6a42-6131-4d62-8f39-853e7093701c"  # seeded admin accessor, bound to 超级管理员
pass=0; fail=0
ok()   { echo "  ok   $*"; pass=$((pass+1)); }
bad()  { echo "  FAIL $*"; fail=$((fail+1)); }
# jget <python-expr over data 'd'>
jget() { python3 -c "import sys,json;d=json.load(sys.stdin);print($1)"; }

api() { # METHOD PATH [BODY] -> prints http code; body in /tmp/safe_body
  local m="$1" p="$2" b="${3:-}"
  if [ -n "$b" ]; then
    curl -s -o /tmp/safe_body -w '%{http_code}' -X "$m" "$SAFE$p" -H 'Content-Type: application/json' -d "$b"
  else
    curl -s -o /tmp/safe_body -w '%{http_code}' -X "$m" "$SAFE$p"
  fi
}

aapi() { # admin METHOD PATH [BODY] -> token-gated /api/safe/v1/admin call
  local m="$1" p="$2" b="${3:-}"
  if [ -n "$b" ]; then
    curl -s -o /tmp/safe_body -w '%{http_code}' -X "$m" "$SAFE$p" \
      -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' -d "$b"
  else
    curl -s -o /tmp/safe_body -w '%{http_code}' -X "$m" "$SAFE$p" -H "Authorization: Bearer $ADMIN_TOKEN"
  fi
}

# chk ACCESSOR TYPE ID OP -> prints True/False
chk() { api POST /api/safe/v1/authz/check "{\"accessor_id\":\"$1\",\"resource\":{\"type\":\"$2\",\"id\":\"$3\"},\"operation\":\"$4\"}" >/dev/null; jget "d['allowed']" </tmp/safe_body; }

echo "== health =="
[ "$(api GET /health/ready)" = "200" ] && ok "health ready" || bad "health"

echo "== authz: seeded 超级管理员 wildcard + deny (internal, tokenless) =="
[ "$(chk "$SUPER" anything z whatever)" = "True" ]  && ok "超级管理员 wildcard (any/any)" || bad "super wildcard"
[ "$(chk "nobody-$$" catalog c1 create)" = "False" ] && ok "unbound accessor denied" || bad "deny leak"
api POST /api/safe/v1/authz/operations "{\"accessor_id\":\"$SUPER\",\"resource\":{\"type\":\"agent\",\"id\":\"probe\"}}" >/dev/null
jget "'use' in d['operations']" </tmp/safe_body | grep -q True && ok "operations returns allowed set" || bad "operations"

echo "== authz: per-object grant + revoke (internal) =="
OBJU="objtest-$$"
[ "$(api POST /api/safe/v1/authz/policies "{\"accessor_id\":\"$OBJU\",\"resource\":{\"type\":\"pipeline\",\"id\":\"p1\"},\"operations\":[\"read\"]}")" = "204" ] && ok "grant pipeline:p1 read" || bad "grant"
[ "$(chk "$OBJU" pipeline p1 read)" = "True" ]  && ok "pipeline:p1 read allowed" || bad "obj grant check"
[ "$(chk "$OBJU" pipeline p2 read)" = "False" ] && ok "pipeline:p2 read denied (no leak)" || bad "obj grant leak"
api DELETE /api/safe/v1/authz/policies "{\"resource\":{\"type\":\"pipeline\",\"id\":\"p1\"}}" >/dev/null
[ "$(chk "$OBJU" pipeline p1 read)" = "False" ] && ok "pipeline:p1 read revoked" || bad "revoke"

echo "== directory: names / departments / search-org (internal) =="
api POST /api/safe/v1/directory/names "{\"user_ids\":[\"ghost\"]}" >/dev/null
[ "$(jget "len(d['user_names'])" </tmp/safe_body)" = "0" ] && ok "names omits unknown id" || bad "names"
[ "$(api GET '/api/safe/v1/directory/departments')" = "200" ] && ok "departments list" || bad "departments"
[ "$(api POST /api/safe/v1/directory/search-org "{\"user_ids\":[\"$SUPER\"],\"scope\":[]}")" = "200" ] && ok "search-org" || bad "search-org"

echo "== admin API (gated /api/safe/v1/admin) =="
if [ -z "$ADMIN_TOKEN" ]; then
  echo "  skip — no ADMIN_TOKEN (export a super-admin token to run admin checks)"
else
  # 401 without the token proves the gate is on.
  [ "$(api GET /api/safe/v1/admin/roles)" = "401" ] && ok "admin gate rejects no-token (401)" || bad "admin gate open"

  ACC="apitest-$$"
  code=$(aapi POST /api/safe/v1/admin/users "{\"account\":\"$ACC\",\"name\":\"API Test\",\"password\":\"p@ss1234\",\"account_type\":\"other\"}")
  case "$code" in 201) USER=$(jget "d['id']" </tmp/safe_body); ok "created user id=$USER" ;; *) bad "create user (got $code)"; USER="" ;; esac

  if [ -n "${USER:-}" ]; then
    [ "$(aapi POST /api/safe/v1/admin/role-bindings "{\"accessor_id\":\"$USER\",\"role_id\":\"1572fb82-526f-11f0-bde6-e674ec8dde71\"}")" = "204" ] && ok "bound 应用管理员" || bad "role-binding"
    [ "$(chk "$USER" agent probe use)" = "True" ]      && ok "应用管理员 agent:probe use" || bad "agent use"
    [ "$(chk "$USER" catalog c1 create)" = "False" ]   && ok "应用管理员 NOT catalog create" || bad "catalog leak"

    mkrole() { local a="r$1-$$"; aapi POST /api/safe/v1/admin/users "{\"account\":\"$a\",\"name\":\"$1\",\"password\":\"p@ss1234\"}" >/dev/null; local u; u=$(jget "d['id']" </tmp/safe_body); aapi POST /api/safe/v1/admin/role-bindings "{\"accessor_id\":\"$u\",\"role_id\":\"$2\"}" >/dev/null; echo "$u"; }
    DU=$(mkrole data 00990824-4bf7-11f0-8fa7-865d5643e61f)
    AU=$(mkrole ai 3fb94948-5169-11f0-b662-3a7bdba2913f)
    [ "$(chk "$DU" catalog c1 create)" = "True" ]    && ok "数据管理员 catalog create" || bad "data-admin catalog"
    [ "$(chk "$DU" operator o1 execute)" = "False" ] && ok "数据管理员 NOT operator" || bad "data-admin operator leak"
    [ "$(chk "$AU" operator o1 execute)" = "True" ]  && ok "AI管理员 operator execute" || bad "ai-admin operator"

    # role enumeration + cleanup of created users
    [ "$(aapi GET /api/safe/v1/admin/roles)" = "200" ] && ok "admin roles list" || bad "roles list"
    for u in "$USER" "$DU" "$AU"; do aapi DELETE "/api/safe/v1/admin/users/$u" >/dev/null; done
    ok "cleaned up test users"
  fi
fi

echo ""
echo "== RESULT: $pass passed, $fail failed =="
[ "$fail" = "0" ]
