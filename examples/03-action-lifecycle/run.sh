#!/usr/bin/env bash
# =============================================================================
# 03-action-lifecycle: Self-Evolving Knowledge Network
#
# Flow: CSV Files → Knowledge Network → Register Action Tool →
#       Define Action → Schedule → Execute → Audit Log
#
# No local MySQL client needed — CSV files are uploaded to the platform.
# =============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# ── CLI flags ─────────────────────────────────────────────────────────────────
usage() {
    cat <<EOF
Usage: $(basename "$0") [options]

Options:
  -h, --help   Show this help.

Environment variables are read from .env (see env.sample).
EOF
}
while [ $# -gt 0 ]; do
    case "$1" in
        -h|--help) usage; exit 0 ;;
        *) echo "Unknown argument: $1" >&2; usage >&2; exit 2 ;;
    esac
    shift
done

# ── Debug helpers ─────────────────────────────────────────────────────────────
DEBUG="${DEBUG:-0}"
debug() {
    if [ "$DEBUG" = "1" ] || [ "$DEBUG" = "true" ]; then
        echo "[debug] $*" >&2
    fi
}
debug_dump_json() {
    local label="$1" payload="$2"
    if [ "$DEBUG" = "1" ] || [ "$DEBUG" = "true" ]; then
        echo "[debug] --- ${label} ---" >&2
        echo "$payload" >&2
    fi
}

# ── Load config ───────────────────────────────────────────────────────────────
if [ -f "$SCRIPT_DIR/.env" ]; then
    # shellcheck disable=SC1091
    source "$SCRIPT_DIR/.env"
fi

DB_HOST="${DB_HOST:?Set DB_HOST in .env}"
DB_PORT="${DB_PORT:-3306}"
DB_NAME="${DB_NAME:?Set DB_NAME in .env}"
DB_USER="${DB_USER:?Set DB_USER in .env}"
DB_PASS="${DB_PASS:?Set DB_PASS in .env}"
DB_HOST_SEED="${DB_HOST_SEED:-$DB_HOST}"
export NODE_TLS_REJECT_UNAUTHORIZED="${NODE_TLS_REJECT_UNAUTHORIZED:-0}"

MYSQL_BIN="${MYSQL_BIN:-mysql}"
if ! command -v "$MYSQL_BIN" >/dev/null 2>&1; then
    for _p in "$(brew --prefix mysql-client 2>/dev/null)/bin/mysql" /opt/homebrew/opt/mysql-client/bin/mysql /usr/local/opt/mysql-client/bin/mysql; do
        [ -x "$_p" ] && { MYSQL_BIN="$_p"; break; }
    done
fi
command -v "$MYSQL_BIN" >/dev/null 2>&1 || { echo "Error: mysql client not found. Ubuntu: sudo apt install -y mysql-client"; exit 1; }
jget() { python3 -c "import json,sys;d=json.load(sys.stdin);print(d.get('$1','') if isinstance(d,dict) else '')" 2>/dev/null || true; }

TIMESTAMP=$(date +%s)
CAT_NAME="example_action_cat_${TIMESTAMP}"
KN_NAME="example_action_kn_${TIMESTAMP}"

# Track all created resources for cleanup
CAT_ID=""
KN_ID=""
BOX_ID=""
AT_ID=""
SCHED_ID=""

cleanup() {
    if [ "${KEEP_RESOURCES:-0}" = "1" ]; then
        echo ""; echo "=== Cleanup skipped (KEEP_RESOURCES=1) ==="
        echo "  Inspect: kweaver toolbox list | grep eval_action_toolbox ; kweaver bkn action-log list $KN_ID"
        return 0
    fi
    echo ""
    echo "=== Cleanup ==="
    [ -n "$SCHED_ID" ] && kweaver bkn action-schedule delete "$KN_ID" "$SCHED_ID" -y 2>/dev/null \
        && echo "  Deleted action-schedule $SCHED_ID" || true
    [ -n "$AT_ID" ] && kweaver bkn action-type delete "$KN_ID" "$AT_ID" -y 2>/dev/null \
        && echo "  Deleted action-type $AT_ID" || true
    [ -n "$BOX_ID" ] && kweaver toolbox delete "$BOX_ID" -y 2>/dev/null \
        && echo "  Deleted toolbox $BOX_ID" || true
    [ -n "$KN_ID" ] && kweaver bkn delete "$KN_ID" -y 2>/dev/null \
        && echo "  Deleted KN $KN_ID" || true
    [ -n "$CAT_ID" ] && kweaver vega catalog delete "$CAT_ID" -y 2>/dev/null \
        && echo "  Deleted catalog $CAT_ID" || true
    echo "Done."
}
trap cleanup EXIT

