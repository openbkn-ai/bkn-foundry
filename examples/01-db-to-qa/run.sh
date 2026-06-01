#!/usr/bin/env bash
# =============================================================================
# 01-db-to-qa: From Database to Intelligent Q&A
#
# End-to-end flow (Vega catalog model):
#   MySQL → Vega Catalog → Discover → Knowledge Network → Real-time Query → Agent Chat
#
# Note: this example uses the Vega catalog/connector model (vega-backend), NOT the
# legacy data-connection datasource flow. Object types bind to Vega *resource* IDs
# and are queried in real time — no `bkn build` needed.
# =============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# Self-signed ingress: kweaver (node) talks https to the platform.
export NODE_TLS_REJECT_UNAUTHORIZED="${NODE_TLS_REJECT_UNAUTHORIZED:-0}"

# ── CLI flags ────────────────────────────────────────────────────────────────
SEED_ONLY=0
usage() {
    cat <<EOF
Usage: $(basename "$0") [options]

Options:
  -s, --seed-only   Run Step 0 only: import seed.sql into MySQL, then exit.
  -h, --help        Show this help.

Environment variables are read from .env (see env.sample).
EOF
}
while [ $# -gt 0 ]; do
    case "$1" in
        -s|--seed-only) SEED_ONLY=1 ;;
        -h|--help) usage; exit 0 ;;
        *) echo "Unknown argument: $1" >&2; usage >&2; exit 2 ;;
    esac
    shift
done

DEBUG="${DEBUG:-0}"
debug() { if [ "$DEBUG" = "1" ] || [ "$DEBUG" = "true" ]; then echo "[debug] $*" >&2; fi; }

# ── Load config ──────────────────────────────────────────────────────────────
if [ -f "$SCRIPT_DIR/.env" ]; then
    # shellcheck disable=SC1091
    source "$SCRIPT_DIR/.env"
fi

DB_HOST="${DB_HOST:?Set DB_HOST in .env}"
DB_HOST_SEED="${DB_HOST_SEED:-$DB_HOST}"
DB_PORT="${DB_PORT:-3306}"
DB_NAME="${DB_NAME:?Set DB_NAME in .env}"
DB_USER="${DB_USER:?Set DB_USER in .env}"
DB_PASS="${DB_PASS:?Set DB_PASS in .env}"

# MySQL client binary (Step 0 seeds locally; only `kweaver` talks to the platform)
MYSQL_BIN="${MYSQL_BIN:-mysql}"
if ! command -v "$MYSQL_BIN" >/dev/null 2>&1; then
    if [ "$MYSQL_BIN" = "mysql" ]; then
        _brew_mysql="$(brew --prefix mysql-client 2>/dev/null)/bin/mysql"
        for _p in "$_brew_mysql" /opt/homebrew/opt/mysql-client/bin/mysql /usr/local/opt/mysql-client/bin/mysql; do
            [ -x "$_p" ] && { MYSQL_BIN="$_p"; break; }
        done
    fi
fi
if ! command -v "$MYSQL_BIN" >/dev/null 2>&1; then
    echo "Error: MySQL client not found (${MYSQL_BIN}). Step 0 runs mysql to import seed.sql."
    echo "  macOS: brew install mysql-client | Ubuntu: sudo apt install -y mysql-client"
    exit 1
fi

# ── JSON helper: read a top-level field from stdin ───────────────────────────
jget() { python3 -c "import json,sys;d=json.load(sys.stdin);print(d.get('$1','') if isinstance(d,dict) else '')" 2>/dev/null || true; }

TIMESTAMP=$(date +%s)
CAT_NAME="example_cat_${TIMESTAMP}"
KN_NAME="example_kn_${TIMESTAMP}"

CAT_ID=""
KN_ID=""

