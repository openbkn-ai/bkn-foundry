"""HTTP client helpers for outbound provider requests."""

import aiohttp


def client_session(*args, **kwargs):
    """Create a session that honors standard proxy environment variables."""
    kwargs.setdefault("trust_env", True)
    return aiohttp.ClientSession(*args, **kwargs)


class _ProxyAwareAiohttpModule:
    """Local aiohttp facade that leaves all non-session aiohttp APIs intact."""

    def __init__(self, module):
        self._module = module

    def ClientSession(self, *args, **kwargs):
        return client_session(*args, **kwargs)

    def __getattr__(self, name):
        return getattr(self._module, name)


def proxy_aware_aiohttp(module):
    """Return a module-local aiohttp facade whose sessions honor proxy env vars."""
    return _ProxyAwareAiohttpModule(module)
