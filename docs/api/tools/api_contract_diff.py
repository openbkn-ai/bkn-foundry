#!/usr/bin/env python3
"""
api_contract_diff.py —— 对比"服务实际返回"与"OpenAPI 设计文档",输出字段级缺口报告。

与静态代码比对不同:本脚本真实调用运行中的服务,把实际响应 JSON 的字段结构
与文档声明的 200 响应 schema 逐字段比对,因此能覆盖静态分析看不到的部分
(handler 返回结构体、跨层组装、序列化 omitempty 等)。

用法
----
  # 走集群内部面(免 token),经 ssh + kubectl exec 发请求
  ./api_contract_diff.py --spec-dir docs/api \
      --exec-mode kubectl --ssh parallels@10.211.55.4 \
      --namespace openbkn --exec-pod deploy/bkn-agent \
      --face in --out report.md

  # 直连(本机可达服务时),走外部面,需要 token
  ./api_contract_diff.py --spec-dir docs/api --exec-mode direct \
      --face ex --token "$TOKEN" --out report.md

只读保证
--------
默认仅发送 GET。带 x-http-method-override:GET 语义的只读 POST 需显式 --include-query-post。
任何情况下都不会发送 PUT/DELETE,也不会发送不带 override 的 POST。

退出码:0 无缺口;1 有缺口;2 执行错误。
"""

import argparse
import json
import os
import re
import subprocess
import sys
from collections import OrderedDict

try:
    import yaml
except ImportError:
    sys.exit("需要 PyYAML: pip install pyyaml")


# --------------------------------------------------------------------------
# OpenAPI 解析
# --------------------------------------------------------------------------

