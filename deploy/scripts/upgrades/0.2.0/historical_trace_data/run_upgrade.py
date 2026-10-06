"""One-instance post-upgrade administrator entry point."""

from collections import Counter
from datetime import datetime, timezone
import os
from pathlib import Path
import re
import subprocess
import time
import uuid
import base64
from urllib.parse import urlparse

from logs import convert_log
import reconcile
from snapshot import SQLSource, canonical, digest, private_write, read_snapshot, save_snapshot, strict_loads
from trace import convert_evidence, convert_span
from opensearch_history import (OpenSearchHistoryWriter, document_from_legacy_audit,
                                evidence_document_from_legacy_row)


def _command(arguments, data=None):
    process = subprocess.run(arguments, input=data, capture_output=True)
    if process.returncode:
        raise ValueError("deployment_command_failed")
    return process.stdout


class DeploymentRuntime:
    """Fixed existing Kubernetes deployment; secrets stay inside its pods."""

    def discover(self):
        self.context = _command(["kubectl", "config", "current-context"]).decode().strip()
        namespace = strict_loads(_command(["kubectl", "--context", self.context, "get", "namespace", "kube-system", "-o", "json"]))
        cluster_uid = namespace.get("metadata", {}).get("uid")
        if not isinstance(cluster_uid, str) or not cluster_uid.strip():
            raise ValueError("cluster_identity_missing")
        pods = strict_loads(_command(["kubectl", "--context", self.context, "get", "pods", "-A", "-o", "json"]))["items"]
        database = [pod for pod in pods if pod["metadata"]["namespace"] == "resource" and
                    pod["metadata"]["name"].startswith("mariadb-") and pod["status"].get("phase") == "Running"]
        trace = [pod for pod in pods if pod["metadata"]["namespace"] == "openbkn" and
                 pod["metadata"]["name"].startswith("agent-observability-") and pod["status"].get("phase") == "Running"]
        if len(database) != 1 or not trace:
            raise ValueError("deployment_not_ready")
        pod = sorted(trace, key=lambda value: value["metadata"]["name"])[0]
        self.pod = pod["metadata"]["name"]
        self.source = SQLSource({"kind": "kubernetes", "context": self.context,
                                 "namespace": "resource", "pod": database[0]["metadata"]["name"]})
        containers = pod["spec"]["containers"]
        container = next((value for value in containers if "agent-observability" in value["name"]), containers[0])
        self.container = container["name"]
        values = {entry["name"]: entry.get("value") for entry in container.get("env", [])}
        for entry in container.get("env", []):
            if entry["name"] not in {"OPENSEARCH_AUTH_USERNAME", "OPENSEARCH_AUTH_PASSWORD"}:
                continue
            ref = entry.get("valueFrom", {}).get("secretKeyRef", {})
            if ref.get("name") and ref.get("key"):
                values[entry["name"]] = self._secret(ref["name"], ref["key"])
        self.opensearch_endpoint = values.get("OPENSEARCH_ENDPOINT")
        self.opensearch_log_index = values.get("OPENSEARCH_LOG_INDEX")
        self.opensearch_evidence_index = values.get("OPENSEARCH_EVIDENCE_INDEX")
        self.opensearch_username = values.get("OPENSEARCH_AUTH_USERNAME")
        self.opensearch_password = values.get("OPENSEARCH_AUTH_PASSWORD")
        if not self.opensearch_endpoint or not self.opensearch_log_index or not self.opensearch_evidence_index:
            raise ValueError("opensearch_log_configuration_missing")
        environment = values.get("BKN_AUDIT_ENVIRONMENT")
        if environment not in {"development", "test", "staging", "production"}:
            raise ValueError("deployment_environment_missing")
        self.environment = environment
        return {"instance": "instance-" + digest(cluster_uid.encode()), "cluster_uid": cluster_uid, "environment": environment,
                "target_image": container["image"], "context": self.context,
                "span_source": "no_frozen_015_index_provenance"}

    def _secret(self, name, key):
        encoded = _command(["kubectl", "--context", self.context, "-n", "openbkn", "get", "secret", name,
                            "-o", "jsonpath={.data." + key + "}"]).strip()
        if not encoded:
            raise ValueError("opensearch_secret_missing")
        return base64.b64decode(encoded).decode()

    def _native(self, flags, data):
        command = ["kubectl", "--context", self.context, "-n", "openbkn", "exec", "-i", self.pod,
                   "-c", self.container, "--", "/app/historical-data-validate"] + flags
        return _command(command, data)

    def snapshot(self):
        return self.source.export()

    def validate(self, requests):
        data = "".join(canonical(request) + "\n" for request in requests).encode()
        return [strict_loads(line) for line in self._native([], data).splitlines()] if data else []

    def fetch(self, item):
        # reconcile.fetch_audit reads the 020 bkn_audit month/dedup tables via
        # the discovered MariaDB connection; it never queries the 015 source
        # audit tables. The source snapshot remains immutable for reruns.
        return reconcile.fetch_audit(self.source, item)

    def publish(self, requests):
        data = "".join(canonical(request) + "\n" for request in requests).encode()
        output = self._native(["--publish-audit", "--in-place-upgrade", "--expected-plan-sha256", digest(data)], data)
        return [strict_loads(line) for line in output.splitlines()]

    def migrate_evidence(self, records):
        raise ValueError("evidence_dependency_set_not_verified")

    def migrate_spans(self, records):
        raise ValueError("span_target_config_not_verified")

    def publish_logs(self, events):
        endpoint = self.opensearch_endpoint
        parsed = urlparse(endpoint)
        port_forward = None
        if parsed.hostname and parsed.hostname.endswith(".svc.cluster.local"):
            # The administrator entry point runs on the host, while the
            # deployed endpoint is only resolvable inside Kubernetes.
            port_forward = subprocess.Popen(
                ["kubectl", "--context", self.context, "-n", "resource", "port-forward",
                 "svc/opensearch-cluster-master", "0:9200"],
                stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
            line = port_forward.stdout.readline() if port_forward.stdout else ""
            match = re.search(r"Forwarding from 127\.0\.0\.1:(\d+)", line)
            if not match:
                port_forward.terminate()
                raise RuntimeError("opensearch_port_forward_failed")
            endpoint = "http://127.0.0.1:" + match.group(1)
        try:
            writer = OpenSearchHistoryWriter(endpoint, self.opensearch_log_index,
                                             self.opensearch_username, self.opensearch_password)
            return writer.publish(events, datetime.now(timezone.utc))
        finally:
            if port_forward is not None:
                port_forward.terminate()
                port_forward.wait(timeout=5)

    def publish_history(self, records):
        """Write every retained 015 log/evidence row to its 020 index."""
        endpoint = self.opensearch_endpoint
        parsed = urlparse(endpoint)
        port_forward = None
        if parsed.hostname and parsed.hostname.endswith(".svc.cluster.local"):
            port_forward = subprocess.Popen(
                ["kubectl", "--context", self.context, "-n", "resource", "port-forward",
                 "svc/opensearch-cluster-master", "0:9200"],
                stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
            line = port_forward.stdout.readline() if port_forward.stdout else ""
            match = re.search(r"Forwarding from 127\.0\.0\.1:(\d+)", line)
            if not match:
                port_forward.terminate()
                raise RuntimeError("opensearch_port_forward_failed")
            endpoint = parsed.scheme + "://127.0.0.1:" + match.group(1)
        try:
            observed_at = datetime.now(timezone.utc)
            audit_items = [document_from_legacy_audit(record, observed_at, self.environment)
                           for record in records if record.get("kind") == "audit"]
            evidence_items = [evidence_document_from_legacy_row(record, observed_at)
                              for record in records if record.get("kind") == "evidence"]
            tls_options = {
                "verify_tls": os.environ.get("BKN_HISTORY_OPENSEARCH_TLS_VERIFY", "false" if port_forward else "true").lower() != "false",
                "ca_file": os.environ.get("BKN_HISTORY_OPENSEARCH_CA_FILE") or None,
            }
            log_writer = OpenSearchHistoryWriter(endpoint, self.opensearch_log_index,
                                                 self.opensearch_username, self.opensearch_password, **tls_options)
            evidence_writer = OpenSearchHistoryWriter(endpoint, self.opensearch_evidence_index,
                                                      self.opensearch_username, self.opensearch_password, **tls_options)
            return {
                "logs": log_writer.publish_documents(audit_items, observed_at),
                "evidence": evidence_writer.publish_documents(evidence_items, observed_at),
            }
        finally:
            if port_forward is not None:
                port_forward.terminate()
                port_forward.wait(timeout=5)


def _status(runtime, item):
    return reconcile.check([item], runtime.fetch)["results"][0]["status"]


def _report(directory, result, reasons):
    lines = ["# 015 -> 020 Historical Data Conversion", "", "## Result", "",
             "- State: " + result["state"],
             "- Source records: %d" % result["source_count"],
             "- Source records written to 020 OpenSearch: %d" % result.get("history_written_count", 0),
             "- OpenSearch log documents created: %d" % result.get("opensearch_log_created", 0),
             "- OpenSearch log documents updated: %d" % result.get("opensearch_log_updated", 0),
             "- OpenSearch log documents already verified: %d" % result.get("opensearch_log_already_verified", 0),
             "- OpenSearch log conflicts: %d" % result.get("opensearch_log_conflict", 0),
             "- OpenSearch evidence documents created: %d" % result.get("opensearch_evidence_created", 0),
             "- OpenSearch evidence documents updated: %d" % result.get("opensearch_evidence_updated", 0),
             "- OpenSearch evidence documents already verified: %d" % result.get("opensearch_evidence_already_verified", 0),
             "- OpenSearch evidence conflicts: %d" % result.get("opensearch_evidence_conflict", 0),
             "- Already verified before publication: %d" % result["already_verified_count"],
             "- Source records not written to 020 OpenSearch: %d" % (
                 result["source_count"] - result.get("history_written_count", 0)
                 if result.get("history_mode") else result["retained_count"]),
             "", "## Conversion Notes", ""]
    if result.get("history_mode") and result.get("complete"):
        lines.append("- All source rows were written to their corresponding 020 OpenSearch store.")
        lines.append("- 020-only fields absent from 015 remain absent; source values were not changed.")
    elif result.get("history_mode"):
        lines.append("- History publication did not complete; inspect the failure reasons below before retrying.")
        lines.extend("- %s: %d" % entry for entry in sorted(reasons.items()))
    else:
        lines.extend("- %s: %d" % entry for entry in sorted(reasons.items()))
    lines.extend(["", "Original rows remain in the private source snapshot for repeatable reruns.",
                  "OpenSearch written counts are confirmed by document readback.",
                  "Existing completed archive files/jobs are unchanged and are not imported.",
                  "",])
    private_write(directory / "report.md", "\n".join(lines).encode())


def run(runtime, state_root):
    """Single run, retaining independent rejected records and unknown dependencies."""
    state_root = Path(state_root)
    state_root.mkdir(mode=0o700, parents=True, exist_ok=True)
    directory = state_root / ("run-" + datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%S") + "-" + uuid.uuid4().hex[:8])
    directory.mkdir(mode=0o700)
    result = {"complete": False, "state": "precheck_failed", "source_count": 0,
              "target_verified_count": 0, "already_verified_count": 0, "retained_count": 0,
             "opensearch_log_created": 0, "opensearch_log_already_verified": 0,
             "opensearch_log_updated": 0, "opensearch_log_conflict": 0, "opensearch_evidence_created": 0,
             "opensearch_evidence_updated": 0,
              "opensearch_evidence_already_verified": 0, "opensearch_evidence_conflict": 0,
              "history_written_count": 0, "history_mode": False, "run_directory": str(directory)}
    reasons, items = Counter(), []
    phase = "precheck_failed"
    try:
        deployment = runtime.discover()
        instance = deployment["instance"]
        if not re.fullmatch(r"[a-zA-Z0-9_.-]+", instance):
            raise ValueError("invalid_instance_identity")
        source = state_root / instance / "source"
        phase = "source_snapshot_failed"
        if not source.exists():
            records, tables = runtime.snapshot()
            save_snapshot(source, records, instance, {"tables": tables, "deployment": deployment})
        phase = "snapshot_deployment_mismatch"
        manifest = strict_loads((source / "snapshot.json").read_text())
        frozen = manifest.get("metadata", {}).get("deployment", {})
        if (not deployment.get("cluster_uid") or manifest.get("source_deployment") != instance or
                frozen.get("instance") != instance or frozen.get("cluster_uid") != deployment["cluster_uid"]):
            raise ValueError("snapshot_deployment_mismatch")
        phase = "source_snapshot_failed"
        records = list(read_snapshot(source))
        result["source_count"] = len(records)
        if hasattr(runtime, "publish_history"):
            result["history_mode"] = True
            phase = "history_publication_failed"
            history = runtime.publish_history(records)
            for prefix, key in (("opensearch_log", "logs"), ("opensearch_evidence", "evidence")):
                for state in ("created", "updated", "already_verified", "conflict"):
                    result[prefix + "_" + state] = int(history[key].get(state, 0))
                if history[key].get("conflict", 0):
                    reasons[prefix + "_conflict"] += int(history[key]["conflict"])
            result["history_written_count"] = sum(
                result[name] for name in (
                    "opensearch_log_created", "opensearch_log_updated", "opensearch_log_already_verified",
                    "opensearch_evidence_created", "opensearch_evidence_updated",
                    "opensearch_evidence_already_verified"))
            result["complete"] = result["history_written_count"] == result["source_count"] and not reasons
            result["state"] = "completed" if result["complete"] else "partial_requires_reconciliation"
            private_write(directory / "items.jsonl", "".join(canonical({"kind": r["kind"], "source_id": r["source_id"], "source": r, "disposition": "migrated" if result["complete"] else "requires_readback"}) + "\n" for r in records).encode())
            _report(directory, result, reasons)
            return result
        clock = datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")
        requests, candidates = [], []
        delegated = {"evidence": [], "span": []}
        phase = "conversion_failed"
        for ordinal, record in enumerate(records):
            if record.get("kind") == "evidence":
                # Validate the stored Event wrapper before retaining it. Admission
                # still requires the old owner/sequence dependency set, which is
                # deliberately not guessed from the 020 database.
                candidate = convert_evidence(record["row"], instance)
                if record.get("frozen_015_provenance") is True:
                    delegated["evidence"].append(record)
                    item = {"ordinal": ordinal, "kind": "evidence", "source": record,
                            "disposition": "convert", "reason": "delegated_to_existing_writer"}
                else:
                    item = {"ordinal": ordinal, "kind": "evidence", "source": record,
                            "disposition": "archive",
                            "reason": "evidence_dependency_set_not_verified",
                            "format_disposition": candidate.get("disposition"),
                            "format_reason": candidate.get("reason", "")}
            elif record.get("kind") == "span":
                # A current 020 index is not proof that a document came from
                # 015. Without frozen source provenance, retain the raw span.
                candidate = convert_span(record["row"], instance)
                if record.get("frozen_015_provenance") is True:
                    delegated["span"].append(record)
                    item = {"ordinal": ordinal, "kind": "span", "source": record,
                            "disposition": "convert", "reason": "delegated_to_existing_writer"}
                else:
                    item = {"ordinal": ordinal, "kind": "span", "source": record,
                            "disposition": "archive",
                            "reason": "span_source_provenance_not_verified",
                            "format_disposition": candidate.get("disposition"),
                            "format_reason": candidate.get("reason", "")}
            else:
                converted = convert_log(record["source_id"], record["row"], deployment["environment"], instance)
                item = {"ordinal": ordinal, "kind": "audit", "source_id": record["source_id"], "source": record,
                        "source_sha256": digest(canonical(record).encode()), **converted}
                if converted["disposition"] == "convert":
                    requests.append({"kind": "audit", "payload": converted["event"], "broker_time": clock})
                    candidates.append(len(items))
            items.append(item)
        validations = runtime.validate(requests)
        if len(validations) != len(requests):
            raise ValueError("native_validation_count_mismatch")
        for index, request, validation in zip(candidates, requests, validations):
            item = items[index]
            if (not isinstance(validation, dict) or validation.get("accepted") is not True or
                    canonical(validation.get("canonical_payload")) != canonical(request["payload"])):
                item.update(disposition="archive", reason="native_validation_rejected")
            else:
                item.update(payload=validation["canonical_payload"], target_id=validation["event_id"],
                            content_hash=validation["content_hash"],
                            payload_sha256=digest(canonical(validation["canonical_payload"]).encode()))
        for kind, records_for_writer in delegated.items():
            if not records_for_writer:
                continue
            try:
                outcome = getattr(runtime, "migrate_evidence" if kind == "evidence" else "migrate_spans")(records_for_writer)
                verified = int(outcome.get("verified", 0))
                retained = int(outcome.get("retained", len(records_for_writer) - verified))
                result["target_verified_count"] += verified
                result["retained_count"] += retained
                for record in records_for_writer[:verified]:
                    for item in items:
                        if item.get("source") is record:
                            item.update(disposition="writer_verified", reason="existing_writer_readback_verified")
                            break
                if retained:
                    reasons[outcome.get("reason", kind + "_writer_retained")] += retained
            except (OSError, ValueError, KeyError, TypeError):
                result["retained_count"] += len(records_for_writer)
                reasons[kind + "_writer_requires_dependencies"] += len(records_for_writer)
                for item in items:
                    if item.get("source") in records_for_writer and item["disposition"] == "convert":
                        item.update(disposition="archive", reason=kind + "_writer_requires_dependencies")
        identities = {}
        for item in items:
            if item["disposition"] == "convert":
                peers = identities.setdefault(item["target_id"], [])
                peers.append(item)
        for peers in identities.values():
            if len({item["content_hash"] for item in peers}) > 1:
                for item in peers:
                    item.update(disposition="archive", reason="source_identity_content_conflict")
        private_write(directory / "items.jsonl", "".join(canonical(item) + "\n" for item in items).encode())
        missing = []
        verified_audit_events = {}
        phase = "target_readback_failed"
        for item in items:
            if item["disposition"] == "writer_verified":
                continue
            if item["disposition"] != "convert":
                reasons[item["reason"]] += 1
                result["retained_count"] += 1
                continue
            status = _status(runtime, item)
            if status == "verified":
                result["already_verified_count"] += 1
                result["target_verified_count"] += 1
                verified_audit_events[item["payload"]["event_id"]] = item["payload"]
            elif status == "missing":
                missing.append(item)
            else:
                reasons["target_content_conflict"] += 1
                result["retained_count"] += 1
        if missing:
            phase = "publication_requires_readback"
            request_bytes = "".join(canonical({"kind": "audit", "payload": item["payload"], "broker_time": clock}) + "\n" for item in missing).encode()
            private_write(directory / "approved-requests.jsonl", request_bytes)
            publication_error = False
            try:
                replies = runtime.publish([strict_loads(line) for line in request_bytes.splitlines()])
                private_write(directory / "publication-receipt.json", (canonical({"request_sha256": digest(request_bytes), "entries": replies, "database_confirmation": False}) + "\n").encode())
                publication_error = len(replies) != len(missing) or any(reply.get("accepted") is not True for reply in replies)
            except (OSError, ValueError, KeyError, TypeError):
                publication_error = True
                private_write(directory / "publication-receipt.json", (canonical({"request_sha256": digest(request_bytes), "state": "outcome_unknown_requires_readback", "database_confirmation": False}) + "\n").encode())
            for item in missing:
                status = "missing"
                for attempt in range(5):
                    status = _status(runtime, item)
                    if status != "missing":
                        break
                    if attempt < 4 and isinstance(runtime, DeploymentRuntime):
                        time.sleep(2)
                if status == "verified":
                    result["target_verified_count"] += 1
                    verified_audit_events[item["payload"]["event_id"]] = item["payload"]
                else:
                    result["retained_count"] += 1
                    reasons["publication_requires_readback" if publication_error else "target_" + status] += 1
        if verified_audit_events and hasattr(runtime, "publish_logs") and not hasattr(runtime, "publish_history"):
            try:
                log_result = runtime.publish_logs(list(verified_audit_events.values()))
                for key in ("created", "already_verified", "conflict"):
                    result["opensearch_log_" + key] = int(log_result.get(key, 0))
                if result["opensearch_log_conflict"]:
                    reasons["opensearch_log_conflict"] += result["opensearch_log_conflict"]
            except (OSError, RuntimeError, ValueError, KeyError, TypeError):
                reasons["opensearch_log_publish_failed"] += len(verified_audit_events)
        result["complete"] = (
            result["history_written_count"] == result["source_count"] and
            not any(name.endswith("_conflict") for name in reasons)
        ) if hasattr(runtime, "publish_history") else (
            result["target_verified_count"] + result["retained_count"] == result["source_count"] and
            not any(name.startswith(("publication_", "target_", "opensearch_log_")) for name in reasons)
        )
        result["state"] = "completed" if result["complete"] else "partial_requires_reconciliation"
    except (OSError, RuntimeError, ValueError, KeyError, TypeError):
        reasons[phase] += 1
        result["state"] = phase
    _report(directory, result, reasons)
    return result


def main():
    os.umask(0o077)
    root = Path.home() / ".bkn" / "upgrades" / "015-to-020-historical"
    result = run(DeploymentRuntime(), root)
    print(str(Path(result["run_directory"]) / "report.md"))
    return 0 if result["complete"] else 2


if __name__ == "__main__":
    raise SystemExit(main())
