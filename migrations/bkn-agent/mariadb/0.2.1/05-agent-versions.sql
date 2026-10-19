-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

USE openbkn;

-- Every Agent's configuration as versions: a snapshot of model, tools, limits
-- and the shared Prompt version it points at (Prompt content stays in
-- t_agent_prompt_version). Any change writes a new version; switching applies a
-- snapshot back. Exactly one version per Agent is current. A deleted version is
-- only flagged, so its number is never handed out again. A built-in Agent's v1
-- is its initial version, which restore returns to.
create table if not exists t_agent_version
(
    f_agent_id       varchar(50)  not null,
    f_version        int          not null,
    f_model          varchar(100) not null default '',
    f_tools          json         not null,
    f_limits         json         null,
    f_prompt_version int          null,
    f_changes        json         null,
    f_initial        tinyint(1)   not null default 0,
    f_current        tinyint(1)   not null default 0,
    f_deleted        tinyint(1)   not null default 0,
    f_create_user    varchar(50)  not null,
    f_create_time    bigint       not null,
    primary key (f_agent_id, f_version)
) engine = InnoDB default charset = utf8mb4;

insert into t_agent_version (
    f_agent_id, f_version, f_model, f_tools, f_limits, f_prompt_version,
    f_changes, f_initial, f_current, f_deleted, f_create_user, f_create_time
)
-- Older rows may hold SQL NULL tools or model; the snapshot columns do not.
select a.f_agent_id, 1, coalesce(a.f_model, ''), coalesce(a.f_tools, '[]'), a.f_limits,
    p.f_current_version,
    null,
    case when a.f_agent_id in (
        'studio_smart_qa',
        'business_provenance_claim_attribution',
        'business_provenance_optimizer',
        'catalog-semantic-understanding',
        'resource-semantic-understanding'
    ) then 1 else 0 end,
    1, 0, a.f_update_user, unix_timestamp(now(3)) * 1000
from t_agent a
left join t_agent_prompt p on p.f_prompt_id = a.f_prompt_id
where not exists (select 1 from t_agent_version v where v.f_agent_id = a.f_agent_id);
