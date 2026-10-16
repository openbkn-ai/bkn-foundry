"""Focused regression tests for sandbox installer gateway ownership."""

import unittest

from sandbox_gateway import base_url, discover, preserve


class SandboxGatewayTests(unittest.TestCase):
    def service(self, name="edge", namespace="gateway"):
        return {
            "metadata": {"name": name, "namespace": namespace},
            "spec": {
                "selector": {"app": name},
                "ports": [{"name": "http", "port": 8080, "targetPort": "web", "nodePort": 32080}],
            },
        }

    def test_rediscovery_does_not_preserve_previous_system_defaults(self):
        old = discover({"items": [self.service()]})
        self.assertEqual(preserve(old), {})
        new = discover({"items": [self.service("new", "other")]})
        self.assertEqual(new["controlPlane"]["gateway"]["baseURL"], "http://new.other.svc:8080")

    def test_preserve_only_operator_changes_to_generated_policy(self):
        old = discover({"items": [self.service()]})
        # Simulate Helm JSON serialization; these are independent objects after storage.
        import json

        old = json.loads(json.dumps(old))
        old["networkPolicy"]["gateway"]["port"] = 8081
        old["controlPlane"]["env"] = {"BKN_BASE_URL": "http://explicit:80"}
        kept = preserve(old)
        self.assertEqual(kept["networkPolicy"]["gateway"], {"port": 8081})
        self.assertEqual(kept["controlPlane"]["env"]["BKN_BASE_URL"], "http://explicit:80")

    def test_admission_service_is_not_an_http_gateway(self):
        service = self.service()
        service["spec"]["ports"][0]["name"] = "https-webhook"
        with self.assertRaises(ValueError):
            discover({"items": [service]})

    def test_invalid_origin_is_rejected_without_echoing_credentials(self):
        with self.assertRaisesRegex(ValueError, "without credentials or path"):
            base_url({"manifest": '  BKN_BASE_URL: "http://user:secret@gateway"\n'})

    def test_empty_legacy_values_are_supported(self):
        self.assertEqual(preserve(None), {})
        self.assertEqual(preserve({"controlPlane": {"env": {"BKN_BASE_URL": " "}}}), {})


if __name__ == "__main__":
    unittest.main()
