import time

import aiohttp

from app.commons import locale
from app.config import config
from app.errors import err

# The key includes the caller identity (see the note inside load_skills), so the
# cardinality is accounts x skills and each value is a whole skill body. It needs
# both a bound and expiry sweeping, otherwise retired accounts and their skill
# bodies stay resident forever.
_CACHE_MAX = 1024
_cache: dict[tuple[str, str, str], tuple[float, str]] = {}


def _cache_put(key: tuple[str, str, str], now: float, content: str) -> None:
    if len(_cache) >= _CACHE_MAX:
        ttl = config.SKILL_CACHE_TTL_S
        for k in [k for k, (ts, _) in _cache.items() if now - ts >= ttl]:
            del _cache[k]
        while len(_cache) >= _CACHE_MAX:  # Still full after sweeping expiries: evict the oldest to stay bounded
            del _cache[min(_cache, key=lambda k: _cache[k][0])]
    _cache[key] = (now, content)


def normalize_skill_id(capability_id: str) -> str:
    """A capabilities-lab capability id looks like `skill:<uuid>` while the
    execution factory wants the bare uuid. Accept both, so a prefixed id stored
    by an older configuration keeps working."""
    return capability_id[6:] if capability_id.startswith("skill:") else capability_id


def strip_frontmatter(text: str) -> str:
    """The published surface returns the raw SKILL.md including YAML
    frontmatter, which is stripped before injection: name and description exist
    for marketplace search and are only noise inside a system prompt."""
    if not text.startswith("---"):
        return text.strip()
    end = text.find("\n---", 3)
    return text[end + 4 :].strip() if end != -1 else text.strip()


async def _fetch_skill_content(session: aiohttp.ClientSession, capability_id: str, headers: dict) -> str:
    """Fetch a published skill body from the execution factory, through the same
    door as toolbox; see core/toolbox.py.

    It uses the published surface rather than the management one: the management
    surface is the draft view, where editing a skill would change live agent
    behaviour immediately and make the publishing flow pointless. The cost is two
    hops, because the published surface returns a presigned URL rather than the
    body.
    """
    skill_id = normalize_skill_id(capability_id)
    url = f"{config.OPERATOR_INTEGRATION_BASE}/internal-v1/skills/{skill_id}/content"
    async with session.get(url, headers=headers) as resp:
        if resp.status == 404:
            raise err(400, "BknAgent.Skill.NotFound", skill_id=skill_id)
        if resp.status != 200:
            raise err(502, "BknAgent.Skill.FetchFailed", status=resp.status, skill_id=skill_id)
        meta = await resp.json()

    # Second hop: the presigned URL points at in-cluster MinIO and carries no
    # business identity, so the headers must not be forwarded.
    async with session.get(meta["url"]) as resp:
        if resp.status != 200:
            raise err(502, "BknAgent.Skill.ContentFetchFailed", status=resp.status, skill_id=skill_id)
        body = strip_frontmatter(await resp.text())

    # The attachment listing has to reach the context: it is how the model
    # learns which files it may read and which skill_id read_skill_file needs.
    # Without it progressive loading is invisible to the model, which is the
    # same as not having it.
    # The scaffolding text below is model-facing and intentionally stays as it
    # is. What language the agent thinks and answers in is an open platform
    # design question (#826, "Agent / LLM output language"); switching it would
    # change model behaviour, not the API contract.
    others = [f["rel_path"] for f in (meta.get("files") or []) if f["rel_path"].upper() != "SKILL.MD"]
    header = f"## 技能 {skill_id}\n"
    if others:
        header += (
            f"\n本技能有以下附属文件，未加载进上下文，需要时用 read_skill_file 工具读取"
            f"（skill_id={skill_id}，path 取下列之一）：\n"
            + "".join(f"- {p}\n" for p in others)
            + "\n"
        )
    return header + body


async def load_skills(capability_ids: list[str], account_id: str, account_type: str) -> str:
    """Fetch skill bodies and inject them into the system prompt. Loading is
    progressive: bulky resources stay out of the context and the model reads them
    on demand through the read_skill_file tool (see tools.py). Updates propagate
    within the cache TTL."""
    if not capability_ids:
        return ""
    headers = locale.internal_request_headers(
        {"x-account-id": account_id, "x-account-type": account_type}
    )
    parts: list[str] = []
    now = time.monotonic()
    async with aiohttp.ClientSession(timeout=aiohttp.ClientTimeout(total=30)) as session:
        for cid in dict.fromkeys(capability_ids):
            # The cache key includes the caller identity: the execution factory
            # may authorize private skills per account, and keying on skill_id
            # alone would hand B the private body fetched for A inside the TTL
            # window, without ever re-checking B's identity.
            key = (account_type, account_id, cid)
            cached = _cache.get(key)
            if cached and now - cached[0] < config.SKILL_CACHE_TTL_S:
                parts.append(cached[1])
                continue
            content = await _fetch_skill_content(session, cid, headers)
            _cache_put(key, now, content)
            parts.append(content)
    body = "\n\n---\n\n".join(parts)
    return (
        "\n\n# Skills\n\n以下技能已挂载，请按其中的说明执行。技能的附属文件不在上下文中，"
        "需要时调用 read_skill_file 工具按需读取。\n\n" + body
    )
