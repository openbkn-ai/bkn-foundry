"""published agent → 算子工厂 toolbox 注册（ToolDependencySync 同款机制，#212）。

启动时全量 upsert（指数退避重试直到成功），agent 增删改后异步重同步。
operator-integration 的 upsert 对 box 是整包替换（先删箱内工具再插入），
因此取消发布/删除的 agent 会自动下架，无残留。

.adp 硬约束：每个工具条目 metadata.version 必须 == source_id，
否则导入静默损坏 metadata。build_package 内保证，测试守护。
"""
import asyncio
import json
import logging
import time
import uuid

import aiohttp

from app import dao
from app.config import config
from app.db import SessionLocal
from app.models import AgentOut

logger = logging.getLogger("bkn-agent.toolbox-sync")

# 平台管理员身份 + 公共业务域（与 agent-retrieval ToolDependencySync 一致）
_ADMIN_ACCOUNT_ID = "266c6a42-6131-4d62-8f39-853e7093701c"
_BUSINESS_DOMAIN = "bd_public"
_IMPORT_URI = "/internal-v1/impex/intcomp/import/toolbox"

# 确定性 ID：box 与工具 ID 由固定命名空间派生，重启/重灌不漂移
_NS = uuid.uuid5(uuid.NAMESPACE_URL, "openbkn://bkn-agent/toolbox")
BOX_ID = str(uuid.uuid5(_NS, "box"))

_ACCOUNT_HEADER_PARAMS = [
    {
        "name": "x-account-id",
        "in": "header",
        "description": "调用方账户 ID（/in 约定透传）",
        "required": False,
        "schema": {"type": "string"},
    },
    {
        "name": "x-account-type",
        "in": "header",
        "description": "账户类型：user(用户), app(应用)",
        "required": False,
        "schema": {"enum": ["user", "app"], "type": "string"},
    },
]

_INVOKE_BODY_SCHEMA = {
    "type": "object",
    "required": ["message"],
    "properties": {
        "message": {"type": "string", "description": "交给 agent 的任务描述/问题"},
        "prompt_vars": {"type": "object", "description": "提示词模板变量（可选）"},
    },
}

_INVOKE_RESPONSES = {
    "200": {
        "description": "任务终态。status=succeeded 时 output 为结果；failed 时看 failure_detail。",
        "content": {
            "application/json": {
                "schema": {
                    "type": "object",
                    "properties": {
                        "task_id": {"type": "string"},
                        "status": {"type": "string", "enum": ["succeeded", "failed"]},
                        "output": {"type": ["string", "null"]},
                        "failure_detail": {"type": ["string", "null"]},
                    },
                }
            }
        },
    }
}


def _tool_entry(agent: AgentOut, now_ns: int) -> dict:
    source_id = str(uuid.uuid5(_NS, f"source:{agent.agent_id}"))
    return {
        "tool_id": str(uuid.uuid5(_NS, f"tool:{agent.agent_id}")),
        "name": agent.name,
        "description": f"平台内置 agent「{agent.name}」一次性执行。",
        "status": "enabled",
        "metadata_type": "openapi",
        "use_rule": "",
        "global_parameters": {
            "name": "",
            "description": "",
            "required": False,
            "in": "",
            "type": "",
            "value": None,
        },
        "create_time": agent.create_time * 1_000_000,
        "update_time": agent.update_time * 1_000_000,
        "create_user": agent.create_user,
        "update_user": agent.update_user,
        "extend_info": None,
        "resource_object": "tool",
        "source_id": source_id,
        "source_type": "openapi",
        "script_type": "",
        "code": "",
        "dependencies": [],
        "dependencies_url": "",
        "metadata": {
            # 硬约束：version 必须 == source_id
            "version": source_id,
            "summary": agent.name,
            "description": f"平台内置 agent「{agent.name}」一次性执行。",
            "server_url": config.SELF_BASE_URL,
            # agent_id 固化进 path，不暴露给调用侧 LLM 决策
            "path": f"/api/bkn-agent/v1/invoke/{agent.agent_id}",
            "method": "POST",
            "create_time": now_ns,
            "update_time": now_ns,
            "create_user": _ADMIN_ACCOUNT_ID,
            "update_user": _ADMIN_ACCOUNT_ID,
            "api_spec": {
                "parameters": _ACCOUNT_HEADER_PARAMS,
                "request_body": {
                    "description": "",
                    "required": True,
                    "content": {"application/json": {"schema": _INVOKE_BODY_SCHEMA}},
                },
                "responses": _INVOKE_RESPONSES,
                "callbacks": {},
                "components": {},
                "external_docs": None,
                "security": None,
                "tags": None,
            },
        },
    }


