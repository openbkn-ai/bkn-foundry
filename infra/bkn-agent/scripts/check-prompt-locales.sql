-- Read-only preflight: run before deploying an Agent model that selects this column.
-- A missing table/column is an error; no stored content is returned.
SELECT f_localized_content, f_source_language FROM openbkn.t_agent_prompt_version LIMIT 0;
