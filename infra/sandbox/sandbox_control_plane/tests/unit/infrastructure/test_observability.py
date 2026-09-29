import unittest
from unittest.mock import patch

from src.infrastructure import observability
from src.infrastructure.observability import build_http_span_attributes


class TestObservability(unittest.TestCase):
    def test_trace_setup_is_independent_from_log_switch(self):
        with (
            patch.dict(
                "os.environ",
                {"OTEL_LOGS_ENABLED": "false", "OTEL_TRACES_ENABLED": "true"},
            ),
            patch.object(observability, "_provider", None),
            patch.object(observability, "_trace_provider", None),
            patch.object(observability, "_tracer", None),
            patch.object(observability, "_setup_logs", create=True) as setup_logs,
            patch.object(observability, "_setup_traces", create=True) as setup_traces,
        ):
            observability.setup()

        setup_logs.assert_not_called()
        setup_traces.assert_called_once()

    def test_span_attributes_use_route_template(self):
        self.assertEqual(
            build_http_span_attributes("POST", "/api/v1/sessions/{session_id}", 201),
            {
                "http.request.method": "POST",
                "http.route": "/api/v1/sessions/{session_id}",
                "http.response.status_code": 201,
            },
        )