def build_package(agents: list[AgentOut]) -> dict:
    now_ns = time.time_ns()
    return {
        "toolbox": {
            "configs": [
                {
                    "box_id": BOX_ID,
                    "box_name": "bkn-agent 内置 agent",
                    "box_desc": "bkn-agent published agent 自动注册；契约见 docs/api/bkn-agent.yaml",
                    "box_svc_url": config.SELF_BASE_URL,
                    "status": "published",
                    "category_type": "other_category",
                    "category_name": "其他",
                    "is_internal": True,
                    "source": "custom",
                    "metadata_type": "openapi",
                    "create_time": now_ns,
                    "update_time": now_ns,
                    "create_user": _ADMIN_ACCOUNT_ID,
                    "update_user": _ADMIN_ACCOUNT_ID,
                    "tools": [_tool_entry(a, now_ns) for a in agents],
                }
            ]
        }
    }


async def sync_once() -> int:
    """全量同步一次；返回注册的工具数。失败抛异常由调用方决定重试。"""
    async with SessionLocal() as session:
        agents = await dao.list_published_agents(session)
    package = build_package(agents)

    form = aiohttp.FormData()
    form.add_field("mode", "upsert")
    form.add_field(
        "data",
        json.dumps(package, ensure_ascii=False).encode("utf-8"),
        filename="bkn_agent_agents.adp",
        content_type="application/octet-stream",
    )
    headers = {
        "x-business-domain": _BUSINESS_DOMAIN,
        "x-account-id": _ADMIN_ACCOUNT_ID,
        "x-account-type": "user",
    }
    url = config.OPERATOR_INTEGRATION_BASE + _IMPORT_URI
    async with aiohttp.ClientSession(timeout=aiohttp.ClientTimeout(total=30)) as http:
        async with http.post(url, data=form, headers=headers) as resp:
            if not 200 <= resp.status < 300:
                body = await resp.text()
                raise RuntimeError(f"toolbox import failed: {resp.status} {body[:500]}")
    logger.info("[ToolboxSync] synced %d published agent(s) to operator-integration", len(agents))
    return len(agents)


async def _startup_loop() -> None:
    delay = max(config.TOOLBOX_SYNC_RETRY_INITIAL_S, 1)
    while True:
        try:
            await sync_once()
            return
        except Exception as e:
            logger.warning("[ToolboxSync] startup sync failed, retry in %ss: %s", delay, e)
            await asyncio.sleep(delay)
            delay = min(delay * 2, max(config.TOOLBOX_SYNC_RETRY_MAX_S, delay))


_background: set[asyncio.Task] = set()


def _spawn(coro) -> None:
    task = asyncio.create_task(coro)
    _background.add(task)
    task.add_done_callback(_background.discard)


def start_startup_sync() -> None:
    if not config.TOOLBOX_SYNC_ENABLED:
        logger.info("[ToolboxSync] disabled, skip startup sync")
        return
    _spawn(_startup_loop())


def schedule_resync() -> None:
    """agent 增删改后触发。失败仅告警——下次变更或重启兜底，不做重试风暴。"""
    if not config.TOOLBOX_SYNC_ENABLED:
        return

    async def _once() -> None:
        try:
            await sync_once()
        except Exception as e:
            logger.warning("[ToolboxSync] resync failed (will catch up on next change/restart): %s", e)

    _spawn(_once())
