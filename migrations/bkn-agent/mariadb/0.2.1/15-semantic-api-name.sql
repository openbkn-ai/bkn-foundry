-- Copyright (c) 2026 OpenBKN
-- SPDX-License-Identifier: LicenseRef-OpenBKN
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

-- NAME-1b: ask resource semantic understanding for an English short name
-- (api_name) per resource and field, in the same pass. Same rules as 08:
-- upgrade only the untouched official v4, preserve customized prompts, every
-- historical prompt/Agent version and the operator's runtime settings; replay
-- resumes interrupted updates. The original seed runtime is Agent version 1,
-- whose prompt is v3 after an upgrade from 0.2.0. It is v4 on a database
-- installed from an earlier 0.2.1 snapshot; data-migrator does not re-run
-- deltas of the installed version, so such a database gets this script only
-- when it is applied by hand or its progress is reset.
USE openbkn;

insert into t_agent_prompt_version (
    f_prompt_id, f_version, f_content, f_vars_schema, f_create_user, f_create_time
)
select v.f_prompt_id, 5,
    concat(v.f_content, '\n\napi_name 规则：为资源和 fields 中返回的每个字段给出 api_name，作为机器可用的英文短名称。使用 snake_case：以小写英文字母开头，只含小写英文字母、数字和下划线，不超过 40 个字符。api_name 始终使用英文，不受 options.language 影响。应表达业务含义，不使用拼音或逐字音译；物理名称已是合适的英文时，使用其规范化形式，例如 supplierId 输出 supplier_id。同一资源内各字段的 api_name 不得重复。无法给出合适的英文短名称时省略 api_name，不要编造无意义的名称。示例：表“供应商档案”输出 supplier，字段“联系电话”输出 contact_phone。'),
    v.f_vars_schema, '266c6a42-6131-4d62-8f39-853e7093701c', unix_timestamp(now(3)) * 1000
from t_agent_prompt_version v
join t_agent_prompt p on p.f_prompt_id = v.f_prompt_id
where v.f_prompt_id = 'resource-semantic-understanding-prompt'
  and v.f_version = 4 and p.f_current_version = 4
  and sha2(v.f_content, 256) = '0ab0a265fc0f0953d6f06b001ed0671893f20019ddde4049af1602a742e3e3f3'
  and not exists (
      select 1 from t_agent_prompt_version newer
      where newer.f_prompt_id = v.f_prompt_id and newer.f_version > 4
  );

update t_agent_prompt
set f_current_version = 5,
    f_update_user = '266c6a42-6131-4d62-8f39-853e7093701c',
    f_update_time = unix_timestamp(now(3)) * 1000
where f_prompt_id = 'resource-semantic-understanding-prompt'
  and f_current_version = 4
  and f_update_user = '266c6a42-6131-4d62-8f39-853e7093701c'
  and exists (
      select 1 from t_agent_prompt_version v
      where v.f_prompt_id = t_agent_prompt.f_prompt_id and v.f_version = 4
        and sha2(v.f_content, 256) = '0ab0a265fc0f0953d6f06b001ed0671893f20019ddde4049af1602a742e3e3f3'
  )
  and exists (
      select 1 from t_agent_prompt_version v
      where v.f_prompt_id = t_agent_prompt.f_prompt_id and v.f_version = 5
        and sha2(v.f_content, 256) = '813dff66c8ea6f830601b173347936752f02e545d45781b4bb68266d13855eb1'
        and t_agent_prompt.f_update_time <= v.f_create_time
  );

-- Record the changed shared Prompt as a new Agent configuration snapshot,
-- keeping the operator's model/tools/limits and retaining all historical snapshots.
insert into t_agent_version (
    f_agent_id, f_version, f_model, f_tools, f_limits, f_prompt_version,
    f_changes, f_initial, f_current, f_deleted, f_create_user, f_create_time
)
select a.f_agent_id, v.f_version + 1, coalesce(a.f_model, ''), coalesce(a.f_tools, '[]'),
    a.f_limits, 5, '["prompt_version"]',
    0, 0, 0, '266c6a42-6131-4d62-8f39-853e7093701c', unix_timestamp(now(3)) * 1000
from t_agent a
join t_agent_prompt p on p.f_prompt_id = a.f_prompt_id
join t_agent_prompt_version prompt on prompt.f_prompt_id = p.f_prompt_id and prompt.f_version = 5
join t_agent_version v on v.f_agent_id = a.f_agent_id and v.f_current = 1
where a.f_agent_id = 'resource-semantic-understanding'
  and p.f_current_version = 5 and v.f_prompt_version = 4
  and sha2(prompt.f_content, 256) = '813dff66c8ea6f830601b173347936752f02e545d45781b4bb68266d13855eb1'
  and not exists (
      select 1 from t_agent_version newer
      where newer.f_agent_id = a.f_agent_id and newer.f_version > v.f_version
  );

