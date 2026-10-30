-- Copyright openbkn.ai
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

USE openbkn;

-- Legacy originals stay unmarked until an administrator confirms their language.
alter table t_agent_prompt_version
    add column if not exists f_source_language varchar(10) null after f_content;
