"""agent 定义导入的领域逻辑。

从 routers/impex.py 抽出，供两个入口共用：人工导入（POST /import）与启动预置
（bootstrap/preset_sync.py）。语义与取舍见 routers/impex.py 模块注释。

抽出的边界：这里只做「按条导入」，不含身份解析与 HTTP 形态；调用方决定用谁的
身份写、要不要查归属、要不要触发工厂重同步。
"""

import logging

from sqlalchemy.exc import IntegrityError
from sqlalchemy.ext.asyncio import AsyncSession

from app import dao
from app.bootstrap import toolbox_sync
from app.models import AgentExportItem, ImportItemResult, ImportResult

logger = logging.getLogger("bkn-agent.impex")


async def import_items(
    session: AsyncSession,
    items: list[AgentExportItem],
    account_id: str,
    *,
    enforce_ownership: bool = True,
    resync: bool = True,
) -> ImportResult:
    """按 agent_id upsert 一批 agent 定义（幂等，重复导入=同步更新）。

    enforce_ownership=False 用于平台预置：内置 agent 是平台资产不是某个用户的，
    存量环境里它可能由工程师手工建过（f_create_user 是某个真人），带归属检查会
    让每次升级都 failed。写入身份仍是 account_id，但不拿它去卡既有行的归属。

    resync=False 用于启动预置：紧随其后的 toolbox_sync 启动全量同步会带上这批
    agent，不必再排一次整包替换。
    """
    results: list[ImportItemResult] = []
    warnings: list[str] = []
    package_ids = {item.agent_id for item in items}

    for item in items:
        prompt_action = "none"
        # 先检后写：prompt 与 agent 分两次 commit，写到一半再发现冲突就回不去了
        # （rollback 撤不掉已提交的 prompt 新版本，线上 agent 会静默换词）
        conflict = await dao.check_import_conflict(
            session,
            item.agent_id,
            item.spec.name,
            item.prompt.prompt_id if item.prompt else None,
            item.prompt.name if item.prompt else None,
            account_id if enforce_ownership else "",
        )
        if conflict:
            results.append(
                ImportItemResult(
                    agent_id=item.agent_id, name=item.spec.name, action="failed", error=conflict
                )
            )
            continue
        try:
            # 单事务：prompt 与 agent 都 flush（commit=False），末尾一起 commit。
            # 任一步失败整体 rollback——不再出现「prompt 新版本已生效但 agent 导入失败」的半写。
            if item.prompt:
                prompt_action = await dao.upsert_prompt_with_id(
                    session,
                    item.prompt.prompt_id,
                    item.prompt.name,
                    item.prompt.content,
                    item.prompt.vars_schema,
                    account_id,
                    commit=False,
                )
            agent, action = await dao.upsert_agent_with_id(
                session, item.agent_id, item.spec, account_id, commit=False
            )
            await session.commit()
        except (ValueError, IntegrityError) as e:  # 并发占名：ValueError 预检 / IntegrityError 唯一键兜底
            await session.rollback()  # 未提交，prompt 也一并撤销，不留半写
            results.append(
                ImportItemResult(
                    agent_id=item.agent_id,
                    name=item.spec.name,
                    action="failed",
                    prompt_action="none",  # 整体回滚，prompt 未生效
                    error=str(e),
                )
            )
            continue
        results.append(
            ImportItemResult(
                agent_id=agent.agent_id, name=agent.name, action=action, prompt_action=prompt_action
            )
        )
        for ref in item.spec.tools:
            if ref.get("type") == "agent":
                ref_id = ref.get("agent_id") or ""
                if ref_id not in package_ids and not await dao.get_agent(session, ref_id):
                    warnings.append(
                        f"agent {item.spec.name} 引用的子 agent {ref_id} 不在包内也不在目标环境"
                    )

    if resync and any(r.action in ("created", "updated") for r in results):
        toolbox_sync.schedule_resync()  # published agent 上架/更新到执行工厂
    return ImportResult(results=results, warnings=warnings)
