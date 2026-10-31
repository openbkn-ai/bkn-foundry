-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

-- Retire bkn-project-drafter, seeded by 13 (#2574): bkn-project never calls it,
-- drafting its ontology from counts on the data instead. Only the Agent the seed
-- created goes, as its creator, the built-in account, shows; an Agent of that id
-- created by anyone else stays with its prompt. Its versions go before it, and
-- its prompt with every version once no Agent uses the prompt. Settings,
-- overrides, threads and tasks that name it are left as records. Each statement
-- deletes only what is left, so the migration can be run again; a fresh install
-- never seeds the Agent.

USE openbkn;

delete from t_agent_version
where f_agent_id = 'bkn-project-drafter'
  and exists (
      select 1 from t_agent a
      where a.f_agent_id = 'bkn-project-drafter'
        and a.f_create_user = '266c6a42-6131-4d62-8f39-853e7093701c'
  );

delete from t_agent
where f_agent_id = 'bkn-project-drafter'
  and f_create_user = '266c6a42-6131-4d62-8f39-853e7093701c';

delete from t_agent_prompt_version
where f_prompt_id = 'bkn-project-drafter-prompt'
  and not exists (select 1 from t_agent a where a.f_prompt_id = 'bkn-project-drafter-prompt');

delete from t_agent_prompt
where f_prompt_id = 'bkn-project-drafter-prompt'
  and not exists (select 1 from t_agent a where a.f_prompt_id = 'bkn-project-drafter-prompt');
