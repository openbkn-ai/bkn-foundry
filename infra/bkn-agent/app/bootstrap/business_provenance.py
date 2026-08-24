"""Idempotently import and verify the Enterprise business-provenance Agent."""

import json
import os
import sys
import time
from pathlib import Path
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen


EXPECTED_OWNER_ACCOUNT = "openbkn-business-provenance"
EXPECTED_OWNER_NAME = "OpenBKN Business Provenance Service"


class BootstrapError(RuntimeError):
    """A deterministic bootstrap contract failure."""


class JsonClient:
    def __init__(self, attempts: int = 30, delay_s: float = 2.0):
        self.attempts = attempts
        self.delay_s = delay_s

    def get_json(self, url: str, headers: dict[str, str] | None = None):
        return self._request(Request(url, headers={"Accept": "application/json", **(headers or {})}))

    def post_json(self, url: str, body: dict, headers: dict[str, str]):
        return self._request(
            Request(
                url,
                data=json.dumps(body, ensure_ascii=False).encode("utf-8"),
                headers={"Content-Type": "application/json", **headers},
                method="POST",
            )
        )

    def _request(self, request: Request):
        for attempt in range(1, self.attempts + 1):
            try:
                with urlopen(request, timeout=10) as response:
                    return json.load(response)
            except HTTPError as exc:
                detail = exc.read().decode("utf-8", errors="replace")
                if exc.code < 500 or attempt == self.attempts:
                    raise BootstrapError(f"HTTP {exc.code} from {request.full_url}: {detail}") from exc
            except (URLError, TimeoutError) as exc:
                if attempt == self.attempts:
                    raise BootstrapError(f"cannot reach {request.full_url}: {exc}") from exc
            time.sleep(self.delay_s)
        raise AssertionError("unreachable")


def _join(base: str, path: str) -> str:
    return f"{base.rstrip('/')}/{path.lstrip('/')}"


def _validate_owner(owner: dict, owner_id: str) -> None:
    expected = {
        "id": owner_id,
        "account": EXPECTED_OWNER_ACCOUNT,
        "name": EXPECTED_OWNER_NAME,
        "enabled": True,
        "account_type": "app",
    }
    for field, value in expected.items():
        if owner.get(field) != value:
            raise BootstrapError(
                f"business provenance owner {field} is {owner.get(field)!r}, expected {value!r}"
            )


def _validate_import_result(result: dict, agent_id: str) -> str:
    entries = result.get("results")
    if not isinstance(entries, list) or len(entries) != 1:
        raise BootstrapError("Agent import must return exactly one result")
    entry = entries[0]
    if entry.get("agent_id") != agent_id:
        raise BootstrapError(f"Agent import returned unexpected id {entry.get('agent_id')!r}")
    if entry.get("action") not in {"created", "updated"}:
        raise BootstrapError(entry.get("error") or f"Agent import action is {entry.get('action')!r}")
    if entry.get("prompt_action") not in {"created", "version_published", "unchanged"}:
        raise BootstrapError(
            f"Agent import prompt action is {entry.get('prompt_action')!r}"
        )
    warnings = result.get("warnings") or []
    if warnings:
        raise BootstrapError(f"Agent import returned warnings: {warnings}")
    return entry["action"]


def _validate_agent(actual: dict, package_item: dict, owner_id: str) -> None:
    expected = package_item["spec"]
    for field, value in expected.items():
        if actual.get(field) != value:
            raise BootstrapError(
                f"Agent {package_item['agent_id']} {field} is {actual.get(field)!r}, expected {value!r}"
            )
    if actual.get("create_user") != owner_id:
        raise BootstrapError(
            f"Agent {package_item['agent_id']} owner is {actual.get('create_user')!r}, expected {owner_id!r}"
        )


def bootstrap(
    client,
    package_path,
    bkn_safe_url: str,
    bkn_agent_url: str,
    owner_id: str,
    bootstrap_token: str,
) -> str:
    package = json.loads(Path(package_path).read_text(encoding="utf-8"))
    if package.get("format") != "bkn-agent/v1" or len(package.get("items") or []) != 1:
        raise BootstrapError("business provenance package must contain exactly one bkn-agent/v1 item")
    item = package["items"][0]
    agent_id = item["agent_id"]

    owner = client.get_json(_join(bkn_safe_url, f"directory/users/{owner_id}"))
    _validate_owner(owner, owner_id)

    owner_headers = {
        "x-account-id": owner_id,
        "x-account-type": "app",
        "x-bkn-provenance-bootstrap-token": bootstrap_token,
    }
    result = client.post_json(
        _join(bkn_agent_url, "import"),
        {"package": package},
        owner_headers,
    )
    action = _validate_import_result(result, agent_id)
    actual = client.get_json(_join(bkn_agent_url, f"agents/{agent_id}"), owner_headers)
    _validate_agent(actual, item, owner_id)
    return action


def main() -> int:
    service_root = Path(__file__).resolve().parents[2]
    try:
        action = bootstrap(
            JsonClient(),
            os.getenv(
                "BKN_PROVENANCE_PACKAGE",
                str(service_root / "deploy" / "agents" / "business-provenance-optimizer.json"),
            ),
            os.environ["BKN_SAFE_URL"],
            os.environ["BKN_AGENT_URL"],
            os.environ["BKN_PROVENANCE_OWNER_ID"],
            os.environ["BKN_PROVENANCE_BOOTSTRAP_TOKEN"],
        )
    except (BootstrapError, KeyError, OSError, ValueError) as exc:
        print(f"business provenance bootstrap failed: {exc}", file=sys.stderr)
        return 1
    print(f"business provenance bootstrap complete: {action}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
