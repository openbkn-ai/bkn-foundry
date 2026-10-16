# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Narrow user execution surface; legacy service routes remain unchanged."""

import asyncio
import hashlib
import json
import uuid

import anyio
from fastapi import APIRouter, Depends, HTTPException
from fastapi.responses import StreamingResponse
from sqlalchemy.ext.asyncio import AsyncSession

from app import dao
from app.auth import Account
from app.commons.i18n import build_error_content
from app.config import config
from app.core.graph import stream_chat
from app.core.public_chat import PublicChat
from app.db import SessionLocal, get_session
from app.errors import err, not_found
from app.models import ChatRequest, ExecutionOut, ExecutionRequest
from app.public_auth import authorize_execution, get_public_account

router = APIRouter()
_PROTOCOL = "public-execution-v1"
# Like the existing chat/thread guard, this is deliberately single-replica.
_active: dict[str, asyncio.Task] = {}
_thread_claims: set[str] = set()


def _identity(account: Account) -> dict:
    return {"account_id": account.account_id, "account_type": account.account_type}


def _failure(code: str | None) -> dict[str, str]:
    return {
        "code": code
        if code and code.startswith("BknAgent.")
        else "BknAgent.Execution.Failed",
        "detail": build_error_content("BknAgent.Execution.Failed")["detail"],
    }


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
        or info.get("protocol") != _PROTOCOL
        or info.get("subject") != _identity(account)
    ):
        raise not_found("task", run_id)
    await authorize_execution(
        account, task.agent_id, info["request"]["context"]["kn_id"]
    )
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
            "description": "SSE: meta, token, tool_started, tool_finished, usage, result, terminal. "
            "Each event carries run_id and sequence. Successful result is persisted before "
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
    # Only the migrated built-in chat is available initially. EE tools/agent
    # admission belongs to the independent graph-explorer delivery.
    if req.agent_id != "studio_smart_qa":
        raise not_found("agent", req.agent_id)
    agent = await dao.get_agent(session, req.agent_id)
    if not agent or agent.status != "published" or agent.mode != "chat":
        raise not_found("agent", req.agent_id)
    await authorize_execution(account, agent.agent_id, req.context.kn_id)
    if any(ref.get("type") != "context_loader" for ref in agent.tools):
        raise err(409, "BknAgent.Execution.Unsupported")

    request_data = req.model_dump(mode="json")
    fingerprint = hashlib.sha256(
        json.dumps(request_data, sort_keys=True).encode()
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
        return _replay(previous, fingerprint, account)
    thread_id = req.thread_id or str(uuid.uuid4())
    if req.thread_id:
        row = await dao.get_thread_row(session, thread_id)
        origin = await dao.execution_thread_input(session, thread_id)
        if (
            not row
            or row.f_account_id != account.account_id
            or row.f_agent_id != agent.agent_id
            or not origin
            or origin.get("protocol") != _PROTOCOL
            or origin.get("subject") != _identity(account)
        ):
            raise not_found("thread", thread_id)
        if origin["request"]["context"] != request_data["context"]:
            raise err(409, "BknAgent.Execution.Conflict")
    if thread_id in _thread_claims:
        raise err(409, "BknAgent.Thread.Busy", thread_id=thread_id)
    _thread_claims.add(thread_id)
    try:
        task_input = {
            "protocol": _PROTOCOL,
            "subject": _identity(account),
            "request": request_data,
            "fingerprint": fingerprint,
            "agent_snapshot": agent.model_dump(mode="json"),
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
            )
    except BaseException:
        _thread_claims.discard(thread_id)
        raise
    public = PublicChat(run_id, req.context.kn_id)

    async def events():
        _active[run_id] = asyncio.current_task()
        status, output, failure = "failed", None, "cancelled"
        stream = None
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
                stream = await stream_chat(
                    execution_session,
                    agent,
                    ChatRequest(
                        agent_id=agent.agent_id,
                        thread_id=thread_id,
                        message=req.message,
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
                        failure = json.loads(event.split("data: ", 1)[1])["code"]
                    elif not event.startswith("event: meta\n"):
                        yield event
            if status == "succeeded":
                output = json.dumps(
                    {
                        "answer": "".join(public.answer),
                        "context": request_data["context"],
                        "prompt_version": public.prompt_version,
                        "usage": public.usage or None,
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
                    {"error": _failure(failure)}
                    if failure and failure != "cancelled"
                    else {}
                ),
            },
        )

    return _response(events())


def _response(events):
    return StreamingResponse(
        events,
        media_type="text/event-stream",
        headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"},
    )


def _replay(task, fingerprint, account):
    if (
        not task
        or (task.input or {}).get("protocol") != _PROTOCOL
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
            yield public.event("result", {"content": result.result})
        yield public.event(
            "terminal",
            {
                "status": result.status,
                **({"error": result.error} if result.error else {}),
            },
        )

    return _response(events())
