-- Copyright openbkn.ai
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

USE openbkn;

-- Keep current-read binding range locks scoped to one Prompt.
CREATE INDEX IF NOT EXISTS ix_agent_prompt_id_agent_id
    ON t_agent (f_prompt_id, f_agent_id);
