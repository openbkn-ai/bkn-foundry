-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

ALTER TABLE t_build_task
    ADD COLUMN IF NOT EXISTS f_keys MEDIUMTEXT NULL COMMENT '按主键同步的任务输入(JSON)' AFTER f_index_config;