update t_agent_version
set f_current = 0
where f_agent_id = 'resource-semantic-understanding' and f_current = 1 and f_prompt_version = 4
  and exists (
      select 1 from t_agent_prompt p
      where p.f_prompt_id = 'resource-semantic-understanding-prompt' and p.f_current_version = 5
  )
  and exists (
      select 1 from t_agent_version newer
      where newer.f_agent_id = t_agent_version.f_agent_id
        and newer.f_version = t_agent_version.f_version + 1
        and newer.f_prompt_version = 5
        and newer.f_create_user = '266c6a42-6131-4d62-8f39-853e7093701c'
  );

update t_agent_version
set f_current = 1
where f_agent_id = 'resource-semantic-understanding' and f_prompt_version = 5
  and exists (
      select 1 from t_agent_prompt p
      where p.f_prompt_id = 'resource-semantic-understanding-prompt' and p.f_current_version = 5
  )
  and f_create_user = '266c6a42-6131-4d62-8f39-853e7093701c'
  and json_contains(f_changes, '["prompt_version"]') = 1
  and not exists (
      select 1 from t_agent_version current_version
      where current_version.f_agent_id = t_agent_version.f_agent_id and current_version.f_current = 1
  );

-- Advance the official restore baseline without adopting custom runtime settings.
-- Reuse the current v5 snapshot when its runtime matches the original seed;
-- otherwise append an inactive baseline, leaving the customized current intact.
insert into t_agent_version (
    f_agent_id, f_version, f_model, f_tools, f_limits, f_prompt_version,
    f_changes, f_initial, f_current, f_deleted, f_create_user, f_create_time
)
select original.f_agent_id, latest.f_version + 1,
    original.f_model, original.f_tools, original.f_limits, 5,
    replace(concat('[',
        case when original.f_model <> latest.f_model then '"model",' else '' end,
        case when original.f_tools <> latest.f_tools then '"tools",' else '' end,
        case when coalesce(original.f_limits, '') <> coalesce(latest.f_limits, '') then '"limits",' else '' end,
        ']'), ',]', ']'),
    0, 0, 0, '266c6a42-6131-4d62-8f39-853e7093701c', unix_timestamp(now(3)) * 1000
from t_agent_version original
join t_agent_version latest on latest.f_agent_id = original.f_agent_id
where original.f_agent_id = 'resource-semantic-understanding'
  and original.f_version = 1
  and original.f_create_user = '266c6a42-6131-4d62-8f39-853e7093701c'
  and latest.f_version = (
      select max(v.f_version) from t_agent_version v where v.f_agent_id = original.f_agent_id
  )
  and exists (
      select 1 from t_agent_prompt p join t_agent_prompt_version v
        on v.f_prompt_id = p.f_prompt_id and v.f_version = 5
      where p.f_prompt_id = 'resource-semantic-understanding-prompt' and p.f_current_version = 5
        and sha2(v.f_content, 256) = '813dff66c8ea6f830601b173347936752f02e545d45781b4bb68266d13855eb1'
  )
  and exists (
      select 1 from t_agent_version v where v.f_agent_id = original.f_agent_id
        and v.f_current = 1 and v.f_prompt_version = 5
  )
  and not exists (
      select 1 from t_agent_version v where v.f_agent_id = original.f_agent_id
        and v.f_prompt_version = 5 and v.f_deleted = 0
        and v.f_model = original.f_model and v.f_tools = original.f_tools
        and coalesce(v.f_limits, '') = coalesce(original.f_limits, '')
  );

-- Transfer only the initial marker. Earlier snapshots remain available in
-- history; restoring defaults now selects the matching official v5 snapshot.
update t_agent_version
set f_initial = case when f_version = (
    select min(baseline.f_version)
    from t_agent_version baseline join t_agent_version original
      on original.f_agent_id = baseline.f_agent_id and original.f_version = 1
    where baseline.f_agent_id = 'resource-semantic-understanding'
      and baseline.f_prompt_version = 5 and baseline.f_deleted = 0
      and baseline.f_model = original.f_model and baseline.f_tools = original.f_tools
      and coalesce(baseline.f_limits, '') = coalesce(original.f_limits, '')
) then 1 else 0 end
where f_agent_id = 'resource-semantic-understanding'
  and exists (
      select 1 from t_agent_version original
      where original.f_agent_id = 'resource-semantic-understanding' and original.f_version = 1
        and original.f_create_user = '266c6a42-6131-4d62-8f39-853e7093701c'
  )
  and exists (
      select 1 from t_agent_version baseline join t_agent_version original
        on original.f_agent_id = baseline.f_agent_id and original.f_version = 1
      where baseline.f_agent_id = 'resource-semantic-understanding'
        and baseline.f_prompt_version = 5 and baseline.f_deleted = 0
        and baseline.f_model = original.f_model and baseline.f_tools = original.f_tools
        and coalesce(baseline.f_limits, '') = coalesce(original.f_limits, '')
  )
  and exists (
      select 1 from t_agent_prompt p join t_agent_prompt_version v
        on v.f_prompt_id = p.f_prompt_id and v.f_version = 5
      where p.f_prompt_id = 'resource-semantic-understanding-prompt' and p.f_current_version = 5
        and sha2(v.f_content, 256) = '813dff66c8ea6f830601b173347936752f02e545d45781b4bb68266d13855eb1'
  );
