from fastapi import APIRouter, Depends, Response
from sqlalchemy.ext.asyncio import AsyncSession

from app import dao
from app.auth import Account, get_account
from app.core.graph import (
    claim_thread,
    delete_thread_checkpoints,
    read_thread_messages,
    release_thread,
)
from app.db import get_session
from app.errors import err, not_found
from app.models import ThreadOut
from app.routers import executions

router = APIRouter()


async def _owned_row(session: AsyncSession, thread_id: str, account: Account):
    row = await dao.get_thread_row(session, thread_id)
    if not row or row.f_account_id != account.account_id:  # A non-owner answers exactly like a missing thread.
        raise not_found("thread", thread_id)
    return row


@router.get("/threads/{thread_id}", response_model=ThreadOut)
async def get_thread(
    thread_id: str,
    account: Account = Depends(get_account),
    session: AsyncSession = Depends(get_session),
):
    row = await _owned_row(session, thread_id, account)
    return ThreadOut(
        thread_id=row.f_thread_id,
        agent_id=row.f_agent_id,
        create_time=row.f_create_time,
        update_time=row.f_update_time,
        messages=await read_thread_messages(thread_id),
    )


@router.delete("/threads/{thread_id}", status_code=204, response_class=Response)
async def delete_thread(
    thread_id: str,
    account: Account = Depends(get_account),
    session: AsyncSession = Depends(get_session),
):
    """Delete a thread's history: its checkpoints and its ownership row. Run records (tasks and executions that point at the thread) are kept: they have their own owner check and keep execution replay idempotent. Continuing the thread through /executions then answers 404; /chat with its id starts an empty thread, as with any unknown id. A thread with a turn in progress answers 409."""
    await _owned_row(session, thread_id, account)
    # A public execution claims the thread before its turn starts; both guards
    # are single-replica, like the rest of thread serialization.
    if thread_id in executions._thread_claims or not claim_thread(thread_id):
        raise err(409, "BknAgent.Thread.Busy", thread_id=thread_id)
    try:
        # Checkpoints first: if that fails the row remains and a retry can finish.
        await delete_thread_checkpoints(thread_id)
        await dao.delete_thread_row(session, thread_id)
    finally:
        release_thread(thread_id)
    return Response(status_code=204)
