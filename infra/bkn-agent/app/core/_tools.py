"""BKN 能力的沙箱侧 stub —— 由 gen_sandbox_tools.py 生成，请勿手工编辑。

每个函数对应一个 MCP 工具。只用标准库：MCP streamable HTTP 就是 JSON-RPC over
POST，urllib 足够，沙箱镜像无需预装任何依赖，也就没有 SDK 版本漂移的问题。

凭据与会话上下文经 _configure(event) 注入，由 agent 侧在发起执行时下发。
"""

import json
import urllib.request

_CFG = {}
_SESSION = {}

# 显式不走代理：MCP 端点是集群内地址，任何继承来的代理配置都只会让请求发不出去。
# 且 urllib 一旦认定要走代理就改发 absolute-form 请求行（POST http://host/path），
# nginx 对此直接 400 —— 在装了系统代理的开发机上跑本地校验时必踩。
_OPENER = urllib.request.build_opener(urllib.request.ProxyHandler({}))


class ToolError(RuntimeError):
    """工具调用失败。message 为服务端原文，供模型据此修正参数后重试。"""


def _configure(event):
    """由执行入口调用，注入 MCP 端点、凭据与生命周期上下文。"""
    _CFG.update(event)
    _SESSION.clear()


def _rpc(method, params=None, notify=False):
    body = {"jsonrpc": "2.0", "method": method}
    if params is not None:
        body["params"] = params
    if not notify:
        body["id"] = _SESSION.get("seq", 0) + 1
        _SESSION["seq"] = body["id"]
    headers = {
        "Content-Type": "application/json",
        "Accept": "application/json, text/event-stream",
        "Authorization": "Bearer " + _CFG["token"],
    }
    if _SESSION.get("id"):
        headers["Mcp-Session-Id"] = _SESSION["id"]
    # 端点必须带尾斜杠：缺斜杠时服务端 307 跳转，而 urllib 不对 POST 跟随重定向。
    request = urllib.request.Request(
        _CFG["mcp"], data=json.dumps(body).encode(),
        headers=headers, method="POST",
    )
    timeout = _CFG.get("timeout", 120)
    response = _OPENER.open(request, timeout=timeout)
    if not _SESSION.get("id"):
        _SESSION["id"] = response.headers.get("Mcp-Session-Id")
    raw = response.read().decode()
    if not raw.strip():
        return None
    for line in raw.splitlines():
        if line.startswith("data: "):
            return json.loads(line[6:])
    return json.loads(raw)


def _ensure_session():
    """MCP 会话在模块级复用，一次执行内 initialize 只发生一次。"""
    if _SESSION.get("ready"):
        return
    _rpc("initialize", {
        "protocolVersion": "2025-06-18",
        "capabilities": {},
        "clientInfo": {"name": "bkn-sandbox", "version": "1"},
    })
    _rpc("notifications/initialized", {}, notify=True)
    _SESSION["ready"] = True


def _call(tool, args):
    """调用 MCP 工具。None 值不下发，交由服务端使用 schema 默认值。"""
    _ensure_session()
    payload = {k: v for k, v in args.items() if v is not None}
    # 业务类工具受会话守卫约束，缺 bkn_context 会被拒（conversation_required）。
    # 该上下文由 agent 透传，模型无需感知，故不出现在函数签名里。
    if _CFG.get("bkn"):
        payload["bkn_context"] = _CFG["bkn"]

    result = _rpc("tools/call", {"name": tool, "arguments": payload})["result"]
    text = "".join(c["text"] for c in result["content"] if c["type"] == "text")
    if result.get("isError") or result.get("is_error"):
        raise ToolError(tool + ": " + text)
    try:
        return json.loads(text)
    except json.JSONDecodeError:
        # response_format=toon 等非 JSON 形态按原文返回
        return text



