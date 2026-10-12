import hashlib

import pytest

from app.utils.http_client import validate_provider_url
from app.utils.reshape_utils import _credential_digest, is_credential_digest
from app.utils.external_small_model_utils import InnerClient


def test_provider_url_validation_preserves_private_and_bare_hosts():
    assert validate_provider_url("http://10.0.0.8:8080/v1") == "http://10.0.0.8:8080/v1"
    assert validate_provider_url("model.internal:8080/v1") == "http://model.internal:8080/v1"


@pytest.mark.parametrize("value", ["", "ftp://model.example", "http://user:pass@model.example"])
def test_provider_url_validation_rejects_malformed_urls(value):
    with pytest.raises(ValueError):
        validate_provider_url(value)


def test_credential_digest_accepts_new_and_legacy_placeholders():
    secret = "provider-secret"
    assert _credential_digest(secret) != hashlib.md5(secret.encode(), usedforsecurity=False).hexdigest()
    assert is_credential_digest(_credential_digest(secret), secret)
    assert is_credential_digest(hashlib.md5(secret.encode(), usedforsecurity=False).hexdigest(), secret)


def test_adapter_client_does_not_require_provider_url():
    client = InnerClient("", "adapter-model", adapter=True, adapter_code="async def main(value): return value")
    assert client.url == "http://"
