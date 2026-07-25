from app import observability


def test_openinference_redaction_defaults_hide_sensitive_payloads(monkeypatch):
    for key in observability.OPENINFERENCE_REDACTION_DEFAULTS:
        monkeypatch.delenv(key, raising=False)

    observability.configure_openinference_redaction()

    for key in (
        "OPENINFERENCE_HIDE_INPUTS",
        "OPENINFERENCE_HIDE_OUTPUTS",
        "OPENINFERENCE_HIDE_INPUT_MESSAGES",
        "OPENINFERENCE_HIDE_OUTPUT_MESSAGES",
        "OPENINFERENCE_HIDE_LLM_INVOCATION_PARAMETERS",
        "OPENINFERENCE_HIDE_LLM_TOOLS",
        "OPENINFERENCE_HIDE_PROMPTS",
    ):
        assert observability.os.environ[key] == "true"


def test_openinference_redaction_does_not_override_deployment_policy(monkeypatch):
    monkeypatch.setenv("OPENINFERENCE_HIDE_INPUTS", "false")

    observability.configure_openinference_redaction()

    assert observability.os.environ["OPENINFERENCE_HIDE_INPUTS"] == "false"
