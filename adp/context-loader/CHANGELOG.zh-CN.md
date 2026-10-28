# 变更日志

英文版本：[CHANGELOG.md](CHANGELOG.md)

## Unreleased

- `search_schema` 展开枚举并输出别名（BKN 规范 3.0 阶段 2）。引用枚举值类型的属性带 `value_type` 与枚举项 `enum: [{value, display, deprecated}]`，停用项保留；超过展开上限（`concept_search_config.enum_expand_limit`，默认 50）的枚举以及精简模式下的所有枚举只给 `enum_count`，新参数 `enum_query` 按显示名或编码检索枚举项。对象类、关系类、行动类在两种模式下都输出 `aliases`，数据属性的别名只在完整模式输出。`get_kn_detail`、`get_object_types`、`get_relation_types` 不输出别名。无值类型、无别名的网络输出与之前相同。 (#2282)

- `search_schema` 输出 BKN 规范 3.0 的关系声明：`reverse_name`、`cardinality`、`required`、`characteristics`、`verification`、`importance`（primary 时不输出）与关系属性。主关系在 `fallback` 下带上替代路径及替代规则，替代路径不再单独出现：只召回到替代路径时补出它的主关系。对象类、关系类、行动类非 active 时带 `lifecycle`，按 ID 查到已弃用的定义（`get_object_types`、`get_relation_types`、指定 ID 的对象类）时可从中看到替代定义。无这些声明的网络输出与之前相同。 (#2285)

- 在既有生命周期预算内为确认恢复保留时间，使用原幂等键重放 start，并冻结终态载荷与附件引用；不重复执行业务工具。 (#2539)

### 功能与改进

- `search_schema` 在数据属性上输出 BKN 规范 3.0 阶段 1 的属性声明：完整模式输出 `semantic`、`required`、`unit`、`crs`、`timezone`，精简模式只输出 `semantic`；未声明的字段不输出，无声明网络的输出与之前相同。`get_kn_detail` 和 `get_object_types` 不输出这些字段。 (#2260)

- 复用既有 Evidence 捕获开关，记录平台函数的实际执行身份与既有结构化结果，无需用户或样例额外声明证据，交给 Core 组装正式 Claim；发布前未生成快照不影响原回执完整性；一旦声明快照事件，其投递或确认失败仍可能使证据变为 partial。运行期捕获失败记录脱敏诊断；受管 finish 仍拒绝调用方提交 claims。 (#1332)

- Interaction finish 使用独立、有界的终态提交／重放预算（默认 5 秒），有限重试保留完全相同的请求和幂等键。未确认提交返回 MCP 工具错误并指引 `retry_same_finish`；只有匹配的 Core 终态 ACK 才确认成功，不重新执行业务。 (#2340)

- Kafka 证据发布保留规范业务引用、操作关联与因果事件 ID；工具 attempt 收尾遇到临时基础设施错误时在现有有界预算内恢复，不重新执行业务工具。 (#2425)

- artifact 上传遇到明确的采集认证配置缺失时不再重复重试；区分 artifact 端点故障与生命周期 Core 故障，保留已提交的 start ID、采集权限拒绝和现有 Trace I/O 预算。 (#1691)

- 可选地将答案可靠交给 Core managed finish，保留原 Trace 时间预算，避免同步等待答案 artifact 上传；业务完成与证据待补齐分别返回。默认旧路径不变，须在全部 Core worker 升级后启用。 (#1691)

- `run_cypher`、`search_instance` 和 `get_logic_properties_values` 返回可操作的权限申请引导
  - 在 BKN Backend 与 Context Loader 之间保留明确的行过滤和属性脱敏影响信息
  - 在结构化工具输出和用户可见消息中同时提供本地化的 Studio 申请链接，便于 MCP 宿主稳定展示

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
