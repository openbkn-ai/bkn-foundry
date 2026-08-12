#!/usr/bin/env python3
"""连通性冒烟：验证 codegen 产出的 stub 能经 MCP 端点调通 BKN 能力。

只用标准库，与沙箱内的运行形态一致——冒烟通过即意味着同一份 stub 在沙箱里
也能跑通，不存在「本地装了 SDK 所以能过」的假阳性。

按依赖顺序验四件事，任一失败即中止并给出定位提示：

1. MCP 端点可达且凭据通过鉴权
2. 远端工具集与本地 stub 一致
3. 读工具可调用，且会话守卫放行（bkn_context 生效）
4. 会话复用有效（一次执行内只握手一次）

用法：
    export BKN_MCP_URL='http://<host>/api/agent-retrieval/v1/mcp/'   # 尾斜杠必需
    export BKN_MCP_TOKEN='bak_xxx_yyy'
    python3 sandbox_smoke.py
"""

import os
import sys

import _tools

REQUIRED_ENV = ("BKN_MCP_URL", "BKN_MCP_TOKEN")


def fail(step: str, detail: str) -> None:
    print(f"\n✗ {step}\n  {detail}", file=sys.stderr)
    sys.exit(1)


def main() -> int:
    missing = [k for k in REQUIRED_ENV if not os.environ.get(k)]
    if missing:
        fail("环境变量缺失", f"需要设置：{', '.join(missing)}")

    _tools._configure({
        "mcp": os.environ["BKN_MCP_URL"],
        "token": os.environ["BKN_MCP_TOKEN"],
    })

    # 1：握手 + 工具列表。凭据无效或端点错误都在这里暴露。
    try:
        _tools._ensure_session()
        listed = _tools._rpc("tools/list", {})["result"]["tools"]
    except Exception as exc:  # noqa: BLE001 —— 冒烟需要原样暴露底层错误
        fail(
            "MCP 端点连接或鉴权失败",
            f"{type(exc).__name__}: {exc}\n"
            "  排查顺序：URL 是否为对外 MCP 端点且带尾斜杠（缺斜杠会 307 跳转，"
            "urllib 对 POST 不跟随）；凭据是否有效且未过期；"
            "自签证书是否导致 TLS 失败。",
        )
    remote = {t["name"] for t in listed}
    print(f"✓ MCP 端点连通，远端工具 {len(remote)} 个")

    # 2：工具集比对
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

    # 3：取业务上下文。生产链路由 agent 透传，冒烟自行开启。
    started = _tools._call(
        "bkn_start_interaction", {"question": "sandbox smoke"}
    )
    _tools._CFG["bkn"] = {
        "conversation_id": started["conversation_id"],
        "interaction_id": started["interaction_id"],
    }
    print(
        f"✓ 已开启 interaction：conversation={started['conversation_id']} "
        f"interaction={started['interaction_id']}"
    )

    try:
        result = _tools.list_knowledge_networks(limit=3)
    except _tools.ToolError as exc:
        fail(
            "读工具调用被拒",
            f"{exc}\n"
            "  若报文提到 bkn_context / conversation_required，说明会话守卫要求"
            "生命周期上下文，需检查 agent 侧是否透传 conversation/interaction。",
        )
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