def list_knowledge_networks(response_format: str = 'json', name_pattern: str = None, limit: int = None, offset: int = None, sort: str = None, direction: str = None) -> dict:
    """列出可用的知识网络（返回 kn_id、名称、描述）。其余查询工具均需 kn_id，作为探索的第一步入口。
    
    response_format: 文本格式：json 或 toon，默认 toon
    name_pattern: 按知识网络名称模糊过滤，可选
    limit: 单页数量，默认 20
    offset: 偏移量，用于翻页，默认 0
    sort: 排序字段，默认 update_time
    direction: 排序方向，默认 desc
    """
    return _call("list_knowledge_networks", {"response_format": response_format, "name_pattern": name_pattern, "limit": limit, "offset": offset, "sort": sort, "direction": direction})


def get_kn_detail(kn_id: str, response_format: str = 'json', detail_level: str = 'summary') -> dict:
    """获取知识网络 schema（概念组、对象类型含 data_source.id、关系类型、行动类型）。默认 detail_level=summary：返回骨架 + 每个属性仅 name+type（不含 display_name/comment/字段映射/查询算子/映射规则）——足够理解结构与规划查询，体积小。需要某对象的完整字段映射（含 comment）时调 get_object_types，需要关系的 mapping_rules 时调 get_relation_types。detail_level=full 一次拿全量（体积大，慎用）。对象类条目附带 related_metric_count（该对象类下已建模指标数），用来判断是否值得下钻取指标。
    
    response_format: 文本格式：json 或 toon，默认 toon
    kn_id: 知识网络 ID。也可改用 X-Kn-ID 请求头传入。
    detail_level: 详情级别。summary（默认）：对象类/关系类/行动类骨架 + 每个属性仅 name+type（扁平同构，TOON 渲染成紧凑表格），不含 display_name/comment/字段映射/查询算子/映射规则，足够理解 schema 与规划查询；需要某对象完整字段映射（含 comment）改调 get_object_types，要关系 mapping_rules 改调 get_relation_types。full：一次返回全量（体积大，慎用）。
    """
    return _call("get_kn_detail", {"response_format": response_format, "kn_id": kn_id, "detail_level": detail_level})


def search_schema(kn_id: str, query: str, response_format: str = 'json', search_scope: dict = None, max_concepts: int = 10, schema_brief: bool = True, enable_rerank: bool = True, include_columns: bool = False) -> dict:
    """统一的 Schema 探索入口，先锁定对象类（object_types）。根据 query 返回相关 object_types、relation_types、action_types、metric_types，供 query_*、find_*、get_* 工具继续消费；其中 metric_types 只是辅助线索，指标的权威清单以 get_object_types 返回的 related_metrics 为准（按对象类 scope 全量枚举）。默认返回精简 Schema（schema_brief=true：保留 data_source.id 与属性 name/type/condition_operations，省约 70%），够规划查询；需要属性备注/主键/标签的完整 Schema 时传 schema_brief=false。
    
    response_format: 文本格式：json 或 toon，默认 toon
    kn_id: 知识网络 ID。也可改用 X-Kn-ID 请求头传入。
    query: 用户查询问题或关键词，多个关键词之间用空格隔开
    search_scope: Schema 探索范围。至少需要开启一种概念类型；不传时默认四类全开。concept_groups 用于按 BKN 概念分组限定 Schema 召回范围。
    max_concepts: Schema 候选规模上限
    schema_brief: 是否返回精简 Schema（MCP 默认 true）。精简版保留写查询所需的 data_source.id 与属性 name/type/condition_operations，省去属性 comment、tags、primary_keys，体积约省 70%。需要属性备注 / 主键 / 标签等完整信息时显式传 false。
    enable_rerank: 是否对关系类型启用 Rerank
    include_columns: 是否在每个对象类的 data_property 上额外返回物理列名 column（取自 mapped_field）。写 run_sql 时列名要用 column（物理列），而非 name（逻辑名）——二者可不同，且同一资源可被多个对象类用不同逻辑名映射。默认 false 以保持响应精简，准备写 SQL 时再开启。
    """
    return _call("search_schema", {"response_format": response_format, "kn_id": kn_id, "query": query, "search_scope": search_scope, "max_concepts": max_concepts, "schema_brief": schema_brief, "enable_rerank": enable_rerank, "include_columns": include_columns})


