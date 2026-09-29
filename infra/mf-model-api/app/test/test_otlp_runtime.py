import unittest
from unittest.mock import patch

from app.core.config import _positive_int_env
from app.utils.observability.observability_log import build_http_log_attributes


class TestOTLPRuntime(unittest.TestCase):
    def test_http_log_attributes_are_a_complete_product_log_envelope(self):
        attributes = build_http_log_attributes("POST", "/api/mf-model-api/v1/models/{model_id}/chat", 200)
        self.assertEqual(attributes["schema_version"], "1.0.0")
        self.assertEqual(attributes["source_id"], "model-api")
        self.assertEqual(attributes["source_log_id"], attributes["log_id"])
        self.assertEqual(attributes["log_category"], "runtime.system")
        self.assertEqual(attributes["event_name"], "http.request.completed")
        self.assertEqual(attributes["outcome"], "success")
        self.assertEqual(
            attributes["safe_summary"],
            "POST /api/mf-model-api/v1/models/{model_id}/chat completed with HTTP 200",
        )

    def test_http_log_outcome_uses_response_status(self):
        self.assertEqual(build_http_log_attributes("GET", "/route", 401)["outcome"], "denied")
        self.assertEqual(build_http_log_attributes("GET", "/route", 500)["outcome"], "failure")

    def test_empty_batch_setting_uses_bounded_default(self):
        with patch.dict("os.environ", {"TRACE_MAX_EXPORT_BATCH_SIZE": ""}):
            self.assertEqual(_positive_int_env("TRACE_MAX_EXPORT_BATCH_SIZE", 512), 512)
