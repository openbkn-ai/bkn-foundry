#!/usr/bin/env bash
# End-to-end validation: bkn-safe (login/consent provider) + standard hydra.
# Drives a full authorization-code flow as a human user, then introspects the
# resulting token and asserts the §1 ext claims bkn-safe injected at consent.
#
# Assumes: dev hydra up (public 4444 / admin 4445), bkn-safe up on :3000 wired
# as hydra's URLS_LOGIN/CONSENT, MySQL 'safe' DB present.
set -euo pipefail

PUB=http://127.0.0.1:4444
ADMIN=http://127.0.0.1:4445
SAFE=http://127.0.0.1:3000
JAR=$(mktemp)
fail() { echo "FAIL: $*" >&2; exit 1; }
loc() { grep -i '^location:' | sed -E 's/^[Ll]ocation: *//; s/\r//'; }
# urldecode percent-encoding (challenges in redirect URLs are encoded; e.g. =%3D).
urldec() { printf '%b' "${1//%/\\x}"; }

echo "== 0. register an authcode client =="
curl -fsS -X DELETE "$ADMIN/admin/clients/safe-e2e" >/dev/null 2>&1 || true
curl -fsS -X POST "$ADMIN/admin/clients" -H 'Content-Type: application/json' -d '{
  "client_id":"safe-e2e","client_secret":"e2e-secret",
  "grant_types":["authorization_code","refresh_token"],
  "response_types":["code"],
  "scope":"openid offline",
  "redirect_uris":["http://127.0.0.1:9010/callback"],
  "token_endpoint_auth_method":"client_secret_post"
}' >/dev/null
echo "  client safe-e2e ready"

echo "== 1. create local user test/111111 in bkn-safe =="
curl -fsS -X DELETE "$ADMIN/admin/clients/none" >/dev/null 2>&1 || true
curl -fsS -X POST "$SAFE/api/safe/v1/directory/users" -H 'Content-Type: application/json' \
  -d '{"account":"test","name":"Test User","password":"111111","account_type":"other"}' \
  >/dev/null 2>&1 || echo "  (user may already exist — continuing)"
echo "  user test ready"

echo "== 2. start authcode flow =="
AUTH="$PUB/oauth2/auth?client_id=safe-e2e&response_type=code&scope=openid+offline&redirect_uri=http://127.0.0.1:9010/callback&state=e2e-state-0001"
L=$(curl -fsS -c "$JAR" -D - -o /dev/null "$AUTH" | loc)
[ -n "$L" ] || fail "no redirect to login"
echo "  -> $L"
case "$L" in *"/login?login_challenge="*) ;; *) fail "expected login redirect, got $L";; esac
LC=$(urldec "${L#*login_challenge=}")

echo "== 3. POST credentials to bkn-safe /login =="
L=$(curl -fsS -c "$JAR" -b "$JAR" -D - -o /dev/null -X POST "$SAFE/login" \
  --data-urlencode "login_challenge=$LC" --data-urlencode "account=test" --data-urlencode "password=111111" | loc)
[ -n "$L" ] || fail "login POST gave no redirect (bad credentials?)"
echo "  -> (login accepted) $L"

echo "== 4. follow back to hydra -> consent =="
L=$(curl -fsS -c "$JAR" -b "$JAR" -D - -o /dev/null "$L" | loc)
echo "  -> $L"
case "$L" in *"/consent?consent_challenge="*) ;; *) fail "expected consent redirect, got $L";; esac

echo "== 5. bkn-safe /consent — render then POST allow (+ ext inject) =="
CC=$(urldec "${L#*consent_challenge=}")
# GET renders the consent screen (200, no redirect); POST the decision.
curl -fsS -c "$JAR" -b "$JAR" -o /dev/null "$L"
L=$(curl -fsS -c "$JAR" -b "$JAR" -D - -o /dev/null -X POST "$SAFE/consent" \
  --data-urlencode "consent_challenge=$CC" --data-urlencode "decision=allow" | loc)
[ -n "$L" ] || fail "consent POST gave no redirect"
echo "  -> (consent allowed) $L"

echo "== 6. follow to redirect_uri, capture code =="
L=$(curl -fsS -c "$JAR" -b "$JAR" -D - -o /dev/null "$L" | loc)
echo "  -> $L"
case "$L" in *"code="*) ;; *) fail "no authorization code, got $L";; esac
CODE="${L#*code=}"; CODE="${CODE%%&*}"
echo "  code=${CODE:0:12}..."

echo "== 7. exchange code for token =="
TOK=$(curl -fsS -X POST "$PUB/oauth2/token" \
  -d grant_type=authorization_code -d "code=$CODE" \
  -d redirect_uri=http://127.0.0.1:9010/callback \
  -d client_id=safe-e2e -d client_secret=e2e-secret \
  | sed -n 's/.*"access_token":"\([^"]*\)".*/\1/p')
[ -n "$TOK" ] || fail "no access_token"
echo "  access token (${#TOK} chars)"

echo "== 8. introspect -> assert ext claims (the §1 contract) =="
INTRO=$(curl -fsS -X POST "$ADMIN/admin/oauth2/introspect" -d "token=$TOK")
echo "  $INTRO"
echo "$INTRO" | grep -q '"active":true' || fail "token not active"
echo "$INTRO" | grep -q '"visitor_type":"realname"' || fail "ext.visitor_type != realname"
echo "$INTRO" | grep -q '"account_type":"other"' || fail "ext.account_type missing"
echo "$INTRO" | grep -q '"client_type":"web"' || fail "ext.client_type missing"
echo "$INTRO" | grep -q '"udid"' || fail "ext.udid missing"
echo "  OK: bkn-safe injected the full user-type ext claims via consent."

echo "== E2E PASS: bkn-safe + upstream hydra produced a contract-valid user token =="
rm -f "$JAR"