# ── Step 1: Load CSVs into MySQL + register Vega catalog ─────────────────────
echo "=== Step 1: Load CSVs into MySQL + register Vega catalog ==="
echo "  Files: $(ls "$SCRIPT_DIR/data/"*.csv | xargs -n1 basename | tr '\n' ' ')"
python3 - "$SCRIPT_DIR/data" <<'PY' | "$MYSQL_BIN" -h "$DB_HOST_SEED" -P "$DB_PORT" -u "$DB_USER" -p"$DB_PASS" "$DB_NAME"
import csv,glob,os,sys,re
ddir=sys.argv[1]
def coltype(vals):
    vals=[v for v in vals if v!='']
    if vals and all(re.fullmatch(r'-?\d+',v) for v in vals): return 'BIGINT'
    if vals and all(re.fullmatch(r'-?\d+(\.\d+)?',v) for v in vals): return 'DECIMAL(18,2)'
    return 'VARCHAR(512)'
def sqlval(v): return 'NULL' if v=='' else "'"+v.replace("\\","\\\\").replace("'","''")+"'"
for path in sorted(glob.glob(os.path.join(ddir,'*.csv'))):
    tbl=re.sub(r'[^0-9a-zA-Z_]','_',os.path.splitext(os.path.basename(path))[0])
    rows=list(csv.reader(open(path,encoding='utf-8'))); hdr=rows[0]; data=rows[1:]
    types=[coltype([r[i] for r in data if i<len(r)]) for i in range(len(hdr))]
    print(f"DROP TABLE IF EXISTS `{tbl}`;")
    print(f"CREATE TABLE `{tbl}` ({', '.join(f'`{c}` {t}' for c,t in zip(hdr,types))});")
    for r in data:
        r=(r+['']*len(hdr))[:len(hdr)]
        print(f"INSERT INTO `{tbl}` VALUES ({', '.join(sqlval(v) for v in r)});")
PY
CONN_CFG=$(python3 -c "import json,sys;print(json.dumps({'host':sys.argv[1],'port':int(sys.argv[2]),'username':sys.argv[3],'password':sys.argv[4],'databases':[sys.argv[5]]}))" "$DB_HOST" "$DB_PORT" "$DB_USER" "$DB_PASS" "$DB_NAME")
CAT_ID=$(kweaver vega catalog create --name "$CAT_NAME" --connector-type mysql --connector-config "$CONN_CFG" 2>/dev/null | jget id)
[ -z "$CAT_ID" ] && { echo "Error: catalog create failed (is DB_HOST reachable from vega-backend pods?)." >&2; exit 1; }
kweaver call "/api/vega-backend/v1/catalogs/${CAT_ID}/enable" -X POST >/dev/null 2>&1 || true
kweaver vega catalog discover "$CAT_ID" --wait >/dev/null 2>&1 || true
RES_JSON='{}'; RES_N=0
for _i in $(seq 1 20); do
    RES_JSON=$(kweaver vega resource list --catalog-id "$CAT_ID" --category table --json 2>/dev/null || echo '{}')
    RES_N=$(echo "$RES_JSON" | python3 -c "import json,sys;print(len(json.load(sys.stdin).get('entries',[])))" 2>/dev/null || echo 0)
    [ "${RES_N:-0}" -gt 0 ] && break; sleep 3
