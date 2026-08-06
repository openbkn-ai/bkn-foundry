"""Context Loader 的 MCP 面装载（ToolRef type=context_loader）。

与 toolbox 那条路的区别，以及为什么要有这个模块：

- Context Loader 的 MCP JSON-RPC 只挂在**公开路由**上，且该路由挂了
  `middlewareIntrospectVerify`，只认真实令牌（OAuth access token 或 bak_ AppKey）。
  它不吃 `/in` 那套 `x-account-id` 头部身份。所以这里必须带 Authorization。
- Context Loader 对所有业务工具有生命周期守卫：`bkn_context`（conversation_id +
  interaction_id）是**必填入参**，缺了返回 `conversation_required`。
- 这对 id **不能本地铸**。实测本地铸的 `int_...` 会被 `resource_not_disclosed`
  拒掉——MCP 侧的 owner tuple 从令牌推导，与自铸 id 对不上。正路是先调
  `bkn_start_interaction` 领一对真 id，再拿它调业务工具。领到的 id 与令牌身份
  绑定、不与 MCP 会话绑定，所以一轮对话领一次即可复用。

凭据只有一个来源：调用方透传的 Authorization（`auth.caller_token()`）。

刻意不留服务凭据兜底。用服务 AppKey 顶上去的话，Context Loader 看到的是该
AppKey 的签发人而不是真实调用者，per-user 授权当场塌缩成「签发人可见的范围」——
一个默认关闭但存在的开关，迟早会被人为了「让它跑起来」打开。没令牌就不挂工具，
并记一条 warning（不静默返回空工具集），失败留在看得见的地方。
"""
import logging
from contextvars import ContextVar
from typing import Any, Optional

from langchain_core.tools import StructuredTool
from langchain_mcp_adapters.client import MultiServerMCPClient

from app import observability
from app.auth import caller_token
from app.config import config

logger = logging.getLogger("bkn-agent.context-loader")

# 生命周期工具本身不是业务工具，不需要 bkn_context，也不该暴露给模型 ——
# 模型不该自己决定何时开一轮交互。
_LIFECYCLE_TOOLS = {"bkn_start_interaction", "bkn_finish_interaction"}

_CONNECTION_NAME = "context_loader"

# 本轮执行的会话。load_tools 装载时设置，runner / graph 取里面的真 id 交给
# evidence.begin_interaction，让证据链与 Context Loader 用同一对 id ——
# 否则一轮对话在 trace 里会裂成两条。
_current: ContextVar[Optional["ContextLoaderSession"]] = ContextVar(
    "bkn_agent_context_loader_session", default=None
)


def current_session() -> Optional["ContextLoaderSession"]:
    return _current.get()


def set_current(session: Optional["ContextLoaderSession"]):
    return _current.set(session)


def reset_current(token) -> None:
    _current.reset(token)


def wanted(tool_refs: list[dict]) -> bool:
    return any(ref.get("type") == "context_loader" for ref in tool_refs)


def _credential() -> Optional[str]:
    """当前调用方透传的 Authorization 头原文；没有则 None。"""
    return caller_token()


# 服务端据此把多轮归到同一个 managed conversation；不给就每轮新铸一个，
# 一段多轮对话会被存成 N 个各含一次交互的独立会话，且事后无法合并。
# 见 adp/context-loader/.../mcp/host_lifecycle_hints.go:22。
_HOST_CONVERSATION_KEY_HEADER = "X-OpenBKN-Host-Conversation-Key"


def _client(
    authorization: str, host_conversation_key: Optional[str] = None
) -> MultiServerMCPClient:
    headers = {"Authorization": authorization, **observability.outbound_headers()}
    if host_conversation_key:
        headers[_HOST_CONVERSATION_KEY_HEADER] = host_conversation_key
    return MultiServerMCPClient(
        {
            _CONNECTION_NAME: {
                "transport": "streamable_http",
                "url": config.CONTEXT_LOADER_MCP_URL,
                "headers": headers,
                "timeout": config.CONTEXT_LOADER_MCP_TIMEOUT_S,
            }
        }
    )