class SpecLoader:
    """加载 spec 目录下全部 yaml,解析 $ref(含跨文件)/oneOf/anyOf/allOf。"""

    def __init__(self, spec_dir, skip_files=()):
        self.spec_dir = spec_dir
        self.skip = tuple(skip_files)
        self._cache = {}

    def _load(self, path):
        if path not in self._cache:
            with open(path) as fh:
                self._cache[path] = yaml.safe_load(fh) or {}
        return self._cache[path]

    def files(self):
        out = []
        for root, _, names in os.walk(self.spec_dir):
            if any(part in root for part in ("_shared", "_templates", "_generated")):
                continue
            for n in sorted(names):
                if not n.endswith((".yaml", ".yml")):
                    continue
                p = os.path.join(root, n)
                if any(s in p for s in self.skip):
                    continue
                out.append(p)
        return out

    def resolve(self, node, doc, path, depth=0):
        """展开 $ref,返回实际 schema 节点。"""
        if not isinstance(node, dict) or depth > 20:
            return node if isinstance(node, dict) else {}
        if "$ref" not in node:
            return node
        ref = node["$ref"]
        if ref.startswith("#/"):
            cur = doc
            for seg in ref[2:].split("/"):
                cur = cur.get(seg, {}) if isinstance(cur, dict) else {}
            return self.resolve(cur, doc, path, depth + 1)
        file_part, _, frag = ref.partition("#")
        target = os.path.normpath(os.path.join(os.path.dirname(path), file_part))
        if not os.path.exists(target):
            return {}
        doc2 = self._load(target)
        cur = doc2
        # frag 形如 "/components/schemas/X",只需去掉开头的一个 "/"
        for seg in (frag.lstrip("/").split("/") if frag else []):
            cur = cur.get(seg, {}) if isinstance(cur, dict) else {}
        return self.resolve(cur, doc2, target, depth + 1)

    MAX_DEPTH = 7

    def field_paths(self, schema, doc, path, prefix="", depth=0, seen=None,
                    required=True, out=None):
        """把 schema 摊平成 {字段路径: (类型, 是否 required 链)}。

        seen 记录已展开的 $ref,避免递归 schema(如 condition.sub_conditions)
        无限自引用炸出成百上千条无意义路径。
        required 表示从根到此字段是否整条链都在 required 列表里 —— 只有整条
        链必填的字段"未返回"才算硬缺口,其余归入可选未观测。
        """
        if out is None:
            out = OrderedDict()
        if seen is None:
            seen = frozenset()
        if depth > self.MAX_DEPTH:
            return out

        ref_key = schema.get("$ref") if isinstance(schema, dict) else None
        if ref_key:
            if ref_key in seen:
                return out           # 环:停止展开
            seen = seen | {ref_key}
        schema = self.resolve(schema, doc, path)
        if not isinstance(schema, dict):
            return out

        for key in ("oneOf", "anyOf", "allOf"):
            for sub in schema.get(key) or []:
                # oneOf / anyOf 只需满足其中一个分支,分支内的 required 不能
                # 当成全局必填(否则 action_source 的 Tool 形态与 MCP 形态会
                # 互相报对方的字段缺失)。allOf 是合取,保留 required。
                sub_req = required if key == "allOf" else False
                self.field_paths(sub, doc, path, prefix, depth + 1, seen, sub_req, out)

        if schema.get("type") == "array" or "items" in schema:
            # 记下数组形态本身。oneOf 分支里的数组(如 mapping_rules 的 direct
            # 形态)否则只会留下父级的 object 记录,和实际的 `key[]` 对不上。
            if prefix:
                out.setdefault(prefix + "[]",
                               ((schema.get("items") or {}).get("type", "object"),
                                required))
            self.field_paths(schema.get("items", {}), doc, path, prefix + "[]",
                             depth + 1, seen, required, out)
            return out

        # additionalProperties 表示该节点的 key 是开放的:
        #   true      —— 完全 opaque(如工具执行结果)
        #   {schema}  —— key 任意、值符合该 schema(如 map[string]FieldConfig)
        # 两种都用 "<prefix>.*" 这个通配路径表示,实际返回的 key 会被归一化到它。
        ap = schema.get("additionalProperties")
        if prefix and ap is True:
            out.setdefault(prefix + ".*", ("any", False))
        elif prefix and isinstance(ap, dict) and ap:
            out.setdefault(prefix + ".*", ("object", False))
            self.field_paths(ap, doc, path, prefix + ".*",
                             depth + 1, seen, False, out)

        req_names = set(schema.get("required") or [])
        for name, sub in (schema.get("properties") or {}).items():
            sub_ref = sub.get("$ref") if isinstance(sub, dict) else None
            sub_r = self.resolve(sub, doc, path)
            key = f"{prefix}.{name}" if prefix else name
            child_req = required and (name in req_names)
            t = sub_r.get("type")
            if t == "array":
                out[key + "[]"] = ((sub_r.get("items") or {}).get("type", "object"), child_req)
                if not (sub_ref and sub_ref in seen):
                    self.field_paths(sub_r.get("items", {}), doc, path, key + "[]",
                                     depth + 1, seen | ({sub_ref} if sub_ref else set()),
                                     child_req, out)
            else:
                out[key] = (t or "object", child_req)
                # 属性值可能是 oneOf/anyOf/allOf 组合(如 mapping_rules 按
                # 关系类型分三种形态),这类节点既无 type 也无 properties,
                # 漏掉就等于整条分支从未展开。
                combi = any(k in sub_r for k in ("oneOf", "anyOf", "allOf"))
                if (t == "object" or "properties" in sub_r or combi) \
                        and not (sub_ref and sub_ref in seen):
                    self.field_paths(sub_r, doc, path, key, depth + 1,
                                     seen | ({sub_ref} if sub_ref else set()),
                                     child_req, out)
        return out

    def operations(self, face="ex"):
        """产出所有操作:(module, method, url, doc_schema_fields, op_meta)"""
        ops = []
        for p in self.files():
            doc = self._load(p)
            server = ""
            for s in doc.get("servers") or []:
                server = s.get("url", "")
                break
            server = re.sub(r"^https?://[^/]+", "", server).rstrip("/")
            module = os.path.basename(os.path.dirname(p))
            if module == os.path.basename(self.spec_dir):
                module = os.path.splitext(os.path.basename(p))[0]
            for raw_path, item in (doc.get("paths") or {}).items():
                if not isinstance(item, dict):
                    continue
                shared_params = item.get("parameters") or []
                for method, op in item.items():
                    if method not in ("get", "post", "put", "patch", "delete"):
                        continue
                    if not isinstance(op, dict):
                        continue
                    url = server + raw_path
                    if face == "in":
                        url = re.sub(r"(/api/[^/]+)/v1", r"\1/in/v1", url)
                    resp = (op.get("responses") or {}).get(200) \
                        or (op.get("responses") or {}).get("200") or {}
                    resp = self.resolve(resp, doc, p)
                    schema = None
                    for ctype, media in (resp.get("content") or {}).items():
                        if "json" in ctype:
                            schema = media.get("schema", {})
                            break
                    params = list(shared_params) + list(op.get("parameters") or [])
                    ops.append({
                        "module": module,
                        "method": method.upper(),
                        "url": url,
                        "spec_file": p,
                        "doc_fields": self.field_paths(schema, doc, p) if schema is not None else None,
                        "params": [self.resolve(x, doc, p) for x in params],
                        "has_body": bool(op.get("requestBody")),
                        "override": self._override_header(op, doc, p),
                    })
        return ops

    def _override_header(self, op, doc, path):
        for prm in (op.get("parameters") or []):
            prm = self.resolve(prm, doc, path)
            if prm.get("in") == "header" and prm.get("name", "").lower() == "x-http-method-override":
                return True
        return False


