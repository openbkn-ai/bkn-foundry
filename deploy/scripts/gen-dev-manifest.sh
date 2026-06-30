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
# --latest overrides the above: resolve EACH chart to its NEWEST main build,
# i.e. among tags of the form <semver>-main.<YYYYMMDDHHMMSS>.sha<7hex>, pick the
# one with the most recent embedded commit time (a plain string compare on the
# fixed-width date — no local git history needed). If a chart has no such build,
# fall back to stable (highest clean semver), then to the normal missing/error
# handling. --latest is independent of --branch; if both are given, --latest
# wins. Use this for "newest of everything from main", restricted-network safe.
#
# Requires: python3 + git (queries GHCR OCI registry anonymously; no gh/PAT for public packages). For --branch, fetch the branch first so origin/<branch> resolves. On macOS the system python3 may lack CA certs; set SSL_CERT_FILE=/etc/ssl/cert.pem (or `pip install certifi`) if every chart resolves NOT FOUND.
#
# Examples:
#   ./gen-dev-manifest.sh                          # latest stable, all charts
#   ./gen-dev-manifest.sh --branch=fix/my-thing    # my branch + stable fallback
#   ./gen-dev-manifest.sh --branch=feat/x --base=release/0.2 --out=/tmp/m.yaml
#   ./gen-dev-manifest.sh --latest --out=/tmp/m.yaml  # newest main build per chart
# =============================================================================
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

ORG="${ORG:-openbkn-ai}"
TEMPLATE="${TEMPLATE:-${SCRIPT_DIR}/../release-manifests/0.1.0/bkn-foundry.yaml}"
BRANCH=""
BASE="main"
OUT="./bkn-foundry.dev.yaml"
LATEST=""

usage() {
    sed -n '2,35p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
    echo "Flags: --latest --branch=<b> --base=<b,def main> --template=<path> --out=<path> --org=<org>"
}
while [ $# -gt 0 ]; do
    case "$1" in
        --latest)     LATEST="1" ;;
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

if [ -n "${LATEST}" ] && [ -n "${BRANCH}" ]; then
    echo "Note: --latest overrides --branch ('${BRANCH}'); resolving newest main build per chart." >&2
fi

command -v python3 >/dev/null 2>&1 || { echo "Error: python3 required." >&2; exit 1; }
command -v git >/dev/null 2>&1 || { echo "Error: git required (for --branch HEAD sha)." >&2; exit 1; }
[ -f "${TEMPLATE}" ] || { echo "Error: template not found: ${TEMPLATE}" >&2; exit 1; }

ORG="$ORG" TEMPLATE="$TEMPLATE" BRANCH="$BRANCH" BASE="$BASE" OUT="$OUT" LATEST="$LATEST" python3 - <<'PY'
import os, re, json, subprocess, ssl, sys

ORG=os.environ["ORG"]; TEMPLATE=os.environ["TEMPLATE"]
BRANCH=os.environ["BRANCH"]; BASE=os.environ["BASE"]; OUT=os.environ["OUT"]
LATEST=bool(os.environ.get("LATEST"))

def sanitize(b):
    b=b.lower()
    b=re.sub(r'[^0-9a-zA-Z.-]+','-',b)
    b=re.sub(r'-+','-',b)
    return b.strip('.-')

SAN_BRANCH=sanitize(BRANCH) if BRANCH else ""
SAN_BASE=sanitize(BASE) if BASE else ""

SEMVER=re.compile(r'^(\d+)\.(\d+)\.(\d+)$')

import urllib.request
REPO_DIR=os.path.dirname(os.path.abspath(TEMPLATE))

def _make_ssl_context():
    """Build an SSL context that actually verifies on macOS system python (3.7),
    whose urllib otherwise dies with CERTIFICATE_VERIFY_FAILED (no local issuer)."""
    try:
        import certifi
        return ssl.create_default_context(cafile=certifi.where())
    except Exception:
        pass
    env_ca=os.environ.get("SSL_CERT_FILE")
    if env_ca and os.path.exists(env_ca):
        return ssl.create_default_context(cafile=env_ca)
    if os.path.exists("/etc/ssl/cert.pem"):
        return ssl.create_default_context(cafile="/etc/ssl/cert.pem")
    return ssl.create_default_context()

SSL_CTX=_make_ssl_context()

def reg_tags(chart):
    """List tags for charts/<chart> via the GHCR OCI registry (no gh; anonymous
    token works for public packages)."""
    repo=f"{ORG}/charts/{chart}"
    try:
        tok=json.load(urllib.request.urlopen(
            f"https://ghcr.io/token?scope=repository:{repo}:pull",
            timeout=20, context=SSL_CTX))["token"]
        req=urllib.request.Request(f"https://ghcr.io/v2/{repo}/tags/list",
                                   headers={"Authorization": f"Bearer {tok}"})
        return json.load(urllib.request.urlopen(req, timeout=20, context=SSL_CTX)).get("tags") or []
    except ssl.SSLCertVerificationError:
        # don't swallow silently at the top level; surfaced after the resolve
        # loop if every chart ends up NOT FOUND (see the macOS-CA hint below).
        return []
    except Exception:
        return []

def git_short_sha(ref):
    """7-char sha of a branch ref (origin/<ref> preferred), matching CI's tag sha."""
    if not ref: return None
    for r in (f"origin/{ref}", ref):
        try:
            out=subprocess.run(["git","rev-parse","--short=7",r],
                               capture_output=True, text=True, cwd=REPO_DIR)
            if out.returncode==0 and out.stdout.strip():
                return out.stdout.strip()
        except Exception:
            pass
    return None

