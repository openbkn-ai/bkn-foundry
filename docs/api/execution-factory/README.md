# 执行工厂 API 文档

> 执行工厂（服务名 `agent-operator-integration`）HTTP API 的 OpenAPI 3.0.3 定义。
> 平台的「能力」都在这里落地：一段代码、一个算子、一箱工具、一个 MCP、一个 Skill，最终都通过它注册、调试、发布与执行。

## 文件索引

| 文件 | 主题 | 包含的端点（`/api/agent-operator-integration/v1` 下） |
|---|---|---|
| [function.yaml](function.yaml) | 函数 | `POST /function/execute`、`GET /function/dependencies`、`GET /function/dependency-versions/{package_name}`、`GET /template/{template_type}`、`POST /ai_generate/function/{type}`、`GET /ai_generate/prompt/{type}` |
| [sandbox.yaml](sandbox.yaml) | 沙箱观测 | `GET /sandbox/health`、`GET /sandbox/pool`、`GET /sandbox/sessions`、`GET /sandbox/sessions/{id}` |
| [impex.yaml](impex.yaml) | 导入导出 | `GET /impex/export/{type}/{id}`、`POST /impex/import/{type}` |
| [operator.yaml](operator.yaml) | 算子 | 注册 / 编辑 / 更新 / 列表 / 详情 / 批量取名 / 状态 / 删除 / 调试 / 历史版本 / 市场 / 分类 / 内置算子，共 15 条 |
| [mcp.yaml](mcp.yaml) | MCP | 探测 / 增删改查 / 状态 / 工具调试 / 市场 3 条 / 代理列工具与调用 / 对外端点 3 条，共 16 条 |

> 工具箱 / Skill 两面（47 个端点）尚未收录，见本文末「覆盖边界」。

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
- **时间戳一律是纳秒**：所有 `*_time` 字段由 `time.Now().UnixNano()` 生成，形如 `1784880971306127803`；按毫秒解析会得到 1970 年附近的日期。全服务统一，算子 / MCP / 工具箱 / Skill 都是。
- **契约巡检**：只读 GET 上标了 `x-contract-probe`，需巡检工具支持该扩展（见 #578）才会生效；在那之前这些标注是惰性的。

## 收录范围为什么是这些

只收 **Ingress 暴露的公开面** `/api/agent-operator-integration/v1`——那是浏览器
（Studio）能够到达的面。内部面 `internal-v1` **刻意不挂 Ingress**：它不校验令牌，
身份取自调用方自填的 `X-Account-ID` 头，该头缺失时调用者会被降级为硬编码管理员，
一旦暴露，其下约 40 条写接口即可从集群外无凭据调用（见 chart values 注释与 #326）。
因此本文档不描述内部面，也请不要把它写进任何对外文档。

## 覆盖边界

已收录**函数 6 + 沙箱观测 4 + 导入导出 2 + 算子 15 + MCP 16 = 43 个端点**
（公开面共 90）。剩余 47 个：

| 面 | 端点数 | 说明 |
|---|---|---|
| 工具箱 toolbox | 22 | 工具箱与工具的增删改查、调试、代理调用、市场 |
| Skill | 25 | 注册 / 发布 / 版本 / 内容读取 / 下载 / 索引构建 |

> 端点总数从 89 修正为 90：MCP 的 `Any /mcp/app/{mcp_id}/mcp`（Streamable HTTP
> 端点）此前统计时被漏掉——抽路由的正则只匹配 GET/POST/PUT/DELETE/PATCH，没算
> `Any`。

**验证程度分两级**，模块内不同文件不一样：**路由与收录范围**全部从代码的
`RegisterPublic` 核过；**字段级**只有实机打过的算数——函数 5 条、沙箱 3 条、
算子的分类与两个列表。其余按 Go 类型与服务目录草稿写成，标注为未实机验证。

这些接口的**响应结构未经本批次验证**，改动时请人工核对。服务目录下
`adp/execution-factory/operator-integration/docs/apis/` 里有一份历史草稿可作参照，
但它与实现存在漂移（context-loader 的同类草稿实测就有三处写错），不要直接当作真相源。
