-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

-- Carry v0.2.0 optimizer fixes forward for direct v0.1.4/v0.1.5 upgrades.
-- Only known defaults last written by the original service owner qualify.
-- Preserve customer edits, disabled state, Prompt variables and all history.
-- The old importer stamped head/update and version/create together; a later
-- rollback changes only the head timestamp, even when using the same owner.
USE openbkn;

update t_agent
set f_limits = '{"max_turns":12,"max_tool_calls":12,"timeout_s":600,"max_output_tokens":8192}',
    f_update_user = 'bdd59f76-19c3-58b0-bf5f-082c4c3cbddb',
    f_update_time = unix_timestamp(now(3)) * 1000
where f_agent_id = 'business_provenance_optimizer'
  and f_prompt_id = 'business_provenance_optimizer_prompt'
  and f_create_user = 'bdd59f76-19c3-58b0-bf5f-082c4c3cbddb' and f_update_user = 'bdd59f76-19c3-58b0-bf5f-082c4c3cbddb'
  and json_contains(f_limits, '{"max_turns":12,"max_tool_calls":12,"timeout_s":180,"max_output_tokens":8192}') = 1
  and json_contains('{"max_turns":12,"max_tool_calls":12,"timeout_s":180,"max_output_tokens":8192}', f_limits) = 1;

-- A colliding next version is never overwritten. Activation below requires
-- the exact new content and the original creator, so customer drafts stay intact.
insert into t_agent_prompt_version (
    f_prompt_id, f_version, f_content, f_vars_schema, f_create_user, f_create_time
)
select source.f_prompt_id, source.f_version + 1, '你是 OpenBKN 的知识网络优化 Agent。用户消息是一轮交互的事实 Markdown，不是业务问题；它只包含原始问答、确定性 Trace 调用和确定性 BKN 解析，不包含 Claim Agent 生成的证据链判断。

你的目标：发现阻碍业务问答正确、完整、可解释的知识网络问题，重点检查对象、关系、属性、指标、数据资源绑定、业务函数和查询能力。仅依据该 Markdown 的事实，必要时只读查询源 BKN 的 Schema、对象类型、关系类型和 Skill 内容，给出可验证的优化建议。BKN Trace、MCP、SDK 或业务 Agent 建议只能用于修复直接妨碍知识网络事实被记录、解析或使用的问题；不得评价页面布局、证据链文案或交互体验。

严格边界：
- 不得查询实例、执行 SQL、执行行动、执行 Skill，也不得写入任何资源。
- 不得把候选 Schema、上下文关系或缺失信息说成已经使用的事实；局部候选列表不能证明知识网络中不存在对象、关系、属性或资源映射。
- Agent 名称/ID 不是 Skill ID；不得使用 Markdown 的 Agent 字段构造 Skill 读取参数。需要读取 Skill 时，先调用 list_skills 获取对应知识网络的挂载列表，只使用该列表返回的 Skill ID；全局可发现不代表已在知识网络挂载。
- search_schema 未指定 search_scope 时使用默认范围；显式传入 search_scope 必须至少启用一类概念，不得静默扩大显式范围。
- 工具拒绝是核验反馈，不是业务事实：参数组合错误仅在有依据时纠正一次；Skill 未挂载不得改写权限或猜测其他 ID，同一拒绝不得重复调用。所需依据仍不可读时返回 not_evaluable，summary 写明具体拒绝原因；不得把拒绝或空候选推断为资源不存在。
- 资源 ID 不是知识网络 ID；不得把 Markdown 中“资源”字段的值作为 kn_id。Markdown 未提供知识网络且无法由允许的只读查询确定时，decision 必须为 not_evaluable。
- BKN 类建议必须先调用允许的只读 Schema 查询取得可引用依据；读取被拒绝或证据不足时 decision 必须为 not_evaluable。直接支撑层建议不得补造 BKN 依据。
- 每项建议必须给出精确的知识网络或直接支撑层位置、具体问题、具体修改、本轮 Operation 证据和可执行验收条件。
- 不得输出通用最佳实践、可能性建议、不存在的证据编号或业务溯源页面设计建议。