cleanup() {
    [ -z "$KN_ID" ] && [ -z "$CAT_ID" ] && return 0
    echo ""
    echo "=== Cleanup ==="
    [ -n "$KN_ID" ]  && kweaver bkn delete "$KN_ID" -y 2>/dev/null && echo "  Deleted KN $KN_ID"
    [ -n "$CAT_ID" ] && kweaver vega catalog delete "$CAT_ID" -y 2>/dev/null && echo "  Deleted catalog $CAT_ID"
    echo "Done."
}
trap cleanup EXIT

# ── Step 0: Seed the database ───────────────────────────────────────────────
echo "=== Step 0: Seed sample data into MySQL ==="
"$MYSQL_BIN" -h "$DB_HOST_SEED" -P "$DB_PORT" -u "$DB_USER" -p"$DB_PASS" "$DB_NAME" < "$SCRIPT_DIR/seed.sql"
echo "  Imported seed.sql → ${DB_NAME} (erp_material_bom, erp_purchase_order)"

if [ "$SEED_ONLY" = "1" ]; then
    echo ""
    echo "=== Seed-only mode: stopping after Step 0 ==="
    exit 0
fi

# ── Step 1: Register a Vega catalog + discover tables ────────────────────────
echo ""
echo "=== Step 1: Register Vega catalog (MySQL connector) ==="
echo "  Host: $DB_HOST:$DB_PORT  Database: $DB_NAME"

CONN_CFG=$(python3 -c "import json,sys;print(json.dumps({'host':sys.argv[1],'port':int(sys.argv[2]),'username':sys.argv[3],'password':sys.argv[4],'databases':[sys.argv[5]]}))" \
    "$DB_HOST" "$DB_PORT" "$DB_USER" "$DB_PASS" "$DB_NAME")
debug "Step 1: vega catalog create --connector-type mysql"
CAT_ID=$(kweaver vega catalog create --name "$CAT_NAME" --connector-type mysql --connector-config "$CONN_CFG" 2>/dev/null | jget id)
if [ -z "$CAT_ID" ]; then
    echo "Error: catalog create failed (check DB_HOST is reachable from vega-backend pods, and connector config)." >&2
    exit 1
fi
echo "  Catalog created: $CAT_ID"

# Catalogs are created disabled — enable before discovery.
kweaver call "/api/vega-backend/v1/catalogs/${CAT_ID}/enable" -X POST >/dev/null 2>&1 || true
echo "  Catalog enabled"

echo "  Discovering tables ..."
kweaver vega catalog discover "$CAT_ID" --wait >/dev/null 2>&1 || true

# Discovery is asynchronous — poll the resource list until tables appear (~60s max).
RES_JSON='{}'; RES_COUNT=0
for _i in $(seq 1 20); do
    RES_JSON=$(kweaver vega resource list --catalog-id "$CAT_ID" --category table --json 2>/dev/null || echo '{}')
    RES_COUNT=$(echo "$RES_JSON" | python3 -c "import json,sys;d=json.load(sys.stdin);print(len(d.get('entries',[])))" 2>/dev/null || echo 0)
    [ "${RES_COUNT:-0}" -gt 0 ] && break
    sleep 3
done
if [ "${RES_COUNT:-0}" -eq 0 ]; then
    echo "Error: no table resources discovered for catalog $CAT_ID (after polling)." >&2
    exit 1
fi
echo "  Discovered ${RES_COUNT} table resource(s):"
echo "$RES_JSON" | python3 -c "import json,sys
for r in json.load(sys.stdin).get('entries',[]): print('    -', r.get('name'), '('+r.get('id','')+')')"

