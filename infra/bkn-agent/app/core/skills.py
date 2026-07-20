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


def normalize_skill_id(capability_id: str) -> str:
    """capabilities-lab 的 capability id 形如 `skill:<uuid>`，执行工厂要裸 uuid。
    两种都收，历史配置里存的带前缀 id 不至于失效。"""
    return capability_id[6:] if capability_id.startswith("skill:") else capability_id


def strip_frontmatter(text: str) -> str:
    """发布态返回原始 SKILL.md（含 YAML frontmatter），注入前剥掉：
    name/description 是给市场检索用的，进 system prompt 只是噪声。"""
    if not text.startswith("---"):
        return text.strip()
    end = text.find("\n---", 3)
    return text[end + 4 :].strip() if end != -1 else text.strip()


async def _fetch_skill_content(session: aiohttp.ClientSession, capability_id: str, headers: dict) -> str:
    """从执行工厂取已发布技能正文，与 toolbox 同门（见 core/toolbox.py）。

    走发布态而非管理态：管理态是草稿面，改技能会立刻改变线上 agent 行为，
    发布流程就失去意义。代价是两跳——发布态只回 presigned URL 不回正文。
    """
    skill_id = normalize_skill_id(capability_id)
    url = f"{config.OPERATOR_INTEGRATION_BASE}/internal-v1/skills/{skill_id}/content"
    async with session.get(url, headers=headers) as resp:
        if resp.status == 404:
            raise err(
                400,
                "Skill.NotFound",
                "技能不存在",
                f"技能 {skill_id} 在执行工厂中不存在或未发布",
                "请检查 skill_id，技能失效时不会被静默跳过。",
            )
        if resp.status != 200:
            raise err(
                502,
                "Skill.FetchFailed",
                "技能拉取失败",
                f"执行工厂返回 {resp.status}（技能 {skill_id}）",
            )
        meta = await resp.json()

    # 第二跳：presigned URL 指向集群内 MinIO，不带业务身份，不能透传 headers
    async with session.get(meta["url"]) as resp:
        if resp.status != 200:
            raise err(
                502,
                "Skill.FetchFailed",
                "技能正文拉取失败",
                f"对象存储返回 {resp.status}（技能 {skill_id}）",
            )
        body = strip_frontmatter(await resp.text())

    # 附属文件清单必须进上下文：模型据此知道有哪些文件可读、以及 read_skill_file
    # 要传的 skill_id —— 否则渐进式加载对模型不可见，等于没有。
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
    """拉取 SKILL 正文注入 system prompt。渐进式：大体积资源不进上下文，
    模型经 read_skill_file 工具按需读取（见 tools.py）。缓存 TTL 内热更新。"""
    if not capability_ids:
        return ""
    headers = {"x-account-id": account_id, "x-account-type": account_type}
    parts: list[str] = []
    now = time.monotonic()
    async with aiohttp.ClientSession(timeout=aiohttp.ClientTimeout(total=30)) as session:
        for cid in dict.fromkeys(capability_ids):
            # 缓存键含调用方身份：执行工厂可能按账户授权私有技能，只按
            # skill_id 缓存会让 TTL 窗口内 B 拿到 A 的私有正文且不重发 B 身份。
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
