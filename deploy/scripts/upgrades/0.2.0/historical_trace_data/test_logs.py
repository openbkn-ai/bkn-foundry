import copy
import unittest
import uuid

from logs import convert_log


def row(**overrides):
    value = dict(event_id="old-event-1", event_time="2026-09-12T21:25:44.168466Z",
                 actor_id="user-1", actor_name="Administrator", actor_type="user",
                 auth_method="oauth", request_id="req-1", source_channel="api",
                 method="POST", action="create", target_type="catalog", target_id="catalog-1",
                 target_name="Demo Catalog", outcome="failure", failure_code="http_400",
                 recorded_at="2026-09-12T21:25:45Z", failure_message="private explanation")
    value.update(overrides)
    return value


class LogConverterTests(unittest.TestCase):
    def convert(self, source="vega", **overrides):
        if source.startswith("bkn-safe-"):
            overrides.setdefault("id", "safe-row-1")
            overrides.setdefault("created_at", row()["event_time"])
        return convert_log(source, row(**overrides), "test", "deployment-1")

    def test_vega_original_http_failure_and_names_are_preserved(self):
        result = self.convert()
        self.assertEqual(result["disposition"], "convert")
        event = result["event"]
        self.assertEqual(event["http_status"], 400)
        self.assertEqual(event["failure_code"], "HTTP_400")
        self.assertEqual(event["actor"]["display_name_snapshot"], "Administrator")
        self.assertEqual(event["occurred_at"], row()["event_time"])
        self.assertEqual(event["event_id"], str(uuid.uuid5(uuid.NAMESPACE_URL, "old-event-1")))
        self.assertEqual(result["sidecar"]["provenance"]["http_status"], "source_failure_code")
        self.assertEqual(result["sidecar"]["residue"]["failure_message"], "private explanation")

    def test_repeat_is_deterministic_and_input_unchanged(self):
        original = row()
        saved = copy.deepcopy(original)
        self.assertEqual(convert_log("vega", original, "test", "dep"),
                         convert_log("vega", original, "test", "dep"))
        self.assertEqual(original, saved)

    def test_missing_status_maps_success_and_preserves_outcome(self):
        for source in ("vega", "bkn-backend", "model-manager"):
            result = self.convert(source, outcome="success", failure_code="",
                                  target_type="knowledge_network" if source == "bkn-backend" else
                                  "llm_model" if source == "model-manager" else "catalog",
                                  knowledge_network_id="kn-1")
            self.assertEqual(result["disposition"], "convert")
            self.assertEqual(result["event"]["http_status"], 200)
            self.assertEqual(result["event"]["outcome"], "success")
            self.assertEqual(result["sidecar"]["provenance"]["http_status"], "default_from_source_outcome")

    def test_missing_failure_status_maps_recorded_status_text(self):
        for code, status in (("Service Unavailable", 503), ("Bad Request", 400), ("CUSTOM_ERROR", 500)):
            result = self.convert("bkn-backend", target_type="knowledge_network", target_id="kn-1",
                                  knowledge_network_id="kn-1", failure_code=code)
            self.assertEqual(result["disposition"], "convert")
            self.assertEqual(result["event"]["http_status"], status)
            self.assertEqual(result["event"]["outcome"], "failure")
            self.assertEqual(result["sidecar"]["residue"]["failure_code"], code)

    def test_verified_backend_status_and_kn_scope(self):
        result = self.convert("bkn-backend", target_type="knowledge_network", target_id="kn-1",
                              knowledge_network_id="kn-1", _provenance={"http_status": 400})
        self.assertEqual(result["disposition"], "convert")
        self.assertFalse(result["event"]["scope"]["platform_scope"])
        self.assertEqual(result["event"]["scope"]["knowledge_network_ids"], ["kn-1"])

    def test_backend_stored_network_scope_is_preserved(self):
        result = self.convert("bkn-backend", target_type="object_type", target_id="obj-1",
                              knowledge_network_id="obj-1", _provenance={"http_status": 400})
        self.assertEqual(result["disposition"], "convert")
        self.assertEqual(result["event"]["scope"]["knowledge_network_ids"], ["obj-1"])

    def test_encoded_status_must_agree_with_outcome(self):
        for outcome, code in (("success", "http_400"), ("failure", "http_403"),
                              ("denied", "http_500"), ("failure", "http_200")):
            self.assertEqual(self.convert(outcome=outcome, failure_code=code)["reason"],
                             "inconsistent_source_outcome")

    def test_denied_status_is_convertible(self):
        result = self.convert(outcome="denied", failure_code="http_403")
        self.assertEqual(result["disposition"], "convert")
        self.assertEqual(result["event"]["facts"]["decision"], "denied")

    def test_stored_snapshot_equal_to_id_is_preserved_without_extra_proof(self):
        for changes, field in (({"actor_name": "user-1"}, "actor"),
                               ({"target_name": "catalog-1"}, "target")):
            result = self.convert(**changes)
            self.assertEqual(result["disposition"], "convert")
            self.assertEqual(result["event"][field]["display_name_snapshot" if field == "actor" else "name"],
                             next(iter(changes.values())))

    def test_unresolved_actor_is_not_anonymous_fallback(self):
        for actor in ("", "unknown", "unauthenticated"):
            self.assertEqual(self.convert(actor_id=actor)["reason"], "unresolved_actor")

    def test_execution_factory_success_has_status_exemption(self):
        result = self.convert("execution-factory", target_type="tool", outcome="success", failure_code="")
        self.assertEqual(result["disposition"], "convert")
        self.assertNotIn("http_status", result["event"])
        self.assertNotIn("decision", result["event"]["facts"])

    def test_model_stored_actor_is_preserved_without_extra_proof(self):
        result = self.convert("model-manager", target_type="llm_model")
        self.assertEqual(result["disposition"], "convert")
        self.assertEqual(result["event"]["actor"]["id"], "user-1")
        self.assertEqual(result["event"]["request_context"]["source_channel"], "api")

    def test_migration_identity_distinguishes_deployments_and_safe_rows(self):
        value = row(target_type="tool", outcome="success", failure_code="")
        first = convert_log("execution-factory", value, "test", "a")
        second = convert_log("execution-factory", value, "test", "b")
        self.assertNotEqual(first["event"]["event_id"], second["event"]["event_id"])

    def test_safe_admin_only_committed_http_records(self):
        result = self.convert("bkn-safe-admin", id="safe-1", created_at=row()["event_time"],
                              actor_name_snapshot="Admin", resource="roles", target_name="Operator",
                              status=201, action="add-member")
        self.assertEqual(result["disposition"], "convert")
        self.assertEqual(result["event"]["target"]["type"], "role")
        self.assertEqual(result["event"]["facts"]["action"], "add_member")
        self.assertEqual(result["event"]["outcome"], "success")
        self.assertEqual(self.convert("bkn-safe-admin", resource="roles", status=400)["reason"],
                         "uncommitted_safe_request")
        self.assertEqual(self.convert("bkn-safe-admin", resource="license", status=200, method="SYSTEM")["reason"],
                         "unsupported_safe_system_event")

    def test_safe_access_subject_channel_and_native_event(self):
        result = self.convert("bkn-safe-access", id="safe-access-1", created_at=row()["event_time"],
                              actor_name_snapshot="Admin", action="login", outcome="success",
                              source_channel="web", failure_code="")
        self.assertEqual(result["disposition"], "convert")
        self.assertEqual(result["event"]["event_name"], "login.succeeded")
        self.assertEqual(result["event"]["category"], "access.user")
        self.assertEqual(result["event"]["target"]["id"], result["event"]["actor"]["id"])
        self.assertEqual(result["event"]["request_context"]["source_channel"], "unknown")
        self.assertEqual(self.convert("bkn-safe-access", actor_id="", action="login")["reason"], "unresolved_actor")

    def test_invalid_input_and_unknown_contract_are_classified(self):
        self.assertEqual(self.convert("unknown")["disposition"], "blocked")
        self.assertEqual(self.convert(event_time="2026-09-01 12:00:00")["reason"], "invalid_source_time")
        self.assertEqual(self.convert(target_type="unknown")["reason"], "unregistered_target")
        self.assertEqual(self.convert(action="read")["reason"], "target_scope_excluded")
        self.assertEqual(self.convert(source_channel="internal_api")["event"]["request_context"]["source_channel"], "api")

    def test_native_uuid_source_identity_is_preserved(self):
        native_id = "019ba408-3220-7f10-b56b-d19bc1c6c352"
        result = self.convert("execution-factory", event_id=native_id, target_type="tool", outcome="success", failure_code="")
        self.assertEqual(result["disposition"], "convert")
        self.assertEqual(result["event"]["event_id"], native_id)

    def test_vega_target_alias_uses_frozen_producer_mapping(self):
        result = self.convert(target_type="discover_schedule")
        self.assertEqual(result["disposition"], "convert")
        self.assertEqual(result["event"]["target"]["type"], "discovery_schedule")

    def test_status_conflict_is_blocked_not_archived(self):
        self.assertEqual(self.convert(http_status=500)["disposition"], "blocked")

    def test_unknown_row_and_invalid_numeric_status_do_not_crash(self):
        result = convert_log("vega", [], "test", "dep")
        self.assertEqual(result["reason"], "invalid_source_row")
        result = self.convert(http_status=True, outcome="success", failure_code="")
        self.assertEqual(result["reason"], "invalid_http_status")

    def test_bounded_original_changed_fields_are_mapped_not_body(self):
        result = self.convert(change_summary='{"changed_fields":["name"],"before":{"credential":"private"}}')
        self.assertEqual(result["disposition"], "convert")
        self.assertEqual(result["event"]["facts"]["changed_fields"], ["name"])
        self.assertNotIn("before", result["event"]["facts"])
        self.assertIn("change_summary", result["sidecar"]["residue"])

    def test_invalid_changed_fields_are_not_silently_dropped(self):
        result = self.convert(change_summary={"changed_fields": ["name", "name"]})
        self.assertEqual(result["reason"], "invalid_changed_fields")


if __name__ == "__main__":
    unittest.main()

class BoundedSnapshotConversionTests(unittest.TestCase):
    def test_oversized_label_is_shortened_without_dropping_record(self):
        result=convert_log('vega',row(target_name='A'*600,target_id='ID'*300),'test','instance')
        self.assertEqual(result['disposition'],'convert')
        self.assertEqual(len(result['event']['target']['name']),512)
        self.assertLessEqual(len(result['event']['target']['id']),256)
        self.assertEqual(result['sidecar']['provenance']['target.display_name_snapshot'],'truncated_to_native_limit')
