-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

-- Register the Enterprise Oracle table connector for existing installations.
USE openbkn;

INSERT INTO t_connector_type (f_type, f_name, f_description, f_mode, f_category, f_enabled)
SELECT 'oracle', 'oracle', 'Oracle 关系型数据库连接器', 'local', 'table', TRUE
FROM DUAL WHERE NOT EXISTS (SELECT f_type FROM t_connector_type WHERE f_type = 'oracle');
