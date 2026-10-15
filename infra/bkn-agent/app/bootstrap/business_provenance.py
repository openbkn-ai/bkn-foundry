"""Compatibility entry point: verify SQL-seeded EE Agents without writing configuration."""

import json
import os
import sys
import time
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen

EXPECTED_OWNER_ACCOUNT = "openbkn-business-provenance"
EXPECTED_OWNER_NAME = "OpenBKN Business Provenance Service"


class BootstrapError(RuntimeError):
    """A deterministic bootstrap contract failure."""


class JsonClient:
    def __init__(self, attempts: int = 10, delay_s: float = 2.0):
        self.attempts = attempts
        self.delay_s = delay_s

    def get_json(
        self,
        url: str,
        headers: dict[str, str] | None = None,
        retry_statuses: set[int] | None = None,
    ):
        return self._request(
            Request(url, headers={"Accept": "application/json", **(headers or {})}),
            retry_statuses=retry_statuses,
        )

    def _request(self, request: Request, retry_statuses: set[int] | None = None):
        retry_statuses = retry_statuses or set()
        for attempt in range(1, self.attempts + 1):
            try:
                with urlopen(request, timeout=10) as response:
                    return json.load(response)
            except HTTPError as exc:
                detail = exc.read().decode("utf-8", errors="replace")
                if (
                    exc.code < 500 and exc.code not in retry_statuses
                ) or attempt == self.attempts:
                    raise BootstrapError(
                        f"HTTP {exc.code} from {request.full_url}: {detail}"
                    ) from exc
            except (URLError, TimeoutError) as exc:
                if attempt == self.attempts:
                    raise BootstrapError(
                        f"cannot reach {request.full_url}: {exc}"
                    ) from exc
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


def bootstrap(
    client, agent_id, bkn_safe_url, bkn_agent_url, owner_id, bootstrap_token
) -> str:
    """Keep the deployment command and identity gate; SQL owns default creation."""
    owner = client.get_json(
        _join(bkn_safe_url, f"directory/users/{owner_id}"), retry_statuses={404}
    )
    _validate_owner(owner, owner_id)
    headers = {
        "x-account-id": owner_id,
        "x-account-type": "app",
        "x-bkn-provenance-bootstrap-token": bootstrap_token,
    }
    actual = client.get_json(_join(bkn_agent_url, f"agents/{agent_id}"), headers)
    if actual.get("agent_id") != agent_id or actual.get("create_user") != owner_id:
        raise BootstrapError(f"Agent {agent_id} identity or owner does not match")
    prompt_id = actual.get("prompt_id")
    if not prompt_id:
        raise BootstrapError(f"Agent {agent_id} has no shared Prompt")
    prompt = client.get_json(_join(bkn_agent_url, f"prompts/{prompt_id}"), headers)
    if prompt.get("prompt_id") != prompt_id or not prompt.get("content"):
        raise BootstrapError(f"Agent {agent_id} shared Prompt is missing or invalid")
    # Administrator edits and disabled state are intentional; never reset them.
    return "verified"


def main() -> int:
    try:
        if os.getenv("BKN_PROVENANCE_PACKAGE") or os.getenv(
            "BKN_CLAIM_ATTRIBUTION_PACKAGE"
        ):
            raise BootstrapError(
                "JSON package overrides are no longer supported; manage shared Agent configuration in the database"
            )
        actions = [
            bootstrap(
                JsonClient(),
                agent_id,
                os.environ["BKN_SAFE_URL"],
                os.environ["BKN_AGENT_URL"],
                os.environ["BKN_PROVENANCE_OWNER_ID"],
                os.environ["BKN_PROVENANCE_BOOTSTRAP_TOKEN"],
            )
            for agent_id in (
                "business_provenance_optimizer",
                "business_provenance_claim_attribution",
            )
        ]
    except (BootstrapError, KeyError, OSError, ValueError) as exc:
        print(f"business provenance bootstrap failed: {exc}", file=sys.stderr)
        return 1
    print(f"business provenance bootstrap complete: {','.join(actions)}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
