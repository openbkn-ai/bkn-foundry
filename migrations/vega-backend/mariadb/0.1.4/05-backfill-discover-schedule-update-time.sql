-- Copyright 2026 openbkn.ai
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.

USE openbkn;

-- 旧版本创建调度时未写入更新审计字段，f_update_time 会保留默认值 0，
-- 无法作为乐观锁版本使用。
UPDATE t_discover_schedule
SET f_update_time = f_create_time
WHERE f_update_time = 0;
