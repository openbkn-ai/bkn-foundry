-- Copyright 2026 openbkn.ai
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.

-- Persist the immutable Resource visibility class. Existing rows retain the
-- false default except for the fixed system Datasets listed below.
USE openbkn;

ALTER TABLE t_resource
    ADD COLUMN IF NOT EXISTS f_internal BOOLEAN NOT NULL DEFAULT FALSE COMMENT '是否为内置资源' AFTER f_category;

UPDATE t_resource
SET f_internal = TRUE
WHERE f_id IN (
    'adp_bkn_concept_dataset',
    'bkn_execution_factory_capability_dataset'
);