# --------------------------------------------------------------------------
# 实际响应结构
# --------------------------------------------------------------------------

def actual_paths(value, prefix="", depth=0, out=None):
    """把实际响应 JSON 摊平成 {字段路径: 类型},与 field_paths 同构。"""
    if out is None:
        out = OrderedDict()
    if depth > 8:
        return out
    if isinstance(value, dict):
        for k, v in value.items():
            key = f"{prefix}.{k}" if prefix else k
            if isinstance(v, list):
                out[key + "[]"] = _jtype(v[0]) if v else "unknown"
                if v:
                    actual_paths(v[0], key + "[]", depth + 1, out)
            else:
                out[key] = _jtype(v)
                if isinstance(v, dict):
                    actual_paths(v, key, depth + 1, out)
    elif isinstance(value, list):
        out[prefix + "[]"] = _jtype(value[0]) if value else "unknown"
        if value:
            actual_paths(value[0], prefix + "[]", depth + 1, out)
    return out


def _jtype(v):
    if v is None:
        return "null"
    if isinstance(v, bool):
        return "boolean"
    if isinstance(v, int):
        return "integer"
    if isinstance(v, float):
        return "number"
    if isinstance(v, str):
        return "string"
    if isinstance(v, list):
        return "array"
    return "object"


TYPE_EQUIV = {
    ("integer", "number"), ("number", "integer"),
    ("integer", "string"), ("string", "integer"),  # id 常以字符串下发
}


def type_ok(doc_t, act_t):
    if act_t in ("null", "unknown") or doc_t in (None, "object", ""):
        return True
    if doc_t == act_t:
        return True
    return (doc_t, act_t) in TYPE_EQUIV


# --------------------------------------------------------------------------
# 请求执行
# --------------------------------------------------------------------------

