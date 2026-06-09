#!/usr/bin/env python3
"""
Collect BKN Foundry install status and emit it as a human table (server-side
`deploy.sh ... status`) or as a non-sensitive JSON snapshot (served at the
public /install-status ingress endpoint).

Invoked from scripts/services/status.sh:
  python3 install_status.py --namespace NS --manifest MANIFEST.yaml \
      --config CONFIG_YAML --product bkn-foundry --format table|json

Two outputs, ONE collector:
  - table  : live, detailed — expected vs deployed chart version, app version,
             helm revision/status, workload ready count, version-drift / missing
             flags. For operators on the server.
  - json   : non-sensitive snapshot — release versions + ready + dep-service
             connection topology. NO credentials (whitelist, never blacklist).

Supports Python 3.6+ (CentOS 7 / old distros); avoids 3.7-only subprocess APIs.
"""
import argparse
import json
import subprocess
import sys
from datetime import datetime

try:
    import yaml
except ImportError as e:
    print(
        "PyYAML is required for install-status. Install one of:\n"
        "  sudo apt-get install -y python3-yaml                       # Debian/Ubuntu\n"
        "  sudo dnf install -y python3-pyyaml                         # Fedora/RHEL/openEuler\n"
        "  pip3 install --user --break-system-packages pyyaml         # any host with pip3",
        file=sys.stderr,
    )
    raise e


# --- depServices whitelist -------------------------------------------------
# Only these fields per service are safe to expose: connection topology + type.
# Anything not listed (password, user, admin_key, root_password, sentinelPassword,
# access keys, tokens) is dropped. Whitelist, not blacklist: a new secret field
# added upstream is excluded by default, never leaked by omission.
DEP_WHITELIST = {
    "rds":        ["type", "host", "port", "database", "source_type"],
    "redis":      ["connectType", "sourceType"],          # connectInfo handled below
    "mq":         ["mqType", "mqHost", "mqPort"],          # auth.mechanism handled below
    "opensearch": ["distribution", "host", "port", "protocol"],
    "mongodb":    ["host", "port", "replicaSet"],          # options.authSource handled below
    "zookeeper":  ["host", "port"],
    "class-443":  ["ingressClass"],
}


def run(cmd):
    """Run a command, return (rc, stdout). Never raises on non-zero."""
    try:
        p = subprocess.run(
            cmd, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
            universal_newlines=True,
        )
        return p.returncode, p.stdout
    except Exception:
        return 1, ""


def load_manifest_releases(manifest_path):
    """Return ordered list of (release_name, expected_version) from the VersionSet."""
    with open(manifest_path) as f:
        doc = yaml.safe_load(f) or {}
    releases = doc.get("releases", {}) or {}
    out = []
    for name, spec in releases.items():
        spec = spec or {}
        out.append((name, str(spec.get("version", "")) or "-"))
    return doc.get("product", ""), str(doc.get("version", "")) or "-", out


def helm_deployed(namespace):
    """release_name -> {chartVersion, appVersion, revision, status}."""
    rc, out = run(["helm", "list", "-n", namespace, "-o", "json"])
    if rc != 0 or not out.strip():
        return {}
    try:
        items = json.loads(out)
    except ValueError:
        return {}
    res = {}
    for it in items:
        name = it.get("name", "")
        chart = it.get("chart", "")           # "<chart>-<version>", e.g. "bkn-safe-0.1.0"
        # Strip the chart-name prefix to get the version. The version itself can
        # contain '-' (dev builds: "0.1.0-feat-isf-replacement.shab0ab73e"), so a
        # naive rsplit('-') is wrong. Chart name == release name for this product.
        if chart.startswith(name + "-"):
            chart_ver = chart[len(name) + 1:]
        elif "-" in chart:
            chart_ver = chart.rsplit("-", 1)[-1]
        else:
            chart_ver = "-"
        res[name] = {
            "chartVersion": chart_ver or "-",
            "appVersion": it.get("app_version", "") or "-",
            "revision": str(it.get("revision", "")) or "-",
            "status": it.get("status", "") or "-",
        }
    return res


