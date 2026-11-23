-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

USE openbkn;

-- Register HTTP only when the type is missing.
-- Preserve existing registrations, including their metadata and enabled state.
INSERT INTO t_connector_type (f_type, f_name, f_description, f_mode, f_category, f_enabled)
SELECT 'http', 'HTTP API', 'HTTP JSON API 连接器', 'local', 'api', TRUE
FROM DUAL WHERE NOT EXISTS (SELECT f_type FROM t_connector_type WHERE f_type = 'http');

-- API storage reuses the existing type/definition columns without changing
-- their types, defaults or stored values. Keep comments aligned with init.sql.
ALTER TABLE t_resource
    MODIFY COLUMN f_logic_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT '按资源类别保存类型：LogicalView为derived/composite，API为read';

ALTER TABLE t_resource
    MODIFY COLUMN f_logic_definition MEDIUMTEXT NOT NULL COMMENT '按资源类别保存LogicalView或API定义（JSON格式）';