done
[ "${RES_N:-0}" -eq 0 ] && { echo "Error: no tables discovered for catalog $CAT_ID." >&2; exit 1; }
res_id() { echo "$RES_JSON" | python3 -c "import json,sys
for r in json.load(sys.stdin).get('entries',[]):
  if r.get('name','').endswith('$1'): print(r['id']);break"; }
echo "  Catalog: $CAT_ID ($RES_N table resources)"

# ── Step 2: Build Knowledge Network (object types bound to Vega resources) ───
echo ""
echo "=== Step 2: Build Knowledge Network ==="
KN_ID=$(kweaver bkn create --name "$KN_NAME" 2>/dev/null | jget kn_id)
[ -z "$KN_ID" ] && KN_ID=$(kweaver bkn create --name "${KN_NAME}_b" 2>/dev/null | jget id)
[ -z "$KN_ID" ] && { echo "Error: no kn_id in response" >&2; exit 1; }
echo "  Knowledge Network: $KN_ID"
INV_RES=$(res_id "eval_inventory"); PO_RES=$(res_id "eval_production_orders")
[ -n "$INV_RES" ] && kweaver bkn object-type create "$KN_ID" --name 物料库存 --resource-id "$INV_RES" \
    --primary-key material_code --display-key material_name >/dev/null 2>&1 && echo "  + 物料库存 (eval_inventory)"
[ -n "$PO_RES" ] && kweaver bkn object-type create "$KN_ID" --name 生产订单 --resource-id "$PO_RES" \
    --primary-key order_id --display-key product_name >/dev/null 2>&1 && echo "  + 生产订单 (eval_production_orders)"

# Material/inventory object type ID (needed for the action condition)
OT_LIST=$(kweaver bkn object-type list "$KN_ID")
MAT_OT_ID=$(echo "$OT_LIST" | python3 -c "
import sys, json
entries = json.load(sys.stdin)
if isinstance(entries, dict): entries = entries.get('entries', [])
for e in entries:
    if e.get('name')=='物料库存': print(e.get('id','')); break
")
[ -z "$MAT_OT_ID" ] && { echo "Error: could not find material/inventory object type" >&2; exit 1; }
echo "  Material object type: $MAT_OT_ID"

# ── Step 3: Register demo action toolbox ──────────────────────────────────────
echo ""
echo "=== Step 3: Register action tool backend ==="
BOX_JSON=$(kweaver toolbox create \
    --name "eval_action_toolbox_${TIMESTAMP}" \
    --service-url "http://bkn-backend-svc:13014" \
    --description "Demo toolbox for action-lifecycle example")
debug_dump_json "create toolbox" "$BOX_JSON"
BOX_ID=$(echo "$BOX_JSON" | python3 -c \
    "import sys,json; print(json.load(sys.stdin).get('box_id',''))")
[ -z "$BOX_ID" ] && { echo "Error: no box_id in toolbox response" >&2; exit 1; }
echo "  Toolbox: $BOX_ID"

# ── Step 4: Register a tool in the toolbox ────────────────────────────────────
echo ""
echo "=== Step 4: Register tool (OpenAPI spec) ==="

_OPENAPI_TMP=$(mktemp /tmp/eval_tool_openapi_XXXXXX.json)
trap 'rm -f "$_OPENAPI_TMP"; cleanup' EXIT
cat > "$_OPENAPI_TMP" <<'OPENAPI'
{
  "openapi": "3.0.0",
  "info": {"title": "采购单风险跟进", "version": "1.0.0"},
  "servers": [{"url": "http://bkn-backend-svc:13014"}],
  "paths": {
    "/health": {
      "get": {
        "summary": "采购单风险跟进",
        "operationId": "follow_up_at_risk_po",
        "responses": {"200": {"description": "ok"}}
      }
    }
  }
}
OPENAPI

_TOOL_RESP=$(kweaver tool upload --toolbox "$BOX_ID" "$_OPENAPI_TMP")
rm -f "$_OPENAPI_TMP"
debug_dump_json "create tool" "$_TOOL_RESP"

TOOL_ID=$(echo "$_TOOL_RESP" | python3 -c \
    "import sys,json; d=json.load(sys.stdin); ids=d.get('success_ids',[]); print(ids[0] if ids else '')")
[ -z "$TOOL_ID" ] && { echo "Error: no tool_id in response" >&2; exit 1; }
echo "  Tool: $TOOL_ID"

# ── Step 5: Publish toolbox and enable tool ───────────────────────────────────
echo ""
echo "=== Step 5: Publish toolbox ==="
kweaver toolbox publish "$BOX_ID" > /dev/null
kweaver tool enable --toolbox "$BOX_ID" "$TOOL_ID" > /dev/null
echo "  Toolbox published, tool enabled"

# ── Step 6: Define action type ────────────────────────────────────────────────
echo ""
echo "=== Step 6: Define action type — 物料库存告急补货预警 ==="

AT_BODY=$(python3 -c "
import json
body = {
    'name': '物料库存告急补货预警',
    'action_type': 'modify',
    'object_type_id': '$MAT_OT_ID',
    'tags': ['物料', '库存预警'],
    'comment': '发现库存告急的物料，自动触发补货预警',
    'action_source': {
        'type': 'tool',
        'box_id': '$BOX_ID',
        'tool_id': '$TOOL_ID'
    },
    'cond': {
        'object_type_id': '$MAT_OT_ID',
        'field': 'material_risk',
        'operation': '==',
        'value_from': 'const',
        'value': 'critical'
    }
}
print(json.dumps(body))
")

AT_JSON=$(kweaver bkn action-type create "$KN_ID" "$AT_BODY")
debug_dump_json "action-type create" "$AT_JSON"
AT_ID=$(echo "$AT_JSON" | python3 -c "
import sys,json
d=json.load(sys.stdin)
if isinstance(d, list): d = d[0]
print(d.get('id',''))
")
[ -z "$AT_ID" ] && { echo "Error: no action-type id in response" >&2; exit 1; }
echo "  Action type: $AT_ID"

# ── Step 7: Query — verify affected instances ─────────────────────────────────
echo ""
echo "=== Step 7: Query — which materials need replenishment? ==="
QUERY_JSON=$(kweaver bkn action-type query "$KN_ID" "$AT_ID" '{}' 2>/dev/null || true)
debug_dump_json "action-type query" "$QUERY_JSON"
AFFECTED=$(echo "$QUERY_JSON" | python3 -c \
    "import sys,json; d=json.load(sys.stdin); print(d.get('total_count') or len(d.get('actions') or d.get('entries') or []))" 2>/dev/null || echo "0")
echo "  Action would target $AFFECTED material instance(s) (candidate set for 物料库存告急补货预警)"
echo "  Note: the action condition (material_risk == critical) filters at data_view-backed query time."
echo "        On real-time resource-backed object types it is not applied here, so all rows are listed."

# Capture first identity for use in Step 10
FIRST_IDENTITY=$(echo "$QUERY_JSON" | python3 -c "
import sys,json
d=json.load(sys.stdin)
actions=d.get('actions',[])
if actions:
    print(json.dumps(actions[0].get('_instance_identity',{})))
else:
    print('{}')
" 2>/dev/null || echo "{}")

# ── Step 8: Create action schedule ────────────────────────────────────────────
echo ""
echo "=== Step 8: Schedule — every day at 08:00 ==="

SCHED_BODY=$(python3 -c "
import json
print(json.dumps({
    'name': '物料库存每日巡检',
    'cron_expression': '0 8 * * *',
    'action_type_id': '$AT_ID',
    '_instance_identities': [{}]
}))
")
SCHED_JSON=$(kweaver bkn action-schedule create "$KN_ID" "$SCHED_BODY")
debug_dump_json "action-schedule create" "$SCHED_JSON"
SCHED_ID=$(echo "$SCHED_JSON" | python3 -c \
    "import sys,json; d=json.load(sys.stdin); print(d.get('id',''))")
[ -z "$SCHED_ID" ] && { echo "Error: no schedule id in response" >&2; exit 1; }
echo "  Schedule: $SCHED_ID (cron: 0 8 * * *)"

# ── Step 9: Confirm schedule is active ────────────────────────────────────────
echo ""
echo "=== Step 9: Confirm schedule active ==="
SCHED_DETAIL=$(kweaver bkn action-schedule get "$KN_ID" "$SCHED_ID")
SCHED_STATUS=$(echo "$SCHED_DETAIL" | python3 -c \
    "import sys,json; print(json.load(sys.stdin).get('status','unknown'))")
if [ "$SCHED_STATUS" = "inactive" ]; then
    kweaver bkn action-schedule set-status "$KN_ID" "$SCHED_ID" active > /dev/null
    SCHED_STATUS="active"
fi
echo "  Schedule status: $SCHED_STATUS"
echo "  The knowledge network will scan materials every morning at 08:00."

# ── Step 10: Trigger action now ───────────────────────────────────────────────
echo ""
echo "=== Step 10: Trigger action — first run ==="
echo "  (In production this runs automatically at 08:00.)"
echo "  Executing now so you can see results immediately..."

EXEC_BODY=$(python3 -c "import json,sys; print(json.dumps({'_instance_identities': [json.loads('$FIRST_IDENTITY')]}))" 2>/dev/null \
    || python3 -c "import json; print(json.dumps({'_instance_identities': [{}]}))")
EXEC_JSON=$(kweaver bkn action-type execute "$KN_ID" "$AT_ID" "$EXEC_BODY" \
    --timeout 60 2>&1 || true)
debug_dump_json "action-type execute" "$EXEC_JSON"

EXEC_ID=$(echo "$EXEC_JSON" | python3 -c \
    "import sys,json; print(json.load(sys.stdin).get('id',''))" 2>/dev/null || true)
EXEC_STATUS=$(echo "$EXEC_JSON" | python3 -c \
    "import sys,json; print(json.load(sys.stdin).get('status','unknown'))" 2>/dev/null || true)
EXEC_TOTAL=$(echo "$EXEC_JSON" | python3 -c \
    "import sys,json; print(json.load(sys.stdin).get('total_count',0))" 2>/dev/null || true)

echo "  Execution ID : ${EXEC_ID:-n/a}"
echo "  Instances    : $EXEC_TOTAL"
echo "  Status       : $EXEC_STATUS"
echo "  (demo tool has no real backend — the execution record is what matters)"

# ── Step 11: Audit log ────────────────────────────────────────────────────────
echo ""
echo "=== Step 11: Audit log — what the knowledge network has done ==="
LOG_JSON=$(kweaver bkn action-log list "$KN_ID" 2>&1 || true)
debug_dump_json "action-log list" "$LOG_JSON"
LOG_COUNT=$(echo "$LOG_JSON" | python3 -c "
import sys,json
d=json.load(sys.stdin)
entries=d.get('entries') or []
print(max(d.get('total_count',0), len(entries)))
" 2>/dev/null || echo 0)
echo "  Total executions recorded: $LOG_COUNT"
echo ""
echo "$LOG_JSON" | python3 -c "
import sys, json, datetime
try:
    d = json.load(sys.stdin)
    entries = d.get('entries') or []
    for e in (entries[:5] if entries else []):
        ts = e.get('create_time', 0)
        t = datetime.datetime.fromtimestamp(ts/1000).strftime('%Y-%m-%d %H:%M') if ts else 'n/a'
        print(f'  [{t}]  {e.get(\"action_type_name\",\"?\")} → {e.get(\"status\",\"?\")}  (id: {e.get(\"id\",\"?\")[:8]}...)')
except Exception:
    pass
" 2>/dev/null || true

echo ""
echo "======================================================"
echo "  Knowledge network is now self-acting."
echo "  Every morning at 08:00 it will:"
echo "    1. Identify materials with critically low inventory (material_risk == critical)"
echo "    2. Trigger the replenishment alert"
echo "    3. Record the result in the audit log"
echo ""
echo "  Check the log anytime:"
echo "    kweaver bkn action-log list $KN_ID"
echo "======================================================"
