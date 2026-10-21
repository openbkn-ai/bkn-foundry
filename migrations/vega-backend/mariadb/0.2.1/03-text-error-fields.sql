-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

-- 扩展资源状态说明和探查任务消息容量，保留已有内容。
USE openbkn;

ALTER TABLE t_resource
    MODIFY COLUMN f_status_message TEXT NOT NULL COMMENT '状态说明';

ALTER TABLE t_discover_task
    MODIFY COLUMN f_message TEXT NOT NULL COMMENT '任务消息/错误信息';
