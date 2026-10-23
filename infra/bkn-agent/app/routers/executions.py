# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Narrow user execution surface; legacy service routes remain unchanged."""

import asyncio
import hashlib
import logging
import json
import uuid

import anyio
from fastapi import APIRouter, Depends, HTTPException
from fastapi.responses import StreamingResponse
from pydantic import ValidationError
from sqlalchemy.ext.asyncio import AsyncSession

from app import dao
from app.auth import Account
from app.commons.i18n import build_error_content
from app.config import config
from app.core import builtin_tools
from app.core.graph import stream_chat
from app.core.public_chat import PublicChat
from app.db import SessionLocal, get_session
from app.errors import err, not_found
from app.models import ChatRequest, ExecutionOut, ExecutionRequest
from app.core.agent_settings import merge
from app.models import AgentSetting
from app.metric_design import (
    AGENT_ID as METRIC_AGENT_ID,
    PROTOCOL as METRIC_PROTOCOL,
    MetricDesignRequest,
    PROPOSAL_SCHEMA,
    validate_proposal,
)
from app.public_auth import (
    PUBLIC_AGENT_IDS,
    authorize_execution,
    authorize_network_setting,
    get_public_account,
)

logger = logging.getLogger("bkn-agent.executions")
router = APIRouter()
_PROTOCOL = "public-execution-v1"
# Like the existing chat/thread guard, this is deliberately single-replica.
_active: dict[str, asyncio.Task] = {}
_thread_claims: set[str] = set()


def _layer(row) -> AgentSetting | None:
    """A stored layer that no longer validates is skipped rather than failing runs."""
    if row is None:
        return None
    try:
        return AgentSetting.model_validate(row.f_content)
    except ValueError:
        return None


def _identity(account: Account) -> dict:
    return {"account_id": account.account_id, "account_type": account.account_type}


def _failure(code: str | None, upstream: dict | None = None) -> dict:
    """The terminal error: the code's own localized text, and the upstream's
    error when it gave one (live stream only; GET returns the code and text)."""
    code = code if code and code.startswith("BknAgent.") else "BknAgent.Execution.Failed"
    error = {"code": code, "detail": build_error_content(code)["detail"]}
    if upstream:
        error["upstream"] = upstream
    return error


def _decode(task) -> ExecutionOut:
    data = json.loads(task.output) if task.output else None
    status = task.status
    if task.status == "failed" and task.failure_detail == "cancelled":
        status = "cancelled"
    return ExecutionOut(
        run_id=task.task_id,
        agent_id=task.agent_id,
        thread_id=task.parent_thread_id,
        status=status,
        result=data if status == "succeeded" else None,
        error=_failure(task.failure_detail) if status == "failed" else None,
    )


async def _owned(session, run_id, account):
    task = await dao.get_task(session, run_id, account_id=account.account_id)
    info = (task.input or {}) if task else {}
    if (
        not task
        or info.get("protocol") not in {_PROTOCOL, METRIC_PROTOCOL}
        or info.get("subject") != _identity(account)
    ):
        raise not_found("task", run_id)
    kn_id = info["request"]["context"]["kn_id"]
    if info["protocol"] == METRIC_PROTOCOL:
        if task.agent_id != METRIC_AGENT_ID or not info.get("metric_request"):
            raise not_found("task", run_id)
        await authorize_network_setting(account, task.agent_id, kn_id, write=True)
    else:
        await authorize_execution(account, task.agent_id, kn_id)
    return task


@router.get("/executions/{run_id}", response_model=ExecutionOut)
async def get_execution(
    run_id: str,
    account: Account = Depends(get_public_account),
    session: AsyncSession = Depends(get_session),
):
    return _decode(await _owned(session, run_id, account))


@router.post("/executions/{run_id}/cancel", response_model=ExecutionOut)
async def cancel_execution(
    run_id: str,
    account: Account = Depends(get_public_account),
    session: AsyncSession = Depends(get_session),
):
    task = await _owned(session, run_id, account)
    driver = _active.get(run_id)
    if task.status in {"pending", "running"} and driver is not None:
        if not driver.cancelling():
            driver.cancel()
        # Cancellation is a request. GET observes the persisted terminal after
        # the generator's finally; do not claim cancellation before it happens.
    elif task.status == "pending":
        await dao.finish_execution(
            session, run_id, "failed", failure_detail="cancelled"
        )
        session.expire_all()
        task = await dao.get_task(session, run_id, account_id=account.account_id)
    return _decode(task)


