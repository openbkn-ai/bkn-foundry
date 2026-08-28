-- Copyright 2026 openbkn.ai
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.

-- Resource enablement and single-resource discovery task support.
USE openbkn;

ALTER TABLE t_resource
    ADD COLUMN IF NOT EXISTS f_enabled TINYINT(1) NOT NULL DEFAULT 1 COMMENT '资源是否启用' AFTER f_category,
    ADD INDEX IF NOT EXISTS idx_enabled (f_enabled);

UPDATE t_resource
SET f_enabled = 0,
    f_status = 'active'
WHERE f_status = 'disabled';

ALTER TABLE t_discover_task
    ADD COLUMN IF NOT EXISTS f_resource_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT '单资源刷新目标；空表示 Catalog 扫描' AFTER f_catalog_id,
    ADD COLUMN IF NOT EXISTS f_queue_priority TINYINT NOT NULL DEFAULT 20 COMMENT '调度优先级，数值越大越优先' AFTER f_trigger_type,
    ADD INDEX IF NOT EXISTS idx_pending_priority (f_status, f_queue_priority, f_create_time, f_id),
    ADD INDEX IF NOT EXISTS idx_resource_active (f_resource_id, f_status);
