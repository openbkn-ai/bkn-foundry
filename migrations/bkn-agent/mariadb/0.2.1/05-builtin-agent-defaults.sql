-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

USE openbkn;

-- What each built-in Agent shipped with, so an administrator can restore it.
-- Captured once, after the seeds and their upgrades above, from the Agent as it
-- stands; an existing row is never overwritten. A later release that ships new
-- defaults for an untouched Agent updates its row in the same delta.
create table if not exists t_agent_builtin_default
(
    f_agent_id       varchar(50)  not null,
    f_model          varchar(100) not null default '',
    f_tools          json         not null,
    f_limits         json         null,
    f_prompt_version int          null,
    f_create_time    bigint       not null,
    primary key (f_agent_id)
) engine = InnoDB default charset = utf8mb4;

insert into t_agent_builtin_default (
    f_agent_id, f_model, f_tools, f_limits, f_prompt_version, f_create_time
)
select a.f_agent_id, a.f_model, a.f_tools, a.f_limits, p.f_current_version,
    unix_timestamp(now(3)) * 1000
from t_agent a
left join t_agent_prompt p on p.f_prompt_id = a.f_prompt_id
where a.f_agent_id in (
        'studio_smart_qa',
        'business_provenance_claim_attribution',
        'business_provenance_optimizer',
        'catalog-semantic-understanding',
        'resource-semantic-understanding'
    )
  and not exists (
      select 1 from t_agent_builtin_default d where d.f_agent_id = a.f_agent_id
  );

-- Prompt versions an administrator removed from a built-in Agent's history. The
-- version row itself stays, so its number is never reused and runs that recorded
-- it can still be traced to the exact content.
create table if not exists t_agent_prompt_version_hidden
(
    f_prompt_id   varchar(50) not null,
    f_version     int         not null,
    f_hide_user   varchar(50) not null,
    f_hide_time   bigint      not null,
    primary key (f_prompt_id, f_version)
) engine = InnoDB default charset = utf8mb4;
