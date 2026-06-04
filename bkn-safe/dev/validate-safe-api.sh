#!/usr/bin/env bash
# Full functional test of bkn-safe's own API surface (no hydra/browser needed):
# user create, role binding, authz check/operations/policies, the seeded role
# grants (app/data/AI/super admin), and the directory queries. Confirms
# everything built in Phase 2-5 works against a running bkn-safe.
#
#   SAFE=http://127.0.0.1:3000 ./validate-safe-api.sh
set -euo pipefail
SAFE="${SAFE:-http://127.0.0.1:3000}"
pass=0; fail=0
ok()   { echo "  ok   $*"; pass=$((pass+1)); }
bad()  { echo "  FAIL $*"; fail=$((fail+1)); }
# jget <json> <python-expr over data 'd'>
jget() { python3 -c "import sys,json;d=json.load(sys.stdin);print($1)"; }

api() { # METHOD PATH [BODY] -> prints body; sets HTTP in global RC
  local m="$1" p="$2" b="${3:-}"
  if [ -n "$b" ]; then
    curl -s -o /tmp/safe_body -w '%{http_code}' -X "$m" "$SAFE$p" -H 'Content-Type: application/json' -d "$b"
  else
    curl -s -o /tmp/safe_body -w '%{http_code}' -X "$m" "$SAFE$p"
  fi
}

echo "== health =="
[ "$(api GET /health/ready)" = "200" ] && ok "health ready" || bad "health"

echo "== create local user + assign 应用管理员 =="
code=$(api POST /api/safe/v1/directory/users '{"account":"apitest","name":"API Test","password":"p@ss1234","account_type":"other"}')
case "$code" in 201|500) ;; *) bad "create user (got $code)";; esac
UID_=$(api GET "/api/safe/v1/directory/users/x" >/dev/null; cat /tmp/safe_body >/dev/null; echo "")
# fetch the id by creating fresh account name each run to avoid dup
ACC="apitest-$$"
api POST /api/safe/v1/directory/users "{\"account\":\"$ACC\",\"name\":\"API Test\",\"password\":\"p@ss1234\"}" >/dev/null
USER=$(jget "d['id']" </tmp/safe_body)
[ -n "$USER" ] && ok "created user id=$USER" || bad "create user id"
[ "$(api POST /api/safe/v1/authz/role-bindings "{\"accessor_id\":\"$USER\",\"role_id\":\"1572fb82-526f-11f0-bde6-e674ec8dde71\"}")" = "204" ] && ok "bound 应用管理员" || bad "role-binding"

echo "== authz: 应用管理员 on agent =="
api POST /api/safe/v1/authz/check "{\"accessor_id\":\"$USER\",\"resource\":{\"type\":\"agent\",\"id\":\"probe\"},\"operation\":\"use\"}" >/dev/null
[ "$(jget "d['allowed']" </tmp/safe_body)" = "True" ] && ok "agent:probe use = allowed" || bad "agent use"
api POST /api/safe/v1/authz/check "{\"accessor_id\":\"$USER\",\"resource\":{\"type\":\"catalog\",\"id\":\"c1\"},\"operation\":\"create\"}" >/dev/null
[ "$(jget "d['allowed']" </tmp/safe_body)" = "False" ] && ok "catalog create = denied (not data-admin)" || bad "catalog should be denied"
api POST /api/safe/v1/authz/operations "{\"accessor_id\":\"$USER\",\"resource\":{\"type\":\"agent\",\"id\":\"probe\"}}" >/dev/null
echo "  agent ops -> $(jget "sorted(d['operations'])" </tmp/safe_body)"
jget "'use' in d['operations'] and 'mgnt_built_in_agent' in d['operations']" </tmp/safe_body | grep -q True && ok "operations includes use+mgnt" || bad "operations subset"

echo "== authz: 数据管理员 / AI管理员 / 超级管理员 (seeded grants) =="
mkrole() { ACC2="r$1-$$"; api POST /api/safe/v1/directory/users "{\"account\":\"$ACC2\",\"name\":\"$1\",\"password\":\"p@ss1234\"}" >/dev/null; U=$(jget "d['id']" </tmp/safe_body); api POST /api/safe/v1/authz/role-bindings "{\"accessor_id\":\"$U\",\"role_id\":\"$2\"}" >/dev/null; echo "$U"; }
DU=$(mkrole data 00990824-4bf7-11f0-8fa7-865d5643e61f)
AU=$(mkrole ai 3fb94948-5169-11f0-b662-3a7bdba2913f)
SU=$(mkrole super 7dcfcc9c-ad02-11e8-aa06-000c29358ad6)
chk() { api POST /api/safe/v1/authz/check "{\"accessor_id\":\"$1\",\"resource\":{\"type\":\"$2\",\"id\":\"$3\"},\"operation\":\"$4\"}" >/dev/null; jget "d['allowed']" </tmp/safe_body; }
[ "$(chk "$DU" catalog c1 create)" = "True" ]  && ok "数据管理员 catalog create" || bad "data-admin catalog"
[ "$(chk "$DU" knowledge_network kn1 data_query)" = "True" ] && ok "数据管理员 KN data_query" || bad "data-admin KN"
[ "$(chk "$DU" operator o1 execute)" = "False" ] && ok "数据管理员 NOT operator" || bad "data-admin operator leak"
[ "$(chk "$AU" operator o1 execute)" = "True" ]  && ok "AI管理员 operator execute" || bad "ai-admin operator"
[ "$(chk "$AU" small_model m1 execute)" = "True" ] && ok "AI管理员 small_model execute" || bad "ai-admin small_model"
[ "$(chk "$SU" anything z whatever)" = "True" ]   && ok "超级管理员 wildcard (any/any)" || bad "super wildcard"

echo "== authz: per-object grant + revoke =="
[ "$(api POST /api/safe/v1/authz/policies "{\"accessor_id\":\"$USER\",\"resource\":{\"type\":\"pipeline\",\"id\":\"p1\"},\"operations\":[\"read\"]}")" = "204" ] && ok "grant pipeline:p1 read" || bad "grant"
[ "$(chk "$USER" pipeline p1 read)" = "True" ]  && ok "pipeline:p1 read allowed" || bad "obj grant check"
[ "$(chk "$USER" pipeline p2 read)" = "False" ] && ok "pipeline:p2 read denied (no leak)" || bad "obj grant leak"
api DELETE /api/safe/v1/authz/policies "{\"resource\":{\"type\":\"pipeline\",\"id\":\"p1\"}}" >/dev/null
[ "$(chk "$USER" pipeline p1 read)" = "False" ] && ok "pipeline:p1 read revoked" || bad "revoke"

echo "== directory: names / departments / search-org =="
api POST /api/safe/v1/directory/names "{\"user_ids\":[\"$USER\",\"ghost\"]}" >/dev/null
[ "$(jget "len(d['user_names'])" </tmp/safe_body)" = "1" ] && ok "names resolves known, omits unknown" || bad "names"
[ "$(api GET '/api/safe/v1/directory/departments')" = "200" ] && ok "departments list" || bad "departments"
[ "$(api POST /api/safe/v1/directory/search-org "{\"user_ids\":[\"$USER\"],\"scope\":[]}")" = "200" ] && ok "search-org" || bad "search-org"

echo ""
echo "== RESULT: $pass passed, $fail failed =="
[ "$fail" = "0" ]
