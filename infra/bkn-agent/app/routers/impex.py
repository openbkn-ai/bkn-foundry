"""agent 定义导入导出（环境迁移/备份/内置 agent 预置分发）。

语义（Owner 拍板 2026-07-13）：导出=agent 定义+绑定 prompt 当前生效版本（不含
会话/任务/按人 override）；导入=保留原 id upsert（幂等，重复导入=同步更新），
同名不同 id 记 failed 不中断其他条目；prompt 内容有变发布新版本。
跨环境引用（toolbox box_id / 外部 mcp url 执行期才校验）不阻塞导入，agent
互调引用缺失记 warning。
"""

import time

from fastapi import APIRouter, Depends
from sqlalchemy.ext.asyncio import AsyncSession

from app import dao
from app.auth import Account, get_account
from app.bootstrap import toolbox_sync
from app.db import get_session
from app.errors import not_found
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
        items.append(
            AgentExportItem(
                agent_id=agent.agent_id,
                spec=AgentSpec(**agent.model_dump(include=AgentSpec.model_fields.keys())),
                prompt=prompt,
            )
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
        try:
            if item.prompt:
                prompt_action = await dao.upsert_prompt_with_id(
                    session,
                    item.prompt.prompt_id,
                    item.prompt.name,
                    item.prompt.content,
                    item.prompt.vars_schema,
                    account.account_id,
                )
            agent, action = await dao.upsert_agent_with_id(
                session, item.agent_id, item.spec, account.account_id
            )
        except ValueError as e:
            await session.rollback()
            results.append(
                ImportItemResult(
                    agent_id=item.agent_id, name=item.spec.name, action="failed", error=str(e)
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
