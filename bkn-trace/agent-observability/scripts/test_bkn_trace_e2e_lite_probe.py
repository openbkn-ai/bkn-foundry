import json
import unittest
from unittest import mock

import bkn_trace_e2e_lite_probe as probe


class BknTraceE2ELiteProbeTest(unittest.TestCase):
    def test_extracts_trace_id_from_traceparent(self):
        traceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

        self.assertEqual(
            probe.extract_trace_id(traceparent),
            "4bf92f3577b34da6a3ce929d0e0e4736",
        )

    def test_rejects_invalid_traceparent(self):
        with self.assertRaises(ValueError):
            probe.extract_trace_id("00-invalid-span-01")

    def test_classifies_vite_base_url_404(self):
        result = probe.classify_http_result(
            404,
            "The server is configured with a public base URL of /studio/",
        )

        self.assertEqual(result.status, "fail")
        self.assertEqual(result.reason, "studio_proxy_not_enabled")

    def test_classifies_backend_index_missing(self):
        body = json.dumps(
            {
                "error": {
                    "type": "index_not_found_exception",
                    "reason": "no such index [ss4o_traces-default-anyshare]",
                },
                "status": 404,
            }
        )

        result = probe.classify_http_result(404, body)

        self.assertEqual(result.status, "fail")
        self.assertEqual(result.reason, "trace_index_missing")

    def test_classifies_auth_failure(self):
        result = probe.classify_http_result(401, '{"message":"unauthorized"}')

        self.assertEqual(result.status, "fail")
        self.assertEqual(result.reason, "auth_failed")

    def test_classifies_bad_gateway(self):
        result = probe.classify_http_result(502, "Bad Gateway")

        self.assertEqual(result.status, "fail")
        self.assertEqual(result.reason, "bad_gateway")

    def test_classifies_tls_failure(self):
        result = probe.classify_http_result(
            0,
            "[SSL: UNEXPECTED_EOF_WHILE_READING] EOF occurred in violation of protocol",
        )

        self.assertEqual(result.status, "fail")
        self.assertEqual(result.reason, "tls_failed")

    def test_classifies_ok_json(self):
        result = probe.classify_http_result(200, '{"trace_id":"trace_001"}')

        self.assertEqual(result.status, "pass")
        self.assertEqual(result.reason, "ok")

    def test_probe_endpoint_maps_partial_to_warn(self):
        with mock.patch.object(
            probe,
            "request_json",
            return_value=(200, '{"partial":true,"partial_reason":["evidence_query_truncated"]}'),
        ):
            result = probe.probe_endpoint(
                "evidence_chain",
                "http://127.0.0.1:8080/evidence-chain",
                token="",
                timeout=1,
                insecure=False,
                retries=0,
                retry_delay=0,
            )

        self.assertEqual(result.status, "warn")
        self.assertEqual(result.reason, "partial")
        self.assertEqual(result.detail, "evidence_query_truncated")

    def test_probe_endpoint_distinguishes_empty_object_from_invalid_json(self):
        with mock.patch.object(probe, "request_json", return_value=(200, "{}")):
            empty_result = probe.probe_endpoint(
                "empty",
                "http://127.0.0.1:8080/empty",
                token="",
                timeout=1,
                insecure=False,
                retries=0,
                retry_delay=0,
            )
        with mock.patch.object(probe, "request_json", return_value=(200, "not-json")):
            invalid_result = probe.probe_endpoint(
                "invalid",
                "http://127.0.0.1:8080/invalid",
                token="",
                timeout=1,
                insecure=False,
                retries=0,
                retry_delay=0,
            )

        self.assertEqual(empty_result.status, "pass")
        self.assertEqual(invalid_result.status, "fail")
        self.assertEqual(invalid_result.reason, "invalid_json")

    def test_parse_args_supports_insecure_tls(self):
        args = probe.parse_args(["--insecure"])

        self.assertTrue(args.insecure)

    def test_builds_minimal_otlp_payload(self):
        payload = probe.build_otlp_trace_payload(
            trace_id="11111111111111111111111111111111",
            span_id="2222222222222222",
            request_id="req_e2e",
        )

        span = payload["resourceSpans"][0]["scopeSpans"][0]["spans"][0]
        self.assertEqual(span["traceId"], "11111111111111111111111111111111")
        self.assertEqual(span["spanId"], "2222222222222222")
        self.assertEqual(span["attributes"][0]["key"], "bkn.request.id")

    def test_builds_minimal_evidence_payload(self):
        payload = probe.build_evidence_payload(
            trace_id="11111111111111111111111111111111",
            span_id="2222222222222222",
            request_id="req_e2e",
        )

        self.assertEqual(payload["trace"]["trace_id"], "11111111111111111111111111111111")
        self.assertEqual(len(payload["events"]), 3)
        self.assertEqual(payload["events"][0]["event_type"], "claim.created")

    def test_run_probe_emits_before_querying(self):
        args = probe.parse_args(
            [
                "--base-url",
                "http://127.0.0.1:8080",
                "--trace-id",
                "11111111111111111111111111111111",
                "--request-id",
                "req_e2e",
                "--emit-otlp-url",
                "http://127.0.0.1:4318/v1/traces",
                "--ingest-evidence",
            ]
        )

        with mock.patch.object(probe, "post_json", return_value=(200, "{}")) as post_json:
            with mock.patch.object(probe, "request_json", return_value=(200, '{"trace_id":"ok"}')):
                results = probe.run_probe(args)

        self.assertEqual(post_json.call_count, 2)
        self.assertEqual(results[0].name, "emit_otlp_trace")
        self.assertEqual(results[1].name, "ingest_evidence")
        self.assertTrue(all(result.status == "pass" for result in results))

    def test_probe_endpoint_retries_eventual_trace_visibility(self):
        with mock.patch.object(
            probe,
            "request_json",
            side_effect=[
                (404, '{"code":"NOT_FOUND"}'),
                (200, '{"trace_id":"ok"}'),
            ],
        ):
            result = probe.probe_endpoint(
                "trace_graph",
                "http://127.0.0.1:8080/trace-graph",
                token="",
                timeout=1,
                insecure=False,
                retries=1,
                retry_delay=0,
            )

        self.assertEqual(result.status, "pass")


if __name__ == "__main__":
    unittest.main()
