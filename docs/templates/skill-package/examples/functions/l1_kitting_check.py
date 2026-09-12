"""L1 kitting check: first-level BOM materials vs. available inventory.

Runs in the platform sandbox as a function tool. Reads the knowledge network
through bkn-osdk; the sandbox injects BKN_BASE_URL, BKN_TOKEN and the managed
turn (BKN_CONVERSATION_ID / BKN_INTERACTION_ID), so nothing is configured here.
"""

from collections import defaultdict

from bkn_osdk import kn

BOM = "supply_ontology_hand_bom"
INVENTORY = "supply_ontology_hand_inventory"
PRODUCTION_WAREHOUSES = [
    "苏州半成品仓", "苏州成品仓", "苏州电子原料仓", "苏州无人机原料仓",
    "苏州装配原料仓", "乌鲁木齐成品仓", "哈尔滨成品仓",
]


def _rows(kn_id: str, ot_id: str, **query) -> list[dict]:
    """Page through query_object_instance until the platform runs dry."""
    out, offset, page = [], 0, 500
    while True:
        answer = kn.query_object_instance(kn_id, ot_id, limit=page, offset=offset,
                                          response_format="json", **query)
        datas = (answer or {}).get("datas") or []
        out.extend(datas)
        if len(datas) < page:
            return out
        offset += page


def handler(event: dict) -> dict:
    kn_id = event["kn_id"]
    product = str(event["product"]).strip()
    qty = float(event.get("qty", 1))

    # 1. First-level main materials of the product (alt_priority 0 = main line).
    lines = _rows(kn_id, BOM,
                  filters=[{"field": "parent_material_code", "op": "==", "value": product},
                           {"field": "bom_level", "op": "==", "value": 1},
                           {"field": "alt_priority", "op": "==", "value": 0}],
                  properties=["material_code", "material_name", "usage_numerator", "usage_denominator"])
    if not lines:
        return {"product": product, "qty": qty, "kitting_ok": False,
                "reason": "no first-level BOM lines found", "gaps": []}
    def usage(ln: dict) -> float:
        num = float(ln.get("usage_numerator") or 0)
        den = float(ln.get("usage_denominator") or 1) or 1.0
        return num / den

    gross = {ln["material_code"]: usage(ln) * qty for ln in lines}
    names = {ln["material_code"]: ln.get("material_name", "") for ln in lines}

    # 2. Available inventory in the production warehouses, summed per material.
    stock = _rows(kn_id, INVENTORY,
                  filters=[{"field": "material_code", "op": "in", "value": list(gross)},
                           {"field": "warehouse", "op": "in", "value": PRODUCTION_WAREHOUSES},
                           {"field": "stock_status", "op": "==", "value": "可用"}],
                  properties=["material_code", "available_inventory_qty"])
    available = defaultdict(float)
    for row in stock:
        available[row["material_code"]] += float(row.get("available_inventory_qty") or 0)

    # 3. Net requirement per material; anything positive is a gap.
    gaps = []
    for code, need in gross.items():
        net = need - available[code]
        if net > 0:
            gaps.append({"material_code": code, "material_name": names[code],
                         "gross_requirement": need, "available_qty": available[code],
                         "net_requirement": net})
    gaps.sort(key=lambda g: g["net_requirement"], reverse=True)
    return {"product": product, "qty": qty, "l1_line_count": len(lines),
            "kitting_ok": not gaps, "gap_count": len(gaps), "gaps": gaps,
            "warehouse_scope": PRODUCTION_WAREHOUSES}
