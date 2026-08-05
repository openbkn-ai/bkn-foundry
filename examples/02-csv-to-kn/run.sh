#!/usr/bin/env bash
# =============================================================================
# 02-csv-to-kn: From CSV Files to Knowledge Network
#
# Load local CSVs into MySQL → Vega catalog → Knowledge Network → Search Index
# → Query instances
#
# Uses the Vega catalog/connector model (vega-backend). Catalogs connect to an
# existing database, so the CSVs are first loaded into MySQL with the standard
# `mysql` client (the legacy `create-from-csv` data-connection import is gone).
# Object types bind to Vega resource IDs.
#
# Reading a table resource takes one of two paths, and Step 4 decides which:
#   - no local index  → Vega queries the source database on every call (live)
#   - local index built → Vega serves the build snapshot from OpenSearch, and a
#     later UPDATE in the source database stays invisible until the next build
# Full-text and vector search require that index, so Step 4 builds one per
# resource (DO_INDEX=0 keeps the live path).
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

# Search index (Step 4). DO_INDEX=0 skips it; EMBEDDING_MODEL_NAME='' builds
# full-text only. The name must match a registered embedding small model —
# Vega resolves models by name, not by id.
DO_INDEX="${DO_INDEX:-1}"
EMBEDDING_MODEL_NAME="${EMBEDDING_MODEL_NAME-text-embedding-v4}"
INDEX_TIMEOUT="${INDEX_TIMEOUT:-300}"
# Filled in by Step 4; Step 6 reports the read path from what actually built.
INDEX_OK=0
INDEX_FAIL=0
debug() { if [ "$DEBUG" = "1" ] || [ "$DEBUG" = "true" ]; then echo "[debug] $*" >&2; fi; }

MYSQL_BIN="${MYSQL_BIN:-mysql}"
if ! command -v "$MYSQL_BIN" >/dev/null 2>&1; then
    for _p in "$(brew --prefix mysql-client 2>/dev/null)/bin/mysql" /opt/homebrew/opt/mysql-client/bin/mysql /usr/local/opt/mysql-client/bin/mysql; do
        [ -x "$_p" ] && { MYSQL_BIN="$_p"; break; }
    done
fi
command -v "$MYSQL_BIN" >/dev/null 2>&1 || { echo "Error: mysql client not found. macOS: brew install mysql-client | Ubuntu: sudo apt install -y mysql-client"; exit 1; }

jget() { python3 -c "import json,sys;d=json.load(sys.stdin);print((d.get('$1') or '') if isinstance(d,dict) else '')" 2>/dev/null || true; }

TIMESTAMP=$(date +%s)
CAT_NAME="csv_cat_${TIMESTAMP}"
KN_NAME="csv_kn_${TIMESTAMP}"
CAT_ID=""; KN_ID=""

