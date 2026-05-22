-- Copyright The kweaver.ai Authors.
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.

SET SCHEMA kweaver;

ALTER TABLE t_discover_task ADD COLUMN IF NOT EXISTS f_strategy VARCHAR(32 CHAR) NOT NULL DEFAULT 'full_sync';
ALTER TABLE t_discover_schedule ADD COLUMN IF NOT EXISTS f_strategy VARCHAR(32 CHAR) NOT NULL DEFAULT 'full_sync';

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
