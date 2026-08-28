"""HTTP client helpers for outbound model-provider requests."""

import aiohttp


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
