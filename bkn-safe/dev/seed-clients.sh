#!/usr/bin/env bash
# Register the OAuth2 clients the ISF replacement needs, against the hydra admin
# API. Idempotent: deletes by id first. Internal services need NO client (they
# only introspect / propagate the user token) — only login entry-points do.
#
# Three clients (per landing-design §5 grant matrix):
#   ci-runner    — client_credentials (CI / automation, app identity)
#   openbkn-sdk      — device_code (+ refresh) public client (headless human / CLI login)
#   openbkn-web  — authorization_code + PKCE public client (browser / SPA login)
#
# Env:
#   HYDRA_ADMIN       admin endpoint (default dev http://127.0.0.1:4445; in-cluster
#                     use http://bkn-safe-hydra-admin:4445)
#   WEB_REDIRECT_URI  SPA callback (default dev http://localhost:3000/callback;
#                     set to the real https://<host>/callback in prod)
set -euo pipefail

ADMIN="${HYDRA_ADMIN:-http://127.0.0.1:4445}"
WEB_REDIRECT_URI="${WEB_REDIRECT_URI:-http://localhost:3000/callback}"

create() { # $1=json
  curl -fsS -X POST "$ADMIN/admin/clients" \
    -H 'Content-Type: application/json' -d "$1"
}
del() { curl -fsS -X DELETE "$ADMIN/admin/clients/$1" >/dev/null 2>&1 || true; }

echo "== seeding clients against $ADMIN =="

del ci-runner
create '{
  "client_id": "ci-runner",
  "client_secret": "ci-runner-secret",
  "grant_types": ["client_credentials"],
  "token_endpoint_auth_method": "client_secret_post",
  "scope": "authz.read authz.write",
  "audience": ["bkn-safe"]
}' >/dev/null
echo "  + ci-runner (client_credentials)"

del openbkn-sdk
create '{
  "client_id": "openbkn-sdk",
  "grant_types": ["urn:ietf:params:oauth:grant-type:device_code", "refresh_token"],
  "response_types": ["code"],
  "token_endpoint_auth_method": "none",
  "scope": "openid offline",
  "audience": ["bkn-safe"]
}' >/dev/null
echo "  + openbkn-sdk (device_code, public)"

del openbkn-web
create "{
  \"client_id\": \"openbkn-web\",
  \"grant_types\": [\"authorization_code\", \"refresh_token\"],
  \"response_types\": [\"code\"],
  \"token_endpoint_auth_method\": \"none\",
  \"redirect_uris\": [\"${WEB_REDIRECT_URI}\"],
  \"scope\": \"openid offline\",
  \"audience\": [\"bkn-safe\"]
}" >/dev/null
echo "  + openbkn-web (authorization_code + PKCE, public; redirect=${WEB_REDIRECT_URI})"

echo "== done =="