PROBE_SRC = r'''
import json, sys, urllib.request, urllib.error
reqs = json.loads(sys.stdin.read())
out = []
for r in reqs:
    body = r.get("body")
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(r["url"], data=data, method=r["method"],
                                 headers=r.get("headers") or {})
    item = {"id": r["id"]}
    try:
        resp = urllib.request.urlopen(req, timeout=r.get("timeout", 20))
        item["status"] = resp.status
        raw = resp.read()
        try:
            item["json"] = json.loads(raw.decode() or "null")
        except Exception:
            item["text"] = raw[:400].decode("utf-8", "replace")
    except urllib.error.HTTPError as e:
        item["status"] = e.code
        item["text"] = e.read()[:400].decode("utf-8", "replace")
    except Exception as e:
        item["status"] = 0
        item["error"] = str(e)[:200]
    out.append(item)
print(json.dumps(out))
'''


class Executor:
    # probe 内部每个请求 20s 超时且串行，外层按请求数给出上界，
    # 再留 60s 覆盖 ssh 握手与 kubectl exec 建流。
    PER_REQUEST_TIMEOUT = 20
    SETUP_TIMEOUT = 60

    def __init__(self, args):
        self.args = args

    def run(self, requests):
        if not requests:
            return {}
        payload = json.dumps(requests)
        timeout = self.SETUP_TIMEOUT + self.PER_REQUEST_TIMEOUT * len(requests)
        if self.args.exec_mode == "direct":
            cmd = [sys.executable, "-c", PROBE_SRC]
        else:
            remote = ["kubectl", "exec", "-n", self.args.namespace, "-i",
                      self.args.exec_pod, "--", "python3", "-c", PROBE_SRC]
            # BatchMode 让缺密钥时立即失败而非等交互输入，ConnectTimeout 兜住网络不通
            cmd = ["ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10",
                   self.args.ssh, " ".join(_q(c) for c in remote)] \
                if self.args.ssh else remote
        try:
            proc = subprocess.run(cmd, input=payload, capture_output=True,
                                  text=True, timeout=timeout)
        except subprocess.TimeoutExpired:
            raise RuntimeError(
                f"probe 执行超时({timeout}s，{len(requests)} 个请求)："
                "ssh 或目标 pod 无响应") from None
        if proc.returncode != 0:
            raise RuntimeError(f"probe 执行失败: {proc.stderr[:400]}")
        line = proc.stdout.strip().splitlines()[-1] if proc.stdout.strip() else "[]"
        return {r["id"]: r for r in json.loads(line)}


def _q(s):
    return "'" + s.replace("'", "'\\''") + "'" if re.search(r"[\s\"'$]", s) else s


def sample_value(param):
    """给必填 query 参数造一个合规取值。

    不猜业务语义，只按 schema 声明来：example > default > enum 首项 >
    按 type/format 生成。造不出来就返回 None，让调用方把该接口标为
    「需要人工提供参数」而不是发一个必然 400 的请求。
    """
    sch = param.get("schema") or {}
    for src in (param.get("example"), sch.get("example"), sch.get("default")):
        if src is not None:
            return str(src)
    if sch.get("enum"):
        return str(sch["enum"][0])
    fmt, typ = sch.get("format"), sch.get("type")
    if fmt == "date":
        return "2000-01-01"
    if fmt == "date-time":
        return "2000-01-01T00:00:00Z"
    if typ in ("integer", "number"):
        return str(sch.get("minimum", 0))
    if typ == "boolean":
        return "false"
    return None


# --------------------------------------------------------------------------
# 路径参数发现
# --------------------------------------------------------------------------

