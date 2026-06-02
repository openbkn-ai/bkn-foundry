-- Copyright 2026 openbkn.ai
-- Copyright The kweaver.ai Authors.
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.

USE kweaver;

ALTER TABLE t_resource
    ADD COLUMN f_last_discover_status VARCHAR(32) NOT NULL DEFAULT '' COMMENT '最近一次扫描观察状态'
    AFTER f_status_message;
