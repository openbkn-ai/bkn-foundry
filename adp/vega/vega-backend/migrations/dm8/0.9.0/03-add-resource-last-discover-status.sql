-- Copyright 2026 openbkn.ai
-- Copyright The kweaver.ai Authors.
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.

SET SCHEMA kweaver;

ALTER TABLE t_resource ADD f_last_discover_status VARCHAR(32 CHAR) DEFAULT '' NOT NULL;