def get_object_types(kn_id: str, ids: list, response_format: str = 'json') -> dict:
    """按 id 批量取对象类的完整定义（data_properties 含 mapped_field/condition_operations，logic_properties 含 data_source/parameters），并返回该对象类下可用的指标 related_metrics（scope_type=object_type 且 scope_ref=对象类 id，含未绑逻辑属性的指标）。渐进式下钻用：先 get_kn_detail（summary）拿对象 id，再用本工具展开需要的对象。选定指标后：实例级且已绑逻辑属性走 get_logic_properties_values，类级或未绑的走 query_metric。ids 支持多个，一次取回；未匹配的 id 会在 missing 返回。
    
    response_format: 文本格式：json 或 toon，默认 toon
    kn_id: 知识网络 ID。也可改用 X-Kn-ID 请求头传入。
    ids: 要展开的对象类 id（取自 get_kn_detail summary 的 object_types[].id，也接受 name）。支持多个，一次批量取回。
    """
    return _call("get_object_types", {"response_format": response_format, "kn_id": kn_id, "ids": ids})


def get_relation_types(kn_id: str, ids: list, response_format: str = 'json') -> dict:
    """按 id 批量取关系类的完整定义（含 mapping_rules、source/target 对象名）。渐进式下钻用：先 get_kn_detail（summary）拿关系 id，再用本工具展开。ids 支持多个，一次取回；未匹配的 id 会在 missing 返回。
    
    response_format: 文本格式：json 或 toon，默认 toon
    kn_id: 知识网络 ID。也可改用 X-Kn-ID 请求头传入。
    ids: 要展开的关系类 id（取自 get_kn_detail summary 的 relation_types[].id，也接受 name）。支持多个，一次批量取回。
    """
    return _call("get_relation_types", {"response_format": response_format, "kn_id": kn_id, "ids": ids})


def query_object_instance(kn_id: str, ot_id: str, response_format: str = 'json', include_logic_params: bool = None, condition: dict = None, filters: list = None, limit: int = None, search_after: list = None, offset: int = None, properties: list = None) -> dict:
    """单对象类实例过滤查询。已知对象类与过滤条件时，查询实例列表。支持 search_after 游标顺序翻页（用上一页返回的 search_after 取下一页，不支持跳页）。
    
    response_format: 文本格式：json 或 toon，默认 toon
    kn_id: 知识网络 ID。也可改用 X-Kn-ID 请求头传入。
    ot_id: 对象类型ID
    include_logic_params: 是否返回逻辑属性计算参数
    condition: 过滤条件。operation 必填。比较算子（==/!=/>/>=/</<=/in/not_in/like/not_like/exist/not_exist/match）配 field + value + value_from；逻辑算子（and/or）配 sub_conditions 递归嵌套同结构条件。value 与 value_from 必须同时出现，value_from 仅支持 const。算子白名单以对象类的 condition_operations 为准。无过滤条件时可整体省略 condition。
    filters: 扁平过滤简写：多个条件按 and 组合，等价于手写 condition 的 and 嵌套。覆盖最常见的「字段 op 值 [AND ...]」场景。与 condition 互斥（同传时 condition 优先）；需要 or 或多层嵌套时改用 condition。算子白名单以对象类的 condition_operations 为准。
    limit: 单页返回数量，默认 10，范围 1-10000
    search_after: 游标分页（对象索引/数据视图路径）：传上一次响应返回的 search_after 取下一页；首次不传。仅顺翻，不跳页。
    offset: 偏移翻页（资源/vega 表源路径）：跳过前 N 条，支持跳到任意页。与 search_after 互斥（同传时 search_after 优先）。
    properties: 指定返回的对象属性字段列表，默认返回所有属性
    """
    return _call("query_object_instance", {"response_format": response_format, "kn_id": kn_id, "ot_id": ot_id, "include_logic_params": include_logic_params, "condition": condition, "filters": filters, "limit": limit, "search_after": search_after, "offset": offset, "properties": properties})


