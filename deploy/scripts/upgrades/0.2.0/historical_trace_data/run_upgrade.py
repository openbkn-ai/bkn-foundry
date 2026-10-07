"""One-instance post-upgrade administrator entry point."""

from contextlib import contextmanager
from collections import Counter
from datetime import datetime, timezone
import os
from pathlib import Path
import re
import subprocess
import tempfile
import time
import uuid
import base64
from urllib.parse import urlparse

from logs import convert_log
import reconcile
from snapshot import SQLSource, canonical, digest, private_write, read_snapshot, save_snapshot, strict_loads
from trace import convert_evidence, convert_span
from native_evidence import plan_aggregates
from native_core import plan_core, core_import_batches, CORE_FIELDS
from opensearch_history import OpenSearchHistoryWriter


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
                   "-c", self.container, "--", getattr(self, "native_program", "/app/historical-data-validate")] + flags
        return _command(command, data)

    def snapshot(self):
        return self.source.export()

    def validate(self, requests):
        data = "".join(canonical(request) + "\n" for request in requests).encode()
        answers = [strict_loads(line) for line in self._native([], data).splitlines()] if data else []
        self._audit_ids = [answer["event_id"] for answer in answers if answer.get("accepted")]
        self._audit_cache = None
        self._audit_published = False
        return answers

    def fetch(self, item):
        # reconcile.fetch_audit reads the 020 bkn_audit month/dedup tables via
        # the discovered MariaDB connection; it never queries the 015 source
        # audit tables. The source snapshot remains immutable for reruns.
        if not hasattr(self, "_audit_ids"):
            return reconcile.fetch_audit(self.source, item)
        if self._audit_cache is None or (self._audit_published and item["target_id"] not in self._audit_cache):
            self._audit_cache = reconcile.fetch_audits(self.source, self._audit_ids)
        return self._audit_cache.get(item["target_id"])

    def publish(self, requests):
        data = "".join(canonical(request) + "\n" for request in requests).encode()
        # Kafka delivery may have succeeded even when the command loses its ACK.
        self._audit_cache = None
        self._audit_published = True
        output = self._native(["--publish-audit", "--in-place-upgrade", "--expected-plan-sha256", digest(data)], data)
        return [strict_loads(line) for line in output.splitlines()]

    def rebuild_core_projection(self):
        options = self._tls_options(False)
        settings = {"tls_verify": options["verify_tls"]}
        if options.get("ca_file"):
            settings["ca_pem"] = Path(options["ca_file"]).read_text()
        answer = strict_loads(self._native(["--rebuild-core-projection"], canonical(settings).encode()))
        if answer.get("verified") is not True:
            raise ValueError("native_core_projection_not_verified")
        return answer

    def migrate_evidence(self, records):
        plan = plan_core(records, getattr(self, "actor_names", {}))
        items, rejected = plan_aggregates(plan["records"], datetime.now(timezone.utc), receipts=plan["receipts"])
        prior_items, _ = plan_aggregates(plan["records"], datetime.now(timezone.utc))
        prior_by_id = {item["_id"]: item["document"] for item in prior_items}
        if rejected:
            raise ValueError("native_evidence_conversion_incomplete")
        # The original representation is only an optional exact update baseline.
        # Owner/capacity conflicts already resolved in the target must not reject it.
        originals, _ = plan_aggregates(records, datetime.now(timezone.utc))
        original_by_id = {item["_id"]: item["document"] for item in originals}
        for item in items:
            item["previous_documents"] = [doc for doc in
                (original_by_id.get(item["_id"]), prior_by_id.get(item["_id"])) if doc is not None]
        # Finish all target and transport preflight before any Core write.
        batches = list(core_import_batches(plan))
        for data in batches:
            answer = strict_loads(self._native(["--validate-core-records"], data))
            if answer.get("verified") is not True:
                raise ValueError("native_core_validation_not_verified")
        created = 0
        for data in batches:
            answer = strict_loads(self._native(["--import-core-records"], data))
            if answer.get("verified") is not True:
                raise ValueError("native_core_import_not_verified")
            created += answer["created"]
        total = sum(len(plan[key]) for key in CORE_FIELDS)
        core = {"verified": True, "created": created, "already_verified": total - created,
                "batches": len(batches)}
        states = {}
        with self._opensearch_connection() as (endpoint, forwarded):
            writer = OpenSearchHistoryWriter(endpoint, self.opensearch_evidence_index,
                                             self.opensearch_username, self.opensearch_password,
                                             **self._tls_options(forwarded))
            counts = writer.publish_documents(items, allow_update=True,
                                               on_result=lambda key, state: states.update({key: state}))
        results = [{"verified": False, "reason": rejected.get(i, "native_evidence_write_failed")} for i in range(len(records))]
        for item in items:
            state = states.get(item["_id"], "conflict")
            for ordinal in item["source_ordinals"]:
                results[ordinal] = {"verified": state != "conflict", "reason": "native_aggregate_readback" if state != "conflict" else "native_evidence_content_conflict",
                                    "target_id": item["_id"], "state": state,
                                    "losses": [], "field_defaults": plan["source_map"][ordinal]["defaults"],
                                    "identity_mapping": plan["source_map"][ordinal]}
        return {"results": results, "counts": counts, "core_import": core}

    def prepare_agent_history(self, records, before):
        from agent_history import plan_agent_history
        plan = plan_agent_history(records, before, getattr(self, "actor_names", {}))
        batches = list(core_import_batches(plan["core"]))
        for data in batches:
            if strict_loads(self._native(["--validate-core-records"], data)).get("verified") is not True:
                raise ValueError("native_agent_core_validation_failed")
        data = "".join(canonical({"kind": "artifact", "payload": a}) + "\n" for a in plan["artifacts"]).encode()
        answers = [strict_loads(line) for line in self._native([], data).splitlines()] if data else []
        if len(answers) != len(plan["artifacts"]) or any(a.get("accepted") is not True for a in answers):
            raise ValueError("native_agent_artifact_validation_failed")
        plan["artifacts"] = [a["canonical_payload"] for a in answers]
        items, rejected = plan_aggregates(plan["records"], datetime.now(timezone.utc), receipts=plan["core"]["receipts"])
        if rejected:
            raise ValueError("native_agent_evidence_validation_failed")
        # Exact baseline of the earlier offline conversion which omitted tools.
        # Only that known document, or an identical current result, may be updated.
        roots = {f["operation_id"] for f in plan["core"]["call_facts"] if not f.get("parent_operation_id")}
        prior_records = [r for r in plan["records"]
                         if r["row"]["envelope"]["event"]["operation_id"] in roots]
        prior_receipts = [r for r in plan["core"]["receipts"] if r["operation_id"] in roots]
        prior, _ = plan_aggregates(prior_records, datetime.now(timezone.utc), receipts=prior_receipts)
        prior_by_id = {item["_id"]: item["document"] for item in prior}
        for item in items:
            item["previous_documents"] = [prior_by_id[item["_id"]]] if item["_id"] in prior_by_id else []
        return {"plan": plan, "batches": batches, "items": items}

    def migrate_agent_history(self, prepared):
        plan = prepared["plan"]
        # Artifacts use the ordinary native normalizer and store, not a second document format.
        settings = {"artifacts": plan["artifacts"], "tls_verify": self._tls_options(False)["verify_tls"]}
        ca_file = self._tls_options(False).get("ca_file")
        if ca_file:
            settings["ca_pem"] = Path(ca_file).read_text()
        artifacts = strict_loads(self._native(["--import-artifact-records"], canonical(settings).encode()))
        if artifacts.get("verified") is not True or artifacts.get("verified_count") != len(plan["artifacts"]):
            raise ValueError("native_agent_artifact_readback_failed")
        created = 0
        for data in prepared["batches"]:
            answer = strict_loads(self._native(["--import-core-records"], data))
            if answer.get("verified") is not True:
                raise ValueError("native_agent_core_readback_failed")
            created += answer["created"]
        with self._opensearch_connection() as (endpoint, forwarded):
            writer = OpenSearchHistoryWriter(endpoint, self.opensearch_evidence_index,
                self.opensearch_username, self.opensearch_password, **self._tls_options(forwarded))
            counts = writer.publish_documents(prepared["items"], allow_update=True)
        if counts.get("conflict", 0):
            raise ValueError("native_agent_evidence_conflict")
        total = sum(len(plan["core"][key]) for key in CORE_FIELDS)
        return {"verified": True, "source_count": len(plan["source_map"]),
                "source_map": plan["source_map"], "field_defaults": plan["defaults"],
                "artifacts": artifacts, "evidence_documents": counts,
                "core_import": {"verified": True, "created": created, "already_verified": total-created}}

    def migrate_spans(self, records):
        raise ValueError("span_target_config_not_verified")

    @contextmanager
    def _opensearch_connection(self, timeout=15):
        endpoint = self.opensearch_endpoint
        parsed = urlparse(endpoint)
        match = re.fullmatch(r"([a-z0-9-]+)\.([a-z0-9-]+)\.svc\.cluster\.local", parsed.hostname or "")
        if not match:
            yield endpoint, False
            return
        service, namespace = match.groups()
        remote_port = parsed.port or (443 if parsed.scheme == "https" else 80)
        # A file avoids pipe backpressure during large migrations. Read it via
        # a separate descriptor so the tail reader cannot move kubectl's offset.
        with tempfile.NamedTemporaryFile(mode="w+", prefix="bkn-history-port-forward-") as output:
            process = subprocess.Popen(
                ["kubectl", "--context", self.context, "-n", namespace, "port-forward",
                 "svc/" + service, "0:" + str(remote_port)],
                stdout=output, stderr=subprocess.STDOUT, text=True)
            try:
                deadline = time.monotonic() + timeout
                with open(output.name) as reader:
                    while True:
                        line = reader.readline()
                        ready = re.search(r"Forwarding from 127\.0\.0\.1:(\d+)", line)
                        if ready:
                            endpoint = parsed._replace(netloc="127.0.0.1:" + ready.group(1)).geturl()
                            break
                        if (not line and process.poll() is not None) or time.monotonic() >= deadline:
                            raise RuntimeError("opensearch_port_forward_failed")
                        if not line:
                            time.sleep(0.05)
                yield endpoint, True
            finally:
                if process.poll() is None:
                    process.terminate()
                try:
                    process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=5)

    def _tls_options(self, forwarded):
        return {
            "verify_tls": os.environ.get("BKN_HISTORY_OPENSEARCH_TLS_VERIFY", "false" if forwarded else "true").lower() != "false",
            "ca_file": os.environ.get("BKN_HISTORY_OPENSEARCH_CA_FILE") or None,
        }



