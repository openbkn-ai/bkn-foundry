-- Copyright The kweaver.ai Authors.
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.

USE kweaver;

-- Split catalog enabled state from health check status.
UPDATE t_catalog
SET f_enabled = FALSE,
    f_health_check_status = 'unchecked'
WHERE f_health_check_status = 'disabled';

ALTER TABLE t_catalog
    MODIFY f_health_check_status VARCHAR(20) NOT NULL DEFAULT 'unchecked'
    COMMENT '连接状态: unchecked, healthy, degraded, unhealthy, offline';