def query_instance_subgraph(kn_id: str, relation_type_paths: list, response_format: str = 'json', include_logic_params: bool = None) -> dict:
    """沿关系路径拉取实例子图，支持多跳关系查询。
    
    response_format: 文本格式：json 或 toon，默认 toon
    kn_id: 知识网络 ID。也可改用 X-Kn-ID 请求头传入。
    include_logic_params: 是否返回逻辑属性计算参数，默认 false
    relation_type_paths: 关系路径模板。object_types 与 relation_types 的顺序必须严格对应：n 跳路径 => object_types 长度为 n+1，relation_types 长度为 n；relation_types 不可为空
    """
    return _call("query_instance_subgraph", {"response_format": response_format, "kn_id": kn_id, "include_logic_params": include_logic_params, "relation_type_paths": relation_type_paths})


def get_logic_properties_values(kn_id: str, ot_id: str, query: str, _instance_identities: list, properties: list, response_format: str = 'json', additional_context: str = None, llm_model: str = None) -> dict:
    """逻辑属性解析（指标/算子）。针对实例批量计算已绑逻辑属性的值——指标路径里对应「实例级 + 已绑 logic property」这一支；类级指标、或未绑逻辑属性的指标改调 query_metric。
    
    response_format: 文本格式：json 或 toon，默认 toon
    kn_id: 知识网络 ID。也可改用 X-Kn-ID 请求头传入。
    ot_id: 对象类型ID
    query: 用户查询，需含时间、统计维度、业务上下文
    _instance_identities: 对象实例标识数组。必须从 query_object_instance 或 query_instance_subgraph 的 _instance_identity 字段提取，不可臆造。按原顺序组成数组。
    properties: 逻辑属性名列表（metric/operator）。必须为 Schema 中存在的逻辑属性
    additional_context: 可选。补充 timezone、instant、step 等上下文
    llm_model: 可选。覆盖动态参数生成所用大模型；为空走系统默认大模型。仅供测试/验证指定模型。
    """
    return _call("get_logic_properties_values", {"response_format": response_format, "kn_id": kn_id, "ot_id": ot_id, "query": query, "_instance_identities": _instance_identities, "properties": properties, "additional_context": additional_context, "llm_model": llm_model})


def run_sql(sql: str, response_format: str = 'json', query_timeout: int = None) -> dict:
    """对知识网络挂载的数据资源执行只读 SQL（Trino 方言）。注意：已建模指标不要用 SQL 重写口径——指标走 get_object_types 选定后的 query_metric / get_logic_properties_values；run_sql 用于未建成指标的即席聚合与跨表统计。表名用占位符 {{.resource_id}} 引用；vega 会解析成真实表并限量。仅允许单条 SELECT：支持同一 catalog 内的 JOIN、WHERE、GROUP BY/HAVING、ORDER BY、LIMIT 和常用聚合函数；不支持 WITH/CTE、UNION/INTERSECT/EXCEPT、多语句、写入/DDL 和跨目录 join，子查询与窗口函数不在兼容性承诺内。两种拿 resource_id + 物理列的方式：本体路 search_schema（对象类 data_source.id + data_property.column），数据层路 list_resources → describe_resource（直查裸资源，脱离本体）。
    
    response_format: 文本格式：json 或 toon，默认 toon
    sql: 只读 SQL（Trino 方言）。表名必须用占位符 {{.<RESOURCE_ID>}} 引用，其中 <RESOURCE_ID> 替换成具体资源 id 的真实值——即 search_schema 返回的 data_source.id，或 list_resources 返回的 resource_id；点可选（{{id}} 与 {{.id}} 等价）。规则：① 禁止原样写 'resource_id'/'.resource_id' 等字面量，必须替换成真实 id；② 一张表对应一个 id，JOIN 多表时每个表用各自不同的 id；③ 列名用物理列名（search_schema 的 data_property.column，需 include_columns=true；或 describe_resource 返回），不是逻辑名。示例 ✗错（两表共用占位、且写成字面量）：FROM {{.resource_id}} g JOIN {{.resource_id}} t ON g.tournament_id=t.tournament_id ；✓对（先 search_schema 取 goals 与 tournaments 各自的 data_source.id，再逐表替换）：SELECT t.tournament_name, g.family_name, COUNT(*) AS c FROM {{.<goals的data_source.id>}} g JOIN {{.<tournaments的data_source.id>}} t ON g.tournament_id=t.tournament_id GROUP BY t.tournament_name, g.family_name ORDER BY c DESC。仅允许单条 SELECT。支持同一 catalog 内的 JOIN、WHERE、GROUP BY/HAVING、ORDER BY、LIMIT 和常用聚合函数（COUNT/SUM/AVG/MIN/MAX）。不支持 WITH / CTE、UNION / INTERSECT / EXCEPT（vega 会以 `VegaBackend.Query.InvalidParameter` 拒绝）、多语句、任何写入与 DDL、跨数据目录 join；子查询和窗口函数不在当前兼容性承诺内，请勿依赖。vega 会自动限量（最多 10000 行）。
    query_timeout: 查询超时（秒），范围 1-3600，默认 60。可选。
    """
    return _call("run_sql", {"response_format": response_format, "sql": sql, "query_timeout": query_timeout})


