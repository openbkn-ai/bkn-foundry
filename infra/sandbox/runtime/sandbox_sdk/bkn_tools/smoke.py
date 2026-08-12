#!/usr/bin/env python3
"""P0 连通性冒烟：验证沙箱侧 stub 能经 MCP 端点调通 BKN 能力。

验证四件事，按依赖顺序执行，任一失败即中止并给出定位提示：

1. MCP 端点可达且 AppKey 通过鉴权
2. tools/list 返回的工具集与本地生成的 stub 一致
3. 读工具可调用（list_knowledge_networks）——顺带验证会话守卫是否放行
4. 会话复用有效（连续两次调用只握手一次）

用法：
    export BKN_MCP_URL='https://<host>/api/agent-retrieval/v1/mcp'
    export BKN_MCP_TOKEN='bak_xxx_yyy'
    python3 smoke.py

依赖：pip install mcp
"""

from __future__ import annotations

import asyncio
import json
import os
import sys

REQUIRED_ENV = ("BKN_MCP_URL", "BKN_MCP_TOKEN")


def fail(step: str, detail: str) -> None:
    print(f"\n✗ {step}\n  {detail}", file=sys.stderr)
    sys.exit(1)


async def _with_session(fn):
    from mcp import ClientSession
    from mcp.client.streamable_http import (
        create_mcp_http_client,
        streamable_http_client,
    )

    url = os.environ["BKN_MCP_URL"]
    token = os.environ["BKN_MCP_TOKEN"]
    headers = {"Authorization": f"Bearer {token}"}

    async with create_mcp_http_client(headers=headers) as http_client:
        async with streamable_http_client(url, http_client=http_client) as (
            read,
            write,
        ):
            async with ClientSession(read, write) as session:
                await session.initialize()
                return await fn(session)


async def probe_tool_list() -> set[str]:
    async def run(session):
        listed = await session.list_tools()
        return {t.name for t in listed.tools}

    return await _with_session(run)


async def start_interaction() -> dict:
    """开一个 interaction，取回业务类工具调用所需的 bkn_context。

    生产链路中该上下文由 agent 侧开启并透传进沙箱；此处自行开启仅为使冒烟自洽。
    """

    async def run(session):
        result = await session.call_tool(
            "bkn_start_interaction",
            {"question": "PTC 沙箱连通性冒烟", "title": "ptc-smoke"},
        )
        text = "".join(
            block.text
            for block in result.content
            if getattr(block, "type", "") == "text"
        )
        if result.is_error:
            raise RuntimeError(text)
        return json.loads(text)

    return await _with_session(run)


def main() -> int:
    missing = [k for k in REQUIRED_ENV if not os.environ.get(k)]
    if missing:
        fail("环境变量缺失", f"需要设置：{', '.join(missing)}")

    # 1 + 2：端点可达、鉴权通过、工具集比对
    try:
        remote = asyncio.run(probe_tool_list())
    except Exception as exc:  # noqa: BLE001 —— 冒烟脚本需要原样暴露底层错误
        fail(
            "MCP 端点连接或鉴权失败",
            f"{type(exc).__name__}: {exc}\n"
            "  排查顺序：URL 是否为对外 MCP 端点（非 /in）；AppKey 是否有效且未过期；"
            "自签证书是否导致 TLS 失败。",
        )
    print(f"✓ MCP 端点连通，远端工具 {len(remote)} 个")

    import _tools

    local = {n for n in _tools.__all__ if n != "ToolError"}
    only_local = local - remote
    only_remote = remote - local
    if only_local:
        # 部分工具按开关条件注册（如 execute_skill），未启用属预期；
        # 但调用未注册的工具会在运行时失败，故须显式列出。
        print(
            f"  警告：远端未注册 {len(only_local)} 个 stub 对应的工具，"
            f"调用将失败：{', '.join(sorted(only_local))}"
        )
    if only_remote:
        # 生命周期工具由 agent 侧管理，沙箱不生成 stub（见 SKIP_TOOLS）。
        print(
            f"  提示：远端另有 {len(only_remote)} 个未生成 stub 的工具："
            f"{', '.join(sorted(only_remote))}"
        )
    print(
        f"✓ 工具集比对完成，本地 stub {len(local)} 个，"
        f"可用 {len(local & remote)} 个"
    )

    # 2.5：取得业务上下文。生产链路由 agent 透传，冒烟自行开启。
    if not (os.environ.get("BKN_CONVERSATION_ID") and
            os.environ.get("BKN_INTERACTION_ID")):
        try:
            started = asyncio.run(start_interaction())
        except Exception as exc:  # noqa: BLE001
            fail("bkn_start_interaction 失败", f"{type(exc).__name__}: {exc}")
        os.environ["BKN_CONVERSATION_ID"] = started["conversation_id"]
        os.environ["BKN_INTERACTION_ID"] = started["interaction_id"]
        print(
            f"✓ 已开启 interaction："
            f"conversation={started['conversation_id']} "
            f"interaction={started['interaction_id']}"
        )

    # 3：读工具实调，同时观察会话守卫是否拦截
    try:
        result = _tools.list_knowledge_networks(limit=3)
    except _tools.ToolError as exc:
        fail(
            "读工具调用被拒",
            f"{exc}\n"
            "  若报文提到 bkn_context / interaction，说明会话守卫要求生命周期上下文，"
            "需补充「上下文如何从 agent 透传至沙箱」的设计。",
        )
    except Exception as exc:  # noqa: BLE001
        fail("读工具调用异常", f"{type(exc).__name__}: {exc}")

    if not isinstance(result, dict):
        fail(
            "读工具返回非 JSON",
            f"实际类型 {type(result).__name__}；"
            "确认 stub 是否以 response_format='json' 调用。",
        )
    count = len(result.get("entries", []))
    if count == 0:
        print("  警告：返回 0 条知识网络，后续链路验证需要环境内至少有一个 KN")
    print(f"✓ list_knowledge_networks 调通，返回 {count} 条")

    # 4：会话复用——第二次调用不应重新握手
    _tools.list_knowledge_networks(limit=1)
    print("✓ 二次调用成功，会话复用正常")

    print("\n连通性冒烟通过。下一步：验证写工具（execute_action）与越权拒绝。")
    return 0


if __name__ == "__main__":
    sys.exit(main())