BR_SHA=git_short_sha(BRANCH)
BASE_SHA=git_short_sha(BASE)

def highest_semver(tags):
    cand=[t for t in tags if SEMVER.match(t)]
    if not cand: return None
    return max(cand, key=lambda t:tuple(int(x) for x in SEMVER.match(t).groups()))

# <semver>-main.<YYYYMMDDHHMMSS>.sha<7hex> — CI embeds the commit time, so the
# fixed-width date sorts lexicographically == chronologically (no local git).
MAIN_BUILD=re.compile(r'.*-main\.(\d{14})\.sha[0-9a-f]{7}$')

def newest_main_build(tags):
    """Among tags of the form <semver>-main.<date>.sha<7hex>, the one with the
    most recent embedded commit time. Pure string compare on the fixed-width
    date — no sha-to-history lookup, so it can't silently mis-order on a
    shallow/foreign checkout the way the old commit-time-via-git scheme did."""
    cand=[]
    for t in tags:
        m=MAIN_BUILD.fullmatch(t)
        if m: cand.append((t, m.group(1)))
    if not cand: return None
    return max(cand, key=lambda ts: ts[1])[0]

# Branch build at an exact HEAD sha. Accepts both the dated form
# (-<san>.<date>.sha<7>) and the legacy un-dated form (-<san>.sha<7>).
def _branch_tag(tags, san, sha):
    """Tag for a branch = the build at the branch HEAD sha exactly. No HEAD-sha
    build (component not rebuilt on this branch, or branch not fetched) -> None,
    so the caller falls back to stable rather than picking a stale older build."""
    if not sha: return None
    pat=re.compile(rf'-{re.escape(san)}\.(?:\d{{14}}\.)?sha{re.escape(sha)}$')
    for t in tags:
        if pat.search(t): return t
    return None

def resolve(chart):
    tags=reg_tags(chart)
    # 0) --latest: newest main build per chart (wins over branch); else stable;
    #    else fall through to the normal missing/error handling.
    if LATEST:
        t=newest_main_build(tags)
        if t: return t, "latest-main"
        s=highest_semver(tags)
        if s: return s, "stable"
        return None, "missing"
    # 1) branch build (match branch HEAD sha; fetch the branch first if stale)
    if SAN_BRANCH:
        t=_branch_tag(tags, SAN_BRANCH, BR_SHA)
        if t: return t, "branch"
    # 2) latest stable (highest clean semver)
    s=highest_semver(tags)
    if s: return s, "stable"
    # 3) base branch build
    if SAN_BASE:
        t=_branch_tag(tags, SAN_BASE, BASE_SHA)
        if t: return t, "base"
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
mode=("latest (newest main build per chart, else stable)" if LATEST
      else f"branch={BRANCH or '-'}, base={BASE}")
print(f"Resolving {len(charts)} charts from ghcr.io/{ORG}/charts "
      f"({mode})...", file=sys.stderr)
if not LATEST and SAN_BRANCH and not BR_SHA:
    print(f"  WARNING: cannot resolve sha for branch '{BRANCH}' (fetch it: "
          f"git fetch origin {BRANCH}); branch matching disabled -> all stable.",
          file=sys.stderr)

resolved={}; sources={}
for c in charts:
    v,src=resolve(c)
    resolved[c]=v; sources[c]=src
    print(f"  {c:30} {v or '!! NOT FOUND':40} [{src}]", file=sys.stderr)

missing=[c for c in charts if resolved[c] is None]
if missing:
    if len(missing)==len(charts):
        # nothing resolved at all — on macOS this is almost always the system
        # python lacking CA certs (reg_tags TLS verify failing silently).
        print("\nAll charts NOT FOUND — if on macOS, the system python may lack "
              "CA certs; set SSL_CERT_FILE=/etc/ssl/cert.pem (or `pip install "
              "certifi`) and retry.", file=sys.stderr)
    print(f"\nERROR: no package found for: {', '.join(missing)}", file=sys.stderr)
    sys.exit(1)

# rewrite version lines
for i,chart in ver_lines.items():
    lines[i]=re.sub(r'(version:\s*)\S+', lambda m: m.group(1)+resolved[chart], lines[i])

# --version_file overrides Foundry/core only; keep dependency manifests (e.g. ISF)
# pinned to the committed file by rewriting their relative `manifest:` paths to
# absolute (relative paths would otherwise resolve next to OUT, not the repo).
TPL_DIR=os.path.dirname(os.path.abspath(TEMPLATE))
for i,ln in enumerate(lines):
    m=re.match(r'^(\s*manifest:\s*)(\S+)\s*$', ln)
    if m and not m.group(2).startswith('/'):
        abs_dep=os.path.normpath(os.path.join(TPL_DIR, m.group(2)))
        lines[i]=f"{m.group(1)}{abs_dep}"

# prepend a provenance header comment
mode_line=("mode=latest (newest main build per chart)" if LATEST
           else f"branch={BRANCH or '(none, stable)'}  base={BASE}")
hdr=[f"# Generated by gen-dev-manifest.sh — DEV/TEST manifest, NOT a release lockfile.",
     f"# {mode_line}  org={ORG}",
     f"# Per-chart source: " + ", ".join(f"{c}={sources[c]}" for c in charts),
     "#"]
open(OUT,"w").write("\n".join(hdr+lines)+"\n")
print(f"\nWrote {OUT}", file=sys.stderr)
print(f"Install with:  deploy.sh foundry install --version_file={OUT}", file=sys.stderr)
PY