def query_metric(kn_id: str, metric_id: str, response_format: str = 'json', time: dict = None, condition: dict = None, analysis_dimensions: list = None, order_by: list = None, having: dict = None, limit: int = None, fill_null: bool = None) -> dict:
    """按已建模指标的口径取数（OT-first 路径第 3 步）。先 search_schema / get_kn_detail 锁定对象类，再 get_object_types 从 related_metrics 里选定 metric_id，最后调本工具计算——口径写在 MetricDefinition 里，别用 run_sql 自己重写。分流：实例级且已绑逻辑属性的走 get_logic_properties_values；类级、或未绑逻辑属性的走本工具。可选 condition 过滤、analysis_dimensions 拆维、time 取单点或序列。时间窗规则：省略 instant 等同于序列查询，必须给 step；start 与 end 要么都给要么都不给。
    
    response_format: 文本格式：json 或 toon，默认 toon
    kn_id: 知识网络 ID。也可改用 X-Kn-ID 请求头传入。
    metric_id: 指标 ID，取自 get_object_types 返回的 related_metrics[].id。不要自己编造。
    time: 时间窗。指标无时间维度（related_metrics[].time_dimension 为空）时可整体省略。注意：省略 instant 等同于 instant=false（序列查询），此时必须传 step。start 与 end 要么都传要么都不传。
    condition: 过滤条件，结构与 query_object_instance 的 condition 一致（field / operation / value / value_from，可 and/or 嵌套）。
    analysis_dimensions: 分析维度（按维度拆分结果），取值必须来自 related_metrics[].analysis_dimensions。
    order_by: 结果排序
    having: 对聚合结果过滤
    limit: 返回条数上限
    fill_null: 区间序列查询时，无数据的步长点是否补空，默认 false。仅对序列查询有效，且必须同时给 time.start 与 time.end。
    """
    return _call("query_metric", {"response_format": response_format, "kn_id": kn_id, "metric_id": metric_id, "time": time, "condition": condition, "analysis_dimensions": analysis_dimensions, "order_by": order_by, "having": having, "limit": limit, "fill_null": fill_null})


def get_action_info(kn_id: str, at_id: str, response_format: str = 'json', _instance_identities: list = None) -> dict:
    """行动信息召回。针对对象实例返回可执行行动的工具定义。
    
    response_format: 文本格式：json 或 toon，默认 toon
    kn_id: 知识网络 ID。也可改用 X-Kn-ID 请求头传入。
    at_id: 行动类型ID
    _instance_identities: 对象实例标识列表（可选）。每个元素必须从 query_object_instance 或 query_instance_subgraph 的 _instance_identity 字段提取，不可臆造。
    """
    return _call("get_action_info", {"response_format": response_format, "kn_id": kn_id, "at_id": at_id, "_instance_identities": _instance_identities})


def execute_action(kn_id: str, at_id: str, _instance_identities: list = None, dynamic_params: dict = None) -> dict:
    """执行行动。先用 get_action_info 获取行动的 dynamic_params schema，再填入真实动态参数值触发执行（异步，返回 execution_id）。同一 kn + 行动类型 + 实例集合与 dynamic_params 在窗口期内重复提交会返回 409，不要重试，改用 get_action_execution 查询已有 execution。
    
    kn_id: 知识网络 ID。也可改用 X-Kn-ID 请求头传入。
    at_id: 行动类型 ID，由 search_schema 或 get_kn_detail 返回。
    _instance_identities: 目标实例列表；为空时由行动驱动按行动条件扫描全部匹配实例。每个元素需从 query_object_instance 或 query_instance_subgraph 的 _instance_identity 字段提取，不可臆造。
    dynamic_params: 行动的动态参数值（对应行动类型中 value_from=input 的参数）。请先调用 get_action_info 获取该行动的 dynamic_params schema 与参数名，再在此按名逐个填入真实值。注意：每个参数是独立的键，不要把多个参数名拼成一个键。
    """
    return _call("execute_action", {"kn_id": kn_id, "at_id": at_id, "_instance_identities": _instance_identities, "dynamic_params": dynamic_params})


