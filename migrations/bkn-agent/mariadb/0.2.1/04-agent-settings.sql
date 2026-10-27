-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

USE openbkn;

-- Per-network and per-user Agent settings for the public execution surface.
-- Each row only appends instructions and narrows tools and limits on top of the
-- shared Agent definition; it never replaces the shared Prompt. The legacy
-- t_agent_prompt_override keeps its full-replacement meaning for internal routes.
create table if not exists t_agent_network_setting
(
    f_agent_id    varchar(50) not null,
    f_kn_id       varchar(128) not null,
    f_content     json        not null,
    f_version     int         not null default 1,
    f_update_user varchar(50) not null,
    f_update_time bigint      not null,
    primary key (f_agent_id, f_kn_id)
) engine = InnoDB default charset = utf8mb4;

create table if not exists t_agent_user_setting
(
    f_agent_id    varchar(50) not null,
    f_account_id  varchar(50) not null,
    f_content     json        not null,
    f_version     int         not null default 1,
    f_update_time bigint      not null,
    primary key (f_agent_id, f_account_id)
) engine = InnoDB default charset = utf8mb4;
