-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

USE openbkn;

-- Complex metric AST proposals exhausted the seeded 4096-token limit in
-- reasoning before emitting JSON. Upgrade only the untouched seed default;
-- preserve administrator-edited limits and all other Agent settings.
UPDATE t_agent
SET f_limits = JSON_SET(f_limits, '$.max_output_tokens', 8192),
    f_update_time = UNIX_TIMESTAMP(NOW(3)) * 1000
WHERE f_agent_id = 'object_metric_designer'
  AND f_prompt_id = 'object_metric_designer_prompt'
  AND JSON_CONTAINS(f_limits, '{"max_turns":6,"max_tool_calls":0,"timeout_s":120,"max_output_tokens":4096}') = 1
  AND JSON_CONTAINS('{"max_turns":6,"max_tool_calls":0,"timeout_s":120,"max_output_tokens":4096}', f_limits) = 1;