def get_action_execution(kn_id: str, execution_id: str, response_format: str = 'json') -> dict:
    """查询单次行动执行的状态与结果。用 execute_action 返回的 execution_id 查询整体 status 与逐对象 results。
    
    response_format: 响应格式：json 或 toon，默认 toon（更省 token）。
    kn_id: 知识网络 ID。也可改用 X-Kn-ID 请求头传入。
    execution_id: 行动执行 ID，由 execute_action 返回。
    """
    return _call("get_action_execution", {"response_format": response_format, "kn_id": kn_id, "execution_id": execution_id})


def list_action_executions(kn_id: str, response_format: str = 'json', action_type_id: str = None, status: str = None, trigger_type: str = None, start_time_from: int = None, start_time_to: int = None, offset: int = None, limit: int = None, search_after: list = None) -> dict:
    """列出行动执行历史，可按行动类型、状态、触发方式过滤并分页。
    
    response_format: 响应格式：json 或 toon，默认 toon（更省 token）。
    kn_id: 知识网络 ID。也可改用 X-Kn-ID 请求头传入。
    action_type_id: 按行动类型 ID 过滤（可选）。
    status: 按执行状态过滤（可选）：pending/running/completed/failed/cancelled。
    trigger_type: 按触发方式过滤（可选）：manual/scheduled。
    start_time_from: 起始时间下界（Unix 毫秒，可选）。
    start_time_to: 起始时间上界（Unix 毫秒，可选）。
    offset: 分页偏移（可选）。
    limit: 分页条数（可选），默认 20，最大 1000。
    search_after: 游标分页（可选）：将上一页响应返回的 search_after 原样传入以获取下一页，比 offset 更适合深翻页。
    """
    return _call("list_action_executions", {"response_format": response_format, "kn_id": kn_id, "action_type_id": action_type_id, "status": status, "trigger_type": trigger_type, "start_time_from": start_time_from, "start_time_to": start_time_to, "offset": offset, "limit": limit, "search_after": search_after})


def list_resources(response_format: str = 'json', catalog_id: str = None, type: str = None, offset: int = None, limit: int = None) -> dict:
    """数据层资源直查（脱离本体）：列出当前账户有权查看的数据资源（resource_id、name、type、catalog_id），可按 catalog_id / type 过滤。资源未建成对象类、或想绕本体直查数据时用。配合 describe_resource + run_sql。
    
    response_format: 文本格式：json 或 toon，默认 toon
    catalog_id: 可选。限定某数据目录（catalog）下的资源。
    type: 可选。按资源类别过滤（table / file / fileset / api / metric / topic / index / logicview / dataset）。
    offset: 可选。分页偏移，默认 0。
    limit: 可选。分页大小，默认 20。
    """
    return _call("list_resources", {"response_format": response_format, "catalog_id": catalog_id, "type": type, "offset": offset, "limit": limit})


def describe_resource(resource_id: str, response_format: str = 'json') -> dict:
    """取单个数据资源的物理 schema：connector_type 与列（columns:[{name,type}]）。写 run_sql 前用它拿物理列名。入参 resource_id 取自 list_resources。
    
    response_format: 文本格式：json 或 toon，默认 toon
    resource_id: 资源 ID（取自 list_resources 的 resource_id）。
    """
    return _call("describe_resource", {"response_format": response_format, "resource_id": resource_id})