class ContextLoaderSession:
    """一轮 agent 执行期间对 Context Loader 的一次会话。

    持有真 id 与已装载的工具。`bkn_context` 由本类在调用时注入，不进模型可见的
    参数表——什么时候算一轮交互是运行时的事，不是模型该决策的事。
    """

    def __init__(self, conversation_id: str, interaction_id: str):
        self.conversation_id = conversation_id
        self.interaction_id = interaction_id
        # 收尾在正常路径上提前做一次、finally 再兜一次，必须幂等
        self.closed = False
        self._tools: list[Any] = []
        self._finish: Any = None

    @property
    def bkn_context(self) -> dict[str, str]:
        return {
            "conversation_id": self.conversation_id,
            "interaction_id": self.interaction_id,
        }

    def tools(self) -> list[Any]:
        return self._tools


def _strip_bkn_context(tool: Any) -> None:
    """把 bkn_context 从模型可见的入参表里摘掉（含 required 列表）。

    MCP 侧把它声明为 required，照原样透给模型会让模型去编造 id；实际值由
    ContextLoaderSession 在调用时注入。
    """
    schema = getattr(tool, "args_schema", None)
    if not isinstance(schema, dict):
        return
    props = schema.get("properties")
    if isinstance(props, dict):
        props.pop("bkn_context", None)
    required = schema.get("required")
    if isinstance(required, list) and "bkn_context" in required:
        schema["required"] = [r for r in required if r != "bkn_context"]


def _bind_context(tool: Any, session: ContextLoaderSession) -> Any:
    """包一层：调用时补上 bkn_context 再转给原工具。"""
    inner = getattr(tool, "coroutine", None)
    if inner is None:
        return tool

    async def call(**kwargs):
        kwargs["bkn_context"] = session.bkn_context
        return await inner(**kwargs)

    _strip_bkn_context(tool)
    return StructuredTool.from_function(
        coroutine=call,
        name=tool.name,
        description=tool.description,
        args_schema=tool.args_schema,
        metadata={**(getattr(tool, "metadata", None) or {}), "bkn_context_loader": True},
    )


async def open_session(
    question: str = "",
    *,
    agent_name: str | None = None,
    host_conversation_key: str | None = None,
) -> Optional[ContextLoaderSession]:
    """握手并装载工具。凭据缺失或握手失败返回 None（调用方决定是否致命）。

    question 是 bkn_start_interaction 的必填项，语义是「这一轮用户问了什么」，
    传本轮真实输入而不是空串——生命周期服务按它归档这一轮。

    host_conversation_key 是多轮连续性的锚：chat 传 thread_id，服务端据此把
    同一 thread 的各轮归到同一个 conversation。不传就每轮新铸一个，一段五轮
    对话会被存成五个互不相干的单轮会话，且是持久化数据、事后补不回来。
    """
    authorization = _credential()
    if not authorization:
        logger.warning(
            "[ContextLoader] 调用方未透传 Authorization，跳过 context_loader 工具装载。"
            "Context Loader 的 MCP 面只认真实令牌，调用 bkn-agent 时需带上最终用户的令牌"
        )
        return None

    client = _client(authorization, host_conversation_key)
    try:
        tools = await client.get_tools()
    except Exception as e:
        # ExceptionGroup 的 str() 只有 "unhandled errors in a TaskGroup"，真因全在
        # 子异常里。VM 上第一次排查就卡在这条日志上（真因是 401），所以摊开打。
        detail = "; ".join(f"{type(x).__name__}: {x}" for x in getattr(e, "exceptions", ()) or ()) or f"{type(e).__name__}: {e}"
        logger.warning("[ContextLoader] 连接 MCP 面失败，跳过工具装载：%s", detail)
        return None

    by_name = {getattr(t, "name", ""): t for t in tools}
    start = by_name.get("bkn_start_interaction")
    if start is None:
        logger.warning("[ContextLoader] MCP 面未暴露 bkn_start_interaction，跳过工具装载")
        return None

    try:
        args = {"question": question or "(未提供)"}
        if agent_name:
            args["agent_name"] = agent_name[:128]
        raw = await start.coroutine(**args)
    except Exception as e:
        logger.warning("[ContextLoader] bkn_start_interaction 失败，跳过工具装载：%s", e)
        return None

    ids = _parse_ids(raw)
    if not ids:
        logger.warning("[ContextLoader] bkn_start_interaction 未返回可用 id：%r", raw)
        return None

    session = ContextLoaderSession(ids[0], ids[1])
    session._finish = by_name.get("bkn_finish_interaction")
    session._tools = [
        _bind_context(t, session)
        for name, t in by_name.items()
        if name not in _LIFECYCLE_TOOLS
    ]
    logger.info(
        "[ContextLoader] 会话就绪：%d 个工具，interaction %s",
        len(session._tools),
        session.interaction_id,
    )
    return session


