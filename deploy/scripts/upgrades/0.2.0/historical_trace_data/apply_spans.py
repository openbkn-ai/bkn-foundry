"""Native SS4O writer with complete source readback, never source mutation."""

import base64
import copy
import json
import os
from pathlib import Path
import re
from urllib.error import HTTPError, URLError
from urllib.parse import quote, urlencode, urlsplit
from urllib.request import Request, build_opener, ProxyHandler, HTTPRedirectHandler

from apply_audit import _write_receipt, _loopback
from plan import verify
from snapshot import canonical, digest, strict_loads
from trace import _timestamp_ns


_BASE_MAPPING = json.loads((Path(__file__).parent / "contracts" / "span-index-mapping.json").read_text())
_MAPPING_HASH = digest(canonical(_BASE_MAPPING).encode())
_MAPPING = {**_BASE_MAPPING, "_meta": {"historical_span_mapping_sha256": _MAPPING_HASH}}
_NANOS_MAX = (1 << 63) - 1


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, request, file, code, message, headers, new_url):
        raise HTTPError(request.full_url, code, "redirect refused", headers, file)


class OpenSearch:
    def __init__(self, profile):
        endpoint = profile.get("endpoint")
        index = profile.get("index")
        if not isinstance(endpoint, str):
            raise ValueError("invalid OpenSearch endpoint")
        parsed = urlsplit(endpoint)
        if (parsed.scheme not in {"http", "https"} or not parsed.hostname or parsed.username is not None or
                parsed.password is not None or parsed.query or parsed.fragment):
            raise ValueError("OpenSearch endpoint must not contain credentials or query parameters")
        if not isinstance(index, str) or not re.fullmatch(r"[a-z0-9][a-z0-9_.-]{0,254}", index):
            raise ValueError("explicit single OpenSearch index required")
        self.endpoint, self.index = endpoint.rstrip("/"), index
        self.authorization = None
        username_env, password_env = profile.get("user_env"), profile.get("password_env")
        if username_env or password_env:
            if not all(isinstance(name, str) and re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", name)
                       for name in (username_env, password_env)):
                raise ValueError("both credential environment variable names are required")
            username, password = os.environ.get(username_env), os.environ.get(password_env)
            if not username or not password:
                raise ValueError("OpenSearch credential environment is incomplete")
            self.authorization = "Basic " + base64.b64encode((username + ":" + password).encode()).decode()

    def request(self, method, path, data=None, ndjson=False):
        encoded = data if ndjson else canonical(data).encode() if data is not None else None
        headers = {"Content-Type": "application/x-ndjson" if ndjson else "application/json"}
        if self.authorization:
            headers["Authorization"] = self.authorization
        request = Request(self.endpoint + path, data=encoded, headers=headers, method=method)
        try:
            opener = build_opener(ProxyHandler({}), NoRedirect())
            with opener.open(request, timeout=60) as response:
                status, payload = response.status, response.read()
        except HTTPError as error:
            status = error.code
            error.close()
            if 300 <= status < 400:
                raise ValueError("OpenSearch redirect refused; target isolation preserved") from None
            return status, {}
        except (URLError, OSError):
            raise ValueError("OpenSearch transport failed; reconcile target privately") from None
        try:
            return status, strict_loads(payload)
        except (ValueError, UnicodeError):
            raise ValueError("OpenSearch response JSON invalid") from None


def _contains(actual, expected):
    if isinstance(expected, dict):
        if (isinstance(actual, dict) and expected.get("type") == "object" and "type" not in actual and
                ("properties" in actual or "dynamic" in actual)):
            actual = {**actual, "type": "object"}
        return isinstance(actual, dict) and all(key in actual and _contains(actual[key], value) for key, value in expected.items())
    if isinstance(expected, bool):
        return (type(actual) is bool and actual == expected) or actual == str(expected).lower()
    return actual == expected


def _profile_approval(profile, expected_profile_sha256, qualification=False, writing=False):
    if digest(canonical(profile).encode()) != expected_profile_sha256:
        raise ValueError("exact Span target profile SHA-256 approval required")
    if writing:
        if qualification is not True:
            raise ValueError("release Span writes are not qualified; explicit development qualification required")
        parsed = urlsplit(profile.get("endpoint", ""))
        if not _loopback(parsed.hostname) or not profile.get("index", "").startswith("bkn-history-test-"):
            raise ValueError("qualification Span writes require loopback endpoint and bkn-history-test- index")


