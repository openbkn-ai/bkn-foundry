# 执行工厂 API 文档

> 执行工厂（服务名 `agent-operator-integration`）HTTP API 的 OpenAPI 3.0.3 定义。
> 平台的「能力」都在这里落地：一段代码、一个算子、一箱工具、一个 MCP、一个 Skill，最终都通过它注册、调试、发布与执行。

## 文件索引

| 文件 | 主题 | 包含的端点（`/api/agent-operator-integration/v1` 下） |
|---|---|---|
| [function.yaml](function.yaml) | 函数 | `POST /function/execute`、`POST /function/infer-schema`、`GET /function/dependencies`、`GET /function/dependency-versions/{package_name}`、`GET /template/{template_type}`、`POST /ai_generate/function/{type}`、`GET /ai_generate/prompt/{type}` |
| [sandbox.yaml](sandbox.yaml) | 沙箱观测 | `GET /sandbox/health`、`GET /sandbox/pool`、`GET /sandbox/sessions`、`GET /sandbox/sessions/{id}` |
| [impex.yaml](impex.yaml) | 导入导出 | `GET /impex/export/{type}/{id}`、`POST /impex/import/{type}` |
| [operator.yaml](operator.yaml) | 算子 | 注册 / 编辑 / 更新 / 列表 / 详情 / 批量取名 / 状态 / 删除 / 调试 / 历史版本 / 市场 / 分类 / 内置算子，共 15 条 |
| [mcp.yaml](mcp.yaml) | MCP | 探测 / 增删改查 / 状态 / 工具调试 / 市场 3 条 / 代理列工具与调用 / 对外端点 3 条，共 16 条 |
| [toolbox.yaml](toolbox.yaml) | 工具箱 | 工具箱 CRUD 与状态 / 箱内工具增删改查与启停 / 调试与代理调用 / 算子转工具 / OpenAPI 能力包 / 市场 4 条，共 22 条 |
| [skill.yaml](skill.yaml) | Skill | 注册 / 列表 / 详情 / 元数据与包更新 / 发布与历史 / 市场 2 条 / 消费态与管理态读取各 3 条 / 执行 / 索引构建 5 条，共 25 条 |

**公开面 91 条已全部收录。**

## 写一个函数：完整走一遍

**1. 看骨架**——入口函数必须叫 `handler`，这是硬约定。

```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  "$BASE/api/agent-operator-integration/v1/template/python"
```

**2. 看沙箱里已经有什么库**——列表里有的直接 `import`，不用在 `dependencies` 里再声明。

```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  "$BASE/api/agent-operator-integration/v1/function/dependencies"
```

**3. 跑起来**。`event` 是唯一入参，`handler` 的返回值进 `result`，`print` 进 `stdout`。

```bash
curl -s -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  "$BASE/api/agent-operator-integration/v1/function/execute" -d '{
    "code": "from typing import Dict, Any\n\ndef handler(event: Dict[str, Any]) -> Any:\n    name = event.get(\"name\", \"world\")\n    print(f\"greeting {name}\")\n    return {\"message\": f\"Hello, {name}\"}\n",
    "event": {"name": "BKN"}
  }'
```

```json
{
  "stdout": "greeting BKN\n",
  "stderr": "",
  "result": { "message": "Hello, BKN" },
  "metrics": {
    "duration_ms": 71.97115616872907,
    "cpu_time_ms": 3.9721400000019003,
    "peak_memory_mb": null,
    "io_read_bytes": null,
    "io_write_bytes": null
  },
  "exit_code": 0,
  "execution_time_ms": 0,
  "artifacts": [],
  "session_id": "sess_aoi_0"
}
```

> 上面这段是测试服的真实返回。`peak_memory_mb` 与 IO 计数依赖沙箱运行时能力，取不到就是 `null`。

**4. 要用第三方库**——先查版本，再声明依赖。首次执行会因装包变慢。

```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  "$BASE/api/agent-operator-integration/v1/function/dependency-versions/requests?python_version=3.10"
```

```jsonc
// 然后在 execute 请求体里加：
"dependencies": [{ "name": "requests", "version": "2.32.3" }],
"dependencies_url": "https://pypi.tuna.tsinghua.edu.cn/simple/"   // 内网换私有源
```

**5. 不想自己写**——用大模型生成，或反过来由代码推出参数定义。

```bash
# 由描述生成代码
curl -s -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  "$BASE/api/agent-operator-integration/v1/ai_generate/function/python_function_generator" \
  -d '{"query": "写一个函数，接收订单列表，返回总金额和订单数"}'

# 由代码反推入参 / 出参定义
curl -s -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  "$BASE/api/agent-operator-integration/v1/ai_generate/function/metadata_param_generator" \
  -d '{"code": "def handler(event):\n    return {}\n"}'
```

## 三个最容易踩的点

- **入口函数名固定是 `handler`**，签名 `handler(event: Dict[str, Any]) -> Any`。改名不会报「找不到入口」，而是行为不符合预期。
- **代码抛异常时接口仍返回 200**。判断成败看 `exit_code`（0 才是成功）与 `stderr`，不能只看 HTTP 状态码。
- **`timeout` 单位是秒**。内部面的 `POST /internal-v1/function/exec/{version}` 用的是毫秒，两者不一致，别照搬。

## 约定