def release_workloads(namespace, release):
    """(kind, name) of Deployments/StatefulSets a release owns.

    Asks helm for the rendered manifest rather than matching labels: chart labels
    are inconsistent across this product (some set app.kubernetes.io/instance,
    some only managed-by, some use module=, and release name != workload name —
    e.g. agent-backend -> agent-factory, sandbox -> sandbox-control-plane)."""
    rc, out = run(["helm", "get", "manifest", release, "-n", namespace])
    if rc != 0 or not out.strip():
        return []
    wls = []
    try:
        for doc in yaml.safe_load_all(out):
            if not doc:
                continue
            kind = doc.get("kind")
            if kind in ("Deployment", "StatefulSet"):
                name = (doc.get("metadata", {}) or {}).get("name")
                if name:
                    wls.append((kind, name))
    except yaml.YAMLError:
        return []
    return wls


def workload_ready(namespace, release):
    """Sum readyReplicas/replicas over a release's Deployments+StatefulSets.

    Returns 'ready/desired' (e.g. '1/1'), or '-' when the release owns no such
    workload (Jobs/hooks like data-migrator have none)."""
    wls = release_workloads(namespace, release)
    if not wls:
        return "-"
    ready = desired = 0
    for kind, name in wls:
        res = "deployment" if kind == "Deployment" else "statefulset"
        rc, out = run(["kubectl", "get", res, name, "-n", namespace, "-o", "json"])
        if rc != 0 or not out.strip():
            continue
        try:
            w = json.loads(out)
        except ValueError:
            continue
        desired += int(w.get("spec", {}).get("replicas", 0) or 0)
        ready += int(w.get("status", {}).get("readyReplicas", 0) or 0)
    return "{}/{}".format(ready, desired)


# --- per-service application health ----------------------------------------
# Health paths vary across services (some /health/ready with db+redis checks,
# some /health returning service-info, some /api/v1/health, some none). Probe in
# this order, deepest first; stop at the first that responds.
HEALTH_PATHS = ["health/ready", "api/v1/health", "healthz", "health"]
HEALTHY_TOKENS = {"ok", "healthy", "up", "ready", "pass", "serving"}


def _classify_health(body):
    """up | degraded from a non-empty health response body.

    A JSON status field outside HEALTHY_TOKENS -> degraded; any other response
    (plain text, or JSON service-info without a status field) -> up (it answered)."""
    body = body.strip()
    try:
        j = json.loads(body)
        if isinstance(j, dict):
            for key in ("status", "health", "state"):
                if key in j:
                    return "up" if str(j[key]).lower() in HEALTHY_TOKENS else "degraded"
        return "up"
    except ValueError:
        return "up"


def service_pod_readiness(namespace, selector):
    """(ready, total, restarts) over the pods a service selects.

    Universal fallback for services with no HTTP health route: k8s already runs
    whatever native probe the chart defines (tcpSocket/exec/grpc), and the pod
    Ready condition reflects it."""
    if not selector:
        return (0, 0, 0)
    sel = ",".join("{}={}".format(k, v) for k, v in selector.items())
    rc, out = run(["kubectl", "get", "pods", "-n", namespace, "-l", sel, "-o", "json"])
    if rc != 0 or not out.strip():
        return (0, 0, 0)
    try:
        items = json.loads(out).get("items", [])
    except ValueError:
        return (0, 0, 0)
    ready = total = restarts = 0
    for p in items:
        total += 1
        conds = (p.get("status", {}) or {}).get("conditions", []) or []
        if any(c.get("type") == "Ready" and c.get("status") == "True" for c in conds):
            ready += 1
        for cs in (p.get("status", {}) or {}).get("containerStatuses", []) or []:
            restarts = max(restarts, int(cs.get("restartCount", 0) or 0))
    return (ready, total, restarts)


