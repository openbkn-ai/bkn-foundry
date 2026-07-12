from fastapi import APIRouter, Depends
from sqlalchemy.ext.asyncio import AsyncSession

from app import dao
from app.auth import Account, get_account
from app.core.graph import read_thread_messages
from app.db import get_session
from app.errors import not_found
from app.models import ThreadOut

router = APIRouter()


@router.get("/threads/{thread_id}", response_model=ThreadOut)
async def get_thread(
    thread_id: str,
    account: Account = Depends(get_account),
    session: AsyncSession = Depends(get_session),
):
    row = await dao.get_thread_row(session, thread_id)
    if not row or row.f_account_id != account.account_id:  # 非 owner 与不存在同响应
        raise not_found("thread", thread_id)
    return ThreadOut(
        thread_id=row.f_thread_id,
        agent_id=row.f_agent_id,
        create_time=row.f_create_time,
        update_time=row.f_update_time,
        messages=await read_thread_messages(thread_id),
    )
