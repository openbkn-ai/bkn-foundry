#!/usr/bin/env bash
# =============================================================================
# gen-dev-manifest.sh — generate a dev/test release manifest with per-chart
# versions resolved from GHCR, for:
#     deploy.sh foundry install --version_file=<generated>
#
# Release builds pin exact versions in a committed manifest (a lockfile).
# For dev/test you usually want "the newest", and CI only republishes the
# components a branch actually changed — so this tool resolves EACH chart
# independently from GHCR and writes a ready-to-use manifest.
#
# Resolution per chart (stable-first, so an untouched component stays on a
# known-good release and any regression is attributable to your branch):
#     --branch=<X>'s newest build   (only the components X rebuilt have one)
#       └─ else  latest stable       (highest clean semver, e.g. 0.1.0)
#            └─ else  --base branch's newest build   (default: main)
#                 └─ else  error (chart has no package at all)
#
# With no --branch it is pure stable: every chart = highest clean semver.
#
# Requires: gh (authenticated, package:read on the org), python3.
#
# Examples:
#   ./gen-dev-manifest.sh                          # latest stable, all charts
#   ./gen-dev-manifest.sh --branch=fix/my-thing    # my branch + stable fallback
#   ./gen-dev-manifest.sh --branch=feat/x --base=release/0.2 --out=/tmp/m.yaml
# =============================================================================
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

ORG="${ORG:-openbkn-ai}"
TEMPLATE="${TEMPLATE:-${SCRIPT_DIR}/../release-manifests/0.1.0/bkn-foundry.yaml}"
BRANCH=""
BASE="main"
OUT="./bkn-foundry.dev.yaml"

usage() {
    sed -n '2,40p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
    echo "Flags: --branch=<b> --base=<b,def main> --template=<path> --out=<path> --org=<org>"
}
while [ $# -gt 0 ]; do
    case "$1" in
        --branch=*)   BRANCH="${1#*=}" ;;
        --base=*)     BASE="${1#*=}" ;;
        --template=*) TEMPLATE="${1#*=}" ;;
        --out=*)      OUT="${1#*=}" ;;
        --org=*)      ORG="${1#*=}" ;;
        -h|--help)    usage; exit 0 ;;
        *) echo "Unknown: $1" >&2; usage >&2; exit 2 ;;
    esac
    shift
done

command -v gh >/dev/null 2>&1 || { echo "Error: gh CLI required (authenticated, package:read)." >&2; exit 1; }
command -v python3 >/dev/null 2>&1 || { echo "Error: python3 required." >&2; exit 1; }
[ -f "${TEMPLATE}" ] || { echo "Error: template not found: ${TEMPLATE}" >&2; exit 1; }

ORG="$ORG" TEMPLATE="$TEMPLATE" BRANCH="$BRANCH" BASE="$BASE" OUT="$OUT" python3 - <<'PY'
import os, re, json, subprocess, sys

ORG=os.environ["ORG"]; TEMPLATE=os.environ["TEMPLATE"]
BRANCH=os.environ["BRANCH"]; BASE=os.environ["BASE"]; OUT=os.environ["OUT"]

def sanitize(b):
    b=b.lower()
    b=re.sub(r'[^0-9a-zA-Z.-]+','-',b)
    b=re.sub(r'-+','-',b)
    return b.strip('.-')

SAN_BRANCH=sanitize(BRANCH) if BRANCH else ""
SAN_BASE=sanitize(BASE) if BASE else ""

SEMVER=re.compile(r'^(\d+)\.(\d+)\.(\d+)$')

def gh_tags(chart):
    """Return list of (tag, created_at) for charts/<chart>."""
    try:
        out=subprocess.run(
            ["gh","api","--paginate",
             f"orgs/{ORG}/packages/container/charts%2F{chart}/versions",
             "--jq", r'.[] | .created_at as $c | (.metadata.container.tags // [])[] | "\(.)\t\($c)"'],
            capture_output=True, text=True)
    except Exception as e:
        return []
    if out.returncode!=0:
        return []
    pairs=[]
    for line in out.stdout.splitlines():
        if "\t" in line:
            t,c=line.split("\t",1); pairs.append((t.strip(),c.strip()))
    return pairs

def newest_by_time(pairs):
    return max(pairs, key=lambda p:p[1])[0] if pairs else None

def highest_semver(tags):
    cand=[t for t in tags if SEMVER.match(t)]
    if not cand: return None
    return max(cand, key=lambda t:tuple(int(x) for x in SEMVER.match(t).groups()))

def resolve(chart):
    pairs=gh_tags(chart)
    tags=[t for t,_ in pairs]
    # 1) branch build
    if SAN_BRANCH:
        bp=[(t,c) for t,c in pairs if f"-{SAN_BRANCH}.sha" in t]
        if bp: return newest_by_time(bp), "branch"
    # 2) latest stable (clean semver)
    s=highest_semver(tags)
    if s: return s, "stable"
    # 3) base branch build
    if SAN_BASE:
        bp=[(t,c) for t,c in pairs if f"-{SAN_BASE}.sha" in t]
        if bp: return newest_by_time(bp), "base"
    return None, "missing"

# parse template: collect release chart names, in order, with line index of each version line
lines=open(TEMPLATE).read().splitlines()
in_rel=False; cur_chart=None
# map line_index -> chart (the version line to rewrite)
ver_lines={}
for i,ln in enumerate(lines):
    if re.match(r'^releases:\s*$', ln): in_rel=True; continue
    if in_rel:
        if re.match(r'^\S', ln): in_rel=False; continue   # left releases block
        m=re.match(r'^    chart:\s*(\S+)', ln)
        if m: cur_chart=m.group(1); continue
        if re.match(r'^    version:\s*\S+', ln) and cur_chart:
            ver_lines[i]=cur_chart

charts=list(dict.fromkeys(ver_lines.values()))
print(f"Resolving {len(charts)} charts from ghcr.io/{ORG}/charts "
      f"(branch={BRANCH or '-'}, base={BASE})...", file=sys.stderr)

resolved={}; sources={}
for c in charts:
    v,src=resolve(c)
    resolved[c]=v; sources[c]=src
    print(f"  {c:30} {v or '!! NOT FOUND':40} [{src}]", file=sys.stderr)

missing=[c for c in charts if resolved[c] is None]
if missing:
    print(f"\nERROR: no package found for: {', '.join(missing)}", file=sys.stderr)
    sys.exit(1)

# rewrite version lines
for i,chart in ver_lines.items():
    lines[i]=re.sub(r'(version:\s*)\S+', lambda m: m.group(1)+resolved[chart], lines[i])

# prepend a provenance header comment
hdr=[f"# Generated by gen-dev-manifest.sh — DEV/TEST manifest, NOT a release lockfile.",
     f"# branch={BRANCH or '(none, stable)'}  base={BASE}  org={ORG}",
     f"# Per-chart source: " + ", ".join(f"{c}={sources[c]}" for c in charts),
     "#"]
open(OUT,"w").write("\n".join(hdr+lines)+"\n")
print(f"\nWrote {OUT}", file=sys.stderr)
print(f"Install with:  deploy.sh foundry install --version_file={OUT}", file=sys.stderr)
PY