# Resolve resource IDs by table name suffix
res_id() { echo "$RES_JSON" | python3 -c "import json,sys
for r in json.load(sys.stdin).get('entries',[]):
  if r.get('name','').endswith('$1'): print(r['id']); break"; }
BOM_RES=$(res_id "erp_material_bom")
PO_RES=$(res_id "erp_purchase_order")

# ── Step 2: Create Knowledge Network + object types (resource binding) ───────
echo ""
echo "=== Step 2: Create Knowledge Network ==="
KN_ID=$(kweaver bkn create --name "$KN_NAME" 2>/dev/null | jget kn_id)
[ -z "$KN_ID" ] && KN_ID=$(kweaver bkn create --name "${KN_NAME}_b" 2>/dev/null | jget id)
if [ -z "$KN_ID" ]; then
    echo "Error: could not create knowledge network." >&2; exit 1
fi
echo "  Knowledge Network created: $KN_ID"

# Object types bound to Vega resources (real-time, no bkn build).
# PK/DK per the seed schema (see README).
if [ -n "$BOM_RES" ]; then
    kweaver bkn object-type create "$KN_ID" --name 物料BOM --resource-id "$BOM_RES" \
        --primary-key material_code --display-key material_name >/dev/null 2>&1 \
        && echo "  + Object type 物料BOM  → $BOM_RES"
fi
if [ -n "$PO_RES" ]; then
    kweaver bkn object-type create "$KN_ID" --name 采购订单 --resource-id "$PO_RES" \
        --primary-key id --display-key material_name >/dev/null 2>&1 \
        && echo "  + Object type 采购订单 → $PO_RES"
fi

# ── Step 3: Explore schema ──────────────────────────────────────────────────
echo ""
echo "=== Step 3: Explore Knowledge Network schema ==="
OT_LIST=$(kweaver bkn object-type list "$KN_ID" 2>/dev/null || echo '{}')
echo "$OT_LIST" | python3 -c "import json,sys
d=json.load(sys.stdin); es=d.get('entries',d if isinstance(d,list) else [])
print(f'  Object types ({len(es)}):')
for e in es: print('    -', e.get('name','?'), e.get('id',''))"
FIRST_OT=$(echo "$OT_LIST" | python3 -c "import json,sys
d=json.load(sys.stdin); es=d.get('entries',d if isinstance(d,list) else [])
print(es[0].get('id','') if es else '')")

# ── Step 4: Query real data through the knowledge network ────────────────────
echo ""
echo "=== Step 4: Query data (real-time via Vega) ==="
if [ -n "$FIRST_OT" ]; then
    echo "  Sample rows from first object type:"
    kweaver bkn object-type query "$KN_ID" "$FIRST_OT" '{"limit":3}' 2>/dev/null | python3 -c "import json,sys
d=json.load(sys.stdin); rows=d.get('datas',d.get('entries',[]))
print(f'    {len(rows)} row(s)')
for r in rows[:3]:
    vals=[str(v) for v in list(r.values())[:4] if not str(v).startswith('_')]
    print('    -', ' | '.join(vals[:4]))" 2>/dev/null || echo "    (query returned no rows)"
fi

echo "  Semantic search: \"物料\""
kweaver bkn search "$KN_ID" "物料" 2>/dev/null | python3 -c "import json,sys
try:
  d=json.load(sys.stdin); cs=d.get('concepts',d.get('entries',[]))
  print(f'    {len(cs)} concept(s) matched')
except Exception: print('    (no search index for real-time resources)')" 2>/dev/null || true

# ── Step 5: Chat with Agent ─────────────────────────────────────────────────
echo ""
echo "=== Step 5: Chat with Agent ==="
if [ -z "${AGENT_ID:-}" ]; then
    AGENT_ID=$(kweaver agent list --limit 1 2>/dev/null | python3 -c "import json,sys
d=json.load(sys.stdin); a=d if isinstance(d,list) else d.get('entries',[])
print(a[0].get('id','') if a else '')" 2>/dev/null || true)
fi
if [ -z "${AGENT_ID:-}" ]; then
    echo "  No agent available. Set AGENT_ID in .env or create one. Skipping chat."
else
    echo "  Agent: $AGENT_ID"
    QUESTION="这个供应链数据库里有哪些核心业务表？物料和采购订单之间是什么关系？"
    echo "  Question: $QUESTION"
    echo "  Agent response:"
    kweaver agent chat "$AGENT_ID" -m "$QUESTION" --stream 2>/dev/null | sed 's/^/    /' || true
fi

echo ""
echo "=== Example complete ==="
