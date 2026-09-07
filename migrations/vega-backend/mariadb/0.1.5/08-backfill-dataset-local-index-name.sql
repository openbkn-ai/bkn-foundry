-- Copyright 2026 openbkn.ai
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.

USE openbkn;

-- Legacy Dataset indexes were named after the Resource ID and were created
-- before f_local_index_name became the source of truth. Preserve the two
-- existing Dataset indexes during the upgrade; newly created Dataset indexes
-- always receive a vega-dataset-<uuidv7> name.
UPDATE t_resource
SET f_local_index_name = f_id,
    f_local_status = 'available'
WHERE f_category = 'dataset'
  AND f_local_index_name = '';
