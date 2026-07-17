import time

import aiohttp

from app.config import config
from app.errors import err

# 键含调用方身份（见 load_skills 内注释），基数=账户数×技能数，值是整段技能正文；
# 必须有上界+过期清理，否则历史账户与技能正文常驻内存永不释放。
_CACHE_MAX = 1024
_cache: dict[tuple[str, str, str], tuple[float, str]] = {}


def _cache_put(key: tuple[str, str, str], now: float, content: str) -> None:
    if len(_cache) >= _CACHE_MAX:
        ttl = config.SKILL_CACHE_TTL_S
        for k in [k for k, (ts, _) in _cache.items() if now - ts >= ttl]:
            del _cache[k]
        while len(_cache) >= _CACHE_MAX:  # 清完过期仍满：逐出最旧，保证有界
            del _cache[min(_cache, key=lambda k: _cache[k][0])]
    _cache[key] = (now, content)


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
            # 缓存键含调用方身份：capabilities-lab 可能按账户授权私有技能，只按
            # capability_id 缓存会让 TTL 窗口内 B 拿到 A 的私有正文且不重发 B 身份。
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
        "\n\n# Skills\n\n以下技能已挂载。技能提到的附属文件不在上下文中，"
        "需要时调用 read_skill_file 工具按需读取。\n\n" + body
    )
