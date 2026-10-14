-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

-- 0.2.1 schema only; default content is installed by the explicit deployment command.
-- No runtime DDL and no rewriting of existing Agent/Prompt configuration.
USE openbkn;

create table if not exists t_agent_preset_version
(
    f_package_id varchar(100) not null,
    f_version varchar(50) not null,
    f_digest varchar(64) not null,
    f_package json not null,
    f_create_user varchar(50) not null,
    f_create_time bigint not null,
    primary key (f_package_id, f_version)
) engine = InnoDB default charset = utf8mb4;

-- Deliberately no cascading FK: deletion of an Agent/Prompt must not erase
-- provenance and cause the next deployment to recreate a removed default.
create table if not exists t_agent_preset_installation
(
    f_scenario varchar(100) not null,
    f_package_id varchar(100) not null,
    f_agent_id varchar(50) not null,
    f_prompt_id varchar(50) not null,
    f_owner_id varchar(50) not null,
    f_applied_version varchar(50) not null,
    f_applied_digest varchar(64) not null,
    f_revision int not null default 1,
    f_update_user varchar(50) not null,
    f_update_time bigint not null,
    primary key (f_scenario),
    unique key uk_agent_preset_package (f_package_id),
    unique key uk_agent_preset_agent (f_agent_id),
    unique key uk_agent_preset_prompt (f_prompt_id)
) engine = InnoDB default charset = utf8mb4;

create table if not exists t_agent_scenario_binding
(
    f_scenario varchar(100) not null,
    f_agent_id varchar(50) not null,
    f_enabled int not null default 1,
    f_revision int not null default 1,
    f_update_user varchar(50) not null,
    f_update_time bigint not null,
    primary key (f_scenario)
) engine = InnoDB default charset = utf8mb4;
