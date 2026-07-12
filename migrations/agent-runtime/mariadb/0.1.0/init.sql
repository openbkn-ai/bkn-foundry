-- agent-runtime 建表（Epic #202）。共享 openbkn 库，agent_ 前缀，纯新增零 ALTER。
-- 由全局 core-data-migrator pre-upgrade hook 执行，幂等。

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

-- LangGraph checkpointer 表（langgraph-checkpoint-mysql ~2.0）：
-- 表名由库固定（checkpoints / checkpoint_blobs / checkpoint_writes），暂不带 agent_ 前缀。
-- DDL 与库版本强绑定，由部署流程在首次安装时执行一次 saver.setup()
--（CHECKPOINTER_ALLOW_RUNTIME_DDL=true 的一次性 job），常态运行关闭 DDL。M6（#211）固化。