cleanup() {
    [ -z "$KN_ID" ] && [ -z "$CAT_ID" ] && return 0
    if [ "${CLEANUP:-0}" != "1" ]; then
        echo ""
        echo "=== Resources kept (set CLEANUP=1 to delete on exit) ==="
        [ -n "$KN_ID" ]  && echo "  KN      $KN_ID   (openbkn bkn delete $KN_ID -y)" || true
        [ -n "$CAT_ID" ] && echo "  Catalog $CAT_ID  (openbkn call /api/vega-backend/v1/catalogs/$CAT_ID -X DELETE)" || true
        return 0
    fi
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
# One create, then read the id out of that same response: the CLI has returned
# `id` rather than `kn_id`, and calling create a second time to "retry" left a
# stray empty KN behind on every run.
KN_JSON=$(openbkn --json bkn create "$KN_NAME" 2>/dev/null || true)
KN_ID=$(printf '%s' "$KN_JSON" | jget kn_id)
[ -n "$KN_ID" ] || KN_ID=$(printf '%s' "$KN_JSON" | jget id)
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
DEPT_OT=$(ot_by_name 部门)
FIRST_OT="$DEPT_OT"

# ── Step 4: Build the search index (full text + vector) ──────────────────────
# Vector and full-text search need an index: a Vega BuildTask copies the rows
# into OpenSearch and vectorises the fields named here.
#
# This also changes how Step 6 reads. Vega serves a table resource from its
# local index as soon as one exists, and queries the source database only while
# it does not — so after this step the rows are the build snapshot, and a later
# UPDATE in MySQL stays invisible until the resource is rebuilt. DO_INDEX=0
# keeps the live path (and loses search).
#
# Index configuration lives on the Vega *resource* (`index_config` + per-field
# `features`); the build task only snapshots it. `openbkn vega dataset build`
# writes the resource, then creates and starts the task. A resource without
# `index_config.build_key_fields` is rejected with HTTP 400.
build_index() { # <resource_id> <build_key> <fulltext_fields> <embedding_fields> <label>
    local rid="$1" key="$2" ft="$3" ef="$4" label="$5"
    local -a args=(--json vega dataset build "$rid"
        --mode batch --execute-type full
        --build-key-fields "$key" --fulltext-fields "$ft"
        --wait --timeout "$INDEX_TIMEOUT")
    if [ -n "$EMBEDDING_MODEL_NAME" ] && [ -n "$ef" ]; then
        args+=(--embedding-fields "$ef" --embedding-model "$EMBEDDING_MODEL_NAME")
    fi
    debug "build index: openbkn ${args[*]}"
    # Keep stderr: the API error is the only thing that explains a failed build
    # (a field missing from the resource schema, an unregistered model, ...).
    local out err rc=0
    err=$(mktemp); out=$(openbkn "${args[@]}" 2>"$err") || rc=$?
    if [ "$rc" -ne 0 ]; then
        echo "  $label: index build failed" >&2
        sed 's/^/    /' "$err" >&2
        rm -f "$err"
        INDEX_FAIL=$((INDEX_FAIL + 1))
        return 0
    fi
    rm -f "$err"
    # `--wait` only waits for a terminal state and still exits 0 when that state is
    # `failed`, so the task's own status decides whether this counted as a success.
    local line status
    line=$(printf '%s' "$out" | python3 -c "import json,sys
d=json.load(sys.stdin)
h=d.get('index_health') or {}
print('%s\t  $label: status=%s synced=%s vectorized=%s fulltext=%s embedding=%s' % (
    d.get('status','?'), d.get('status','?'), d.get('synced_count','?'),
    d.get('vectorized_count','?'), h.get('fulltext','none'), h.get('embedding','none')))" 2>/dev/null) || {
        echo "  $label: index build returned an unexpected payload: $out" >&2
        INDEX_FAIL=$((INDEX_FAIL + 1))
        return 0
    }
    status="${line%%$'\t'*}"
    echo "${line#*$'\t'}"
    if [ "$status" = "completed" ]; then
        INDEX_OK=$((INDEX_OK + 1))
    else
        INDEX_FAIL=$((INDEX_FAIL + 1))
    fi
}

echo ""
echo "=== Step 4: Build search index (full text + vector) ==="
if [ "$DO_INDEX" != "1" ]; then
    echo "  skipped (DO_INDEX=0)"
else
    # Vega resolves the embedding model by name; fall back to full-text only
    # rather than failing the example when that name is not registered.
    if [ -n "$EMBEDDING_MODEL_NAME" ]; then
        if ! openbkn --json model small list 2>/dev/null | python3 -c "import json,sys
d=json.load(sys.stdin); es=d.get('data') or d.get('entries') or []
sys.exit(0 if any(e.get('model_name')=='$EMBEDDING_MODEL_NAME' and e.get('model_type')=='embedding' for e in es) else 1)" 2>/dev/null; then
            echo "  note: embedding model '$EMBEDDING_MODEL_NAME' is not registered — building full-text only."
            EMBEDDING_MODEL_NAME=""
        fi
    fi
    # table : build key : fulltext fields : embedding fields
    for spec in \
        "departments:id:name,location:name" \
        "employees:id:name,role,level:name,role" \
        "projects:id:name,status:name"; do
        IFS=: read -r tbl key ft ef <<<"$spec"
        rid=$(res_id "$tbl")
        [ -z "$rid" ] && continue
        build_index "$rid" "$key" "$ft" "$ef" "$tbl"
    done
fi

# ── Step 5: Explore schema ───────────────────────────────────────────────────
echo ""
echo "=== Step 5: Explore schema ==="
echo "$OT_LIST" | python3 -c "import json,sys
d=json.load(sys.stdin);es=d.get('entries',d if isinstance(d,list) else [])
print(f'  Object types ({len(es)}):')
for e in es: print('    -', e.get('name','?'), e.get('id',''))" 2>/dev/null || true

# ── Step 6: Query instances (via Vega) ───────────────────────────────────────
echo ""
# Report the path the data actually takes, not the one Step 4 intended: a
# resource whose build failed still reads live.
if [ "$INDEX_OK" -gt 0 ] && [ "$INDEX_FAIL" -eq 0 ]; then
    echo "=== Step 6: Query instances (served from the Step 4 index snapshot) ==="
    echo "  Source-database updates appear here only after the resource is rebuilt."
elif [ "$INDEX_OK" -gt 0 ]; then
    echo "=== Step 6: Query instances (mixed read paths) ==="
    echo "  $INDEX_OK resource(s) serve a completed Step 4 snapshot; $INDEX_FAIL did not complete."
    echo "  A resource whose build failed part-way can still serve an incomplete snapshot —"
    echo "  check 'openbkn vega dataset build-list' and its index_health before trusting the rows."
else
    echo "=== Step 6: Query instances (live from the source database) ==="
fi
qrows() { openbkn --json call "/api/ontology-query/v1/knowledge-networks/$KN_ID/object-types/$1" -X POST -H "X-HTTP-Method-Override: GET" -d "{\"limit\":${2:-5}}" 2>/dev/null | python3 -c "import json,sys
d=json.load(sys.stdin);rows=d.get('datas',d.get('entries',[]))
for r in rows: print(', '.join(f'{k}={v}' for k,v in r.items() if not str(k).startswith('_')))" 2>/dev/null; }
if [ -n "$FIRST_OT" ]; then echo "  departments (first 5):"; qrows "$FIRST_OT" 5 | sed 's/^/    /'; fi

echo ""
echo "=== Example complete ==="
