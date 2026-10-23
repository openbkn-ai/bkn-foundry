-- Read-only schema preflight: run after component schema 0.2.3 migration.
USE openbkn;
SELECT f_agent_id FROM t_agent FORCE INDEX (ix_agent_prompt_id_agent_id) LIMIT 0;
SHOW INDEX FROM t_agent WHERE Key_name = 'ix_agent_prompt_id_agent_id';
