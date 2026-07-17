"""agent 定义导入导出（环境迁移/备份/内置 agent 预置分发）。

语义（Owner 拍板 2026-07-13）：导出=agent 定义+绑定 prompt 当前生效版本（不含
会话/任务/按人 override）；导入=保留原 id upsert（幂等，重复导入=同步更新），
同名不同 id 记 failed 不中断其他条目；prompt 内容有变发布新版本。
跨环境引用（toolbox box_id / 外部 mcp url 执行期才校验）不阻塞导入，agent
互调引用缺失记 warning。
"""

import time

from fastapi import APIRouter, Depends
from pydantic import ValidationError
from sqlalchemy.exc import IntegrityError
from sqlalchemy.ext.asyncio import AsyncSession

from app import dao
from app.auth import Account, get_account
from app.bootstrap import toolbox_sync
from app.db import get_session
from app.errors import bad_request, not_found
from app.models import (
    AgentExportItem,
    AgentSpec,
    ExportPackage,
    ExportRequest,
    ImportItemResult,
    ImportRequest,
    ImportResult,
    PromptExport,
)

router = APIRouter()


@router.post("/export", response_model=ExportPackage)
async def export_agents(
    req: ExportRequest,
    account: Account = Depends(get_account),
    session: AsyncSession = Depends(get_session),
):
    items: list[AgentExportItem] = []
    for agent_id in dict.fromkeys(req.agent_ids):
        agent = await dao.get_agent(session, agent_id)
        if not agent:
            raise not_found("agent", agent_id)
        prompt = None
        if agent.prompt_id:
            p = await dao.get_prompt(session, agent.prompt_id)
            if p:
                prompt = PromptExport(
                    prompt_id=p.prompt_id, name=p.name, content=p.content, vars_schema=p.vars_schema
                )
        try:
            # 出库数据回填写入模型会复验（union 工具引用/name 字符集）——存量脏数据
            # 在这里报单条明确错误，不落 500。
            spec = AgentSpec(**agent.model_dump(include=AgentSpec.model_fields.keys()))
        except (ValidationError, ValueError) as e:  # pydantic 校验错 + 显式 ValueError
            raise bad_request(
                "DirtyAgent", "agent 数据不符合当前校验规则，无法导出",
                f"agent {agent.agent_id}: {str(e)[:300]}",
                "先修复该 agent（PUT /agents/{id} 更新为合法配置）再导出。",
            )
        items.append(
            AgentExportItem(agent_id=agent.agent_id, spec=spec, prompt=prompt)
        )
    return ExportPackage(exported_at=int(time.time() * 1000), items=items)


@router.post("/import", response_model=ImportResult)
async def import_agents(
    req: ImportRequest,
    account: Account = Depends(get_account),
    session: AsyncSession = Depends(get_session),
):
    results: list[ImportItemResult] = []
    warnings: list[str] = []
    package_ids = {item.agent_id for item in req.package.items}

    for item in req.package.items:
        prompt_action = "none"
        # 先检后写：prompt 与 agent 分两次 commit，写到一半再发现冲突就回不去了
        # （rollback 撤不掉已提交的 prompt 新版本，线上 agent 会静默换词）
        conflict = await dao.check_import_conflict(
            session,
            item.agent_id,
            item.spec.name,
            item.prompt.prompt_id if item.prompt else None,
            item.prompt.name if item.prompt else None,
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
                    account.account_id,
                    commit=False,
                )
            agent, action = await dao.upsert_agent_with_id(
                session, item.agent_id, item.spec, account.account_id, commit=False
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

    if any(r.action in ("created", "updated") for r in results):
        toolbox_sync.schedule_resync()  # published agent 上架/更新到执行工厂
    return ImportResult(results=results, warnings=warnings)