def find_skills(kn_id: str, object_type_id: str, response_format: str = 'json', instance_identities: list = None, skill_query: str = None, top_k: int = 10) -> dict:
    """技能召回。根据知识网络、对象类型、实例上下文等召回匹配的可用技能列表。
    
    response_format: 文本格式：json 或 toon，默认 toon
    kn_id: 知识网络 ID。也可改用 X-Kn-ID 请求头传入。
    object_type_id: 对象类型 ID（必填）。当前版本仅支持对象类级或实例级技能召回，且该对象类型必须存在于当前知识网络中
    instance_identities: 实例标识列表（可选）。提供时进一步缩小至具体实例关联的技能
    skill_query: 技能查询关键词（可选）。用于语义匹配过滤技能
    top_k: 返回的最大技能数量，默认 10
    """
    return _call("find_skills", {"response_format": response_format, "kn_id": kn_id, "object_type_id": object_type_id, "instance_identities": instance_identities, "skill_query": skill_query, "top_k": top_k})


def list_skills(response_format: str = 'json', name: str = None, category: str = None, page: int = None, page_size: int = None) -> dict:
    """浏览已发布技能（不需要知识网络上下文）。与 find_skills 互补：那条按对象类/实例召回，这条按名称或分类翻列表。拿到 skill_id 后用 get_skill_content 读 SKILL.md，用 read_skill_file 下钻附属文件，用 execute_skill 执行入口命令。
    
    response_format: 文本格式：json 或 toon，默认 toon
    name: 可选。按技能名称模糊过滤。
    category: 可选。按技能分类过滤。
    page: 可选。页码，从 1 开始，默认 1。
    page_size: 可选。每页大小，默认 20。
    """
    return _call("list_skills", {"response_format": response_format, "name": name, "category": category, "page": page, "page_size": page_size})


def get_skill_content(skill_id: str, response_format: str = 'json') -> dict:
    """读技能主文档 SKILL.md 正文，并返回技能包内的文件清单（files[].rel_path）。技能的用法与入口命令都写在这里；需要哪个附属文件再用 read_skill_file 单取，不必整包读。
    
    response_format: 文本格式：json 或 toon，默认 toon
    skill_id: 技能 ID（取自 list_skills 或 find_skills 的 skill_id）。
    """
    return _call("get_skill_content", {"response_format": response_format, "skill_id": skill_id})


def read_skill_file(skill_id: str, rel_path: str, response_format: str = 'json') -> dict:
    """读技能包内单个文件的正文，rel_path 取自 get_skill_content 返回的 files 清单。渐进式加载用：大文件不必常驻上下文。二进制文件只回元数据不回正文。
    
    response_format: 文本格式：json 或 toon，默认 toon
    skill_id: 技能 ID（取自 list_skills 或 find_skills 的 skill_id）。
    rel_path: 技能包内相对路径，取自 get_skill_content 返回的 files[].rel_path。不接受包外路径（../ 会被拒）。
    """
    return _call("read_skill_file", {"response_format": response_format, "skill_id": skill_id, "rel_path": rel_path})


def execute_skill(skill_id: str, entry_shell: str, timeout: int = None) -> dict:
    """在沙箱内执行技能的入口命令，返回 exit_code / stdout / stderr。entry_shell 必须取自 SKILL.md 声明的入口，先用 get_skill_content 读清楚再调。
    
    skill_id: 技能 ID（取自 list_skills 或 find_skills 的 skill_id）。
    entry_shell: 在沙箱内执行的入口命令。必须取自 SKILL.md 中声明的入口，不要自行拼装无关命令。技能包已解压到工作目录，命令相对该目录执行。
    timeout: 可选。执行超时秒数。
    """
    return _call("execute_skill", {"skill_id": skill_id, "entry_shell": entry_shell, "timeout": timeout})


__all__ = [
    "list_knowledge_networks",
    "get_kn_detail",
    "search_schema",
    "get_object_types",
    "get_relation_types",
    "query_object_instance",
    "query_instance_subgraph",
    "get_logic_properties_values",
    "run_sql",
    "query_metric",
    "get_action_info",
    "execute_action",
    "get_action_execution",
    "list_action_executions",
    "list_resources",
    "describe_resource",
    "find_skills",
    "list_skills",
    "get_skill_content",
    "read_skill_file",
    "execute_skill",
    "ToolError",
]
