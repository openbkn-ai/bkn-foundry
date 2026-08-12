"""代码化工具调用：把 BKN 能力从工具面收进沙箱。

模型不再逐个调用 21 个 BKN 工具，而是写一段 Python 交给沙箱执行；脚本内经
MCP 调用这些能力，中间结果留在沙箱，只有 stdout 回到对话上下文。

设计与实测数据见
docs/design/execution-factory/features/ptc-code-mode-tool-calling.md
"""

import logging
from pathlib import Path
from typing import Any

import aiohttp
from langchain_core.tools import tool

from app import observability
from app.config import config

logger = logging.getLogger("bkn-agent.run_code")

# codegen 产物：_tools.py 是 21 个能力的纯标准库 stub，tool_digest.md 是给模型
# 看的签名清单。两者同源，保证描述与实际可调函数一致。
_STUB_PATH = Path(__file__).with_name("_tools.py")
_DIGEST_PATH = Path(__file__).with_name("tool_digest.md")

# 沙箱按 AWS Lambda handler 规范执行：入口必须是单参数的 handler(event)。
# 模型写普通脚本，这一层由我们补——把这个约束推给模型只会增加首次失败率。
#
# stub 源码整段内联而非预装进镜像：它只依赖标准库，内联后镜像无需任何准备，
# codegen 一改立刻生效，也不存在镜像与 schema 版本错配。
_WRAPPER = """\
{stub}


def handler(event):
    _configure(event)
{body}
"""


def _load_digest() -> str:
    try:
        return _DIGEST_PATH.read_text(encoding="utf-8")
    except OSError:
        logger.warning("[run_code] 未找到 tool_digest.md，工具描述将缺少函数清单")
        return ""


def _build_description(available: set[str] | None = None) -> str:
    """装配 run_code 的描述。

    available 为空时给出完整清单；给出时按环境实际注册的工具裁剪——工具存在
    条件注册（如 execute_skill），schema 里有不代表远端可调，让模型看见一个
    调不通的函数只会换来一次难以归因的失败。
    """
    digest = _load_digest()
    if not available:
        return digest
    kept, dropped = [], []
    for line in digest.splitlines():
        name = line.split("(", 1)[0].strip()
        if name and name.isidentifier() and name not in available:
            dropped.append(name)
            continue
        kept.append(line)
    if dropped:
        logger.info("[run_code] 本环境未注册，已从描述中移除：%s", ", ".join(dropped))
    return "\n".join(kept)


def _indent(code: str) -> str:
    return "\n".join(("    " + line) if line.strip() else line
                     for line in code.splitlines())


def _wrap(code: str) -> str:
    return _WRAPPER.format(stub=_STUB_PATH.read_text(encoding="utf-8"),
                           body=_indent(code))


def build_run_code_tool(
    *,
    mcp_url: str,
    authorization: str,
    conversation_id: str,
    interaction_id: str,
    available_tools: set[str] | None = None,
):
    """构造 run_code 工具。

    凭据与会话上下文由本函数闭包持有，经 event 下发——不进函数签名，模型既
    看不到也填不了。走 event 而非 env_vars 是有意的：沙箱会话池化复用，env
    会把上一个调用方的值留在容器里，而 event 是每次调用的入参，不留残留。
    """
    description = _build_description(available_tools)

    @tool(description=description)
    async def run_code(code: str, timeout: int = 60) -> str:
        """在沙箱中执行 Python，只有 stdout 返回。"""
        payload = {
            "code": _wrap(code),
            "language": "python",
            "timeout": timeout,
            "event": {
                "mcp": mcp_url,
                "token": authorization.removeprefix("Bearer ").strip(),
                "bkn": {
                    "conversation_id": conversation_id,
                    "interaction_id": interaction_id,
                },
            },
        }
        headers = {
            "Authorization": authorization,
            "Content-Type": "application/json",
            **observability.outbound_headers(),
        }
        # 公开面 v1（而非 internal-v1）：该端点在沙箱内执行任意代码，
        # 公开面会校验调用方在算子类型上持有 execute 权限（见 #345）。
        url = f"{config.OPERATOR_INTEGRATION_BASE}/v1/function/execute"

        async with aiohttp.ClientSession() as session:
            async with session.post(
                url, json=payload, headers=headers
            ) as resp:
                if resp.status != 200:
                    detail = (await resp.text())[:500]
                    return f"沙箱执行请求失败（HTTP {resp.status}）：{detail}"
                result: dict[str, Any] = await resp.json()

        stdout = (result.get("stdout") or "").strip()
        if result.get("exit_code") == 0:
            return stdout or (
                "(脚本没有输出。只有 print 的内容会返回，记得打印结果。)"
            )

        # 失败时把 stderr 一并回传：traceback 里的服务端报文是模型自行修正
        # 参数的唯一依据，吞掉它就只能盲目重试。
        stderr = (
            result.get("stderr") or result.get("error_message") or ""
        ).strip()
        exit_code = result.get("exit_code")
        return "\n".join(
            part for part in
            [f"执行失败（exit_code={exit_code}）", stdout, stderr] if part
        )

    return run_code