def _status(runtime, item):
    return reconcile.check([item], runtime.fetch)["results"][0]["status"]


def _report(directory, result, reasons):
    lines = ["# 015 -> 020 Historical Data Conversion", "", "## Result", "",
             "- State: " + result["state"],
             "- Source records: %d" % result["source_count"],
             "- Source records converted and native target verified: %d" % result["target_verified_count"],
             "- Already verified Audit records before publication: %d" % result["already_verified_count"],
             "- Source records not converted (originals retained): %d" % result["retained_count"],
             "- Core projection verified: " + str(result.get("core_projection", {}).get("verified", False)),
             "- Core projection documents verified: %d" % result.get("core_projection", {}).get("projected_count", 0),
             "- Core projection index: " + str(result.get("core_projection", {}).get("index_version", "not checked")),
             "- Native Evidence aggregate documents: " + canonical(result.get("evidence_documents", {})),
             "- Agent history: " + canonical(result.get("agent_history", {})),
             "- Agent source tables: " + canonical(result.get("agent_source_tables", {})),
             "- Agent cutover (exclusive): " + str(result.get("agent_cutover_before", "not selected")),
             "- Native Core import: " + canonical(result.get("core_import", {})),
             "- Converted records with explicit field defaults or identity mapping: %d" % result.get("field_conversion_count", 0),
             "", "## Conversion Losses and Execution Failures", ""]
    lines.extend("- %s: %d" % entry for entry in sorted(reasons.items()))
    if not reasons:
        lines.append("- None reported.")
    lines.extend(["", "## Target and Product Meaning", "",
                  "Audit records are confirmed in the native Audit ledger; Kafka acknowledgements do not count as persistence.",
                  "Evidence is converted into native aggregate documents and read back. Associated native Core records are converted and imported before rebuilding the ordinary Trace projection.",
                  "Core projection counts include existing records and converted historical Core records.",
                  "No online historical query or UI compatibility branch is permitted.",
                  "Original rows remain in the private source snapshot for repeatable reruns. Final per-record outcomes are recorded in final-items.jsonl when the conversion phase finishes.",
                  "Existing completed archive files/jobs are unchanged and are not imported.", ""])
    private_write(directory / "report.md", "\n".join(lines).encode())


