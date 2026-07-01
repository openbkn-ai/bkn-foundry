#!/usr/bin/env bash
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

# End-to-end validation of the OAuth2 Device Authorization Grant (RFC 8628) —
# the "gh auth login" style flow: a headless CLI gets a user_code, a human
# approves it in a browser, the CLI polls and receives access + refresh tokens.
#
# This drives BOTH actors with curl:
#   CLI actor    — POST /oauth2/device/auth, then polls /oauth2/token.
#   browser actor — walks hydra's verification page -> bkn-safe /device ->
#                   /login -> /consent, carrying one cookie jar (the device CSRF
#                   cookie hydra sets must survive the whole walk).
# After the browser approves, the CLI poll flips from authorization_pending to a
# token, which we introspect for the §1 ext claims and then refresh.
#
# Assumes: dev hydra up (public 4444 / admin 4445), bkn-safe up on :3000 wired
# as hydra's URLS_LOGIN/CONSENT/DEVICE_VERIFICATION, MySQL 'safe' DB present.
#
# NOTE on rehosting: hydra mints verification_uri / redirect targets using its
# CONFIGURED public host (e.g. https://bkn-foundry.local). When running this via
# port-forward those hosts are unreachable, so we strip scheme+host off every
# redirect and re-point it at the local PUB/SAFE base. A real browser would just
# follow the absolute URLs as-is.
set -euo pipefail

PUB=http://127.0.0.1:4444
ADMIN=http://127.0.0.1:4445
SAFE=http://127.0.0.1:3000
JAR=$(mktemp)
fail() { echo "FAIL: $*" >&2; exit 1; }
loc() { grep -i '^location:' | sed -E 's/^[Ll]ocation: *//; s/\r//'; }
urldec() { printf '%b' "${1//%/\\x}"; }
# naive JSON string-field extractor: jget <field>
jget() { sed -n "s/.*\"$1\":\"\([^\"]*\)\".*/\1/p"; }
# rehost <absolute-url> <base>: keep path+query, swap scheme+host for <base>.
rehost() { printf '%s%s' "$2" "$(printf '%s' "$1" | sed -E 's#^[a-zA-Z]+://[^/]+##')"; }
# qval <url> <param>: pull a single (url-decoded) query param value.
qval() { local v="${1#*$2=}"; v="${v%%&*}"; urldec "$v"; }

echo "== 0. register the device (public) client openbkn-sdk =="
curl -fsS -X DELETE "$ADMIN/admin/clients/openbkn-sdk" >/dev/null 2>&1 || true
curl -fsS -X POST "$ADMIN/admin/clients" -H 'Content-Type: application/json' -d '{
  "client_id":"openbkn-sdk",
  "grant_types":["urn:ietf:params:oauth:grant-type:device_code","refresh_token"],
  "response_types":["code"],
  "token_endpoint_auth_method":"none",
  "scope":"openid offline",
  "audience":["bkn-safe"]
}' >/dev/null
echo "  client openbkn-sdk ready (device_code + refresh, public)"

echo "== 1. create local user test/111111 in bkn-safe =="
curl -fsS -X POST "$SAFE/api/safe/v1/directory/users" -H 'Content-Type: application/json' \
  -d '{"account":"test","name":"Test User","password":"111111","account_type":"other"}' \
  >/dev/null 2>&1 || echo "  (user may already exist — continuing)"
echo "  user test ready"

echo "== 2. CLI: request device + user code =="
DEV=$(curl -fsS -X POST "$PUB/oauth2/device/auth" -d client_id=openbkn-sdk -d scope="openid offline")
echo "  device/auth: $DEV"
DEVICE_CODE=$(echo "$DEV" | jget device_code)
USER_CODE=$(echo "$DEV" | jget user_code)
VURI=$(echo "$DEV" | jget verification_uri)
VURIC=$(echo "$DEV" | jget verification_uri_complete)
INTERVAL=$(echo "$DEV" | sed -n 's/.*"interval":\([0-9]*\).*/\1/p')
[ -n "$DEVICE_CODE" ] || fail "no device_code (device flow not live?)"
[ -n "$USER_CODE" ]   || fail "no user_code"
[ -n "$VURI" ]        || fail "no verification_uri"
echo "  >>> a human would now open: $VURI  and enter code: $USER_CODE"
echo "  device_code=${DEVICE_CODE:0:12}...  interval=${INTERVAL:-(default)}"

echo "== 3. browser: open verification page -> bkn-safe /device =="
# Prefer the complete URL (user_code prefilled); fall back to plain verification_uri.
ENTRY="${VURIC:-$VURI}"
L=$(curl -fsS -c "$JAR" -b "$JAR" -D - -o /dev/null "$(rehost "$ENTRY" "$PUB")" | loc)
[ -n "$L" ] || fail "verification entry gave no redirect to the device page"
echo "  -> $L"
case "$L" in *"device_challenge="*) ;; *) fail "expected /device?device_challenge=..., got $L";; esac
DC=$(qval "$L" "device_challenge")
# render the device page (GET) so the flow matches a real browser
curl -fsS -c "$JAR" -b "$JAR" -o /dev/null "$(rehost "$L" "$SAFE")"

echo "== 4. browser: POST /device (confirm user_code) -> back to hydra -> login =="
L=$(curl -fsS -c "$JAR" -b "$JAR" -D - -o /dev/null -X POST "$SAFE/device" \
  --data-urlencode "device_challenge=$DC" --data-urlencode "user_code=$USER_CODE" | loc)
