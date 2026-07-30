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
from sqlalchemy.ext.asyncio import AsyncSession

from app import dao
from app.auth import Account, get_account
from app.core import impex as impex_core
from app.db import get_session
from app.errors import bad_request, not_found
from app.models import (
    AgentExportItem,
    AgentSpec,
    ExportPackage,
    ExportRequest,
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
    return await impex_core.import_items(session, req.package.items, account.account_id)
