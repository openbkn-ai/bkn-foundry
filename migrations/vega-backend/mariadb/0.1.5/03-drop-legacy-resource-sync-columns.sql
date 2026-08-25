-- Copyright 2026 openbkn.ai
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.

USE openbkn;

ALTER TABLE t_resource
    MODIFY COLUMN f_index_config MEDIUMTEXT NOT NULL COMMENT '本地索引配置（JSON格式）',
    MODIFY COLUMN f_local_index_name VARCHAR(255) NOT NULL DEFAULT '' COMMENT 'Local OpenSearch index name',
    MODIFY COLUMN f_sync_mark TEXT NOT NULL COMMENT 'Committed batch SyncCheckpoint owned by the Resource',
    DROP COLUMN IF EXISTS f_local_enabled,
    DROP COLUMN IF EXISTS f_local_storage_engine,
    DROP COLUMN IF EXISTS f_local_storage_config,
    DROP COLUMN IF EXISTS f_sync_strategy,
    DROP COLUMN IF EXISTS f_sync_config,
    DROP COLUMN IF EXISTS f_sync_status,
    DROP COLUMN IF EXISTS f_last_sync_time,
    DROP COLUMN IF EXISTS f_sync_error_message;

ALTER TABLE t_build_task
    MODIFY COLUMN f_index_config TEXT NOT NULL COMMENT '索引配置快照(JSON)',
    MODIFY COLUMN f_synced_mark TEXT NOT NULL COMMENT 'Task execution checkpoint (batch SyncCheckpoint; streaming opaque)';
