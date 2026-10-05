"""HTTP client helpers for outbound model-provider requests."""

import aiohttp
from urllib.parse import urlsplit


def validate_provider_url(value):
    """Validate provider URL syntax without restricting private deployments."""
    if not isinstance(value, str) or not value.strip():
        raise ValueError("Model service URL must be a non-empty string")
    if "://" not in value:
        value = f"http://{value}"
    parsed = urlsplit(value)
    if parsed.scheme.lower() not in {"http", "https"}:
        raise ValueError("Model service URL must use http or https")
    if not parsed.hostname or parsed.username is not None or parsed.password is not None:
        raise ValueError("Model service URL must contain a valid host")
    return value


def client_session(*args, **kwargs):
    """Create a session that honors HTTP(S)_PROXY and NO_PROXY."""
    kwargs.setdefault("trust_env", True)
    return aiohttp.ClientSession(*args, **kwargs)


class _ProxyAwareAiohttpModule:
    """Module-local aiohttp facade that only changes ClientSession creation."""

    def __init__(self, module):
        self._module = module

    def ClientSession(self, *args, **kwargs):
        return client_session(*args, **kwargs)

    def __getattr__(self, name):
        return getattr(self._module, name)


def proxy_aware_aiohttp(module):
    return _ProxyAwareAiohttpModule(module)
