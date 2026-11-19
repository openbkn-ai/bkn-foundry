-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

-- bkn-project-ontology-ask limits: the pass diagnose needs room for its
-- structured result, which the official 1024 output tokens and 40 seconds cut
-- off. The official limits become 16384 output tokens and 180 seconds; one turn
-- and no tool calls stay. Interactive Ask AI stays bounded by bkn-project's own
-- 45 second wait.
-- Only the untouched official configuration is changed: the Agent and its
-- current version still have exactly the old official limits, written by the
-- official service user. The change is recorded as a new current Agent version
-- (changes ["limits"], the current prompt version), and t_agent, which a run
-- reads, follows it. Restoring defaults then selects the official snapshot with
-- the new limits: an inactive one is appended when the current configuration
-- was customized, whose limits, model, tools and history all stay. Replay
-- resumes interrupted updates. A fresh install starts with the new limits.

USE openbkn;

-- Record the new limits as the next version of the untouched official configuration.
insert into t_agent_version (
    f_agent_id, f_version, f_model, f_tools, f_limits, f_prompt_version,
    f_changes, f_initial, f_current, f_deleted, f_create_user, f_create_time
)
select a.f_agent_id, v.f_version + 1, coalesce(a.f_model, ''), coalesce(a.f_tools, '[]'),
    '{"max_turns":1,"max_tool_calls":0,"timeout_s":180,"max_output_tokens":16384}', v.f_prompt_version,
    '["limits"]', 0, 0, 0, '266c6a42-6131-4d62-8f39-853e7093701c', unix_timestamp(now(3)) * 1000
from t_agent a
join t_agent_version v on v.f_agent_id = a.f_agent_id and v.f_current = 1
where a.f_agent_id = 'bkn-project-ontology-ask'
  and a.f_update_user = '266c6a42-6131-4d62-8f39-853e7093701c'
  and json_contains(a.f_limits, '{"max_turns":1,"max_tool_calls":0,"timeout_s":40,"max_output_tokens":1024}') = 1
  and json_contains('{"max_turns":1,"max_tool_calls":0,"timeout_s":40,"max_output_tokens":1024}', a.f_limits) = 1
  and v.f_create_user = '266c6a42-6131-4d62-8f39-853e7093701c' and v.f_deleted = 0
  and json_contains(v.f_limits, '{"max_turns":1,"max_tool_calls":0,"timeout_s":40,"max_output_tokens":1024}') = 1
  and json_contains('{"max_turns":1,"max_tool_calls":0,"timeout_s":40,"max_output_tokens":1024}', v.f_limits) = 1
  and not exists (
      select 1 from t_agent_version newer
      where newer.f_agent_id = v.f_agent_id and newer.f_version > v.f_version
  );

update t_agent_version
set f_current = 0
where f_agent_id = 'bkn-project-ontology-ask' and f_current = 1
  and json_contains(f_limits, '{"max_turns":1,"max_tool_calls":0,"timeout_s":40,"max_output_tokens":1024}') = 1
  and json_contains('{"max_turns":1,"max_tool_calls":0,"timeout_s":40,"max_output_tokens":1024}', f_limits) = 1
  and exists (
      select 1 from t_agent_version newer
      where newer.f_agent_id = t_agent_version.f_agent_id
        and newer.f_version = t_agent_version.f_version + 1
        and newer.f_prompt_version = t_agent_version.f_prompt_version
        and newer.f_model = t_agent_version.f_model and newer.f_tools = t_agent_version.f_tools
        and newer.f_create_user = '266c6a42-6131-4d62-8f39-853e7093701c'
        and json_contains(newer.f_changes, '["limits"]') = 1 and json_length(newer.f_changes) = 1
        and json_contains(newer.f_limits, '{"max_turns":1,"max_tool_calls":0,"timeout_s":180,"max_output_tokens":16384}') = 1
        and json_contains('{"max_turns":1,"max_tool_calls":0,"timeout_s":180,"max_output_tokens":16384}', newer.f_limits) = 1
  );

