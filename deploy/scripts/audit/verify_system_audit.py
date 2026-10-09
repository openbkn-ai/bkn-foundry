#!/usr/bin/env python3
# Copyright openbkn.ai
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
"""Read-only post-deployment check of an independently produced Audit event.

Uses the caller's existing authorization. Never produces records, grants
permissions, or accepts an empty list as evidence of successful collection.
"""
import argparse
import json
import os
import sys
from urllib import error, parse, request


class NoRedirect(request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None  # Do not forward an authorization token to another origin.


def read_json(opener, url, token):
    req = request.Request(url, headers={"Authorization": "Bearer " + token})
    try:
        with opener.open(req, timeout=15) as response:
            return response.status, json.load(response)
    except error.HTTPError as exc:
        return exc.code, {}  # Never echo error bodies or credentials.


def verify(base_url, token, event_id, source_id, time_from, time_to, opener=None):
    parsed = parse.urlsplit(base_url)
    if parsed.scheme not in ("http", "https") or not parsed.netloc or parsed.username or parsed.password or parsed.query or parsed.fragment:
        raise ValueError("invalid_base_url")
    if not token or not event_id or not source_id:
        raise ValueError("missing_token_or_event_identity")
    opener = opener or request.build_opener(NoRedirect())
    root = base_url.rstrip("/") + "/api/observability/v1"
    status, body = read_json(opener, root + "/log-sources", token)
    if status != 200:
        raise ValueError("log_sources_http_" + str(status))
    sources = body.get("data", [])
    if not any(s.get("source_id") == "audit-ledger" and s.get("status") == "healthy" for s in sources):
        raise ValueError("audit_ledger_not_healthy")
    query = parse.urlencode({"categories": "audit.admin", "source_id": source_id,
                             "time_from": time_from, "time_to": time_to, "limit": 100})
    status, body = read_json(opener, root + "/logs?" + query, token)
    if status != 200:
        raise ValueError("audit_logs_http_" + str(status))
    if not any(r.get("event_id") == event_id and r.get("source_id") == source_id and
               r.get("log_category") == "audit.admin" for r in body.get("data", [])):
        raise ValueError("expected_audit_event_not_found_in_window")
    return {"audit_source_ready": True, "expected_event_visible": True, "read_only": True}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("base-url", "event-id", "source-id", "time-from", "time-to"):
        parser.add_argument("--" + name, required=True)
    args = parser.parse_args()
    try:
        result = verify(args.base_url, os.environ.get("BKN_AUDIT_VERIFY_TOKEN", ""),
                        args.event_id, args.source_id, args.time_from, args.time_to)
    except (ValueError, OSError, error.URLError):
        # Do not include server data, request URLs or token-bearing exceptions.
        print("System Audit verification failed; check authorization, source configuration and the selected event/window", file=sys.stderr)
        return 1
    print(json.dumps(result, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