# (列表接口, 参数名, 作用域前缀)
# 作用域:发现到的值只用于填充该前缀下的 URL。像 `id` 这种通用参数名在
# catalogs / resources / build-tasks 上都出现,不区分作用域就会拿 catalog 的
# id 去查 resource,必然 404。
DISCOVERY = [
    # kn_id 是跨服务通用的(bkn-backend 与 ontology-query 都用),作用域放到 /api/
    ("/api/bkn-backend/{v}/knowledge-networks", ["kn_id"], "/api/"),
    ("/api/vega-backend/{v}/catalogs", ["id"], "/api/vega-backend/{v}/catalogs"),
    ("/api/vega-backend/{v}/resources", ["id"], "/api/vega-backend/{v}/resources"),
    ("/api/vega-backend/{v}/build-tasks", ["id", "ids"], "/api/vega-backend/{v}/build-tasks"),
    ("/api/vega-backend/{v}/discover-tasks", ["id", "ids"], "/api/vega-backend/{v}/discover-tasks"),
    ("/api/vega-backend/{v}/discover-schedules", ["id"], "/api/vega-backend/{v}/discover-schedules"),
    ("/api/vega-backend/{v}/semantic-understanding-tasks", ["id", "ids"],
     "/api/vega-backend/{v}/semantic-understanding-tasks"),
    ("/api/vega-backend/{v}/connector-types", ["type"], "/api/vega-backend/{v}/connector-types"),
]
# 依赖 kn_id 的二级资源
_KN = "/api/bkn-backend/{v}/knowledge-networks/{kn}"
SUB_DISCOVERY = [
    # 每个二级资源的 id 只在自己的路径下有效。relation-types 与 risk-types
    # 在文档里都用 rt_id/rt_ids,不隔离就会拿关系类 id 去查风险类。
    (_KN + "/object-types", ["ot_id", "ot_ids", "ob_id", "ob_ids"], _KN + "/object-types"),
    (_KN + "/relation-types", ["rt_id", "rt_ids"], _KN + "/relation-types"),
    (_KN + "/action-types", ["at_id", "at_ids"], _KN + "/action-types"),
    (_KN + "/concept-groups", ["group_id", "cg_id"], _KN + "/concept-groups"),
    (_KN + "/metrics", ["metric_id", "metric_ids"], _KN + "/metrics"),
    (_KN + "/action-schedules", ["schedule_id", "schedule_ids"], _KN + "/action-schedules"),
    (_KN + "/risk-types", ["rt_id", "rt_ids"], _KN + "/risk-types"),
    # ontology-query 的执行/日志 id 只能从 action-logs 列表里取
    ("/api/ontology-query/{v}/knowledge-networks/{kn}/action-logs",
     ["log_id", "execution_id"], "/api/ontology-query/"),
]


ID_KEYS = ("id", "type", "log_id", "execution_id", "name")


def _pick_id(entry):
    for k in ID_KEYS:
        if entry.get(k) not in (None, ""):
            return str(entry[k])
    return None


def discover_ids(execu, host_of, face, headers, seed):
    """真实调用列表接口,收集路径参数值。

    返回 [(scope_prefix, param_name, value)];填充时按 scope 最长匹配,
    避免通用参数名(id/ids)跨资源串用。
    """
    v = "in/v1" if face == "in" else "v1"
    found = [("/api/", k, str(val)) for k, val in (seed or {}).items()]

    def sweep(specs, fmt):
        reqs, plan = [], []
        for tmpl, names, scope in specs:
            path = tmpl.format(**fmt)
            host = host_of(path)
            if not host:
                continue
            rid = f"disc:{path}"
            reqs.append({"id": rid, "method": "GET",
                         "url": host + path + "?limit=5", "headers": headers})
            plan.append((rid, names, scope.format(**fmt)))
        if not reqs:
            return
        res = execu.run(reqs)
        for rid, names, scope in plan:
            body = res.get(rid, {}).get("json") or {}
            entries = body.get("entries") if isinstance(body, dict) else None
            if not entries:
                continue
            val = _pick_id(entries[0])
            if val is None:
                continue
            for n in names:
                found.append((scope, n, val))

    sweep(DISCOVERY, {"v": v})
    kn = next((val for _, n, val in found if n == "kn_id"), None)
    if kn:
        sweep(SUB_DISCOVERY, {"v": v, "kn": kn})
    return found


def fill_path(url, found):
    """用作用域最长匹配的值填充 URL 中的 {param};返回 (url, 未解析参数)。"""
    missing = []
    for name in re.findall(r"\{([^}]+)\}", url):
        cands = [(len(scope), val) for scope, n, val in found
                 if n == name and url.startswith(scope)]
        if not cands:
            missing.append(name)
            continue
        url = url.replace("{" + name + "}", max(cands)[1])
    return url, missing


