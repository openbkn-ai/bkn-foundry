-- Copyright 2026 openbkn.ai
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.

-- Persist the immutable Resource visibility class. Existing rows intentionally
-- retain the false default; historical internal Resources are not backfilled.
USE openbkn;

ALTER TABLE t_resource
    ADD COLUMN IF NOT EXISTS f_internal BOOLEAN NOT NULL DEFAULT FALSE COMMENT '是否为内置资源' AFTER f_category;