def probe_service_health(namespace):
    """Per-service health. Returns [{name, port, path, state, source, ready, restarts}].

    source 'http'   — answered on an HTTP health path (deepest signal: app's own
                      db/redis checks); state up | degraded.
    source 'pod'    — no HTTP health route, fell back to k8s pod readiness (the
                      service's native probe); state up | degraded.
    source 'none'   — no HTTP route and no pods selected; state no-workload."""
    rc, out = run(["kubectl", "get", "svc", "-n", namespace, "-o", "json"])
    if rc != 0 or not out.strip():
        return []
    try:
        items = json.loads(out).get("items", [])
    except ValueError:
        return []
    results = []
    for svc in items:
        name = (svc.get("metadata", {}) or {}).get("name", "")
        spec = svc.get("spec", {}) or {}
        ports = spec.get("ports", []) or []
        if not name or not ports:
            continue
        port = ports[0].get("port")
        if not port:
            continue

        hit_path, state, source = None, None, None
        for path in HEALTH_PATHS:
            rc2, body = run([
                "kubectl", "get", "--raw",
                "/api/v1/namespaces/{}/services/{}:{}/proxy/{}".format(
                    namespace, name, port, path),
            ])
            if rc2 == 0 and body.strip():
                hit_path, state, source = "/" + path, _classify_health(body), "http"
                break

        ready, total, restarts = service_pod_readiness(namespace, spec.get("selector"))
        if source != "http":
            # Fall back to k8s pod readiness.
            if total == 0:
                state, source = "no-workload", "none"
            elif ready == total and ready > 0:
                state, source = "up", "pod"
            else:
                state, source = "degraded", "pod"

        results.append({
            "name": name, "port": port, "path": hit_path, "state": state,
            "source": source, "ready": "{}/{}".format(ready, total),
            "restarts": restarts,
        })
    return results


def collect_releases(namespace, manifest_path):
    product, product_version, manifest_rel = load_manifest_releases(manifest_path)
    deployed = helm_deployed(namespace)
    rows = []
    for name, expected in manifest_rel:
        d = deployed.get(name)
        ready = workload_ready(namespace, name) if d else "-"
        if d is None:
            rows.append({
                "name": name, "expected": expected, "chartVersion": "-",
                "appVersion": "-", "revision": "-", "status": "missing",
                "ready": "-", "drift": False, "missing": True,
            })
        else:
            rows.append({
                "name": name, "expected": expected,
                "chartVersion": d["chartVersion"], "appVersion": d["appVersion"],
                "revision": d["revision"], "status": d["status"], "ready": ready,
                "drift": (expected not in ("-", "") and d["chartVersion"] != expected),
                "missing": False,
            })
    return product, product_version, rows


def collect_dep_services(config_path):
    """Whitelisted, credential-free view of depServices from config.yaml."""
    try:
        with open(config_path) as f:
            cfg = yaml.safe_load(f) or {}
    except (IOError, OSError):
        return {}, []
    dep = cfg.get("depServices", {}) or {}
    out = []
    for name, spec in dep.items():
        spec = spec or {}
        safe = {"name": name, "installed": True}
        for k in DEP_WHITELIST.get(name, []):
            if k in spec:
                safe[k] = spec[k]
        # nested topology (no credentials)
        if name == "redis":
            ci = spec.get("connectInfo", {}) or {}
            for k in ("host", "port", "sentinelHost", "sentinelPort", "masterGroupName"):
                if k in ci:
                    safe[k] = ci[k]
        if name == "mq":
            mech = (spec.get("auth", {}) or {}).get("mechanism")
            if mech is not None:
                safe["mechanism"] = mech
        if name == "mongodb":
            asrc = (spec.get("options", {}) or {}).get("authSource")
            if asrc is not None:
                safe["authSource"] = asrc
        out.append(safe)
    access = cfg.get("accessAddress", {}) or {}
    meta = {
        "namespace": cfg.get("namespace", ""),
        "accessAddress": {
            "host": access.get("host", ""),
            "port": access.get("port", ""),
            "scheme": access.get("scheme", ""),
            "path": access.get("path", ""),
        },
        "image": {"registry": (cfg.get("image", {}) or {}).get("registry", "")},
    }
    return meta, out


# --- emit ------------------------------------------------------------------
def emit_json(namespace, product, product_version, rows, meta, deps, health, generated_at):
    snapshot = {
        "product": product,
        "version": product_version,
        "generatedAt": generated_at,
        "namespace": meta.get("namespace") or namespace,
        "accessAddress": meta.get("accessAddress", {}),
        "image": meta.get("image", {}),
        "releases": [
            {
                "name": r["name"],
                "chartVersion": r["chartVersion"],
                "appVersion": r["appVersion"],
                "status": r["status"],
                "ready": r["ready"],
            }
            for r in rows
        ],
        "depServices": deps,
        # Per-service app health: classified state only (no raw bodies — those can
        # carry GoVersion / internal topology). path = the health route that answered.
        "serviceHealth": health,
    }
    print(json.dumps(snapshot, indent=2, ensure_ascii=False))