def initialize_index(profile, *, qualification=False, expected_profile_sha256=None):
    """Create an explicit index or verify its frozen mapping; never retrofit it."""
    _profile_approval(profile, expected_profile_sha256, qualification, writing=True)
    client = OpenSearch(profile)
    path = "/" + quote(client.index, safe="")
    status, result = client.request("GET", path + "/_mapping")
    if status == 404:
        status, result = client.request("PUT", path, {"mappings": copy.deepcopy(_MAPPING)})
        if status not in (200, 201) or result.get("acknowledged") is not True:
            raise ValueError("new Span index creation not acknowledged")
        return {"stage": "native_span_index_initialized", "mapping_sha256": _MAPPING_HASH}
    if status != 200:
        raise ValueError("OpenSearch mapping transport unavailable; target state unknown")
    mapping = result.get(client.index, {}).get("mappings") if isinstance(result, dict) else None
    if not _contains(mapping, _MAPPING):
        raise ValueError("existing Span index does not match frozen mapping")
    return {"stage": "native_span_index_mapping_verified", "mapping_sha256": _MAPPING_HASH}


def _types(value, path, seen):
    if value is None:
        return
    kind = ("object" if isinstance(value, dict) else "array" if isinstance(value, list) else
            "boolean" if isinstance(value, bool) else "integer" if isinstance(value, int) else
            "float" if isinstance(value, float) else "string" if isinstance(value, str) else "unsupported")
    if kind == "unsupported" or (kind == "integer" and not -(1 << 63) <= value < (1 << 63)):
        raise ValueError("Span field cannot be indexed without numeric coercion")
    if path in seen and seen[path] != kind:
        raise ValueError("Span field type conflict before target writes")
    seen[path] = kind
    if isinstance(value, dict):
        for key, child in value.items():
            segments = tuple(key.split("."))
            for length in range(1, len(segments)):
                intermediate = path + segments[:length]
                if intermediate in seen and seen[intermediate] != "object":
                    raise ValueError("Span dotted field type conflict before target writes")
                seen[intermediate] = "object"
            _types(child, path + segments, seen)
    elif isinstance(value, list):
        for child in value:
            _types(child, path + ("[]",), seen)


def _nanos(value):
    try:
        nanos = _timestamp_ns(value)
    except (TypeError, ValueError, OverflowError):
        raise ValueError("Span date_nanos timestamp invalid") from None
    if not 0 <= nanos <= _NANOS_MAX:
        raise ValueError("Span date_nanos timestamp outside supported range")


def _prepare(plan_root, expected_items_sha256, expected_plan_sha256):
    manifest_data = (Path(plan_root) / "plan.json").read_bytes()
    if digest(manifest_data) != expected_plan_sha256:
        raise ValueError("complete Span plan manifest SHA-256 approval required")
    manifest = verify(plan_root)
    if (Path(plan_root) / "plan.json").read_bytes() != manifest_data:
        raise ValueError("approved Span plan manifest changed")
    data = (Path(plan_root) / "items.jsonl").read_bytes()
    if manifest.get("items_sha256") != expected_items_sha256 or digest(data) != expected_items_sha256:
        raise ValueError("approved Span plan SHA-256 mismatch")
    items, seen, identities = [], {}, {}
    for line in data.splitlines():
        item = strict_loads(line)
        if item.get("kind") != "span":
            continue
        if item.get("disposition") == "blocked":
            raise ValueError("selected Span plan contains blocked items")
        if item.get("disposition") == "archive":
            continue
        if item.get("disposition") != "convert":
            raise ValueError("unsupported Span plan disposition")
        source = item.get("source")
        if not isinstance(source, dict) or source.get("kind") != "span" or digest(canonical(source).encode()) != item.get("source_sha256"):
            raise ValueError("Span source hash mismatch")
        payload = item.get("payload")
        checksum = digest(canonical(payload).encode())
        if not isinstance(payload, dict) or item.get("payload_sha256") != checksum or item.get("content_hash") != "sha256:" + checksum:
            raise ValueError("Span native payload hash mismatch")
        identity = item.get("target_id")
        sidecar = item.get("sidecar", {})
        if not isinstance(identity, str) or not identity or sidecar.get("target_id") != identity:
            raise ValueError("Span deterministic target identity missing")
        routing = sidecar.get("routing")
        if routing is not None and not isinstance(routing, str):
            raise ValueError("Span original routing invalid")
        key = identity, routing
        if key in identities and identities[key] != checksum:
            raise ValueError("Span target identity content conflict")
        identities[key] = checksum
        _types(payload, (), seen)
        _nanos(payload.get("startTime"))
        _nanos(payload.get("endTime"))
        for event in payload.get("events", []):
            for timestamp in ("@timestamp", "observedTimestamp"):
                if timestamp in event:
                    _nanos(event[timestamp])
        items.append(item)
    return items


