-- bkn-agent 建表（Epic #202）。共享 openbkn 库，agent_ 前缀，纯新增零 ALTER。
-- 由全局 core-data-migrator pre-upgrade hook 执行，幂等。
-- USE 必须有：migrator 连接不带默认库，缺它执行报 1046 No database selected
-- （与其他服务 init.sql 同惯例）。
USE openbkn;

create table if not exists t_agent
(
    f_agent_id           varchar(50)  not null,
    f_name               varchar(100) not null,
    f_mode               varchar(10)  not null default 'chat',
    f_prompt_id          varchar(50)  null,
    f_prompt_vars_schema json         null,
    f_model              varchar(100) not null default '',
    f_tools              json         null,
    f_skills             json         null,
    f_limits             json         null,
    f_status             varchar(20)  not null default 'draft',
    f_create_user        varchar(50)  not null,
    f_update_user        varchar(50)  not null,
    f_create_time        bigint       not null,
    f_update_time        bigint       not null,
    primary key (f_agent_id),
    unique key uk_f_agent_name (f_name)
) engine = InnoDB default charset = utf8mb4;

create table if not exists t_agent_task
(
    f_task_id          varchar(50)  not null,
    f_agent_id         varchar(50)  not null,
    f_status           varchar(20)  not null default 'pending',
    f_input            json         null,
    f_output           mediumtext   null,
    f_failure_detail   text         null,
    f_parent_thread_id varchar(50)  null,
    f_account_id       varchar(50)  not null,
    f_create_time      bigint       not null,
    f_update_time      bigint       not null,
    primary key (f_task_id),
    key idx_agent_status (f_agent_id, f_status),
    key idx_parent_thread (f_parent_thread_id)
) engine = InnoDB default charset = utf8mb4;

create table if not exists t_agent_prompt
(
    f_prompt_id       varchar(50)  not null,
    f_name            varchar(100) not null,
    f_current_version int          not null,
    f_update_user     varchar(50)  not null,
    f_update_time     bigint       not null,
    primary key (f_prompt_id),
    unique key uk_f_prompt_name (f_name)
) engine = InnoDB default charset = utf8mb4;

-- 只增不改；回滚 = t_agent_prompt.f_current_version 指回旧版本
create table if not exists t_agent_prompt_version
(
    f_prompt_id   varchar(50) not null,
    f_version     int         not null,
    f_content     mediumtext  not null,
    f_vars_schema json        null,
    f_create_user varchar(50) not null,
    f_create_time bigint      not null,
    primary key (f_prompt_id, f_version)
) engine = InnoDB default charset = utf8mb4;

create table if not exists t_agent_prompt_override
(
    f_agent_id    varchar(50) not null,
    f_account_id  varchar(50) not null,
    f_content     mediumtext  not null,
    f_update_time bigint      not null,
    primary key (f_agent_id, f_account_id)
) engine = InnoDB default charset = utf8mb4;

-- thread 归属（/chat 续聊与 GET /threads/{id} 的授权依据；消息正文在 checkpointer 表）
create table if not exists t_agent_thread
(
    f_thread_id   varchar(50) not null,
    f_agent_id    varchar(50) not null,
    f_account_id  varchar(50) not null,
    f_create_time bigint      not null,
    f_update_time bigint      not null,
    primary key (f_thread_id),
    key idx_thread_account (f_account_id, f_update_time)
) engine = InnoDB default charset = utf8mb4;

-- LangGraph checkpointer 表（langgraph-checkpoint-mysql ~2.0.15）：表名由库固定
-- （checkpoints / checkpoint_blobs / checkpoint_writes / checkpoint_migrations），
-- 不带 agent_ 前缀。这里落的是库内 22 条迁移（v=0..21）跑完的终态 schema，
-- 常态运行 CHECKPOINTER_ALLOW_RUNTIME_DDL=false，不做运行时 DDL。
--
-- collation 显式钉 utf8mb4_unicode_ci：saver 的 SELECT 用 json_table(... CHARACTER SET
-- utf8mb4) 且不带 COLLATE，取的是服务端 charset 默认 collation；MariaDB 11 默认
-- utf8mb4_uca1400_ai_ci，与建表 collation 不一致会报 1267 Illegal mix of collations。
-- 建表钉死后不再依赖会话级补丁（app/core/checkpoint.py 的 init_command 是二重保险）。

create table if not exists checkpoint_migrations
(
    v integer not null,
    primary key (v)
) engine = InnoDB default charset = utf8mb4 collate = utf8mb4_unicode_ci;

create table if not exists checkpoints
(
    thread_id            varchar(150)  not null,
    checkpoint_ns        varchar(2000) not null default '',
    checkpoint_ns_hash   binary(16),
    checkpoint_id        varchar(150)  not null,
    parent_checkpoint_id varchar(150),
    type                 varchar(150),
    checkpoint           json          not null,
    metadata             json          not null default ('{}'),
    primary key (thread_id, checkpoint_ns_hash, checkpoint_id),
    key checkpoints_thread_id_idx (thread_id),
    key checkpoints_checkpoint_id_idx (checkpoint_id)
) engine = InnoDB default charset = utf8mb4 collate = utf8mb4_unicode_ci;

create table if not exists checkpoint_blobs
(
    thread_id          varchar(150)  not null,
    checkpoint_ns      varchar(2000) not null default '',
    checkpoint_ns_hash binary(16),
    channel            varchar(150)  not null,
    version            varchar(150)  not null,
    type               varchar(150)  not null,
    `blob`             longblob,
    primary key (thread_id, checkpoint_ns_hash, channel, version),
    key checkpoint_blobs_thread_id_idx (thread_id)
) engine = InnoDB default charset = utf8mb4 collate = utf8mb4_unicode_ci;

create table if not exists checkpoint_writes
(
    thread_id          varchar(150)  not null,
    checkpoint_ns      varchar(2000) not null default '',
    checkpoint_ns_hash binary(16),
    checkpoint_id      varchar(150)  not null,
    task_id            varchar(150)  not null,
    task_path          varchar(2000) not null default '',
    idx                integer       not null,
    channel            varchar(150)  not null,
    type               varchar(150),
    `blob`             longblob      not null,
    primary key (thread_id, checkpoint_ns_hash, checkpoint_id, task_id, idx),
    key checkpoint_writes_thread_id_idx (thread_id)
) engine = InnoDB default charset = utf8mb4 collate = utf8mb4_unicode_ci;

-- 声明上述 22 条库内迁移（v=0..21）已应用：万一有人开了 CHECKPOINTER_ALLOW_RUNTIME_DDL，
-- saver.setup() 从 max(v)+1 续跑，对已建表零动作；库升级新增迁移时只跑增量。
insert ignore into checkpoint_migrations (v)
values (0), (1), (2), (3), (4), (5), (6), (7), (8), (9), (10),
       (11), (12), (13), (14), (15), (16), (17), (18), (19), (20), (21);
