#!/usr/bin/env bash
# bkn-redirect.sh — list/add/remove an OAuth2 login client's redirect_uris via the
# bkn-safe admin API (gateway-exposed), so a redirect can be registered without
# kubectl or a helm redeploy.
#
# THIS IS THE EPHEMERAL/DEV PATH. A `helm upgrade` re-seeds the login clients from
# chart values and WIPES anything added here. Durable redirect_uris belong in the
# chart: clientSeed.extraWebRedirectUris (see bkn-safe/docs/oauth-redirect-uris.md).
#
# Requires the openbkn CLI (for an admin token) and a super-admin session — the
# admin API is gated by RequireAdmin. A non-admin caller gets 403.
#
# Usage:
#   bkn-redirect.sh list [client]
#   bkn-redirect.sh add  <redirect_uri> [client]
#   bkn-redirect.sh del  <redirect_uri> [client]
#
# Defaults: client = openbkn-studio.
# Host: set BKN_HOST to the gateway base URL (e.g. https://10.211.55.4); when unset
# the script reads it from `openbkn auth status`.
set -euo pipefail

CLIENT_DEFAULT="openbkn-studio"

die() { echo "error: $*" >&2; exit 1; }

command -v openbkn >/dev/null 2>&1 || die "openbkn CLI not found (needed for the admin token)"
command -v curl    >/dev/null 2>&1 || die "curl not found"

# Resolve the gateway base URL: BKN_HOST wins, else parse the active session.
host="${BKN_HOST:-}"
if [ -z "$host" ]; then
  host="$(openbkn auth status 2>/dev/null | grep -oE 'https?://[^[:space:]]+' | head -1 || true)"
fi
[ -n "$host" ] || die "gateway base URL unknown — set BKN_HOST=https://<host> (or run 'openbkn auth login <url>')"
host="${host%/}" # strip trailing slash

token="$(openbkn auth token 2>/dev/null || true)"
[ -n "$token" ] || die "no access token — run 'openbkn auth login <url>' and switch to a super-admin user"

# api METHOD CLIENT [json-body]
api() {
  local method="$1" client="$2" body="${3:-}"
  local url="$host/api/safe/v1/admin/clients/$client/redirect-uris"
  if [ -n "$body" ]; then
    curl -sk -X "$method" "$url" \
      -H "Authorization: Bearer $token" -H 'Content-Type: application/json' -d "$body"
  else
    curl -sk -X "$method" "$url" -H "Authorization: Bearer $token"
  fi
}

# uri_body URI -> {"redirect_uri":"URI"} with URI JSON-escaped (handles quotes/backslashes)
uri_body() {
  local uri="$1"
  uri="${uri//\\/\\\\}"; uri="${uri//\"/\\\"}"
  printf '{"redirect_uri":"%s"}' "$uri"
}

cmd="${1:-}"
case "$cmd" in
  list)
    client="${2:-$CLIENT_DEFAULT}"
    api GET "$client"
    ;;
  add)
    uri="${2:-}"; [ -n "$uri" ] || die "usage: bkn-redirect.sh add <redirect_uri> [client]"
    client="${3:-$CLIENT_DEFAULT}"
    api POST "$client" "$(uri_body "$uri")"
    ;;
  del|delete|rm)
    uri="${2:-}"; [ -n "$uri" ] || die "usage: bkn-redirect.sh del <redirect_uri> [client]"
    client="${3:-$CLIENT_DEFAULT}"
    api DELETE "$client" "$(uri_body "$uri")"
    ;;
  ""|-h|--help|help)
    sed -n '2,30p' "$0" | sed 's/^# \{0,1\}//'
    ;;
  *)
    die "unknown command '$cmd' (use: list | add | del)"
    ;;
esac
echo