# --------------------------------------------------------------------------
# 主流程
# --------------------------------------------------------------------------

DEFAULT_HOSTS = {
    "/api/bkn-backend/": "http://bkn-backend-svc:13014",
    "/api/ontology-query/": "http://ontology-query-svc:13018",
    "/api/vega-backend/": "http://vega-backend-svc:13014",
    "/api/mf-model-manager/": "http://mf-model-manager:9898",
    "/api/bkn-agent/": "http://bkn-agent:30800",
}


def main():
    ap = argparse.ArgumentParser(description="对比服务实际返回与 OpenAPI 文档")
    ap.add_argument("--spec-dir", required=True)
    ap.add_argument("--face", choices=["ex", "in"], default="in",
                    help="ex=外部面(需 token);in=内部面(免 token)")
    ap.add_argument("--exec-mode", choices=["kubectl", "direct"], default="kubectl")
    ap.add_argument("--ssh", default=None, help="如 parallels@10.211.55.4")
    ap.add_argument("--namespace", default="openbkn")
    ap.add_argument("--exec-pod", default="deploy/bkn-agent")
    ap.add_argument("--token", default=None)
    ap.add_argument("--account-id", default="admin",
                    help="内部面 x-account-id。必须是真实账号 UUID,否则会被授权过滤成空集")
    ap.add_argument("--account-type", default="user")
    ap.add_argument("--host-map", default=None,
                    help='JSON,覆盖默认服务地址,如 \'{"/api/bkn-backend/":"http://h:13014"}\'')
    ap.add_argument("--ids", default=None,
                    help='JSON,手工指定路径参数,如 \'{"kn_id":"abc"}\'')
    ap.add_argument("--include-query-post", action="store_true",
                    help="额外请求带 x-http-method-override:GET 的只读 POST")
    # bkn-agent 尚无稳定巡检入口；agent-observability 当前发布 Swagger 2.0
    # 且只提供外部路径，没有本工具默认 face=in 所需的 /in/v1 路由。两者的
    # 静态合同分别由自身 CI 校验，待巡检器支持对应调用面后再移出默认跳过项。
    ap.add_argument("--skip-file", action="append",
                    default=["bkn-agent.yaml", "agent-observability.yaml"])
    ap.add_argument("--out", default="api_contract_report.md")
    ap.add_argument("--json-out", default=None)
    args = ap.parse_args()

    hosts = dict(DEFAULT_HOSTS)
    if args.host_map:
        hosts.update(json.loads(args.host_map))

    def host_of(url):
        for pref, h in hosts.items():
            if url.startswith(pref):
                return h
        return None

    headers = {"Content-Type": "application/json"}
    if args.face == "in":
        headers.update({"x-account-id": args.account_id,
                        "x-account-type": args.account_type})
    if args.token:
        headers["Authorization"] = "Bearer " + args.token

    loader = SpecLoader(args.spec_dir, skip_files=args.skip_file)
    ops = loader.operations(face=args.face)
    execu = Executor(args)

    seed = json.loads(args.ids) if args.ids else {}
    ids = discover_ids(execu, host_of, args.face, headers, seed)

    # 组装请求:只读
    reqs, index = [], {}
    for i, op in enumerate(ops):
        if op["doc_fields"] is None:
            continue
        readonly_post = op["method"] == "POST" and op["override"] and args.include_query_post
        if op["method"] != "GET" and not readonly_post:
            continue
        url, missing = fill_path(op["url"], ids)
        if missing:
            op["skip"] = "缺少路径参数 " + ",".join(missing)
            continue
        host = host_of(url)
        if not host:
            op["skip"] = "无服务地址映射"
            continue
        # 必填 query 参数不带就是必然 400，按 schema 造一个合规取值
        required_q = [p for p in op["params"]
                      if p.get("in") == "query" and p.get("required")]
        qs, unfillable = [], []
        for p in required_q:
            v = sample_value(p)
            if v is None:
                unfillable.append(p.get("name"))
            else:
                qs.append(f"{p['name']}={v}")
        if unfillable:
            op["skip"] = "必填 query 参数无法自动取值 " + ",".join(unfillable)
            continue
        if qs:
            url = url + ("&" if "?" in url else "?") + "&".join(qs)
            op["filled_query"] = qs

        rid = f"op{i}"
        h = dict(headers)
        body = None
        if readonly_post:
            h["x-http-method-override"] = "GET"
            body = {}
        reqs.append({"id": rid, "method": "POST" if readonly_post else "GET",
                     "url": host + url, "headers": h, "body": body})
        index[rid] = op
        op["req_url"] = url

    results = execu.run(reqs)

    for rid, op in index.items():
        r = results.get(rid) or {}
        op["status"] = r.get("status")
        if r.get("status") != 200:
            op["skip"] = f"HTTP {r.get('status')} {r.get('error') or (r.get('text') or '')[:120]}"
            continue
        if "json" not in r:
            op["skip"] = "响应非 JSON"
            continue
        op["actual"] = actual_paths(r["json"])

    report, stats = render(ops, ids, args)
    with open(args.out, "w") as fh:
        fh.write(report)
    if args.json_out:
        with open(args.json_out, "w") as fh:
            json.dump([{k: v for k, v in o.items() if k != "params"} for o in ops],
                      fh, ensure_ascii=False, indent=1, default=str)
    print(report)
    return 1 if stats["gap_ops"] else 0


