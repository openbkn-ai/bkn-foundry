-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

-- 为已有安装登记 Enterprise Doris 表连接器，重复执行不修改已有设置。
USE openbkn;

INSERT INTO t_connector_type (f_type, f_name, f_description, f_mode, f_category, f_enabled)
SELECT 'doris', 'doris', 'Apache Doris 分析型数据库连接器', 'local', 'table', TRUE
FROM DUAL WHERE NOT EXISTS (SELECT f_type FROM t_connector_type WHERE f_type = 'doris');
