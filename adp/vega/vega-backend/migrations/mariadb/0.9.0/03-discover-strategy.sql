-- Copyright 2026 openbkn.ai
-- Copyright The kweaver.ai Authors.
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.

USE kweaver;

ALTER TABLE t_discover_task ADD COLUMN IF NOT EXISTS f_strategy VARCHAR(32) NOT NULL DEFAULT 'full_sync' COMMENT '发现策略: full_sync, create_only, cleanup_only' AFTER f_schedule_id;
ALTER TABLE t_discover_schedule ADD COLUMN IF NOT EXISTS f_strategy VARCHAR(32) NOT NULL DEFAULT 'full_sync' COMMENT '发现策略: full_sync, create_only, cleanup_only' AFTER f_enabled;

UPDATE t_discover_task
SET f_strategy = CASE
    WHEN f_strategies = '["insert"]' THEN 'create_only'
    WHEN f_strategies = '["delete"]' THEN 'cleanup_only'
    ELSE 'full_sync'
END
WHERE f_strategy = '' OR f_strategy IS NULL OR f_strategy = 'full_sync';

UPDATE t_discover_schedule
SET f_strategy = CASE
    WHEN f_strategies = '["insert"]' THEN 'create_only'
    WHEN f_strategies = '["delete"]' THEN 'cleanup_only'
    ELSE 'full_sync'
END
WHERE f_strategy = '' OR f_strategy IS NULL OR f_strategy = 'full_sync';