def render(ops, ids, args):
    checked, gaps, skipped = [], [], []
    for op in ops:
        if op.get("doc_fields") is None:
            continue
        if "actual" not in op:
            if op.get("skip"):
                skipped.append(op)
            continue
        doc, act = op["doc_fields"], op["actual"]
        # 实际返回了、文档没写
        # 开放 key 的节点(additionalProperties):把实际路径里紧跟其后的那一
        # 段折成 "*",再和文档比。例:field_config.username.name 在文档里对应
        # field_config.*.name。
        # additionalProperties: true  -> 整棵子树放行(内容完全 opaque)
        # additionalProperties: {..}  -> key 折成 "*" 后继续逐字段比对
        free = tuple(k[:-2] + "." for k in doc
                     if k.endswith(".*") and doc[k][0] == "any")
        typed = sorted((k[:-2] for k in doc
                        if k.endswith(".*") and doc[k][0] != "any"),
                       key=len, reverse=True)

        def canon(k):
            for base in typed:
                if k.startswith(base + "."):
                    rest = k[len(base) + 1:].split(".", 1)
                    return base + ".*" + ("." + rest[1] if len(rest) > 1 else "")
            return k

        def known(k):
            if free and k.startswith(free):
                return True
            for cand in (k, canon(k)):
                if cand in doc:
                    return True
                alt = cand[:-2] if cand.endswith("[]") else cand + "[]"
                if alt in doc:
                    return True
            return False

        missing = [k for k in act if not known(k)]
        # 类型不符
        badtype = [(k, doc[k][0], act[k]) for k in doc
                   if k in act and not type_ok(doc[k][0], act[k])]
        # 文档写了、实际没返回。空数组的子字段无法观测,先剔除
        empties = {k[:-2] for k, t in act.items() if k.endswith("[]") and t == "unknown"}
        # `k` 与 `k[]` 是同一字段的两种记法(schema 用 oneOf 描述"可能是数组"
        # 时两者都会出现)。任一形态被实际返回,就不算缺失。
        def satisfied(k):
            alt = k[:-2] if k.endswith("[]") else k + "[]"
            return k in act or alt in act

        absent_all = [k for k in doc if ".*" not in k and not satisfied(k)
                      and not any(k.startswith(e) for e in empties)]
        # 父级本身就没返回时,其子路径是连带的,不重复报
        absent_roots = []
        for k in sorted(absent_all, key=len):
            if not any(k.startswith(r) and k != r for r in absent_roots):
                absent_roots.append(k)
        required_absent = [k for k in absent_roots if doc[k][1]]
        optional_absent = [k for k in absent_roots if not doc[k][1]]

        if missing or required_absent or badtype:
            op["_missing"] = missing
            op["_absent"] = required_absent
            op["_optional"] = optional_absent
            op["_badtype"] = badtype
            gaps.append(op)
        checked.append(op)

    with_schema = [o for o in ops if o.get("doc_fields") is not None]
    probeable = [o for o in with_schema
                 if o["method"] == "GET" or (o["override"] and args.include_query_post)]
    L = []
    L.append("# API 实际返回 vs 设计文档 —— 缺口报告\n")
    L.append(f"- 面:`{args.face}`  执行方式:`{args.exec_mode}`"
             + (f"  经 `{args.ssh}`" if args.ssh else ""))
    L.append(f"- 文档中带 200 响应 schema 的操作:{len(with_schema)}"
             f",其中只读可探测:{len(probeable)}"
             f"(其余为 POST/PUT/DELETE,本工具不发送)")
    L.append(f"- 实际请求成功并完成比对:**{len(checked)} / {len(probeable)}**")
    L.append(f"- 存在缺口:**{len(gaps)}**   未能比对:{len(skipped)}")
    uniq = sorted({(n, v) for _, n, v in ids})
    L.append("- 自动发现的路径参数:`"
             + json.dumps(dict(uniq), ensure_ascii=False) + "`\n")

    if gaps:
        L.append("## 缺口明细\n")
        for op in sorted(gaps, key=lambda o: (o["module"], o["req_url"])):
            L.append(f"### `{op['method']} {op['req_url']}`")
            L.append(f"模块 `{op['module']}` · 文档 `{os.path.relpath(op['spec_file'], args.spec_dir)}`\n")
            for k, dt, at in op["_badtype"]:
                L.append(f"- **类型不符** `{k}`:文档声明 `{dt}`,实际返回 `{at}`")
            if op["_absent"]:
                L.append("- **文档标为必填但实际未返回**:`" + "`, `".join(op["_absent"][:25]) + "`"
                         + (f" …另 {len(op['_absent'])-25} 项" if len(op["_absent"]) > 25 else ""))
            if op["_missing"]:
                L.append("- **实际返回但文档未声明**:`" + "`, `".join(op["_missing"][:25]) + "`"
                         + (f" …另 {len(op['_missing'])-25} 项" if len(op["_missing"]) > 25 else ""))
            if op["_optional"]:
                L.append(f"- 可选字段本次未观测到 {len(op['_optional'])} 项"
                         f"(如 `{'`, `'.join(op['_optional'][:5])}`)—— 可能是 omitempty"
                         f"或该视图不返回,非必然缺陷")
            L.append("")
    else:
        L.append("## 缺口明细\n\n比对成功的接口全部一致。\n")

    if skipped:
        L.append("## 未能比对(需人工跟进)\n")
        by = {}
        for op in skipped:
            by.setdefault(op["skip"].split()[0], []).append(op)
        for reason, group in sorted(by.items()):
            L.append(f"**{reason}** —— {len(group)} 个")
            for op in group[:40]:
                L.append(f"- `{op['method']} {op.get('req_url') or op['url']}` — {op['skip']}")
            if len(group) > 40:
                L.append(f"- …另 {len(group)-40} 个")
            L.append("")
    return "\n".join(L), {"gap_ops": len(gaps), "checked": len(checked)}


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception as exc:  # noqa: BLE001
        print(f"执行错误: {exc}", file=sys.stderr)
        sys.exit(2)
