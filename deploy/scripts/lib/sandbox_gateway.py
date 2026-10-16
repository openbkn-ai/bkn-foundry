#!/usr/bin/env python3
# Copyright openbkn.ai
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Resolve sandbox REST gateway defaults without using the MCP endpoint."""

import json
import sys
from urllib.parse import urlsplit


def preserve(values):
    """Keep operator overrides, but rediscover installer-owned defaults on upgrade."""
    values = values or {}
    result = {}
    base = values.get("controlPlane", {}).get("env", {}).get("BKN_BASE_URL", "")
    if base and base.strip():
        result["controlPlane"] = {"env": {"BKN_BASE_URL": base}}
    policy = values.get("networkPolicy", {}).get("gateway")
    generated = values.get("controlPlane", {}).get("gateway", {}).get("networkPolicy", {})
    if isinstance(policy, dict):
        # Previous discovery output is not an operator override. Keep only
        # changed keys so a moved controller is rediscovered on upgrade.
        policy = {key: value for key, value in policy.items() if generated.get(key) != value}
        if policy:
            result["networkPolicy"] = {"gateway": policy}
    return result


def base_url(render):
    """Read the actual rendered ConfigMap, including chart defaults and all overrides."""
    # Helm's manifest is YAML, but each ConfigMap value is rendered as a quoted
    # string. Inspect only our exact key, without loading or logging Secrets.
    for line in render.get("manifest", "").splitlines():
        if line.startswith("  BKN_BASE_URL:"):
            value = json.loads(line.split(":", 1)[1].strip())
            if value:
                parsed = urlsplit(value)
                if (
                    parsed.scheme not in ("http", "https")
                    or not parsed.hostname
                    or parsed.username
                    or parsed.password
                    or parsed.query
                    or parsed.fragment
                    or parsed.path not in ("", "/")
                ):
                    raise ValueError(
                        "BKN_BASE_URL must be an HTTP(S) origin without credentials or path"
                    )
                if parsed.port is not None and not 1 <= parsed.port <= 65535:
                    raise ValueError("BKN_BASE_URL has an invalid port")
            return value
    raise ValueError("Sandbox ConfigMap BKN_BASE_URL is missing from Helm rendering")


def discover(services):
    """Discover one ingress controller using its Service port, selector and target port."""
    candidates = []
    for service in services.get("items", []):
        spec = service.get("spec", {})
        for port in spec.get("ports", []):
            if port.get("name") != "http" or port.get("protocol", "TCP") != "TCP":
                continue
            metadata = service["metadata"]
            if not spec.get("selector") or not 1 <= int(port["port"]) <= 65535:
                continue
            target_port = port.get("targetPort", port["port"])
            policy = {
                "enabled": True,
                "namespace": metadata["namespace"],
                "podSelector": spec["selector"],
                "port": target_port,
            }
            candidates.append(
                {
                    "controlPlane": {
                        "gateway": {
                            "baseURL": "http://{}.{}.svc:{}".format(
                                metadata["name"], metadata["namespace"], port["port"]
                            ),
                            "networkPolicy": policy,
                        }
                    },
                    "networkPolicy": {"gateway": policy},
                }
            )
    if len(candidates) != 1:
        raise ValueError(
            "Expected one HTTP ingress controller Service; configure "
            "controlPlane.env.BKN_BASE_URL and networkPolicy.gateway explicitly"
        )
    return candidates[0]


def main():
    action = sys.argv[1]
    value = json.load(sys.stdin)
    if action == "preserve":
        json.dump(preserve(value), sys.stdout)
    elif action == "discover":
        json.dump(discover(value), sys.stdout)
    elif action == "base-url":
        print(base_url(value))
    elif action == "validate":
        if not base_url(value):
            raise ValueError("Sandbox requires an explicit or discovered BKN_BASE_URL")
    else:
        raise ValueError("Unknown sandbox gateway action")


if __name__ == "__main__":
    try:
        main()
    except (ValueError, KeyError, TypeError) as error:
        print("Sandbox gateway: {}".format(error), file=sys.stderr)
        sys.exit(1)
