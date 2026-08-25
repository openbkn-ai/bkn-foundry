-- Copyright 2026 openbkn.ai
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.

USE openbkn;

-- Running/stopping tasks are forcibly stopped. Failed tasks retain their
-- terminal status and diagnostics, but all three restart from zero.
UPDATE t_build_task
SET f_finish_time = UNIX_TIMESTAMP() * 1000,
    f_status = 'stopped'
WHERE f_execute_type IN ('full', 'incremental')
  AND f_status IN ('running', 'stopping');

-- Pending, stopped, and failed tasks retain their current status and finish
-- time, but their next restart begins from zero.
UPDATE t_build_task
SET f_total_count = 0,
    f_synced_count = 0,
    f_synced_mark = ''
WHERE f_execute_type IN ('full', 'incremental')
  AND f_status in ('pending', 'stopped', 'failed');

-- Existing V0 batch marks are trusted and wrapped without decoding, preserving
-- the original cursor JSON, including large numeric tokens.
UPDATE t_build_task
SET f_synced_mark = CONCAT('{"version":1,"mode":"batch","cursor":', TRIM(f_synced_mark), '}')
WHERE f_execute_type IN ('full', 'incremental')
  AND f_status = 'completed'
  AND f_synced_mark <> ''
  AND LEFT(LTRIM(f_synced_mark), 1) = '[';

-- Existing non-empty index names are trusted as available for this one-time
-- direct upgrade. Resource checkpoints are deliberately reset so the next
-- batch task starts from the beginning.
UPDATE t_resource
SET f_local_status = 'available',
    f_sync_mark = ''
WHERE f_local_index_name <> '';

UPDATE t_resource
SET f_local_status = 'unavailable',
    f_sync_mark = ''
WHERE f_local_index_name = '';