def _readback(client, item):
    path = "/" + quote(client.index, safe="") + "/_doc/" + quote(item["target_id"], safe="")
    routing = item["sidecar"].get("routing")
    if routing is not None:
        path += "?" + urlencode({"routing": routing})
    status, result = client.request("GET", path)
    if (status != 200 or result.get("found") is not True or result.get("_id") != item["target_id"] or
            digest(canonical(result.get("_source")).encode()) != item["payload_sha256"] or
            (routing is not None and result.get("_routing") != routing)):
        raise ValueError("Span target readback missing or content/routing conflict")


def _receipt(items, expected_items_sha256):
    return {"format_version": 1, "items_sha256": expected_items_sha256, "mapping_sha256": _MAPPING_HASH,
            "stage": "span_write_pending_outcome_unknown", "created_count": 0, "verified_count": 0,
            "existing_verified_count": 0,
            "entries": [{"target_id": item["target_id"], "payload_sha256": item["payload_sha256"],
                         "status": "outcome_unknown_requires_reconciliation"} for item in items]}


def apply(plan_root, expected_items_sha256, profile, receipt_path, *, expected_plan_sha256=None,
          expected_profile_sha256=None, qualification=False):
    """Bulk CREATE complete native sources, then verify each exact readback."""
    _profile_approval(profile, expected_profile_sha256, qualification, writing=True)
    items = _prepare(plan_root, expected_items_sha256, expected_plan_sha256)
    if not items:
        raise ValueError("Span plan has no native candidates")
    client = OpenSearch(profile)
    path = Path(receipt_path)
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    receipt = _receipt(items, expected_items_sha256)
    receipt.update(plan_sha256=expected_plan_sha256, profile_sha256=expected_profile_sha256,
                   qualification=True, release_ready=False)
    _write_receipt(path, receipt, initial=True)
    try:
        initialize_index(profile, qualification=True, expected_profile_sha256=expected_profile_sha256)
        for begin in range(0, len(items), 250):
            batch = items[begin:begin + 250]
            lines = []
            for item in batch:
                action = {"_index": client.index, "_id": item["target_id"]}
                if item["sidecar"].get("routing") is not None:
                    action["routing"] = item["sidecar"]["routing"]
                lines.extend((canonical({"create": action}), canonical(item["payload"])))
            data = ("\n".join(lines) + "\n").encode()
            status, response = client.request("POST", "/" + quote(client.index, safe="") + "/_bulk?refresh=wait_for", data, ndjson=True)
            outcomes = response.get("items", [])
            if status != 200 or not isinstance(outcomes, list) or len(outcomes) != len(batch):
                raise ValueError("Span bulk response count mismatch")
            failed = False
            for offset, (item, outcome) in enumerate(zip(batch, outcomes)):
                entry = receipt["entries"][begin + offset]
                result = outcome.get("create", {})
                code = result.get("status")
                if result.get("_id") != item["target_id"] or code not in (200, 201, 409):
                    entry["status"] = "bulk_failed_requires_reconciliation"
                    failed = True
                    continue
                try:
                    _readback(client, item)
                except ValueError:
                    entry["status"] = "readback_conflict_requires_reconciliation"
                    failed = True
                    continue
                entry["status"] = "native_source_readback_verified"
                receipt["verified_count"] += 1
                if code == 409:
                    receipt["existing_verified_count"] += 1
                else:
                    receipt["created_count"] += 1
            _write_receipt(path, receipt)
            if failed:
                raise ValueError("Span bulk write or readback incomplete")
        receipt["stage"] = "native_span_readback_verified"
        _write_receipt(path, receipt)
        return receipt
    except (ValueError, TypeError, KeyError):
        receipt["stage"] = "span_write_failed_requires_reconciliation"
        _write_receipt(path, receipt)
        raise ValueError("Span write/readback incomplete; inspect private receipt and reconcile") from None


def reconcile(plan_root, expected_items_sha256, profile, *, expected_plan_sha256=None, expected_profile_sha256=None):
    """Read every source back; no bulk request or target mutation."""
    _profile_approval(profile, expected_profile_sha256)
    items = _prepare(plan_root, expected_items_sha256, expected_plan_sha256)
    client = OpenSearch(profile)
    receipt = _receipt(items, expected_items_sha256)
    receipt.update(plan_sha256=expected_plan_sha256, profile_sha256=expected_profile_sha256, release_ready=False)
    for item, entry in zip(items, receipt["entries"]):
        _readback(client, item)
        entry["status"] = "native_source_readback_verified"
        receipt["verified_count"] += 1
    receipt["stage"] = "native_span_readback_verified"
    return receipt