update t_agent_version
set f_current = 1
where f_agent_id = 'bkn-project-ontology-ask'
  and f_create_user = '266c6a42-6131-4d62-8f39-853e7093701c'
  and json_contains(f_changes, '["limits"]') = 1 and json_length(f_changes) = 1
  and json_contains(f_limits, '{"max_turns":1,"max_tool_calls":0,"timeout_s":180,"max_output_tokens":16384}') = 1
  and json_contains('{"max_turns":1,"max_tool_calls":0,"timeout_s":180,"max_output_tokens":16384}', f_limits) = 1
  and not exists (
      select 1 from t_agent_version newer
      where newer.f_agent_id = t_agent_version.f_agent_id and newer.f_version > t_agent_version.f_version
  )
  and not exists (
      select 1 from t_agent_version current_version
      where current_version.f_agent_id = t_agent_version.f_agent_id and current_version.f_current = 1
  );

-- The Agent row, which a run reads, follows its current version.
update t_agent
set f_limits = '{"max_turns":1,"max_tool_calls":0,"timeout_s":180,"max_output_tokens":16384}',
    f_update_user = '266c6a42-6131-4d62-8f39-853e7093701c',
    f_update_time = unix_timestamp(now(3)) * 1000
where f_agent_id = 'bkn-project-ontology-ask'
  and f_update_user = '266c6a42-6131-4d62-8f39-853e7093701c'
  and json_contains(f_limits, '{"max_turns":1,"max_tool_calls":0,"timeout_s":40,"max_output_tokens":1024}') = 1
  and json_contains('{"max_turns":1,"max_tool_calls":0,"timeout_s":40,"max_output_tokens":1024}', f_limits) = 1
  and exists (
      select 1 from t_agent_version v
      where v.f_agent_id = t_agent.f_agent_id and v.f_current = 1
        and v.f_create_user = '266c6a42-6131-4d62-8f39-853e7093701c'
        and json_contains(v.f_changes, '["limits"]') = 1 and json_length(v.f_changes) = 1
        and json_contains(v.f_limits, '{"max_turns":1,"max_tool_calls":0,"timeout_s":180,"max_output_tokens":16384}') = 1
        and json_contains('{"max_turns":1,"max_tool_calls":0,"timeout_s":180,"max_output_tokens":16384}', v.f_limits) = 1
  );

-- Advance the official restore baseline to the new limits without adopting
-- custom runtime settings. Reuse the version recorded above when it is the
-- official snapshot; otherwise append an inactive baseline beside the
-- customized current: the original seed's model and tools, the new limits and
-- the baseline's prompt version.
insert into t_agent_version (
    f_agent_id, f_version, f_model, f_tools, f_limits, f_prompt_version,
    f_changes, f_initial, f_current, f_deleted, f_create_user, f_create_time
)
select original.f_agent_id, latest.f_version + 1,
    original.f_model, original.f_tools,
    '{"max_turns":1,"max_tool_calls":0,"timeout_s":180,"max_output_tokens":16384}', baseline.f_prompt_version,
    replace(concat('[',
        case when original.f_model <> latest.f_model then '"model",' else '' end,
        case when original.f_tools <> latest.f_tools then '"tools",' else '' end,
        case when coalesce(json_contains(latest.f_limits, '{"max_turns":1,"max_tool_calls":0,"timeout_s":180,"max_output_tokens":16384}'), 0) = 1
              and coalesce(json_contains('{"max_turns":1,"max_tool_calls":0,"timeout_s":180,"max_output_tokens":16384}', latest.f_limits), 0) = 1
            then '' else '"limits",' end,
        case when coalesce(latest.f_prompt_version, 0) <> baseline.f_prompt_version then '"prompt_version",' else '' end,
        ']'), ',]', ']'),
    0, 0, 0, '266c6a42-6131-4d62-8f39-853e7093701c', unix_timestamp(now(3)) * 1000
