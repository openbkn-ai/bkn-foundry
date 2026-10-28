# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Dedicated Object Metric Designer execution entry point."""

from fastapi import APIRouter, Depends
from fastapi.responses import StreamingResponse
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth import Account
from app.db import get_session
from app.metric_design import AGENT_ID, MetricDesignRequest, model_message
from app.models import ExecutionRequest, ExecutionScope
from app.public_auth import get_public_account
from app.routers.executions import _execute_impl

router = APIRouter()


@router.post(
    "/metric-design/executions",
    response_class=StreamingResponse,
    responses={
        200: {
            "description": (
                "Scoped SSE execution. Emits meta, token, proposal, usage, result, and terminal. "
                "The proposal is validated and persisted before successful terminal; "
                "GET /executions/{run_id} can recover it. Invalid proposals return "
                "BknAgent.MetricDesign.ProposalInvalid with stage, field, and reason in "
                "terminal.error and the recovered execution error; run_id is included "
                "in the event and execution record."
            ),
            "content": {"text/event-stream": {"schema": {"type": "string"}}},
        }
    },
)
async def execute_metric_design(
    req: MetricDesignRequest,
    account: Account = Depends(get_public_account),
    session: AsyncSession = Depends(get_session),
):
    execution = ExecutionRequest(
        agent_id=AGENT_ID,
        client_request_id=req.client_request_id,
        thread_id=req.thread_id,
        message=model_message(req),
        context=ExecutionScope(kn_id=req.scope.kn_id),
    )
    return await _execute_impl(execution, account, session, metric_request=req)
