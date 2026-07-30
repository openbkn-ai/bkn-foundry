"""平台内置 agent 启动预置（YAML → t_agent / t_agent_prompt）。

背景：内置 agent 此前全靠人手写库，新装环境缺这几条就直接不可用，提示词也不在
git 里——无评审、无回滚基线、多环境必然漂移。这里把定义收进代码库，启动时幂等
upsert，与 toolbox_sync 的「启动全量同步」同一个位置、同一个思路。

复用 core/impex 的按条导入，两个参数与人工导入不同：

- enforce_ownership=False：内置 agent 是平台资产，不是某个人的。存量环境里它可能
  由工程师手工建过（f_create_user 是某个真人），带归属检查会让每次升级都 failed。
- resync=False：紧随其后的 toolbox_sync 启动全量同步会带上这批 agent，不必再排
  一次整包替换。

失败不阻断启动：内置 agent 缺失只让对应功能不可用，没必要连坐整个服务。但每条
未生效都打 ERROR——悄悄少一个 agent 而服务照常起来，没人会发现。
"""

import asyncio
import logging
from pathlib import Path

import yaml

from app import dao
from app.core import impex as impex_core
from app.db import SessionLocal
from app.models import AgentExportItem, ImportResult

logger = logging.getLogger("bkn-agent.preset")

PRESET_DIR = Path(__file__).resolve().parent / "presets"

# 预置写入身份。只落 f_create_user / f_update_user，没有任何读路径按这两列过滤，
# 因此不必像 toolbox_sync 那样借用真实 admin 账户（那边借是因为
# operator-integration 会校验账户真实存在，写本地表没这个约束）。
PRESET_ACCOUNT_ID = "system"

# 有限退避：预置失败最可能是 DB 尚未就绪。重试完仍不行就放弃，等下次重启。
_RETRY_DELAYS = (1, 2, 4)


def load_presets(directory: Path = PRESET_DIR) -> list[AgentExportItem]:
    """读预置目录下全部 YAML。文件形态 = /export 响应里的 items 列表（不含
    exported_at/format——它们是导出时刻的元信息，进 git 只会每次都变）。

    用 AgentExportItem 解析而不是自定义结构：name 字符集、工具引用形态这些校验
    直接继承写入模型，预置包写错在启动期就报明确错误，不会拖到工厂注册 400
    无限重试才暴露。
    """
    items: list[AgentExportItem] = []
    for path in sorted(directory.glob("*.yaml")):
        raw = yaml.safe_load(path.read_text(encoding="utf-8")) or []
        items.extend(AgentExportItem(**entry) for entry in raw)
    return items


async def _preserve_env_fields(session, item: AgentExportItem) -> AgentExportItem:
    """只有 model 归环境管：已存在的 agent 若显式绑过模型，保留库内现值。

    包里 model 恒为空（走系统默认大模型，不钉模型名）。若无条件覆盖，运维在界面上
    给某个内置 agent 换过模型，就会被每次重启悄悄改回去。

    limits 刻意不在此列——它是提示词的配套参数（max_output_tokens 8192 是为 catalog
    语义理解的长 JSON 定的，不是环境偏好）。保留库内值会让「升级调高输出上限」这类
    改动在存量环境永远不生效，而截断的表现是 JSON 断在半截、极难归因。
    """
    existing = await dao.get_agent(session, item.agent_id)
    if not existing or not existing.model:
        return item
    merged = item.model_copy(deep=True)
    merged.spec.model = existing.model
    return merged


def _log_result(result: ImportResult) -> None:
    for r in result.results:
        if r.action in ("created", "updated"):
            logger.info(
                "[Preset] 内置 agent %s 已%s（提示词：%s）",
                r.agent_id,
                "创建" if r.action == "created" else "更新",
                r.prompt_action,
            )
        else:
            logger.error("[Preset] 内置 agent %s 未生效：%s", r.agent_id, r.error)
    for w in result.warnings:
        logger.warning("[Preset] %s", w)


async def sync_presets() -> None:
    """启动调用。解析失败或 DB 始终不可用都只记日志，不往上抛。"""
    try:
        items = load_presets()
    except Exception as e:  # 预置包写错：属于发布物 bug，测试守着，运行时只报不炸
        logger.error("[Preset] 预置包解析失败，内置 agent 未预置：%s", e)
        return
    if not items:
        return

    last_error: Exception | None = None
    for attempt in range(len(_RETRY_DELAYS) + 1):
        try:
            async with SessionLocal() as session:
                merged = [await _preserve_env_fields(session, item) for item in items]
                result = await impex_core.import_items(
                    session,
                    merged,
                    PRESET_ACCOUNT_ID,
                    enforce_ownership=False,
                    resync=False,
                )
            _log_result(result)
            return
        except Exception as e:
            last_error = e
            if attempt < len(_RETRY_DELAYS):
                delay = _RETRY_DELAYS[attempt]
                logger.warning("[Preset] 预置失败，%ss 后重试：%s", delay, e)
                await asyncio.sleep(delay)
    logger.error("[Preset] 预置最终失败，内置 agent 可能缺失（不阻断启动）：%s", last_error)