[ -n "$L" ] || fail "device POST gave no redirect (bad user_code?)"
L=$(curl -fsS -c "$JAR" -b "$JAR" -D - -o /dev/null "$(rehost "$L" "$PUB")" | loc)
[ -n "$L" ] || fail "no redirect from hydra after device accept"
echo "  -> $L"
case "$L" in *"/login?login_challenge="*) ;; *) fail "expected login redirect, got $L";; esac
LC=$(qval "$L" "login_challenge")

echo "== 5. browser: POST credentials to /login -> consent =="
L=$(curl -fsS -c "$JAR" -b "$JAR" -D - -o /dev/null -X POST "$SAFE/login" \
  --data-urlencode "login_challenge=$LC" --data-urlencode "account=test" --data-urlencode "password=111111" | loc)
[ -n "$L" ] || fail "login POST gave no redirect (bad credentials?)"
L=$(curl -fsS -c "$JAR" -b "$JAR" -D - -o /dev/null "$(rehost "$L" "$PUB")" | loc)
echo "  -> $L"
case "$L" in *"/consent?consent_challenge="*) ;; *) fail "expected consent redirect, got $L";; esac
CC=$(qval "$L" "consent_challenge")

echo "== 6. browser: render + POST /consent allow (ext inject) -> device success =="
curl -fsS -c "$JAR" -b "$JAR" -o /dev/null "$(rehost "$L" "$SAFE")"
L=$(curl -fsS -c "$JAR" -b "$JAR" -D - -o /dev/null -X POST "$SAFE/consent" \
  --data-urlencode "consent_challenge=$CC" --data-urlencode "decision=allow" | loc)
[ -n "$L" ] || fail "consent POST gave no redirect"
L=$(curl -fsS -c "$JAR" -b "$JAR" -D - -o /dev/null "$(rehost "$L" "$PUB")" | loc)
echo "  -> (device approved) $L"

echo "== 7. CLI: poll token endpoint until approved =="
TOK=""; RESP=""
for i in $(seq 1 15); do
  RESP=$(curl -sS -X POST "$PUB/oauth2/token" \
    -d grant_type=urn:ietf:params:oauth:grant-type:device_code \
    -d "device_code=$DEVICE_CODE" -d client_id=openbkn-sdk)
  TOK=$(echo "$RESP" | jget access_token)
  [ -n "$TOK" ] && break
  if echo "$RESP" | grep -q 'authorization_pending\|slow_down'; then
    echo "  poll $i: pending..."
    sleep "${INTERVAL:-1}"
    continue
  fi
  fail "device token error: $RESP"
done
RT=$(echo "$RESP" | jget refresh_token)
[ -n "$TOK" ] || fail "device poll never returned access_token: $RESP"
[ -n "$RT" ]  || fail "no refresh_token (offline scope not granted?)"
echo "  access token (${#TOK} chars), refresh token (${#RT} chars)"

echo "== 8. introspect -> assert ext claims (the §1 contract) =="
INTRO=$(curl -fsS -X POST "$ADMIN/admin/oauth2/introspect" -d "token=$TOK")
echo "  $INTRO"
echo "$INTRO" | grep -q '"active":true'              || fail "token not active"
echo "$INTRO" | grep -q '"visitor_type":"realname"'  || fail "ext.visitor_type != realname"
echo "$INTRO" | grep -q '"account_type":"other"'     || fail "ext.account_type missing"
echo "$INTRO" | grep -q '"client_type":"web"'        || fail "ext.client_type missing"
echo "$INTRO" | grep -q '"udid"'                     || fail "ext.udid missing"
echo "  OK: device-flow token carries the full user-type ext claims."

echo "== 9. refresh_token grant -> new tokens, both rotated =="
RESP2=$(curl -fsS -X POST "$PUB/oauth2/token" \
  -d grant_type=refresh_token -d "refresh_token=$RT" -d client_id=openbkn-sdk)
TOK2=$(echo "$RESP2" | jget access_token)
RT2=$(echo "$RESP2" | jget refresh_token)
[ -n "$TOK2" ] || fail "refresh: no new access_token"
[ -n "$RT2" ]  || fail "refresh: no new refresh_token"
[ "$TOK2" != "$TOK" ] || fail "refresh: access_token not rotated"
[ "$RT2" != "$RT" ]   || fail "refresh: refresh_token not rotated (rotation off?)"
echo "  refreshed: new access (${#TOK2} chars) + new refresh (${#RT2} chars), both rotated"

echo "== 10. introspect refreshed access -> same user identity survives =="
INTRO2=$(curl -fsS -X POST "$ADMIN/admin/oauth2/introspect" -d "token=$TOK2")
echo "  $INTRO2"
echo "$INTRO2" | grep -q '"active":true'             || fail "refreshed token not active"
echo "$INTRO2" | grep -q '"visitor_type":"realname"' || fail "refreshed: ext.visitor_type missing"
echo "$INTRO2" | grep -q '"account_type":"other"'    || fail "refreshed: ext.account_type missing"
echo "$INTRO2" | grep -q '"client_type":"web"'       || fail "refreshed: ext.client_type missing"
echo "$INTRO2" | grep -q '"udid"'                    || fail "refreshed: ext.udid missing"
echo "  OK: refreshed device token carries identical user identity — CLI re-auth works."

echo "== DEVICE E2E PASS: RFC 8628 device flow yields contract-valid, refreshable user tokens =="
rm -f "$JAR"
