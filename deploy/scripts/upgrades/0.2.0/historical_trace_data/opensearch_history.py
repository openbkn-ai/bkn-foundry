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


_AUDIT_EVENT_NAMES = {
    "bkn-backend": "backend.operation.observed",
    "vega": "vega.operation.observed",
    "execution-factory": "execution_factory.operation.observed",
    "model-manager": "model_manager.operation.observed",
    "bkn-safe-admin": "safe.admin.operation.observed",
}


def document_from_legacy_audit(record, observed_at):
    """Map one stored 015 row directly to the 020 SS4O log shape.

    This is a history conversion. It preserves the source row's values and
    does not apply the online event admission rules to decide whether history
    exists.
    """
    source_id = record["source_id"]
    row = record["row"]
    source_log_id = str(row.get("event_id") or row.get("id"))
    access = source_id == "bkn-safe-access"
    action = str(row.get("action") or "operation")
    outcome = str(row.get("outcome") or "unknown")
    target_id = str(row.get("target_id") or row.get("resource") or source_log_id)
    target_name = str(row.get("target_name") or row.get("resource") or target_id)
    actor_id = str(row.get("actor_id") or row.get("actor_name") or "")
    event_name = ("login.succeeded" if action == "login" and outcome == "success" else
                  "login.failed" if action == "login" else
                  "logout.succeeded" if action == "logout" else
                  _AUDIT_EVENT_NAMES.get(source_id, "legacy.operation.observed"))
    category = "access.user" if access else "audit.admin"
    summary = str(row.get("summary") or row.get("change_summary") or
                  (action + " " + target_name)).strip()
    attributes = {
        "schema_version": "1.0",
        "log_id": stable_log_id(source_id, source_log_id),
        "source_id": source_id,
        "source_log_id": source_log_id,
        "log_category": category,
        "event_name": event_name,
        "safe_summary": summary,
        "outcome": outcome,
        "actor_id": actor_id,
        "effective_subject_id": actor_id,
        "target_type": str(row.get("target_type") or row.get("resource") or "resource"),
        "target_id": target_id,
        "target_name": target_name,
        "request_id": str(row.get("request_id") or ""),
        "auth_method": str(row.get("auth_method") or ""),
        "ingress_principal": source_id,
        "trust_level": "trusted",
        "migration_source": "015-to-020",
        "historical_event_id": source_log_id,
    }
    if row.get("http_status") is not None:
        attributes["http_status"] = row["http_status"]
    if row.get("method"):
        attributes["http"] = {"request": {"method": str(row["method"])}}
    occurred_at = row.get("event_time") or row.get("created_at") or row.get("recorded_at")
    document = {
        "@timestamp": occurred_at,
        "observedTimestamp": _iso(observed_at),
        "body": summary,
        "instrumentationScope": {"name": "openbkn.historical-migration"},
        "resource": {"service": {"name": source_id}},
        "severity": {"text": "ERROR" if outcome in {"failure", "denied"} else "INFO",
                     "number": 17 if outcome in {"failure", "denied"} else 9},
        "attributes": attributes,
    }
    return {"_id": attributes["log_id"], "document": document}


