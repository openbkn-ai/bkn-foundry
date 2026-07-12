import time

import aiohttp

from app.config import config
from app.errors import err

_cache: dict[str, tuple[float, str]] = {}


async def _fetch_skill_content(session: aiohttp.ClientSession, capability_id: str, headers: dict) -> str:
    url = f"{config.CAPABILITIES_LAB_BASE}/capabilities/{capability_id}/skill/content"
    async with session.get(url, headers=headers) as resp:
        if resp.status == 404:
            raise err(
                400,
                "Skill.NotFound",
                "技能不存在",
                f"capability {capability_id} 在 capabilities-lab 中不存在",
                "请检查 capability_id，技能失效时不会被静默跳过。",
            )
        if resp.status != 200:
            raise err(
                502,
                "Skill.FetchFailed",
                "技能拉取失败",
                f"capabilities-lab 返回 {resp.status}（capability {capability_id}）",
            )
        return await resp.text()


async def load_skills(capability_ids: list[str], account_id: str, account_type: str) -> str:
    """拉取 SKILL 正文注入 system prompt。渐进式：大体积资源不进上下文，
    模型经 read_skill_file 工具按需读取（见 tools.py）。缓存 TTL 内热更新。"""
    if not capability_ids:
        return ""
    headers = {"x-account-id": account_id, "x-account-type": account_type}
    parts: list[str] = []
    now = time.monotonic()
    async with aiohttp.ClientSession(timeout=aiohttp.ClientTimeout(total=30)) as session:
        for cid in dict.fromkeys(capability_ids):
            cached = _cache.get(cid)
            if cached and now - cached[0] < config.SKILL_CACHE_TTL_S:
                parts.append(cached[1])
                continue
            content = await _fetch_skill_content(session, cid, headers)
            _cache[cid] = (now, content)
            parts.append(content)
    body = "\n\n---\n\n".join(parts)
    return (
        "\n\n# Skills\n\n以下技能已挂载。技能提到的附属文件不在上下文中，"
        "需要时调用 read_skill_file 工具按需读取。\n\n" + body
    )
