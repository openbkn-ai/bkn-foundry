-- Copyright 2026 kowell.ai
-- Copyright The kweaver.ai Authors.
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.

SET SCHEMA kweaver;

-- Split catalog enabled state from health check status.
UPDATE t_catalog
SET f_enabled = 0,
    f_health_check_status = 'unchecked'
WHERE f_health_check_status = 'disabled';

ALTER TABLE t_catalog
    MODIFY f_health_check_status VARCHAR(20 CHAR) NOT NULL DEFAULT 'unchecked';