def evidence_document_from_legacy_row(record, observed_at):
    """Map the stored 015 evidence event to the native 020 evidence document."""
    import json as _json
    row = record["row"]
    wrapper = _json.loads(row["envelope"]) if isinstance(row.get("envelope"), str) else row.get("envelope", {})
    event = wrapper.get("event", {})
    inner = event.get("envelope", {}).get("event", event)
    trace_id = str(event.get("trace_id") or inner.get("trace_id") or "")
    request_id = str(event.get("request_id") or inner.get("bkn.request.id") or "")
    conversation_id = str(event.get("conversation_id") or inner.get("bkn.conversation.id") or "")
    owner = event.get("owner") or wrapper.get("owner") or event.get("envelope", {}).get("owner") or {}
    payload = inner.get("payload") if isinstance(inner.get("payload"), dict) else {}
    event_doc = {
        "event_id": str(event.get("event_id") or row.get("event_id")),
        "event_type": str(event.get("event_type") or inner.get("event_type") or ""),
        "bkn.trace.schema.version": str(event.get("bkn.trace.schema.version") or "3.0.0"),
        "observed_at": str(event.get("observed_at") or inner.get("observed_at") or row.get("created_at")),
        "emitted_at": str(event.get("emitted_at") or inner.get("emitted_at") or row.get("created_at")),
        "producer_module": str(event.get("producer_id") or inner.get("producer_module") or record["source_id"]),
        "trace_id": trace_id,
        "span_id": str(event.get("span_id") or inner.get("span_id") or ""),
        "bkn.request.id": request_id,
        "bkn.operation.name": str(inner.get("bkn.operation.name") or ""),
        "interaction_id": str(event.get("interaction_id") or inner.get("interaction_id") or ""),
        "operation_id": str(event.get("operation_id") or inner.get("operation_id") or ""),
        "attempt": int(event.get("attempt") or inner.get("attempt") or 0),
        "payload": payload,
    }
    refs = payload.get("business_refs") if isinstance(payload.get("business_refs"), list) else []
    document_id = "historical-evidence:" + str(event_doc["event_id"])
    document = {
        "document_id": document_id,
        "trace_id": trace_id,
        "bkn.request.id": request_id,
        "bkn.conversation.id": conversation_id,
        "bkn.account.id": str(owner.get("effective_subject_id") or ""),
        "bkn.account.type": str(owner.get("effective_subject_type") or ""),
        "effective_subject_id": str(owner.get("effective_subject_id") or ""),
        "application_principal_id": str(owner.get("application_principal_id") or event.get("producer_id") or record["source_id"]),
        "knowledge_network_ids": [str(payload["kn_id"])] if payload.get("kn_id") else [],
        "bkn.trace.schema.version": str(event.get("bkn.trace.schema.version") or "3.0.0"),
        "events": [event_doc],
        "accepted_event_count": 1,
        "claim_count": len(payload.get("claims", [])) if isinstance(payload.get("claims"), list) else 0,
        "evidence_ref_count": len(payload.get("evidence_refs", [])) if isinstance(payload.get("evidence_refs"), list) else 0,
        "business_ref_count": len(refs),
        "observed_start": event_doc["observed_at"],
        "ingested_at": _iso(observed_at),
        "aggregate": False,
    }
    return {"_id": document_id, "document": document}


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
        if existing.get("ingested_at"):
            comparable["ingested_at"] = existing["ingested_at"]
        return existing == comparable

    def publish(self, events, observed_at):
        return self.publish_documents(
            [document_from_event(event, event.get("event_id"), observed_at) for event in events],
            observed_at,
        )

    def publish_documents(self, items, observed_at=None):
        counts = {"created": 0, "already_verified": 0, "conflict": 0}
        for start in range(0, len(items), 200):
            chunk = items[start:start + 200]
            lines = []
            for item in chunk:
                lines.append(json.dumps({"create": {"_index": self.index, "_id": item["_id"]}}, separators=(",", ":")))
                lines.append(json.dumps(item["document"], ensure_ascii=False, separators=(",", ":")))
            status, body = self._request("POST", "/_bulk", ("\n".join(lines) + "\n").encode(),
                                         content_type="application/x-ndjson")
            if status != 200:
                raise RuntimeError("OpenSearch bulk create failed")
            response = json.loads(body)
            results = response.get("items", [])
            if len(results) != len(chunk):
                raise RuntimeError("OpenSearch bulk response count mismatch")
            created_flags = []
            for item, result in zip(chunk, results):
                create = result.get("create", {})
                if create.get("status") in {200, 201}:
                    counts["created"] += 1
                    created_flags.append(True)
                elif create.get("status") == 409:
                    created_flags.append(False)
                else:
                    created_flags.append(False)
                    counts["conflict"] += 1
            status, body = self._request("POST", "/" + quote(self.index, safe="") + "/_mget",
                                         json.dumps({"docs": [{"_id": item["_id"]} for item in chunk]}, separators=(",", ":")).encode())
            if status != 200:
                raise RuntimeError("OpenSearch history readback failed")
            docs = json.loads(body).get("docs", [])
            for item, doc, created in zip(chunk, docs, created_flags):
                if not doc.get("found"):
                    counts["conflict"] += 1
                elif not self._matches(doc.get("_source"), item["document"]):
                    counts["conflict"] += 1
                elif not created:
                    counts["already_verified"] += 1
        return counts
