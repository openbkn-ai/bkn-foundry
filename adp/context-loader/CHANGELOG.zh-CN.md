# 变更日志

英文版本：[CHANGELOG.md](CHANGELOG.md)

## Unreleased

### 移除

- 移除 `POST /kn/semantic-search`（公有路径与 `in/v1` 内部路径），请改用 `POST /kn/search_schema`
  - 该接口相对 `search_schema` 的全部差异字段——`query_understanding`、`hits_total`、`intent_score` / `match_score` / `rerank_score` 与每个概念的 `samples`——都会在回包前被 handler 清空，因此 `return_query_understanding: true` 从来没有产生过可见效果，实际响应是 `search_schema` 的真子集
  - `agent_intent_planning` 与 `agent_intent_retrieval` 两个 mode 仍会为意图分析真实调用一次大模型，结果随后被丢弃
  - 一并删除 `knretrieval`、`knrerank` 两个包，`SemanticSearch*` / `QueryUnderstanding` / `ConceptResult` / `KnowledgeRerank*` 等类型，以及 `api_public/kn.yaml`、`api_private/kn_schema_search.yaml` 两份文档
  - 一并删除已无人读取的 `rerank_llm` 配置块（服务配置、Helm values 与渲染出的 ConfigMap）。存量对该键的覆盖会被忽略，不会导致部署失败
- 从 `kn_search` 请求契约中移除 `search_scope.include_object_types` / `include_relation_types` / `include_action_types`
  - `kn_search` 从不按概念类型过滤响应，这三个开关被接收后直接丢弃。`search_scope.concept_groups` 不受影响，`search_schema` 自己那套可用的范围开关也不受影响
- 从请求契约中移除 `retrieval_config.semantic_instance_retrieval.max_keywords` 与 `pre_filter_per_type_limit`，这两个字段从未被任何代码读取

## 0.8.0

### 功能与改进

- 为 `search_schema` 增加按概念分组限定 Schema 探索范围的能力
  - 支持通过 `search_scope.concept_groups` 将对象类、关系类和动作类 Schema 探索限定在指定 BKN 概念分组内
  - 未传概念分组时，现有 `search_schema` 行为保持不变
  - 分组范围内返回关系类和动作类时，会一并补齐其引用的对象类，让调用方拿到完整 Schema 上下文
  - 说明：指标类 Schema 请求会携带同一组概念分组范围，但是否真正按组过滤取决于 BKN metrics 侧支持
- 将 ContextLoader 标准工具集内置到服务启动流程
  - ContextLoader 启动时自动同步内置工具集到执行工厂
  - 工具集契约描述统一为 `ContextLoader 标准内置工具集；契约版本: 0.8.0`

### 兼容性

- 兼容 HTTP 路径和 legacy HTTP 路径保持不变；需要概念分组能力的新接入方应使用 `search_schema`

### 文档

- 更新 API、MCP schema、toolset 和发布文档，说明按概念分组限定 Schema 探索范围的使用方式
- 补充 ContextLoader 内置工具集交付方式和契约版本描述规则

## 0.7.0

### 功能与改进

- 新增 `search_schema`，作为 MCP 和 HTTP 调用方的标准 Schema 探索入口
  - 通过一个接口支持对象类、关系类、动作类和指标类 Schema 探索
  - HTTP `search_schema` API 使用请求体中的 `kn_id`
- MCP Schema 探索能力统一收敛到 `search_schema`，减少 Agent 选择工具时的歧义

### 兼容性

- `kn_search` 继续作为兼容 HTTP 路径保留，`semantic-search` 继续作为 legacy HTTP 路径保留

### 文档

- 更新 release overview 和工具使用文档，说明 `search_schema` 入口统一与 metric schema 召回契约
