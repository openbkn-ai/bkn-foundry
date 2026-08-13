# `search_instance`：自然语言直接召回实例

- Issue：[#828](https://github.com/openbkn-ai/bkn-foundry/issues/828)
- 分支：`feature/828-search-instance`
- 模块：context-loader（agent-retrieval）
- 状态：in-review

## 1. 背景与问题

工具面缺「自然语言 → 实例」这一格：

| 工具 | 输入 | 输出 |
|---|---|---|
| `search_schema` | 自然语言 | 概念（对象类 / 关系类 / 行动类 / 指标） |
| `query_object_instance` | 结构化条件（要先知道字段名与取值） | 实例 |
| `query_instance_subgraph` | 已知实例 id | 邻域子图 |
| **缺** | **自然语言** | **实例** |

能力本身早就实现了：`KnSearchService.KnSearch` 在 `only_schema=false` 时会做语义实例召回
（#818 刚把它换成 knn/match 双通道 + RRF 融合）。问题在于**没有入口能触发它**：

- MCP 工具面只注册了 `search_schema`，没有 `kn_search`；
- `search_schema` 在 `NormalizeSearchSchemaReq` 里写死 `onlySchema := true`，
  请求结构体 `SearchSchemaReq` 连 `only_schema` 字段都没有，客户端传了也被 gin 静默丢弃；
- 唯一入口是 REST `POST /kn/kn_search` + `only_schema: false`，而 `kn_search` 在 API 文档里
  被标注为「兼容历史调用方式的接口」，新接入方被明确劝去用 `search_schema`。

净效果：实例召回的算法改进对 Agent 一律不可见。

## 2. 方案

新增 `search_instance` 工具（MCP + REST），而不是给 `search_schema` 挂
`include_instances` 开关：

- **职责单一、响应体积可预测**。挂开关会让 `search_schema` 变成「有时回 schema、
  有时回 schema + 实例」，与 `get_kn_detail` 的渐进式下钻（`detail_level`）确立的体积
  约束相反。
- **不动存量响应结构**。否则要给 `SearchSchemaResp` 加 `nodes`，所有存量调用方的
  响应形状都变了。
- **命名对仗**：`search_*` 是语义找，`query_*` 是条件取。
- **精排开关的归属**。#818 的 PR2 要加 reranker，那是实例检索的属性，不该出现在
  schema 探索工具的参数表里。

### 2.1 契约

```text
POST /kn/search_instance        # public /v1 与内部 /in/v1 都注册
{
  "kn_id": "kn_xxx",              # 也可用 X-Kn-ID 头
  "query": "自然语言",
  "concept_groups": ["g1"],       # 可选，限定召回的概念分组
  "max_object_types": 10,         # 可选，参与实例召回的对象类数量
  "max_instances_per_type": 5     # 可选，每个对象类最多回几条
}
→ {
  "nodes": [ { "object_type_id", "object_type_name", "properties", "score", "recall_score" } ],
  "message": "未检索到符合条件的实例数据"     # 仅无命中时
}
```

服务层复用 `KnSearchService.KnSearch(OnlySchema=false)`，只取 `nodes`。

### 2.2 三个刻意的取舍

**不回 schema。** 调用方要字段含义有 `search_schema` / `get_object_types`，在这里再塞一份
是这套工具面一直在削的体积。

**不暴露 `retrieval_config`。** 那是运维级旋钮（候选池大小、向量子条件预算、各种阈值），
Agent 不该也无法判断怎么调；需要调的人仍可走 `kn_search`。工具参数表里只留三个能被
Agent 正确理解的旋钮：范围（`concept_groups`）、宽度（`max_object_types`）、
深度（`max_instances_per_type`）。这与 `IndexOpsOnly` 那条注释是同一个原则——
不把噪音塞给 Agent。

**沿用现有降级语义。** 实例召回失败不报错，返回空 `nodes` + `message`，与 `kn_search`
今天的行为一致：Agent 拿到「没找到」比拿到 500 更可用。

## 3. 落点

| 层 | 改动 |
|---|---|
| `interfaces/search_instance.go` | 新增 `SearchInstanceReq` / `SearchInstanceResp` |
| `logics/knsearch/search_instance.go` | `NormalizeSearchInstanceReq` + `SearchInstance`，复用 `KnSearch` |
| `driveradapters/knsearch/index.go` | `SearchInstance` handler（绑定 header/query/body → defaults → validate） |
| `rest_public_handler.go` / `rest_private_handler.go` | 注册 `POST /kn/search_instance` |
| `driveradapters/mcp/app.go` + `tools.go` | 工具键 + 注册 + handler |
| `mcp/schemas/search_instance.json` | 输入 schema |
| `mcp/schemas/tools_meta.json` + `locales/en-US/tools_meta.json` | 工具描述（中 / 英） |
| `docs/api/context-loader/object-instance.yaml` | OpenAPI |
| `infra/bkn-agent/app/core/toolbox.py` | `_CONTEXT_LOADER_RETRIEVAL_PATHS` 加入新路径 |

生命周期守卫自动生效（路径含 `/kn/`，`middlewareLifecycle` 只看 path），无需额外接线。
证据链按 `EmitSearchSchemaEvents` 的同一姿势发 `context.search_instance` 检索事件，
引用按命中的对象类去重登记——实例行本身没有受控标识可引，能确证的是「这些对象类被读过」。

第四处对齐是实现期才发现的：bkn-agent 的 `_CONTEXT_LOADER_RETRIEVAL_PATHS` 决定哪些
Context Loader 路径应当产出 `retrieval.completed` 事实事件。新工具发这个事件，路径就必须
登记进去，否则 Agent 侧对不上账。

`execution_factory_tools.adp` **不需要动**：那份 toolbox 是执行工厂的技能工具集，
里面没有任何 KN 检索工具（`grep search_schema` 为 0 命中），MCP 才是这批工具的分发面。

## 4. 测试

单测：

1. `NormalizeSearchInstanceReq` 把 `only_schema` 置为 `false`，且 `TopK` / `PerTypeInstanceLimit`
   按入参落到 `RetrievalConfig`。
2. 缺 `kn_id`（头与 body 都没有）、空 `query`、`max_*` 非正 → 400。
3. 响应只含 `nodes` 与 `message`，不含 schema 三件套。
4. `KnSearch` 返回空 `nodes` 时带 `message`，不报错。
5. MCP 工具已注册且 input schema 可加载：`community_parity_test.go` 的工具基线必须
   在同一个改动里加上 `search_instance`——那份清单是手写的，就是为了让工具面变化必须
   显式声明，而不是跟着代码自动漂。

VM 验证：给一个对象类补 `condition_operations` 后，用 MCP `tools/call` 直接调
`search_instance`，确认拿到实例行且带 `score` / `recall_score`。

## 5. 验收

- MCP 客户端一句自然语言拿到实例行，无需先知道字段名。
- `search_schema` / `kn_search` 行为不变（存量响应结构与默认值都不动）。
- 实例召回失败返回空 `nodes` + `message`，不是 5xx。