@router.post(
    "/executions",
    response_class=StreamingResponse,
    responses={
        200: {
            "description": "SSE: meta, reasoning, token, tool_started, tool_finished, usage, result, terminal. "
            "reasoning carries the model's streamed thinking when it provides any; it is never "
            "part of the answer. "
            "Each event carries run_id and sequence. result.content.answer is the last "
            "assistant message; text streamed before tool calls is progress, not the answer. "
            "tool_finished of a built-in provider's tool may carry progress, {<result key>: what "
            "the run planned since the previous one}, to show before the run ends; the final "
            "result stays authoritative. "
            "Successful result is persisted before "
            "result/terminal; terminal.status is succeeded/failed/cancelled. Retries return "
            "meta and the existing terminal; an active retry returns 409. Disconnect cancels execution.",
            "content": {"text/event-stream": {"schema": {"type": "string"}}},
        }
    },
)
async def execute(
    req: ExecutionRequest,
    account: Account = Depends(get_public_account),
    session: AsyncSession = Depends(get_session),
):
    return await _execute_impl(req, account, session)


async def _execute_impl(
    req: ExecutionRequest,
    account: Account,
    session: AsyncSession,
    metric_request: MetricDesignRequest | None = None,
):
    # Only the built-in Agents admitted on the public surface run here.
    if req.agent_id not in PUBLIC_AGENT_IDS and not (
        metric_request and req.agent_id == METRIC_AGENT_ID
    ):
        raise not_found("agent", req.agent_id)
    agent = await dao.get_agent(session, req.agent_id)
    if not agent or agent.status != "published" or agent.mode != "chat":
        raise not_found("agent", req.agent_id)
    if metric_request:
        if account.account_type != "user":
            raise err(403, "BknAgent.Execution.Forbidden")
        await authorize_network_setting(account, agent.agent_id, req.context.kn_id, write=True)
    else:
        await authorize_execution(account, agent.agent_id, req.context.kn_id)
    if metric_request and (agent.tools or agent.skills):
        raise err(409, "BknAgent.Execution.Unsupported")
    if not metric_request and any(
        ref.get("type") not in ("context_loader", "builtin") for ref in agent.tools
    ):
        raise err(409, "BknAgent.Execution.Unsupported")
    try:
        provider = builtin_tools.provider_of(agent.tools)
    except ValueError:
        raise err(409, "BknAgent.Execution.Unsupported")
    provider_context = _provider_context(req, provider)
    network_row = await dao.get_network_setting(session, agent.agent_id, req.context.kn_id)
    user_row = await dao.get_user_setting(session, agent.agent_id, account.account_id)
    effective = merge(
        agent,
        _layer(network_row),
        network_row.f_version if network_row else 0,
        _layer(user_row),
        user_row.f_version if user_row else 0,
    )
    agent = effective.agent
    if metric_request and (agent.tools or agent.skills):
        raise err(409, "BknAgent.Execution.Unsupported")

    request_data = req.model_dump(mode="json")
    metric_data = metric_request.model_dump(mode="json") if metric_request else None
    protocol = METRIC_PROTOCOL if metric_request else _PROTOCOL
    fingerprint = hashlib.sha256(
        json.dumps(metric_data or request_data, sort_keys=True).encode()
    ).hexdigest()
    run_id = str(
        uuid.uuid5(
            uuid.NAMESPACE_URL,
            json.dumps(
                [account.account_type, account.account_id, req.client_request_id]
            ),
        )
    )
    previous = await dao.get_task(session, run_id, account_id=account.account_id)
    if previous:
        return _replay(previous, fingerprint, account, protocol)
    thread_id = req.thread_id or str(uuid.uuid4())
    if req.thread_id:
        row = await dao.get_thread_row(session, thread_id)
        origin = await dao.execution_thread_input(session, thread_id)
        if (
            not row
            or row.f_account_id != account.account_id
            or row.f_agent_id != agent.agent_id
            or not origin
            or origin.get("protocol") != protocol
            or origin.get("subject") != _identity(account)
        ):
            raise not_found("thread", thread_id)
        if origin["request"]["context"] != request_data["context"]:
            raise err(409, "BknAgent.Execution.Conflict")
        if metric_request and origin.get("metric_request", {}).get("scope") != metric_data["scope"]:
            raise err(409, "BknAgent.Execution.Conflict")
    if thread_id in _thread_claims:
        raise err(409, "BknAgent.Thread.Busy", thread_id=thread_id)
    _thread_claims.add(thread_id)
    try:
        agent_version = await dao.current_version(session, agent.agent_id)
        task_input = {
            "protocol": protocol,
            "subject": _identity(account),
            "request": request_data,
            **({"metric_request": metric_data} if metric_data else {}),
            "fingerprint": fingerprint,
            "agent_snapshot": agent.model_dump(mode="json"),
            "setting_versions": effective.versions,
            # The Agent version (model, tools, limits, Prompt version) this run used.
            "agent_version": agent_version.f_version if agent_version else None,
        }
        created = await dao.reserve_execution(
            session, run_id, agent.agent_id, task_input, account.account_id, thread_id
        )
        if not created:
            _thread_claims.discard(thread_id)
            return _replay(
                await dao.get_task(session, run_id, account_id=account.account_id),
                fingerprint,
                account,
                protocol,
            )
    except BaseException:
        _thread_claims.discard(thread_id)
        raise
    public = PublicChat(run_id, req.context.kn_id)
    public.instructions = effective.instructions
    public.allowed_tools = effective.allowed_tools
    public.allowed_builtin_tools = effective.allowed_builtin_tools
    run = (
        builtin_tools.Run(
            provider=provider,
            kn_id=req.context.kn_id,
            context=provider_context,
            account_id=account.account_id,
            account_type=account.account_type,
        )
        if provider
        else None
    )
    public.builtin_run = run

    async def events():
        _active[run_id] = asyncio.current_task()
        status, output, failure = "failed", None, "cancelled"
        upstream = None
        stream = None
        proposal = None
        fragment = None
        try:
            timeout_s = (
                agent.limits.timeout_s
                if agent.limits and agent.limits.timeout_s
                else config.DEFAULT_TIMEOUT_S
            )
            async with asyncio.timeout(timeout_s), SessionLocal() as execution_session:
                yield public.event(
                    "meta", {"thread_id": thread_id, "agent_id": agent.agent_id}
                )
                if not await dao.begin_execution(execution_session, run_id):
                    yield public.event("terminal", {"status": "cancelled"})
                    return
                # load_tools builds this run's built-in tools from it.
                builtin_tools.set_current(run)
                stream = await stream_chat(
                    execution_session,
                    agent,
                    ChatRequest(
                        agent_id=agent.agent_id,
                        thread_id=thread_id,
                        message=req.message,
                        response_format=PROPOSAL_SCHEMA if metric_request else None,
                    ),
                    account.account_id,
                    account.account_type,
                    public=public,
                )
                async for event in stream:
                    # Old graph emits done/error after its own cleanup; public
                    # protocol owns persistence and emits exactly one terminal.
                    if event.startswith("event: done\n"):
                        status, failure = "succeeded", None
                    elif event.startswith("event: error\n"):
                        status = "failed"
                        data = json.loads(event.split("data: ", 1)[1])
                        failure = data["code"]
                        upstream = data.get("upstream")
                    elif metric_request and event.startswith("event: structured\n"):
                        data = json.loads(event.split("data: ", 1)[1])
                        proposal = validate_proposal(data["content"], metric_request)
                    elif not event.startswith("event: meta\n"):
                        yield event
            if status == "succeeded":
                if metric_request and proposal is None:
                    raise ValueError("metric designer did not return a proposal")
                if run is not None:
                    try:
                        fragment = provider.result(run)
                    except ValueError:
                        logger.exception("public execution %s returned an invalid %s", run_id, provider.result_key)
                        raise err(500, "BknAgent.Execution.ResultInvalid")
                output = json.dumps(
                    {
                        "answer": public.final
                        if public.final is not None
                        else "".join(public.answer),
                        "context": request_data["context"],
                        "prompt_version": public.prompt_version,
                        "usage": public.usage or None,
                        **({"proposal": proposal} if metric_request else {}),
                        **({provider.result_key: fragment} if run is not None else {}),
                    },
                    ensure_ascii=False,
                )
        except asyncio.CancelledError:
            # An explicit cancel can still send its terminal. On disconnect the
            # transport may refuse it; the persisted state remains authoritative.
            status, failure = "failed", "cancelled"
        except TimeoutError:
            status = "failed"
            failure = "BknAgent.Chat.Timeout"
        except HTTPException as exc:
            status = "failed"
            failure = (
                exc.detail.get("code", "BknAgent.Execution.Failed")
                if isinstance(exc.detail, dict)
                else "BknAgent.Execution.Failed"
            )
        except Exception:
            logger.exception("public execution %s failed outside the chat loop", run_id)
            status = "failed"
            failure = "BknAgent.Execution.Failed"
        finally:
            # The tool loop is over. A later stop cannot interrupt the terminal
            # commit; its response may remain running until GET observes it.
            _active.pop(run_id, None)

            # A fresh session is required: the stream may have disconnected or
            # the original session may be in a failed transaction.
            async def finish():
                try:
                    try:
                        if stream is not None:
                            await stream.aclose()
                    finally:
                        async with SessionLocal() as cleanup_session:
                            await dao.finish_execution(
                                cleanup_session, run_id, status, output, failure
                            )
                finally:
                    _active.pop(run_id, None)
                    _thread_claims.discard(thread_id)

            # Starlette cancels the streaming scope on disconnect. Shield the
            # DB commit in this same task/context, without detached cleanup jobs.
            with anyio.CancelScope(shield=True):
                await finish()
        if status == "succeeded":
            if metric_request:
                yield public.event("proposal", {"content": proposal})
            yield public.event(
                "usage",
                {"available": bool(public.usage), "usage": public.usage or None},
            )
            yield public.event("result", {"content": json.loads(output)})
        yield public.event(
            "terminal",
            {
                "status": "succeeded"
                if status == "succeeded"
                else "cancelled"
                if failure == "cancelled"
                else "failed",
                **(
                    {"error": _failure(failure, upstream)}
                    if failure and failure != "cancelled"
                    else {}
                ),
            },
        )

    return _response(events())


