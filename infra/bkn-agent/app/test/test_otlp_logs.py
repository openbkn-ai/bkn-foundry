import logging

from app import observability


def test_agent_logs_batch_to_otlp_independently_of_trace_switch(monkeypatch):
    from opentelemetry.sdk._logs.export import InMemoryLogExporter

    exporter = InMemoryLogExporter()
    monkeypatch.setattr(
        "opentelemetry.exporter.otlp.proto.http._log_exporter.OTLPLogExporter",
        lambda **_kwargs: exporter,
    )
    monkeypatch.setenv("OTEL_ENABLED", "false")
    monkeypatch.setenv("OTEL_LOGS_ENABLED", "true")
    app_logger = logging.getLogger("bkn-agent.telemetry")
    original_level = app_logger.level
    original_handlers = list(app_logger.handlers)
    monkeypatch.setattr(observability, "_log_provider", None, raising=False)
    try:
        provider = observability.setup_otlp_logging()
        assert provider is not None
        assert observability.setup_otlp_logging() is provider
        logging.getLogger("app.core.structured").warning("unreviewed raw diagnostic")
        observability.emit_http_request_log("GET", "/api/bkn-agent/v1/threads/{thread_id}", 200)
        assert provider.force_flush(timeout_millis=3000)
        records = exporter.get_finished_logs()
        assert len(records) == 1
        assert records[0].log_record.body == "http.request.completed"
        assert records[0].log_record.attributes["http.route"] == "/api/bkn-agent/v1/threads/{thread_id}"
        assert records[0].log_record.attributes["http.response.status_code"] == 200
        assert records[0].resource.attributes["service.name"] == "bkn-agent"
    finally:
        app_logger.handlers[:] = original_handlers
        app_logger.setLevel(original_level)
        provider = getattr(observability, "_log_provider", None)
        if provider is not None:
            provider.shutdown()
        observability._log_provider = None


def test_agent_logs_can_be_disabled_without_affecting_trace_setting(monkeypatch):
    monkeypatch.setenv("OTEL_ENABLED", "true")
    monkeypatch.setenv("OTEL_LOGS_ENABLED", "false")
    monkeypatch.setattr(observability, "_log_provider", None, raising=False)
    assert observability.setup_otlp_logging() is None
