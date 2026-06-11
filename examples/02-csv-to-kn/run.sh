#!/usr/bin/env bash
# =============================================================================
# 02-csv-to-kn: From CSV Files to Knowledge Network
#
# Load local CSVs into MySQL → Vega catalog → Knowledge Network → Agent Q&A
#
# Uses the Vega catalog/connector model (vega-backend). Catalogs connect to an
# existing database, so the CSVs are first loaded into MySQL with the standard
# `mysql` client (the legacy `create-from-csv` data-connection import is gone).
# Object types bind to Vega resource IDs and query in real time — no bkn build.
# =============================================================================
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
export NODE_TLS_REJECT_UNAUTHORIZED="${NODE_TLS_REJECT_UNAUTHORIZED:-0}"

usage() { echo "Usage: $(basename "$0") [-h]   (config from .env, see env.sample)"; }
while [ $# -gt 0 ]; do case "$1" in -h|--help) usage; exit 0;; *) echo "Unknown: $1">&2; usage>&2; exit 2;; esac; shift; done

DEBUG="${DEBUG:-0}"
[ -f "$SCRIPT_DIR/.env" ] && source "$SCRIPT_DIR/.env"
DB_HOST="${DB_HOST:?Set DB_HOST in .env}"; DB_PORT="${DB_PORT:-3306}"
DB_NAME="${DB_NAME:?Set DB_NAME in .env}"; DB_USER="${DB_USER:?Set DB_USER in .env}"; DB_PASS="${DB_PASS:?Set DB_PASS in .env}"
DB_HOST_SEED="${DB_HOST_SEED:-$DB_HOST}"

MYSQL_BIN="${MYSQL_BIN:-mysql}"
if ! command -v "$MYSQL_BIN" >/dev/null 2>&1; then
    for _p in "$(brew --prefix mysql-client 2>/dev/null)/bin/mysql" /opt/homebrew/opt/mysql-client/bin/mysql /usr/local/opt/mysql-client/bin/mysql; do
        [ -x "$_p" ] && { MYSQL_BIN="$_p"; break; }
    done
fi
command -v "$MYSQL_BIN" >/dev/null 2>&1 || { echo "Error: mysql client not found. macOS: brew install mysql-client | Ubuntu: sudo apt install -y mysql-client"; exit 1; }

jget() { python3 -c "import json,sys;d=json.load(sys.stdin);print(d.get('$1','') if isinstance(d,dict) else '')" 2>/dev/null || true; }

TIMESTAMP=$(date +%s)
CAT_NAME="csv_cat_${TIMESTAMP}"
KN_NAME="csv_kn_${TIMESTAMP}"
CAT_ID=""; KN_ID=""

cleanup() {
    [ -z "$KN_ID" ] && [ -z "$CAT_ID" ] && return 0
    echo ""; echo "=== Cleanup ==="
    [ -n "$KN_ID" ]  && openbkn bkn delete "$KN_ID" -y 2>/dev/null && echo "  Deleted KN $KN_ID"
    [ -n "$CAT_ID" ] && openbkn call "/api/vega-backend/v1/catalogs/$CAT_ID" -X DELETE 2>/dev/null && echo "  Deleted catalog $CAT_ID"
    echo "Done."
}
trap cleanup EXIT

# ── Step 1: Load CSVs into MySQL ─────────────────────────────────────────────
echo "=== Step 1: Load CSVs into MySQL ==="
echo "  Files: $(ls "$SCRIPT_DIR/data/"*.csv | xargs -n1 basename | tr '\n' ' ')"
# CSV → CREATE TABLE + INSERT (light type inference), piped to the mysql client.
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
    rows=list(csv.reader(open(path,encoding='utf-8')))
    hdr=rows[0]; data=rows[1:]
    types=[coltype([r[i] for r in data if i<len(r)]) for i in range(len(hdr))]
    print(f"DROP TABLE IF EXISTS `{tbl}`;")
    cols=', '.join(f"`{c}` {t}" for c,t in zip(hdr,types))
    print(f"CREATE TABLE `{tbl}` ({cols});")
    for r in data:
        r=(r+['']*len(hdr))[:len(hdr)]
        print(f"INSERT INTO `{tbl}` VALUES ({', '.join(sqlval(v) for v in r)});")
PY
echo "  Loaded: departments, employees, projects"

# ── Step 2: Register Vega catalog + discover ─────────────────────────────────
echo ""
echo "=== Step 2: Register Vega catalog + discover tables ==="
CONN_CFG=$(python3 -c "import json,sys;print(json.dumps({'host':sys.argv[1],'port':int(sys.argv[2]),'username':sys.argv[3],'password':sys.argv[4],'databases':[sys.argv[5]]}))" "$DB_HOST" "$DB_PORT" "$DB_USER" "$DB_PASS" "$DB_NAME")
CAT_BODY=$(python3 -c "import json,sys;print(json.dumps({'name':sys.argv[1],'connector_type':'mysql','connector_config':json.loads(sys.argv[2])}))" "$CAT_NAME" "$CONN_CFG")
CAT_ID=$(openbkn --json call /api/vega-backend/v1/catalogs -X POST -H "Content-Type: application/json" -d "$CAT_BODY" 2>/dev/null | jget id)
[ -z "$CAT_ID" ] && { echo "Error: catalog create failed (is DB_HOST reachable from vega-backend pods?)." >&2; exit 1; }
echo "  Catalog: $CAT_ID"
openbkn call "/api/vega-backend/v1/catalogs/${CAT_ID}/enable" -X POST >/dev/null 2>&1 || true
openbkn call "/api/vega-backend/v1/catalogs/${CAT_ID}/discover?wait=true" -X POST >/dev/null 2>&1 || true
RES_JSON='{}'; RES_N=0
for _i in $(seq 1 20); do
    RES_JSON=$(openbkn --json vega resource list --datasource-id "$CAT_ID" --type table 2>/dev/null || echo '{}')
    RES_N=$(echo "$RES_JSON" | python3 -c "import json,sys;print(len(json.load(sys.stdin).get('entries',[])))" 2>/dev/null || echo 0)
    [ "${RES_N:-0}" -gt 0 ] && break; sleep 3
done
[ "${RES_N:-0}" -eq 0 ] && { echo "Error: no tables discovered." >&2; exit 1; }
echo "  Discovered ${RES_N} table resource(s)"
res_id() { echo "$RES_JSON" | python3 -c "import json,sys
for r in json.load(sys.stdin).get('entries',[]):
  if r.get('name','').endswith('$1'): print(r['id']);break"; }

# ── Step 3: Build Knowledge Network (object types bound to resources) ────────
echo ""
echo "=== Step 3: Build Knowledge Network ==="
KN_ID=$(openbkn --json bkn create "$KN_NAME" 2>/dev/null | jget kn_id)
[ -z "$KN_ID" ] && KN_ID=$(openbkn --json bkn create "${KN_NAME}_b" 2>/dev/null | jget id)
[ -z "$KN_ID" ] && { echo "Error: KN create failed." >&2; exit 1; }
echo "  Knowledge Network: $KN_ID"
# Build the object-type create body ({"entries":[entry]}) for a resource-bound
# OT: data_properties come from the Vega resource schema_definition, falling
# back to pk/dk-only properties when the schema is empty.
ot_create() { # <kn_id> <ot_name> <resource_id> <primary_key> <display_key>
    local kn="$1" name="$2" res="$3" pk="$4" dk="$5" body
    body=$(openbkn --json vega resource get "$res" 2>/dev/null | python3 -c "
import json, sys
TYPE_MAP = {'varchar':'string','char':'string','nvarchar':'string','longtext':'text',
            'mediumtext':'text','tinytext':'text','bigint':'integer','int':'integer',
            'smallint':'integer','tinyint':'integer','double':'float','real':'float',
            'numeric':'decimal','number':'decimal','blob':'binary','longblob':'binary',
            'bit':'boolean','bool':'boolean'}
def norm(t): return TYPE_MAP.get(str(t or 'string').lower().strip(), str(t or 'string').lower().strip())
name, res, pk, dk = sys.argv[1:5]
try:
    dv = json.load(sys.stdin)
except Exception:
    dv = {}
if isinstance(dv, dict) and isinstance(dv.get('entries'), list):
    dv = dv['entries'][0] if dv['entries'] else {}
fields = dv.get('schema_definition') or []
if fields:
    props = [{'name': f['name'], 'display_name': (f.get('display_name') or f['name']),
              'type': norm(f.get('type')),
              'mapped_field': {'name': f['name'], 'type': norm(f.get('type')),
                               'display_name': (f.get('display_name') or f['name'])}}
             for f in fields]
else:
    props = [{'name': n, 'display_name': n, 'type': 'string',
              'mapped_field': {'name': n, 'type': 'string', 'display_name': n}}
             for n in dict.fromkeys([pk, dk])]
print(json.dumps({'entries': [{'branch': 'main', 'name': name,
    'data_source': {'type': 'resource', 'id': res},
    'primary_keys': [pk], 'display_key': dk, 'data_properties': props}]}))
" "$name" "$res" "$pk" "$dk")
    openbkn --json bkn object-type create "$kn" --body "$body" >/dev/null 2>&1
}

# All three CSV tables use id (PK) + name (display key).
declare -a OTS=("departments:部门" "employees:员工" "projects:项目")
for spec in "${OTS[@]}"; do
    tbl="${spec%%:*}"; label="${spec##*:}"; rid=$(res_id "$tbl")
    [ -z "$rid" ] && continue
    ot_create "$KN_ID" "$label" "$rid" id name && echo "  + $label ($tbl) → $rid"
done

# Resolve OT ids from the KN, by name (list order is not guaranteed)
OT_LIST=$(openbkn --json bkn object-type list "$KN_ID" 2>/dev/null || echo '{}')
ot_by_name() { echo "$OT_LIST" | python3 -c "import json,sys
d=json.load(sys.stdin);es=d.get('entries',d if isinstance(d,list) else [])
[print(e.get('id','')) for e in es if e.get('name')=='$1']" 2>/dev/null | head -1; }
DEPT_OT=$(ot_by_name 部门); EMP_OT=$(ot_by_name 员工)
FIRST_OT="$DEPT_OT"; SECOND_OT="$EMP_OT"

# ── Step 4: Explore schema ───────────────────────────────────────────────────
echo ""
echo "=== Step 4: Explore schema ==="
echo "$OT_LIST" | python3 -c "import json,sys
d=json.load(sys.stdin);es=d.get('entries',d if isinstance(d,list) else [])
print(f'  Object types ({len(es)}):')
for e in es: print('    -', e.get('name','?'), e.get('id',''))" 2>/dev/null || true

# ── Step 5: Query instances (real-time via Vega) ─────────────────────────────
echo ""
echo "=== Step 5: Query instances ==="
qrows() { openbkn --json call "/api/ontology-query/v1/knowledge-networks/$KN_ID/object-types/$1" -X POST -H "X-HTTP-Method-Override: GET" -d "{\"limit\":${2:-5}}" 2>/dev/null | python3 -c "import json,sys
d=json.load(sys.stdin);rows=d.get('datas',d.get('entries',[]))
for r in rows: print(', '.join(f'{k}={v}' for k,v in r.items() if not str(k).startswith('_')))" 2>/dev/null; }
if [ -n "$FIRST_OT" ]; then echo "  departments (first 5):"; qrows "$FIRST_OT" 5 | sed 's/^/    /'; fi

# ── Step 6: Agent Q&A ────────────────────────────────────────────────────────
echo ""
echo "=== Step 6: Agent Q&A ==="
AGENT_ID="${AGENT_ID:-}"
[ -z "$AGENT_ID" ] && AGENT_ID=$(openbkn --json agent list --limit 1 2>/dev/null | python3 -c "import json,sys
d=json.load(sys.stdin);a=d if isinstance(d,list) else d.get('entries',[]);print(a[0].get('id','') if a else '')" 2>/dev/null || true)
if [ -z "$AGENT_ID" ]; then
    echo "  No agent available — set AGENT_ID in .env. Skipping Q&A."
else
    DEPT_DATA=$([ -n "$FIRST_OT" ] && qrows "$FIRST_OT" 20 || true)
    EMP_DATA=$([ -n "$SECOND_OT" ] && qrows "$SECOND_OT" 20 || true)
    Q="这份数据里，哪个部门的预算最高？Engineering 部门有多少员工？"
    PROMPT="departments 数据：
${DEPT_DATA}

employees 数据：
${EMP_DATA}

请基于以上数据回答：${Q}"
    echo "  Agent: $AGENT_ID"; echo "  Question: $Q"; echo "  Response:"
    openbkn agent chat "$AGENT_ID" -m "$PROMPT" --stream 2>/dev/null | sed 's/^/    /' || echo "    (agent unavailable)"
fi

echo ""
echo "=== Example complete ==="