def _trunc(s, n):
    s = str(s)
    return s if len(s) <= n else s[:n - 1] + "…"


def emit_table(namespace, product, product_version, rows, meta, deps, health):
    GREEN, YELLOW, RED, NC = "\033[0;32m", "\033[1;33m", "\033[0;31m", "\033[0m"
    print("BKN Foundry install status  —  product {} {}  ns {}".format(
        product, product_version, namespace))
    print("")
    fmt = "{:<28} {:<9} {:<24} {:<24} {:<4} {:<10} {:<7} {}"
    hdr = fmt.format(
        "RELEASE", "EXPECTED", "DEPLOYED", "APP", "REV", "STATUS", "READY", "")
    print(hdr)
    print("-" * len(hdr))
    n_ok = n_drift = n_missing = 0
    for r in rows:
        flag = ""
        color = GREEN
        if r["missing"]:
            flag, color = "MISSING", RED
            n_missing += 1
        elif r["drift"]:
            flag, color = "DRIFT", YELLOW
            n_drift += 1
        else:
            n_ok += 1
        line = fmt.format(
            _trunc(r["name"], 28), _trunc(r["expected"], 9),
            _trunc(r["chartVersion"], 24), _trunc(r["appVersion"], 24),
            r["revision"], r["status"], r["ready"], flag)
        print("{}{}{}".format(color, line, NC) if flag else line)
    print("")
    print("releases: {} ok, {} drift, {} missing  (of {})".format(
        n_ok, n_drift, n_missing, len(rows)))
    if deps:
        parts = []
        for d in deps:
            parts.append("{}✓".format(d["name"]))
        print("depServices: " + "  ".join(parts))
    else:
        print("depServices: none recorded (config.yaml missing or empty)")

    if health:
        print("")
        print("Service health (http = app health endpoint, pod = k8s readiness):")
        n_up = n_deg = n_none = 0
        for h in health:
            st = h["state"]
            if st == "up":
                color, mark = GREEN, "✓"
                n_up += 1
            elif st == "degraded":
                color, mark = YELLOW, "!"
                n_deg += 1
            else:
                color, mark = NC, "·"
                n_none += 1
            src = h.get("source") or "-"
            detail = h["path"] if h.get("source") == "http" else (
                "pods " + h.get("ready", "")) if h.get("source") == "pod" else "—"
            rst = h.get("restarts", 0)
            rtxt = "  (restarts {})".format(rst) if rst else ""
            line = "  {} {:<28} {:<12} {:<6} {}{}".format(
                mark, h["name"], st, src, detail, rtxt)
            print("{}{}{}".format(color, line, NC) if color != NC else line)
        print("  {} up, {} degraded, {} no-workload".format(n_up, n_deg, n_none))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--namespace", required=True)
    ap.add_argument("--manifest", required=True)
    ap.add_argument("--config", default="")
    ap.add_argument("--product", default="bkn-foundry")
    ap.add_argument("--format", choices=["table", "json"], default="table")
    ap.add_argument("--no-health", action="store_true",
                    help="skip per-service health probing (faster)")
    ap.add_argument("--generated-at", default="")
    args = ap.parse_args()

    product, product_version, rows = collect_releases(args.namespace, args.manifest)
    if not product:
        product = args.product
    meta, deps = ({}, [])
    if args.config:
        meta, deps = collect_dep_services(args.config)
    health = [] if args.no_health else probe_service_health(args.namespace)

    if args.format == "json":
        generated_at = args.generated_at or (
            datetime.utcnow().replace(microsecond=0).isoformat() + "Z")
        emit_json(args.namespace, product, product_version, rows, meta, deps,
                  health, generated_at)
    else:
        emit_table(args.namespace, product, product_version, rows, meta, deps, health)


if __name__ == "__main__":
    main()
