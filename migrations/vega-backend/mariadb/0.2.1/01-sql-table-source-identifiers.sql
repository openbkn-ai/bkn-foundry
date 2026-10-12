-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

USE openbkn;

-- 历史源标识准确且尚未引用，点号为层级分隔符。
-- 仅转换 SQL 表源标识；首字符已为对应引用符时跳过，支持重复执行。
-- mysql
UPDATE t_resource r
JOIN t_catalog c ON c.f_id = r.f_catalog_id
SET r.f_source_identifier = CONCAT('`', REPLACE(r.f_source_identifier, '.', '`.`'), '`')
WHERE r.f_category = 'table'
AND c.f_connector_type = 'mysql'
AND LEFT(r.f_source_identifier, 1) <> '`';

-- mariadb
UPDATE t_resource r
JOIN t_catalog c ON c.f_id = r.f_catalog_id
SET r.f_source_identifier = CONCAT('`', REPLACE(r.f_source_identifier, '.', '`.`'), '`')
WHERE r.f_category = 'table'
AND c.f_connector_type = 'mariadb'
AND LEFT(r.f_source_identifier, 1) <> '`';

-- postgresql
UPDATE t_resource r
JOIN t_catalog c ON c.f_id = r.f_catalog_id
SET r.f_source_identifier = CONCAT('"', REPLACE(r.f_source_identifier, '.', '"."'), '"')
WHERE r.f_category = 'table'
AND c.f_connector_type = 'postgresql'
AND LEFT(r.f_source_identifier, 1) <> '"';

-- sqlserver
UPDATE t_resource r
JOIN t_catalog c ON c.f_id = r.f_catalog_id
SET r.f_source_identifier = CONCAT('[', REPLACE(r.f_source_identifier, '.', '].['), ']')
WHERE r.f_category = 'table'
AND c.f_connector_type = 'sqlserver'
AND LEFT(r.f_source_identifier, 1) <> '[';

-- oracle
UPDATE t_resource r
JOIN t_catalog c ON c.f_id = r.f_catalog_id
SET r.f_source_identifier = CONCAT('"', REPLACE(r.f_source_identifier, '.', '"."'), '"')
WHERE r.f_category = 'table'
AND c.f_connector_type = 'oracle'
AND LEFT(r.f_source_identifier, 1) <> '"';

-- hana
UPDATE t_resource r
JOIN t_catalog c ON c.f_id = r.f_catalog_id
SET r.f_source_identifier = CONCAT('"', REPLACE(r.f_source_identifier, '.', '"."'), '"')
WHERE r.f_category = 'table'
AND c.f_connector_type = 'hana'
AND LEFT(r.f_source_identifier, 1) <> '"';
