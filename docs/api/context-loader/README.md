# Context Loader API 文档

> Context Loader（服务名 `agent-retrieval`）HTTP API 的 OpenAPI 3.0.3 定义。
> 这是 Agent 拿业务上下文的统一入口：Schema 探索、实例检索、逻辑属性求值、行动召回与执行、Skill 召回、数据层直查，外加一套等价的 MCP 工具。

## 文件索引

| 文件 | 主题 | 包含的端点（`/api/agent-retrieval/v1` 下） |
|---|---|---|
| [schema-search.yaml](schema-search.yaml) | Schema 检索 | `POST /kn/search_schema`、`POST /kn/kn_search`、`POST /kn/semantic-search` |
| [kn-explore.yaml](kn-explore.yaml) | 知识网络浏览 | `POST /kn/list_knowledge_networks`、`POST /kn/get_kn_detail`、`POST /kn/get_object_types`、`POST /kn/get_relation_types` |
| [object-instance.yaml](object-instance.yaml) | 对象实例查询 | `POST /kn/query_object_instance` |
| [instance-subgraph.yaml](instance-subgraph.yaml) | 实例子图查询 | `POST /kn/query_instance_subgraph` |
| [logic-property.yaml](logic-property.yaml) | 逻辑属性求值与指标取数 | `POST /kn/logic-property-resolver`、`POST /kn/query_metric` |
| [action.yaml](action.yaml) | 行动召回与执行 | `POST /kn/get_action_info`、`POST /kn/execute_action`、`POST /kn/get_action_execution`、`POST /kn/list_action_executions` |
| [skill.yaml](skill.yaml) | Skill 召回与读取 | `POST /kn/find_skills`、`POST /kn/list_skills`、`POST /kn/get_skill_content`、`POST /kn/read_skill_file`、`POST /kn/execute_skill` |
| [data-access.yaml](data-access.yaml) | 数据层直查 | `POST /kn/list_resources`、`POST /kn/describe_resource`、`POST /kn/run_sql` |
| [mcp.yaml](mcp.yaml) | MCP 服务 | `GET /mcp/info`、`POST /mcp` |

## 典型链路

```text
list_knowledge_networks  → 发现 kn_id
search_schema            → 一句话找到相关对象类 / 关系类 / 行动类 / 指标类
get_object_types         → 下钻拿属性的物理列名、可用算子，以及该对象类下的 related_metrics
query_object_instance    → 取实例，从 _instance_identity 拿主键
  ├→ logic-property-resolver → 求指标 / 算子类逻辑属性（实例 + 已绑逻辑属性）
  ├→ get_action_info → execute_action → get_action_execution → 执行闭环
  └→ find_skills            → 召回可装载的 Skill
       └→ get_skill_content → read_skill_file → execute_skill
```

指标取数（OT 优先，已建模指标别用 `run_sql` 重写口径）：

```text
search_schema / get_kn_detail  → 锁定对象类（summary 里 related_metric_count > 0 才有指标）
get_object_types               → 从 related_metrics 选定指标
  ├→ logic-property-resolver   → 实例级 + 已绑逻辑属性
  └→ query_metric              → 类级 / 未绑逻辑属性，按 MetricDefinition 口径算
```

Skill 面另有一条不依赖知识网络的入口：`list_skills` 直接翻已发布 Skill 列表，
再走同样的 `get_skill_content` → `read_skill_file` → `execute_skill`。

绕开本体直查数据：`list_resources` → `describe_resource` → `run_sql`。

## 约定

- **OpenAPI 版本**：3.0.3。
- **认证**：`Authorization: Bearer <token>`，凭据二选一——OAuth access token，或用户自助签发的 AppKey（`bak_` 前缀）。账户身份由服务端从凭据解析，调用方**不需要**传 `x-account-id` / `x-account-type`。
- **全部是 POST**：包括语义上的「查询」类端点；无请求体的（如 `list_knowledge_networks`）也走 POST。
- **响应格式**：所有端点支持 `?response_format=toon`，返回 `application/toon`——同构数组压成表格，比 JSON 省 token；默认 `json`。
- **错误信封**：本服务**不用** `comm-go/rest.BaseError`，字段是 `code` / `description` / `solution` / `link` / `details`，引 [`_shared/errors.yaml#/components/schemas/ErrorAgentRetrieval`](../_shared/errors.yaml)。**下游报错时原样透传下游响应体**，此时字段名是下游的（通常 `error_code` / `error_details`）；看 `code` 前缀判断来源，`Public.*` 与 `agentRetrieval.*` 是本服务产生的。
- **内部接口**：每个端点都有对应的 `/api/agent-retrieval/in/v1/...` 版本，请求 / 响应结构一致，区别只在鉴权（外部走 Token，内部从 `X-Account-ID` / `X-Account-Type` 头取访问者）。内部面另有三个不对外的端点：`POST /kn/full_build_ontology`、`GET /kn/full_ontology_building_status`、`POST /mcp/proxy/{mcp_id}/tools/{tool_name}/call`。**本文档仅描述外部接口**，内部路由以 `driveradapters/rest_private_handler.go` 实际注册的为准。
- **实例标识不可自拼**：需要 `_instance_identities` 的接口，取值一律来自 `query_object_instance` 或 `query_instance_subgraph` 结果里的 `_instance_identity`，两条链路同名同义。
- **算子白名单看对象类**：条件里能用哪些算子以对象类的 `condition_operations` 为准（`get_object_types` 返回）。该声明是建网时客户端写入并原样落库的，未经服务端校验，最终判定在下游 ontology-query。

## 契约巡检覆盖

本模块的端点全是 POST，工具默认只发 GET 覆盖不到，因此在只读端点上标了
`x-contract-probe`（机制见 [`tools/README.md`](../tools/README.md)）。跑法：

```bash
make api-contract-diff CONTRACT_FACE=ex CONTRACT_SSH=root@<host> \
     CONTRACT_ARGS="--include-probe-post --token $TOKEN"
```

25 个操作里 **16 个在探测范围内**，其余 9 个不探测，原因如下——它们的响应结构
**未经实机验证**，改动时请人工核对：

| 端点 | 不探测的原因 |
|---|---|
| `execute_action` | **有副作用**，会真的触发行动执行 |
| `semantic-search` | 走大模型，耗时与成本不适合常态巡检 |
| `logic-property-resolver` | 同上，且需要真实实例标识才能求值 |
| `query_metric` | 需要环境里存在已建模指标，`metric_id` 无法自动合成 |
| `run_sql` | 需要针对具体资源构造有意义的 SQL，无法自动合成 |
| `POST /mcp` | JSON-RPC 会话语义，不是普通请求 / 响应结构 |
| `execute_skill` | **有副作用**，会在沙箱内真的执行命令 |
| `get_skill_content` | 需要环境里存在已发布 Skill 的真实 `skill_id` |
| `read_skill_file` | 同上，且还需要包内一个真实存在的 `rel_path` |

探测范围内的 16 个中，`get_action_info`、`get_action_execution`、`find_skills`
依赖环境里存在行动类 / 执行记录 / `skills` 对象类，数据不具备时报告会列为
「缺少探测参数」或 404，同样按未验证处理。`list_skills` 在没有已发布 Skill 时
返回空列表加 `message`，属正常 200。