def _parse_ids(raw: Any) -> Optional[tuple[str, str]]:
    """从 bkn_start_interaction 的返回里取出 (conversation_id, interaction_id)。

    langchain-mcp-adapters 的实际返回形状（VM 实测）是个二元组：

        ([{"type": "text", "text": "<json>", "id": ...}],
         {"structured_content": {"conversation_id": ..., "interaction_id": ...}})

    早先这里只认 dict 和裸 JSON 串，撞上真实形状直接返回 None —— 握手其实成功了，
    id 也拿到了，却被判成「未返回可用 id」而跳过整个工具装载。这里按「先找
    structured_content，再退回文本块里的 JSON」两级解析，并保留 dict / 裸串两种
    简单形状。
    """
    for candidate in _id_candidates(raw):
        cid = candidate.get("conversation_id")
        iid = candidate.get("interaction_id")
        if isinstance(cid, str) and cid and isinstance(iid, str) and iid:
            return cid, iid
    return None


def _id_candidates(raw: Any):
    """把可能藏着 id 的字典逐个吐出来，从最可信的来源开始。"""
    import json

    seen: list[Any] = []

    def walk(node: Any, depth: int = 0) -> None:
        if depth > 4 or node is None:
            return
        if isinstance(node, str):
            try:
                walk(json.loads(node), depth + 1)
            except (TypeError, ValueError):
                pass
            return
        if isinstance(node, dict):
            # structured_content / structuredContent 是 MCP 的结构化输出，优先
            for key in ("structured_content", "structuredContent"):
                if isinstance(node.get(key), dict):
                    seen.append(node[key])
            seen.append(node)
            if isinstance(node.get("text"), str):
                walk(node["text"], depth + 1)
            return
        if isinstance(node, (list, tuple)):
            for item in node:
                walk(item, depth + 1)

    walk(raw)
    return [c for c in seen if isinstance(c, dict)]


# bkn_finish_interaction 的 outcome 枚举（取自 MCP 面 input_schema）。
# 早先这里传的是 "succeeded"——不在枚举里，连同把 id 塞进 bkn_context 而不是
# interaction_id，导致每一轮收尾都被 resource_not_disclosed 拒掉，交互全挂在
# active 上没人关。
_OUTCOMES = {"completed", "failed", "cancelled", "handed_off"}


async def close_session(
    session: Optional[ContextLoaderSession],
    *,
    outcome: str = "completed",
    answer: str | None = None,
    reason: str | None = None,
) -> None:
    """收尾 bkn_finish_interaction。失败只告警：一轮已经跑完，不该因为收尾失败
    把成功的结果翻成失败。"""
    if session is None or session._finish is None or session.closed:
        return
    session.closed = True
    if outcome not in _OUTCOMES:
        outcome = "completed"
    args: dict[str, Any] = {
        "interaction_id": session.interaction_id,
        "outcome": outcome,
    }
    if answer:
        # 归档的答案是持久化数据，截断必须留痕，否则存档与用户实际收到的
        # 内容会静默不一致。
        args["answer"] = (
            answer if len(answer) <= 4000 else answer[:4000] + "…[truncated by bkn-agent]"
        )
    if reason:
        args["reason"] = reason[:1000]
    try:
        await session._finish.coroutine(**args)
    except Exception as e:
        logger.warning(
            "[ContextLoader] bkn_finish_interaction 失败（interaction %s）：%s",
            session.interaction_id,
            e,
        )
