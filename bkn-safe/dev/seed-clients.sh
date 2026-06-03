#!/usr/bin/env bash
# Register the OAuth2 clients the ISF replacement needs, against the dev hydra
# admin API (http://127.0.0.1:4445). Idempotent-ish: deletes by id first.
#
# Two clients (per landing-design §5 grant matrix):
#   ci-runner        — client_credentials (CI / automation, app identity)
#   kweaver-cli      — device_code (+ refresh) public client (headless human login)
set -euo pipefail

ADMIN="${HYDRA_ADMIN:-http://127.0.0.1:4445}"

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

del kweaver-cli
create '{
  "client_id": "kweaver-cli",
  "grant_types": ["urn:ietf:params:oauth:grant-type:device_code", "refresh_token"],
  "response_types": ["code"],
  "token_endpoint_auth_method": "none",
  "scope": "openid offline",
  "audience": ["bkn-safe"]
}' >/dev/null
echo "  + kweaver-cli (device_code, public)"

echo "== done =="
