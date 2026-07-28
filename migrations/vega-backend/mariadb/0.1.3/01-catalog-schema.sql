-- Copyright 2026 openbkn.ai
-- Copyright The kweaver.ai Authors.
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.

-- Issue #240：Catalog 将发现得到的 schema 快照保存到 f_metadata.schemas；Resource 以 schema 作为筛选维度。
USE openbkn;

ALTER TABLE t_resource
    CHANGE COLUMN f_database f_schema VARCHAR(128) NOT NULL DEFAULT '' COMMENT '所属 schema 名称，由发现流程写入';

ALTER TABLE t_resource
    ADD INDEX idx_catalog_schema (f_catalog_id, f_schema);
