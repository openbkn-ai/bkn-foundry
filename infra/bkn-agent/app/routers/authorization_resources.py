from typing import Literal

from fastapi import APIRouter, Depends, Query
from sqlalchemy.ext.asyncio import AsyncSession

from app import dao
from app.db import get_session

router = APIRouter()


@router.get("/authorization-resources")
async def list_authorization_resources(
    resource_type: Literal["agent", "agent_tpl"],
    name: str = "",
    sort: Literal["name"] = "name",
    direction: Literal["asc", "desc"] = "asc",
    offset: int = Query(0, ge=0),
    limit: int = Query(20, ge=1, le=100),
    session: AsyncSession = Depends(get_session),
):
    """Internal, network-scoped resource directory for bkn-safe.

    The resource_type and sort parameters deliberately mirror the other
    authorization-resource providers even though both supported types share
    the same authoritative agent table.
    """
    del resource_type, sort
    rows, total = await dao.list_authorization_resources(session, name.strip(), direction, offset, limit)
    return {
        "entries": [{"id": agent_id, "name": agent_name} for agent_id, agent_name in rows],
        "total": total,
    }
