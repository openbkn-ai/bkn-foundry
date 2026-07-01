#!/usr/bin/env bash
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

# Phase 1 smoke test against the real upstream hydra dev stack.
#
#   1. client_credentials → access token → introspect → assert the app-type
#      introspect contract (active=true, sub==client_id) holds on REAL hydra.
#   2. device flow → POST /oauth2/device/auth → assert device_code, user_code,
#      verification_uri present (proves RFC 8628 is live in this hydra).
#
# The user-type ext claims (login_ip/udid/...) require bkn-safe's consent
# provider (Phase 2) and are NOT exercised here. This validates only what a
# bare upstream hydra can produce.
set -euo pipefail

PUB="${HYDRA_PUBLIC:-http://127.0.0.1:4444}"
ADMIN="${HYDRA_ADMIN:-http://127.0.0.1:4445}"
fail() { echo "FAIL: $*" >&2; exit 1; }

echo "== 1. client_credentials + introspect (app contract) =="
TOK=$(curl -fsS -X POST "$PUB/oauth2/token" \
  -d grant_type=client_credentials \
  -d client_id=ci-runner -d client_secret=ci-runner-secret \
  -d scope=authz.read \
  | sed -n 's/.*"access_token":"\([^"]*\)".*/\1/p')
[ -n "$TOK" ] || fail "no access_token from client_credentials"
echo "  got access token (${#TOK} chars)"

INTRO=$(curl -fsS -X POST "$ADMIN/admin/oauth2/introspect" -d "token=$TOK")
echo "  introspect: $INTRO"
echo "$INTRO" | grep -q '"active":true' || fail "introspect active != true"
echo "$INTRO" | grep -q '"sub":"ci-runner"' || fail "sub != client_id (app contract broken)"
echo "$INTRO" | grep -q '"client_id":"ci-runner"' || fail "client_id missing"
echo "  OK: active=true, sub==client_id==ci-runner (lib will parse as app type)"

echo "== 2. device authorization (RFC 8628) =="
DEV=$(curl -fsS -X POST "$PUB/oauth2/device/auth" \
  -d client_id=openbkn-sdk -d scope="openid offline")
echo "  device/auth: $DEV"
echo "$DEV" | grep -q '"device_code"' || fail "no device_code (device flow not live?)"
echo "$DEV" | grep -q '"user_code"' || fail "no user_code"
echo "$DEV" | grep -q '"verification_uri"' || fail "no verification_uri"
echo "  OK: device_code + user_code + verification_uri present"

echo "== Phase 1 smoke PASS =="
