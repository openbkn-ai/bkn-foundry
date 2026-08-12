<!-- 由 gen_sandbox_tools.py 生成，请勿手工编辑 -->

下列 BKN 能力已在作用域内，直接调用即可，无需 import。
只有 stdout 会返回给你——中间结果不进上下文，因此请在脚本内完成过滤与聚合，
只 print 你真正需要的内容。调用失败抛 `ToolError`。

签名末尾的 `-> {…}` 是返回值顶层键。**其中部分键可能不出现**（如 `total_count` 在带过滤的查询里就没有），一律用 `.get()` 取，不要下标。
过滤字段必须是该对象类真实的数据属性名——先用 `get_object_types` 查`data_properties`，不要按语义猜。

## 可用函数

### 网络与 Schema

```python
list_knowledge_networks(response_format: str = 'json', name_pattern: str = None, limit: int = None, offset: int = None, sort: str = None, direction: str = None) -> {entries, total_count}
    # 知识网络列表
get_kn_detail(kn_id: str, response_format: str = 'json', detail_level: str = 'summary') -> {id, name, comment, concept_groups, object_types, relation_types, action_types}
    # 网络结构
search_schema(kn_id: str, query: str, response_format: str = 'json', search_scope: dict = None, max_concepts: int = 10, schema_brief: bool = True, enable_rerank: bool = True, include_columns: bool = False) -> {object_types, relation_types, action_types, metric_types}
    # Schema 探索
get_object_types(kn_id: str, ids: list, response_format: str = 'json') -> {kn_id, object_types, missing}
    # 对象类详情
get_relation_types(kn_id: str, ids: list, response_format: str = 'json') -> {kn_id, relation_types, missing}
    # 关系类详情
```

### 实例查询

```python
query_object_instance(kn_id: str, ot_id: str, response_format: str = 'json', include_logic_params: bool = None, condition: dict = None, filters: list = None, limit: int = None, search_after: list = None, offset: int = None, properties: list = None) -> {datas, total_count, search_after}
    # 实例查询
query_instance_subgraph(kn_id: str, relation_type_paths: list, response_format: str = 'json', include_logic_params: bool = None) -> {entries}
    # 子图查询
get_logic_properties_values(kn_id: str, ot_id: str, query: str, _instance_identities: list, properties: list, response_format: str = 'json', additional_context: str = None, llm_model: str = None) -> {datas}
    # 逻辑属性计算
run_sql(sql: str, response_format: str = 'json', query_timeout: int = None) -> {columns, entries, total_count, warnings}
    # SQL 查询
query_metric(kn_id: str, metric_id: str, response_format: str = 'json', time: dict = None, condition: dict = None, analysis_dimensions: list = None, order_by: list = None, having: dict = None, limit: int = None, fill_null: bool = None) -> {kn_id, metric_id, datas, overall_ms}
    # 指标查询
```

### 行动

```python
get_action_info(kn_id: str, at_id: str, response_format: str = 'json', _instance_identities: list = None) -> {headers, _dynamic_tools}
    # 行动信息
execute_action(kn_id: str, at_id: str, _instance_identities: list = None, dynamic_params: dict = None) -> {execution_id, status, message, created_at}
    # 执行行动
get_action_execution(kn_id: str, execution_id: str, response_format: str = 'json') -> {id, status, total_count, success_count, failed_count, results}
    # 执行结果
list_action_executions(kn_id: str, response_format: str = 'json', action_type_id: str = None, status: str = None, trigger_type: str = None, start_time_from: int = None, start_time_to: int = None, offset: int = None, limit: int = None, search_after: list = None) -> {total_count, entries, search_after}
    # 执行历史
```

### 数据资源与技能

```python
list_resources(response_format: str = 'json', catalog_id: str = None, type: str = None, offset: int = None, limit: int = None) -> {entries, total_count}
    # 数据资源列表
describe_resource(resource_id: str, response_format: str = 'json') -> {resource_id, connector_type, columns}
    # 数据资源详情
find_skills(kn_id: str, object_type_id: str, response_format: str = 'json', instance_identities: list = None, skill_query: str = None, top_k: int = 10) -> {entries, message}
    # 技能召回
list_skills(response_format: str = 'json', name: str = None, category: str = None, page: int = None, page_size: int = None) -> {entries, total_count, page, page_size, message}
    # 技能列表
get_skill_content(skill_id: str, response_format: str = 'json') -> {skill_id, status, content, truncated, files, message}
    # 技能内容
read_skill_file(skill_id: str, rel_path: str, response_format: str = 'json') -> {skill_id, rel_path, mime_type, file_type, content, truncated, message}
    # 读技能文件
execute_skill(skill_id: str, entry_shell: str, timeout: int = None) -> {skill_id, exit_code, stdout, stderr, truncated, execution_time, work_dir, command, mocked}
    # 执行技能
```

## 调用顺序

`kn_id`、`ot_id` 不能凭空写，必须先查：

```text
list_knowledge_networks  → kn_id
get_kn_detail(kn_id)     → object_types 概览
get_object_types(...)    → 属性定义与可用算子
```

## 参数写不准时

每个函数的完整 schema 在 docstring 里，脚本内自查，不要猜：

```python
help(query_object_instance)
```

特别是 `condition` 的 `operation`：`match` / `knn` 能否使用取决于该属性的
`condition_operations`（见 `get_object_types` 返回），从 `type` 推不出来。

## 错误处理

调用失败抛 `ToolError`，message 为服务端原文。可在脚本内捕获并修正参数重试，
不必回到对话轮次。
