-- Copyright 2026 openbkn.ai
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.

-- Align catalog prompt output with the logical-view response schema.
-- Prompt versions are append-only; preserve operator-selected versions and
-- do not activate an existing v2 whose content differs from this upgrade.
USE openbkn;

insert into t_agent_prompt_version (
    f_prompt_id, f_version, f_content, f_vars_schema, f_create_user, f_create_time
)
select
    source.f_prompt_id,
    2,
    replace(source.f_content, 'obsolete_logic_views', 'obsolete_logical_views'),
    source.f_vars_schema,
    '266c6a42-6131-4d62-8f39-853e7093701c',
    unix_timestamp(now(3)) * 1000
from t_agent_prompt_version source
where source.f_prompt_id = 'catalog-semantic-understanding-prompt'
  and source.f_version = 1
  and not exists (
    select 1 from t_agent_prompt_version
    where f_prompt_id = 'catalog-semantic-understanding-prompt' and f_version = 2
);

update t_agent_prompt
set f_current_version = 2,
    f_update_user = '266c6a42-6131-4d62-8f39-853e7093701c',
    f_update_time = unix_timestamp(now(3)) * 1000
where f_prompt_id = 'catalog-semantic-understanding-prompt'
  and f_current_version = 1
  and exists (
    select 1
    from t_agent_prompt_version source
    join t_agent_prompt_version target on target.f_prompt_id = source.f_prompt_id
    where source.f_prompt_id = 'catalog-semantic-understanding-prompt'
      and source.f_version = 1 and target.f_version = 2
      and target.f_content = replace(source.f_content, 'obsolete_logic_views', 'obsolete_logical_views')
  );
