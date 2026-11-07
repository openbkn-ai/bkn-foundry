-- Copyright openbkn.ai
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

USE openbkn;

-- Document extraction has no tools: its free-form first call only drafted an
-- answer the structured call redid with the schema, often filling the output cap
-- with an empty body. Publish v2 with single_structured_call. Builds pin the
-- version they started with, so running builds keep v1. The limits are written
-- whole, as the fresh snapshot has them: only the untouched default is upgraded.
-- Preserve v1 and every administrator-customized Agent; replay is a no-op.
START TRANSACTION;
SELECT f_agent_id FROM t_agent
WHERE f_agent_id = 'bkn-project-document-extraction' FOR UPDATE;

insert into t_agent_version (
 f_agent_id, f_version, f_model, f_tools, f_limits, f_prompt_version,
 f_changes, f_initial, f_current, f_deleted, f_create_user, f_create_time
)
select v.f_agent_id, 2, v.f_model, v.f_tools,
 '{"max_turns":1,"max_tool_calls":0,"timeout_s":300,"max_output_tokens":16384,"single_structured_call":true}', v.f_prompt_version,
 '["limits"]', 1, 1, 0, '266c6a42-6131-4d62-8f39-853e7093701c', UNIX_TIMESTAMP(NOW(3)) * 1000
from t_agent_version v join t_agent a on a.f_agent_id = v.f_agent_id
join t_agent_prompt p on p.f_prompt_id = a.f_prompt_id
where a.f_agent_id = 'bkn-project-document-extraction'
 and a.f_create_user = '266c6a42-6131-4d62-8f39-853e7093701c' and v.f_create_user = '266c6a42-6131-4d62-8f39-853e7093701c'
 and a.f_update_time <= UNIX_TIMESTAMP(NOW(3)) * 1000
 and a.f_mode = 'task' and a.f_status = 'published'
 and a.f_prompt_id = 'bkn-project-document-extraction-prompt' and p.f_current_version = 1
 and a.f_model = '' and JSON_LENGTH(a.f_tools) = 0
 and JSON_LENGTH(COALESCE(a.f_skills, '[]')) = 0
 and JSON_LENGTH(COALESCE(a.f_prompt_vars_schema, '{}')) = 0
 and v.f_version = 1 and v.f_current = 1 and v.f_initial = 1 and v.f_deleted = 0
 and v.f_prompt_version = 1 and a.f_model = v.f_model and a.f_tools = v.f_tools
 and JSON_CONTAINS(a.f_limits, '{"max_turns":1,"max_tool_calls":0,"timeout_s":300,"max_output_tokens":16384}') = 1
 and JSON_CONTAINS('{"max_turns":1,"max_tool_calls":0,"timeout_s":300,"max_output_tokens":16384}', a.f_limits) = 1
 and JSON_CONTAINS(v.f_limits, '{"max_turns":1,"max_tool_calls":0,"timeout_s":300,"max_output_tokens":16384}') = 1
 and JSON_CONTAINS('{"max_turns":1,"max_tool_calls":0,"timeout_s":300,"max_output_tokens":16384}', v.f_limits) = 1
 and not exists (select 1 from t_agent_version newer where newer.f_agent_id = a.f_agent_id and newer.f_version > 1);

update t_agent
set f_limits = '{"max_turns":1,"max_tool_calls":0,"timeout_s":300,"max_output_tokens":16384,"single_structured_call":true}',
 f_update_time = UNIX_TIMESTAMP(NOW(3)) * 1000
where f_agent_id = 'bkn-project-document-extraction'
 and JSON_CONTAINS(f_limits, '{"max_turns":1,"max_tool_calls":0,"timeout_s":300,"max_output_tokens":16384}') = 1
 and JSON_CONTAINS('{"max_turns":1,"max_tool_calls":0,"timeout_s":300,"max_output_tokens":16384}', f_limits) = 1
 and exists (select 1 from t_agent_version v where v.f_agent_id = t_agent.f_agent_id
  and v.f_version = 2 and v.f_current = 1 and v.f_initial = 1 and v.f_deleted = 0
  and v.f_create_user = '266c6a42-6131-4d62-8f39-853e7093701c' and v.f_prompt_version = 1
  and v.f_model = t_agent.f_model and v.f_tools = t_agent.f_tools
  and t_agent.f_update_time <= v.f_create_time
  and JSON_CONTAINS(v.f_limits, '{"max_turns":1,"max_tool_calls":0,"timeout_s":300,"max_output_tokens":16384,"single_structured_call":true}') = 1
  and JSON_CONTAINS('{"max_turns":1,"max_tool_calls":0,"timeout_s":300,"max_output_tokens":16384,"single_structured_call":true}', v.f_limits) = 1);

update t_agent_version
set f_current = 0, f_initial = 0
where f_agent_id = 'bkn-project-document-extraction' and f_version = 1
 and exists (select 1 from t_agent a join t_agent_version v on v.f_agent_id = a.f_agent_id
  where a.f_agent_id = t_agent_version.f_agent_id and v.f_version = 2
   and v.f_current = 1 and v.f_initial = 1 and v.f_deleted = 0
   and v.f_create_user = '266c6a42-6131-4d62-8f39-853e7093701c' and v.f_prompt_version = 1
   and JSON_CONTAINS(a.f_limits, '{"max_turns":1,"max_tool_calls":0,"timeout_s":300,"max_output_tokens":16384,"single_structured_call":true}') = 1
   and JSON_CONTAINS('{"max_turns":1,"max_tool_calls":0,"timeout_s":300,"max_output_tokens":16384,"single_structured_call":true}', a.f_limits) = 1);
COMMIT;
