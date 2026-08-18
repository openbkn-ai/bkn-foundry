"""Import and export of agent definitions: environment migration, backup, and
distribution of preset built-in agents.

Semantics, decided by the Owner on 2026-07-13. Export covers the agent
definition plus the currently effective version of the bound prompt; it
excludes threads, tasks, and per-user overrides. Import upserts under the
original id, so it is idempotent and a repeated import is a sync. A name
collision under a different id is recorded as failed without interrupting the
other entries, and changed prompt content publishes a new version.
Cross-environment references (a toolbox box_id or an external mcp url, both
validated only at execution time) do not block an import, and a missing
agent-to-agent reference is recorded as a warning.
"""

import time

from fastapi import APIRouter, Depends
from pydantic import ValidationError
from sqlalchemy.exc import IntegrityError
from sqlalchemy.ext.asyncio import AsyncSession

from app import dao
from app.auth import Account, get_account
from app.commons.i18n import localized_message
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
            # Feeding stored rows back through the write model re-validates
            # them (the tool reference union, the name charset). Legacy dirty
            # data therefore produces one explicit per-item error here instead
            # of a 500.
            spec = AgentSpec(**agent.model_dump(include=AgentSpec.model_fields.keys()))
        except (ValidationError, ValueError) as e:  # pydantic validation errors plus explicit ValueError
            raise bad_request("BknAgent.Impex.DirtyAgent", agent_id=agent.agent_id, error=str(e)[:300])
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
        # Check before writing: prompt and agent used to commit separately, so
        # discovering a conflict halfway left no way back (a rollback cannot
        # undo an already committed new prompt version, and a live agent would
        # silently change its wording).
        conflict = await dao.check_import_conflict(
            session,
            item.agent_id,
            item.spec.name,
            item.prompt.prompt_id if item.prompt else None,
            item.prompt.name if item.prompt else None,
            account.account_id,
        )
        if conflict:
            results.append(
                ImportItemResult(
                    agent_id=item.agent_id, name=item.spec.name, action="failed", error=conflict
                )
            )
            continue
        try:
            # One transaction: prompt and agent both flush with commit=False
            # and commit together at the end. Any failure rolls the whole thing
            # back, so the half-written state where a new prompt version is live
            # but the agent import failed can no longer occur.
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
        except (ValueError, IntegrityError) as e:  # Concurrent name claim: ValueError from the pre-check, IntegrityError from the unique key
            await session.rollback()  # Nothing was committed, so the prompt is withdrawn too; no half-write remains
            results.append(
                ImportItemResult(
                    agent_id=item.agent_id,
                    name=item.spec.name,
                    action="failed",
                    prompt_action="none",  # Rolled back as a whole; the prompt never took effect
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
                        localized_message(
                            "BknAgent.Impex.MissingAgentReference",
                            agent_name=item.spec.name,
                            ref_id=ref_id,
                        )
                    )

    return ImportResult(results=results, warnings=warnings)