- **OpenAPI 版本**：3.0.3。
- **认证**：`Authorization: Bearer <token>`，OAuth access token 或用户自助签发的 AppKey（`bak_` 前缀）。
- **权限**：函数执行要求算子的 `execute` 权限，AI 生成要求 `create` 权限，返回 403 时先查角色授权。
- **错误信封**：本服务**不用** `kweaver-go-lib/rest.BaseError`，字段是 `code` / `description` / `solution` / `link` / `details`，引 [`_shared/errors.yaml#/components/schemas/ErrorCompact`](../_shared/errors.yaml)（与 context-loader 同源同形）。
- **内部接口**：`/api/agent-operator-integration/internal-v1` 是内部面，另有 `POST /function/exec/{version}`（按已注册的函数版本执行，`timeout` 单位毫秒）等端点，**本文档不收录**。
- **能力面**：`/api/capabilities-lab/v1` 是合并进本服务的另一套路由（原 capabilities-lab 独立服务），也挂在 Ingress 上，路径与语义都与 `v1` 不同，**本文档暂不收录**。
- **`*_time` 一律是纳秒**：算子 / MCP / 工具箱 / Skill 的 `*_time` 字段都由 `time.Now().UnixNano()` 生成，形如 `1784880971306127803`；按毫秒解析会得到 1970 年附近的日期。例外是沙箱观测面，它的 `created_at` / `last_used_at` / `checked_at` 是 RFC3339 字符串——两种记法看字段后缀区分（`*_time` 是纳秒整数，`*_at` 是字符串）。
- **契约巡检**：只读 GET **默认就在探测范围内，不需要标注**。本模块 13 处 `x-contract-probe` 只加在「需要分批」的端点上——列表接口用 `provides` 把 `box_id` / `skill_id` / `mcp_id` 等喂给后续批次的详情接口，普通 GET 路径是一次性并发、拿不到上一条的产出。机制见 [`tools/README.md`](../tools/README.md)。

## 收录范围为什么是这些

只收 **Ingress 暴露的公开面** `/api/agent-operator-integration/v1`——那是浏览器
（Studio）能够到达的面。内部面 `internal-v1` **刻意不挂 Ingress**：它不校验令牌，
身份取自调用方自填的 `X-Account-ID` 头，该头缺失时调用者会被降级为硬编码管理员，
一旦暴露，其下约 40 条写接口即可从集群外无凭据调用（见 chart values 注释与 #326）。
因此本文档不描述内部面，也请不要把它写进任何对外文档。

## 覆盖边界

**公开面 `/api/agent-operator-integration/v1` 的 91 个端点已全部收录**：
函数 7 + 沙箱观测 4 + 导入导出 2 + 算子 15 + MCP 16 + 工具箱 22 + Skill 25。

> 端点总数两次修正：89 → 90（MCP 的 `Any /mcp/app/{mcp_id}/mcp` 被漏，抽路由的
> 正则没算 `Any`）→ 91（`POST /function/infer-schema` 被漏，最初枚举时读的是过期
> 分支上的 handler 文件）。现在的数字与代码 `RegisterPublic` 逐条对过，
> 并用实机访问日志交叉验证过没有「日志里有、文档里没有」的路径。

### 验证程度分两级，不要混为一谈

- **路由与收录范围**：全部从代码的 `RegisterPublic` 逐条核过，91 条不多不少。
- **字段级**：只有实机打过的才算验证过（见下节），其余按 Go 类型写成，
  **未经实机验证**，改动时请人工核对。

服务目录下 `adp/execution-factory/operator-integration/docs/apis/` 里有一份历史
草稿可作参照，但它与实现存在漂移（context-loader 的同类草稿实测就有三处写错），
不要直接当作真相源。

### 实机验证覆盖

在开发 VM（`parallels@10.211.55.4`，镜像
`0.1.3-main.20260730112246.sha185a9c2`）跑契约巡检，**91 条里 30 条完成字段级比对、
缺口 0**；但其中 4 条的响应是空列表，数组元素的字段本次无从观测，因此**真正被逐字段
核过的是 26 条**：

```bash
make api-contract-diff CONTRACT_FACE=ex CONTRACT_SSH=parallels@10.211.55.4 \
     CONTRACT_ARGS="--include-probe-post --token $TOKEN"
```

91 条的去向如下。除「完成字段级比对」外都不是「验过没问题」，接手时请自行核对：

| 类别 | 条数 | 说明 |
|---|---|---|
| 完成字段级比对 | 30 | 缺口 0。其中 4 条响应样本为空（`function/dependencies`、`operator/info/list`、`operator/market`、`skills/index/build`），共 112 个字段落在空数组下未观测——巡检报告里单列一节标出，不要把这几条的「0 缺口」当成验过 |
| 写操作 / 未标只读 | 42 | 注册、编辑、删除、发布、执行等，工具按设计不发送 |
| 200 无 JSON 响应体 | 7 | 删除、状态变更、SSE 流、`.adp` 导入——文档本就没有 200 响应 schema，无从比对 |
| 缺路径参数 | 7 | 环境里没有对应数据（如没有算子版本、没有 MCP 工具名） |
| 环境相关失败 | 5 | MCP 代理不可达（503）、代理型 MCP 无接入地址（400）、技能包下载响应非 JSON（2 条）、`/tool-box/market/tools` 必填的 `tool_name` 造不出取值 |

### 实际被调用的有多少

另在测试服 `14.103.77.23` 用该服务 pod 近 3 天访问日志比对过：91 条里 31 条被真实
打到。**这不等于其余「没用」**——那是测试环境，写操作与市场、历史版本、索引重建
本就低频。只作热路径参考：`GET /tool-box/list` 265 次、`GET /skills` 87、
`GET /operator/category` 74、`GET /mcp/list` 67。

日志里另有 40 条打到内部面 `internal-v1`（集群内 bkn-agent / context-loader 走
ClusterIP 调工具代理与 `function/exec`），不经 Ingress，也不在本文档范围。

仍不收录的两个面：内部面 `internal-v1`（刻意不挂 Ingress，见上节）与能力面
`/api/capabilities-lab/v1`。
