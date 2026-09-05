#!/usr/bin/env python3
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Plan or apply managed-proxy backfill through BKN's internal API."""

from __future__ import annotations

import argparse
import json
import sys
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path
from typing import Any


def request_json(base_url: str, path: str, user_id: str, method: str = "GET") -> Any:
    request = urllib.request.Request(
        urllib.parse.urljoin(base_url.rstrip("/") + "/", path.lstrip("/")),
        method=method,
        headers={"x-account-id": user_id, "x-account-type": "user"},
    )
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            payload = response.read()
    except urllib.error.HTTPError as error:
        raise RuntimeError(f"{method} {path} returned HTTP {error.code}") from error
    except urllib.error.URLError as error:
        raise RuntimeError(f"{method} {path} failed: {error.reason}") from error
    return json.loads(payload) if payload else None


def build_report(base_url: str, user_id: str, apply: bool) -> dict[str, Any]:
    listing = request_json(
        base_url,
        "/api/bkn-backend/in/v1/knowledge-networks?limit=-1&offset=0",
        user_id,
    )
    entries = listing.get("entries", [])
    results: list[dict[str, Any]] = []
    for entry in entries:
        kn_id = entry["id"]
        quoted_id = urllib.parse.quote(kn_id, safe="")
        plan = request_json(
            base_url,
            f"/api/bkn-backend/in/v1/knowledge-networks/{quoted_id}/proxy-account/plan",
            user_id,
        )
        item = {
            "kn_id": kn_id,
            "proxy_account_id": plan.get("proxy_account_id", ""),
            "model_version": plan["model_version"],
            "required_sources": plan["sources"],
            "status": "planned",
        }
        if apply:
            mapping = request_json(
                base_url,
                f"/api/bkn-backend/in/v1/knowledge-networks/{quoted_id}/proxy-account/sync",
                user_id,
                method="POST",
            )
            item["proxy_account_id"] = mapping["proxy_account_id"]
            item["status"] = mapping["sync_status"]
        results.append(item)
    return {"mode": "apply" if apply else "dry-run", "networks": results}


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--backend-url", required=True)
    parser.add_argument("--user-id", required=True, help="authorized human grantor")
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument("--dry-run", action="store_true")
    mode.add_argument("--apply", action="store_true")
    parser.add_argument("--report", type=Path, required=True)
    args = parser.parse_args()
    try:
        report = build_report(args.backend_url, args.user_id, args.apply)
    except (KeyError, TypeError, RuntimeError, json.JSONDecodeError) as error:
        print(f"proxy backfill failed: {error}", file=sys.stderr)
        return 1
    rendered = json.dumps(report, ensure_ascii=False, indent=2) + "\n"
    args.report.write_text(rendered, encoding="utf-8")
    print(rendered, end="")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
