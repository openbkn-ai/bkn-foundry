-- Copyright 2026 openbkn.ai
-- Copyright The kweaver.ai Authors.
--
-- Licensed under the Apache License, Version 2.0.

-- Issue #500: register the built-in Microsoft SQL Server table connector.
USE openbkn;

INSERT INTO t_connector_type (f_type, f_name, f_description, f_mode, f_category, f_field_config, f_enabled)
SELECT 'sqlserver', 'sqlserver', 'Microsoft SQL Server 关系型数据库连接器', 'local', 'table',
       '{
           "host": {"name":"主机地址","type":"string","description":"SQL Server 服务器主机地址","required":true,"encrypted":false},
           "port": {"name":"端口号","type":"integer","description":"SQL Server TCP 端口","required":true,"encrypted":false},
           "username": {"name":"用户名","type":"string","description":"SQL Server 登录用户名","required":true,"encrypted":false},
           "password": {"name":"密码","type":"string","description":"SQL Server 登录密码","required":true,"encrypted":true},
           "database": {"name":"数据库名","type":"string","description":"SQL Server 连接目标数据库","required":true,"encrypted":false},
           "schemas": {"name":"Schema 列表","type":"array","description":"可选；为空时扫描所有可访问的非系统 schema","required":false,"encrypted":false},
           "options": {"name":"连接参数","type":"object","description":"连接参数，如 encrypt、trustservercertificate、connection timeout","required":false,"encrypted":false}
       }',
       TRUE
FROM DUAL
WHERE NOT EXISTS (SELECT f_type FROM t_connector_type WHERE f_type = 'sqlserver');
