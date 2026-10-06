"""Publish verified historical Audit events to the 020 SS4O log index."""

import base64
import hashlib
import json
import os
from datetime import timezone
from urllib.error import HTTPError, URLError
from urllib.parse import quote, urlparse
from urllib.request import Request, ProxyHandler, build_opener



def stable_log_id(source_id, source_log_id):
    return "historical-audit:" + source_id + ":" + source_log_id


def _iso(value):
    if value.tzinfo is None:
        value = value.replace(tzinfo=timezone.utc)
    value = value.astimezone(timezone.utc)
    timespec = "microseconds" if value.microsecond else "seconds"
    return value.isoformat(timespec=timespec).replace("+00:00", "Z")


def document_from_event(event, source_log_id, observed_at):
    actor = event.get("actor") or {}
    target = event.get("target") or {}
    scope = event.get("scope") or {}
    request_context = event.get("request_context") or {}
    correlation = event.get("correlation") or {}
    source_id = event["source_id"]
    source_log_id = str(source_log_id)
    attributes = {
        "schema_version": event.get("schema_version", "1.0"),
        "log_id": stable_log_id(source_id, source_log_id),
        "source_id": source_id,
        "source_log_id": source_log_id,
        "log_category": event["category"],
        "event_name": event["event_name"],
        "safe_summary": event.get("summary", ""),
        "outcome": event.get("outcome", ""),
        "actor_id": actor.get("id", ""),
        "effective_subject_id": actor.get("effective_subject", actor.get("id", "")),
        "target_type": target.get("type", ""),
        "target_id": target.get("id", ""),
        "target_name": target.get("name", ""),
        "business_domain_id": scope.get("business_module", ""),
        "request_id": correlation.get("request_id", ""),
        "ingress_principal": source_id,
        "trust_level": "trusted",
        "migration_source": "015-to-020",
        "historical_event_id": event.get("event_id", source_log_id),
    }
    if scope.get("knowledge_network_ids"):
        attributes["knowledge_network_ids"] = list(scope["knowledge_network_ids"])
    if event.get("http_status") is not None:
        attributes["http_status"] = event["http_status"]
    document = {
        "@timestamp": event["occurred_at"],
        "observedTimestamp": _iso(observed_at),
        "body": event.get("summary", ""),
        "instrumentationScope": {"name": "openbkn.historical-migration"},
        "resource": {
            "service": {"name": source_id},
            "deployment": {"environment": scope.get("environment", "")},
        },
        "severity": {
            "text": "ERROR" if event.get("outcome") in {"failure", "denied"} else "INFO",
            "number": 17 if event.get("outcome") in {"failure", "denied"} else 9,
        },
        "attributes": attributes,
    }
    method = request_context.get("method")
    if method:
        attributes["http"] = {"request": {"method": method}}
    return {"_id": attributes["log_id"], "document": document}


class OpenSearchHistoryWriter:
    def __init__(self, endpoint, index, username=None, password=None, opener=None, timeout=15):
        parsed = urlparse(endpoint)
        if parsed.scheme not in {"http", "https"} or not parsed.netloc or parsed.username or parsed.password:
            raise ValueError("invalid OpenSearch endpoint")
        if not index or any(char in index for char in " /#?"):
            raise ValueError("invalid OpenSearch log index")
        self.endpoint = endpoint.rstrip("/")
        self.index = index
        self.username = username
        self.password = password
        self.opener = opener or build_opener(ProxyHandler({})).open
        self.timeout = timeout

    def _request(self, method, path, body=None, content_type="application/json"):
        headers = {"Content-Type": content_type}
        if self.username is not None or self.password is not None:
            if not self.username or not self.password:
                raise ValueError("OpenSearch credentials are incomplete")
            token = base64.b64encode((self.username + ":" + self.password).encode()).decode()
            headers["Authorization"] = "Basic " + token
        request = Request(self.endpoint + path, data=body, headers=headers, method=method)
        try:
            with self.opener(request, timeout=self.timeout) as response:
                return response.status, response.read()
        except HTTPError as error:
            return error.code, error.read()
        except (URLError, OSError) as error:
            raise RuntimeError("OpenSearch request failed") from error

    def _read(self, log_id):
        status, body = self._request("GET", "/" + quote(self.index, safe="") + "/_doc/" + quote(log_id, safe=""))
        if status == 404:
            return None
        if status != 200:
            raise RuntimeError("OpenSearch readback failed")
        value = json.loads(body)
        return value.get("_source")

    @staticmethod
    def _matches(existing, candidate):
        if not isinstance(existing, dict):
            return False
        comparable = dict(candidate)
        if existing.get("observedTimestamp"):
            comparable["observedTimestamp"] = existing["observedTimestamp"]
        return existing == comparable

    def publish(self, events, observed_at):
        counts = {"created": 0, "already_verified": 0, "conflict": 0}
        for event in events:
            source_log_id = event.get("event_id")
            if not source_log_id:
                counts["conflict"] += 1
                continue
            item = document_from_event(event, source_log_id, observed_at)
            log_id = item["_id"]
            existing = self._read(log_id)
            if existing is not None:
                if self._matches(existing, item["document"]):
                    counts["already_verified"] += 1
                else:
                    counts["conflict"] += 1
                continue
            path = "/" + quote(self.index, safe="") + "/_create/" + quote(log_id, safe="")
            status, _ = self._request("PUT", path, json.dumps(item["document"], ensure_ascii=False, separators=(",", ":")).encode())
            if status == 409:
                existing = self._read(log_id)
                if self._matches(existing, item["document"]):
                    counts["already_verified"] += 1
                else:
                    counts["conflict"] += 1
            elif status in {200, 201}:
                if self._matches(self._read(log_id), item["document"]):
                    counts["created"] += 1
                else:
                    counts["conflict"] += 1
            else:
                raise RuntimeError("OpenSearch create failed")
        return counts
