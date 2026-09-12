"""Function A calling Function B through the platform.

Runs in the platform sandbox as a function tool. Nothing here names a URL, a token or a
session: bkn-osdk reads BKN_BASE_URL / BKN_TOKEN and the managed turn
(BKN_CONVERSATION_ID / BKN_INTERACTION_ID / BKN_PARENT_OPERATION_ID) from the sandbox
environment, so the inner call runs as the same caller and lands in the trace as a child
of this function's own execute_tool operation.

Requires the sandbox's bkn-osdk to include `kn.execute_tool` (bkn-sdk #100 or later).
"""

from bkn_osdk import kn


def handler(event: dict) -> dict:
    """event: kn_id, box_id, tool_id (the function to call), product, qty."""
    kn_id = event["kn_id"]
    answer = kn.execute_tool(kn_id, event["box_id"], event["tool_id"],
                             {"kn_id": kn_id, "product": event["product"], "qty": event.get("qty", 1)})

    # execute_tool hands back the tool's raw response: `body.exit_code` says whether B ran,
    # `body.result` is B's own return value. Read both — an HTTP 200 does not mean B succeeded.
    body = answer.get("body", answer) if isinstance(answer, dict) else {}
    if not isinstance(body, dict) or body.get("exit_code") not in (0, None):
        return {"ok": False, "reason": (body.get("stderr") or "")[-300:] if isinstance(body, dict) else str(answer)[:300]}
    result = body.get("result") or {}
    return {"ok": True, "kitting_ok": result.get("kitting_ok"), "gap_count": result.get("gap_count"),
            "gaps": (result.get("gaps") or [])[:5]}
