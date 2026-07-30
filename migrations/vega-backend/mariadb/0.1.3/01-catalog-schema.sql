-- Copyright 2026 openbkn.ai
-- Copyright The kweaver.ai Authors.
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.

-- Issue #240：Catalog 将发现得到的 schema 快照保存到 f_metadata.schemas；Resource 以 schema 作为筛选维度。
USE openbkn;

ALTER TABLE t_resource
    CHANGE COLUMN f_database f_schema VARCHAR(128) NOT NULL DEFAULT '' COMMENT '所属 schema 名称，由发现流程写入';

-- 旧 Resource 的 source_identifier 使用 schema.table。rename 后 f_schema 暂存旧 database 值，
-- 必须由 source_identifier 回填；无法可靠识别的记录置空，等待下一次 discover 写入权威值。
UPDATE t_resource r
JOIN t_catalog c ON c.f_id = r.f_catalog_id
SET r.f_schema = CASE
    WHEN r.f_source_identifier LIKE '%.%'
        THEN SUBSTRING_INDEX(r.f_source_identifier, '.', 1)
    ELSE ''
END
WHERE c.f_connector_type IN ('postgresql', 'mariadb', 'mysql')
  AND r.f_category = 'table';

ALTER TABLE t_resource
    ADD INDEX idx_catalog_schema (f_catalog_id, f_schema);