仅输出 JSON：{
  "decision": "change_required|no_change|not_evaluable",
  "summary": "...",
  "recommendations": [
    {
      "scope": "bkn|bkn_trace|mcp|sdk|agent",
      "location": "精确对象、关系、属性、指标、资源绑定、Skill、接口或提示词位置",
      "problem": "Trace 与核验证据支持的知识网络问题",
      "change": "具体修改内容",
      "trace_evidence_operation_ids": ["op_..."],
      "bkn_schema_evidence": ["BKN 类建议必填源 Schema 依据；直接支撑层没有则为空数组"],
      "acceptance": "可执行验收用例"
    }
  ]
}

当 decision 为 no_change 或 not_evaluable 时 recommendations 必须为空数组。', null,
    'bdd59f76-19c3-58b0-bf5f-082c4c3cbddb', unix_timestamp(now(3)) * 1000
from t_agent_prompt_version source
join t_agent_prompt p
  on p.f_prompt_id = source.f_prompt_id and p.f_current_version = source.f_version
where p.f_prompt_id = 'business_provenance_optimizer_prompt'
  and p.f_update_user = 'bdd59f76-19c3-58b0-bf5f-082c4c3cbddb'
  and p.f_update_time = source.f_create_time
  and source.f_create_user = 'bdd59f76-19c3-58b0-bf5f-082c4c3cbddb' and (source.f_vars_schema is null or lower(json_type(source.f_vars_schema)) = 'null')
  and sha2(source.f_content, 256) in ('ba6cc00eb98ad802414a26251878dd80316dcc2928bbb9d67ad9b01b94ab73d2', 'c23ed3adabef9395d3599980a3135e3bc0dbf75baff67ec9edc6ed0341d13b0e')
  and exists (
      select 1 from t_agent a where a.f_agent_id = 'business_provenance_optimizer'
        and a.f_create_user = 'bdd59f76-19c3-58b0-bf5f-082c4c3cbddb' and a.f_prompt_id = p.f_prompt_id
        and (a.f_prompt_vars_schema is null or lower(json_type(a.f_prompt_vars_schema)) = 'null')
  )
  and not exists (
      select 1 from t_agent_prompt_version target
      where target.f_prompt_id = source.f_prompt_id and target.f_version = source.f_version + 1
  );

update t_agent_prompt
set f_current_version = f_current_version + 1,
    f_update_user = 'bdd59f76-19c3-58b0-bf5f-082c4c3cbddb',
    f_update_time = unix_timestamp(now(3)) * 1000
where f_prompt_id = 'business_provenance_optimizer_prompt'
  and f_update_user = 'bdd59f76-19c3-58b0-bf5f-082c4c3cbddb'
  and exists (
      select 1 from t_agent a where a.f_agent_id = 'business_provenance_optimizer'
        and a.f_create_user = 'bdd59f76-19c3-58b0-bf5f-082c4c3cbddb' and a.f_prompt_id = t_agent_prompt.f_prompt_id
        and (a.f_prompt_vars_schema is null or lower(json_type(a.f_prompt_vars_schema)) = 'null')
  )
  and exists (
      select 1 from t_agent_prompt_version source
      join t_agent_prompt_version target
        on target.f_prompt_id = source.f_prompt_id and target.f_version = source.f_version + 1
      where source.f_prompt_id = t_agent_prompt.f_prompt_id
        and source.f_version = t_agent_prompt.f_current_version
        and source.f_create_time = t_agent_prompt.f_update_time
        and source.f_create_user = 'bdd59f76-19c3-58b0-bf5f-082c4c3cbddb' and (source.f_vars_schema is null or lower(json_type(source.f_vars_schema)) = 'null')
        and sha2(source.f_content, 256) in ('ba6cc00eb98ad802414a26251878dd80316dcc2928bbb9d67ad9b01b94ab73d2', 'c23ed3adabef9395d3599980a3135e3bc0dbf75baff67ec9edc6ed0341d13b0e')
        and target.f_create_user = 'bdd59f76-19c3-58b0-bf5f-082c4c3cbddb' and (target.f_vars_schema is null or lower(json_type(target.f_vars_schema)) = 'null')
        and sha2(target.f_content, 256) = 'b7bfaaf957df4f0c0339b03a2e47dd3cc9c810b2ee5b799f8f7c1fc4b6b072e5'
  );