from t_agent_version original
join t_agent_version latest on latest.f_agent_id = original.f_agent_id
join t_agent_version baseline on baseline.f_agent_id = original.f_agent_id and baseline.f_initial = 1
where original.f_agent_id = 'bkn-project-ontology-ask'
  and original.f_version = 1
  and original.f_create_user = '266c6a42-6131-4d62-8f39-853e7093701c'
  and latest.f_version = (
      select max(v.f_version) from t_agent_version v where v.f_agent_id = original.f_agent_id
  )
  and baseline.f_deleted = 0
  and baseline.f_model = original.f_model and baseline.f_tools = original.f_tools
  and json_contains(baseline.f_limits, '{"max_turns":1,"max_tool_calls":0,"timeout_s":40,"max_output_tokens":1024}') = 1
  and json_contains('{"max_turns":1,"max_tool_calls":0,"timeout_s":40,"max_output_tokens":1024}', baseline.f_limits) = 1
  and not exists (
      select 1 from t_agent_version v where v.f_agent_id = original.f_agent_id
        and v.f_prompt_version = baseline.f_prompt_version and v.f_deleted = 0
        and v.f_model = original.f_model and v.f_tools = original.f_tools
        and json_contains(v.f_limits, '{"max_turns":1,"max_tool_calls":0,"timeout_s":180,"max_output_tokens":16384}') = 1
        and json_contains('{"max_turns":1,"max_tool_calls":0,"timeout_s":180,"max_output_tokens":16384}', v.f_limits) = 1
  );

-- Transfer only the initial marker, from the official snapshot with the old
-- limits to the one with the new limits and the same prompt version. Earlier
-- snapshots remain available in history.
update t_agent_version
set f_initial = case when f_version = (
    select min(target.f_version)
    from t_agent_version target
    join t_agent_version original on original.f_agent_id = target.f_agent_id and original.f_version = 1
    join t_agent_version baseline on baseline.f_agent_id = target.f_agent_id and baseline.f_initial = 1
    where target.f_agent_id = 'bkn-project-ontology-ask'
      and target.f_deleted = 0 and target.f_prompt_version = baseline.f_prompt_version
      and target.f_model = original.f_model and target.f_tools = original.f_tools
      and json_contains(target.f_limits, '{"max_turns":1,"max_tool_calls":0,"timeout_s":180,"max_output_tokens":16384}') = 1
      and json_contains('{"max_turns":1,"max_tool_calls":0,"timeout_s":180,"max_output_tokens":16384}', target.f_limits) = 1
) then 1 else 0 end
where f_agent_id = 'bkn-project-ontology-ask'
  and exists (
      select 1 from t_agent_version original
      where original.f_agent_id = 'bkn-project-ontology-ask' and original.f_version = 1
        and original.f_create_user = '266c6a42-6131-4d62-8f39-853e7093701c'
  )
  and exists (
      select 1 from t_agent_version baseline join t_agent_version original
        on original.f_agent_id = baseline.f_agent_id and original.f_version = 1
      where baseline.f_agent_id = 'bkn-project-ontology-ask'
        and baseline.f_initial = 1 and baseline.f_deleted = 0
        and baseline.f_model = original.f_model and baseline.f_tools = original.f_tools
        and json_contains(baseline.f_limits, '{"max_turns":1,"max_tool_calls":0,"timeout_s":40,"max_output_tokens":1024}') = 1
        and json_contains('{"max_turns":1,"max_tool_calls":0,"timeout_s":40,"max_output_tokens":1024}', baseline.f_limits) = 1
        and exists (
            select 1 from t_agent_version target
            where target.f_agent_id = baseline.f_agent_id
              and target.f_deleted = 0 and target.f_prompt_version = baseline.f_prompt_version
              and target.f_model = original.f_model and target.f_tools = original.f_tools
              and json_contains(target.f_limits, '{"max_turns":1,"max_tool_calls":0,"timeout_s":180,"max_output_tokens":16384}') = 1
              and json_contains('{"max_turns":1,"max_tool_calls":0,"timeout_s":180,"max_output_tokens":16384}', target.f_limits) = 1
        )
  );