def _provider_context(req: ExecutionRequest, provider):
    """The request context a builtin provider needs, validated; other Agents
    must not be sent provider context at all."""
    given = {
        name
        for name in type(req.context).model_fields
        if name != "kn_id" and getattr(req.context, name) is not None
    }
    expected = {provider.context_key} if provider else set()
    if given - expected:
        raise err(400, "BknAgent.Execution.ContextInvalid", field=", ".join(sorted(given - expected)))
    if provider is None:
        return None
    value = getattr(req.context, provider.context_key)
    if value is None:
        raise err(400, "BknAgent.Execution.ContextMissing", field=provider.context_key)
    try:
        return provider.context_model.model_validate(value)
    except ValidationError as exc:
        raise err(
            400,
            "BknAgent.Execution.ContextInvalid",
            field=f"{provider.context_key}: {exc.errors()[0].get('msg', '')}"[:300],
        )


def _response(events):
    return StreamingResponse(
        events,
        media_type="text/event-stream",
        headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"},
    )


def _replay(task, fingerprint, account, protocol=_PROTOCOL):
    if (
        not task
        or (task.input or {}).get("protocol") != protocol
        or task.input.get("subject") != _identity(account)
        or task.input.get("fingerprint") != fingerprint
    ):
        raise err(409, "BknAgent.Execution.Conflict")
    result = _decode(task)
    if result.status in {"pending", "running"}:
        raise err(409, "BknAgent.Execution.Conflict")
    public = PublicChat(task.task_id, task.input["request"]["context"]["kn_id"])

    async def events():
        yield public.event(
            "meta", {"thread_id": task.parent_thread_id, "agent_id": task.agent_id}
        )
        if result.result is not None:
            if protocol == METRIC_PROTOCOL:
                yield public.event(
                    "proposal", {"content": result.result.get("proposal")}
                )
            yield public.event("result", {"content": result.result})
        yield public.event(
            "terminal",
            {
                "status": result.status,
                **({"error": result.error} if result.error else {}),
            },
        )

    return _response(events())