def run(runtime, state_root):
    """Convert every source record; failures remain incomplete and retryable."""
    state_root = Path(state_root)
    state_root.mkdir(mode=0o700, parents=True, exist_ok=True)
    directory = state_root / ("run-" + datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%S") + "-" + uuid.uuid4().hex[:8])
    directory.mkdir(mode=0o700)
    result = {"complete": False, "state": "precheck_failed", "source_count": 0,
              "target_verified_count": 0, "already_verified_count": 0, "retained_count": 0,
              "run_directory": str(directory)}
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
        if isinstance(runtime, DeploymentRuntime):
            runtime.actor_names = {}
            for record in records:
                if record.get("kind") != "evidence":
                    row = record.get("row", {})
                    actor_id = row.get("actor_id") or row.get("user_id") or row.get("operator_id")
                    actor_name = row.get("actor_name_snapshot") or row.get("actor_name") or row.get("user_name") or row.get("operator_name")
                    if actor_id and actor_name:
                        runtime.actor_names[str(actor_id)] = str(actor_name)
        agent_prepared = None
        if hasattr(runtime, "prepare_agent_history"):
            from agent_history import export_agent_source
            from native_core import _time
            phase = "agent_source_precheck_failed"
            before = os.environ.get("BKN_HISTORY_AGENT_BEFORE")
            if not before:
                raise ValueError("agent_history_cutover_required")
            before = _time(before)
            # A separate immutable capture extends the old Audit/Evidence snapshot
            # without replacing it. Customer cutover is explicit, never a release date constant.
            agent_source = state_root / instance / ("agent-source-" + digest(before.encode())[:16])
            if not agent_source.exists():
                agent_records, tables = export_agent_source(runtime.source, before)
                save_snapshot(agent_source, agent_records, instance,
                              {"before": before, "cluster_uid": deployment["cluster_uid"], "tables": tables})
            agent_manifest = strict_loads((agent_source / "snapshot.json").read_text())
            if (agent_manifest.get("source_deployment") != instance or
                    agent_manifest.get("metadata", {}).get("cluster_uid") != deployment["cluster_uid"] or
                    agent_manifest.get("metadata", {}).get("before") != before):
                raise ValueError("agent_snapshot_deployment_mismatch")
            agent_records = list(read_snapshot(agent_source))
            phase = "agent_conversion_preflight_failed"
            agent_prepared = runtime.prepare_agent_history(agent_records, before)
            result["agent_source_tables"] = agent_manifest["source_counts"]
            result["agent_cutover_before"] = before
            result["source_count"] += len(agent_prepared["plan"]["source_map"])
        clock = datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")
        requests, candidates = [], []
        delegated = {"evidence": [], "span": []}
        phase = "conversion_failed"
        for ordinal, record in enumerate(records):
            if record.get("kind") == "evidence":
                # Stored rows are migration input. The deployment helper maps
                # their events and relationships into native Core and Evidence
                # records, without replaying live producer admission.
                candidate = convert_evidence(record["row"], instance)
                if isinstance(runtime, DeploymentRuntime) or record.get("frozen_015_provenance") is True:
                    delegated["evidence"].append(record)
                    item = {"ordinal": ordinal, "kind": "evidence", "source": record,
                            "disposition": "convert", "reason": "delegated_to_existing_writer"}
                else:
                    item = {"ordinal": ordinal, "kind": "evidence", "source": record,
                            "disposition": "archive",
                            "reason": "missing_native_trace_dependencies",
                            "format_disposition": candidate.get("disposition"),
                            "format_reason": candidate.get("reason", "")}
            elif record.get("kind") == "span":
                # The standalone Span writer requires an explicit source index
                # locator; an absent locator is a conversion/configuration gap.
                candidate = convert_span(record["row"], instance)
                if record.get("frozen_015_provenance") is True:
                    delegated["span"].append(record)
                    item = {"ordinal": ordinal, "kind": "span", "source": record,
                            "disposition": "convert", "reason": "delegated_to_existing_writer"}
                else:
                    item = {"ordinal": ordinal, "kind": "span", "source": record,
                            "disposition": "archive",
                            "reason": "missing_source_span_index_metadata",
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
                results = outcome.get("results")
                if results is None:
                    verified = int(outcome.get("verified", 0))
                    results = [{"verified": index < verified, "reason": outcome.get("reason", kind + "_writer_retained")}
                               for index in range(len(records_for_writer))]
                if len(results) != len(records_for_writer):
                    raise ValueError("native_writer_result_count_mismatch")
                if outcome.get("core_import"):
                    result["core_import"] = outcome["core_import"]
                if outcome.get("counts"):
                    result[kind + "_documents"] = outcome["counts"]
                for record, entry in zip(records_for_writer, results):
                    item = next(item for item in items if item.get("source") is record)
                    if entry.get("verified") is True:
                        result["target_verified_count"] += 1
                        item.update(disposition="writer_verified", reason=entry.get("reason", "native_readback_verified"),
                                    target_id=entry.get("target_id"), losses=entry.get("losses", []), state=entry.get("state"),
                                    field_defaults=entry.get("field_defaults", {}), identity_mapping=entry.get("identity_mapping"))
                        for loss in entry.get("losses", []):
                            reasons[loss] += 1
                    else:
                        item.update(disposition="archive", reason=entry.get("reason", kind + "_writer_retained"))
            except (OSError, RuntimeError, ValueError, KeyError, TypeError):
                for item in items:
                    if any(item.get("source") is record for record in records_for_writer):
                        item.update(disposition="archive", reason=kind + "_writer_failed")
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
                item.update(disposition="writer_verified", reason="native_audit_readback", target_status="verified", state="already_verified")
            elif status == "missing":
                missing.append(item)
            else:
                reasons["target_content_conflict"] += 1
                result["retained_count"] += 1
                item.update(disposition="requires_reconciliation", reason="target_content_conflict", target_status=status, state="not_verified")
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
                    item.update(disposition="writer_verified", reason="native_audit_readback", target_status="verified", state="created")
                else:
                    result["retained_count"] += 1
                    reason = "publication_requires_readback" if publication_error else "target_" + status
                    reasons[reason] += 1
                    item.update(disposition="requires_reconciliation", reason=reason, target_status=status, state="not_verified")
        if agent_prepared is not None:
            phase = "agent_target_readback_failed"
            agent_result = runtime.migrate_agent_history(agent_prepared)
            result["agent_history"] = {k:v for k,v in agent_result.items() if k not in {"source_map", "field_defaults"}}
            result["target_verified_count"] += agent_result["source_count"]
            private_write(directory / "agent-field-defaults.json", canonical(agent_result["field_defaults"]).encode())
            defaults_by_interaction = {d["interaction_id"]: d for d in agent_result["field_defaults"]}
            for mapping in agent_result["source_map"]:
                field_defaults = [defaults_by_interaction[i] for i in mapping["interaction_ids"] if i in defaults_by_interaction]
                items.append({"kind": mapping["source_kind"], "source_id": mapping["source_id"],
                              "identity_mapping": mapping, "field_defaults": field_defaults, "disposition": "writer_verified",
                              "reason": "native_agent_core_artifact_evidence_readback"})
        if hasattr(runtime, "rebuild_core_projection"):
            phase = "native_core_projection_failed"
            result["core_projection"] = runtime.rebuild_core_projection()
        result["field_conversion_count"] = sum(bool(item.get("field_defaults") or any(str(value).startswith(("default_", "mapped_source_", "truncated_")) for value in item.get("sidecar", {}).get("provenance", {}).values())) for item in items)
        result["complete"] = result["target_verified_count"] == result["source_count"] and result["retained_count"] == 0
        result["state"] = "completed" if result["complete"] else "partial_requires_reconciliation"
        private_write(directory / "final-items.jsonl", "".join(canonical(item) + "\n" for item in items).encode())
    except (OSError, RuntimeError, ValueError, KeyError, TypeError):
        reasons[phase] += 1
        result["state"] = phase
        result["retained_count"] = max(result["retained_count"], result["source_count"] - result["target_verified_count"])
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
