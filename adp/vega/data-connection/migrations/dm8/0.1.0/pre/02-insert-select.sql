-- 迁移数据从vega schema 到 adp schema
-- 注意：使用WHERE NOT EXISTS避免重复插入数据
SET SCHEMA adp;

-- 迁移 vega.t_data_source 表数据到 adp.data_source
INSERT INTO adp."data_source" (
    "id", "name", "type_name", "bin_data", "comment",
    "created_by_uid", "created_at", "updated_by_uid", "updated_at"
)
SELECT
    "id", "name", "type_name", "bin_data", "comment",
    "created_by_uid", "created_at", "updated_by_uid", "updated_at"
FROM vega."data_source"
WHERE NOT EXISTS (
        SELECT 1 FROM adp."data_source" t
        WHERE t."id" = vega."data_source"."id"
    );

-- 迁移 vega.t_data_source_info 表数据到 adp.t_data_source_info
INSERT INTO adp."t_data_source_info" (
    "f_id", "f_name", "f_type", "f_catalog", "f_database",
    "f_schema", "f_connect_protocol", "f_host", "f_port",
    "f_account", "f_password", "f_storage_protocol", "f_storage_base",
    "f_token", "f_replica_set", "f_is_built_in", "f_comment",
    "f_created_by_uid", "f_created_at", "f_updated_by_uid", "f_updated_at"
)
SELECT
    "f_id", "f_name", "f_type", "f_catalog", "f_database",
    "f_schema", "f_connect_protocol", "f_host", "f_port",
    "f_account", "f_password", "f_storage_protocol", "f_storage_base",
    "f_token", "f_replica_set", "f_is_built_in", "f_comment",
    "f_created_by_uid", "f_created_at", "f_updated_by_uid", "f_updated_at"
FROM vega."t_data_source_info"
WHERE NOT EXISTS (
        SELECT 1 FROM adp."t_data_source_info" t
        WHERE t."f_id" = vega."t_data_source_info"."f_id"
    );

-- 迁移 vega.t_task_scan 表数据到 adp.t_task_scan
INSERT INTO adp."t_task_scan" (
    "id", "type", "name", "ds_id", "scan_status",
    "start_time", "end_time", "create_user",
    "task_params_info", "task_process_info", "task_result_info"
)
SELECT
    "id", "type", "name", "ds_id", "scan_status",
    "start_time", "end_time", "create_user",
    "task_params_info", "task_process_info", "task_result_info"
FROM vega."t_task_scan"
WHERE NOT EXISTS (
        SELECT 1 FROM adp."t_task_scan" t
        WHERE t."id" = vega."t_task_scan"."id"
    );

-- 迁移 vega.t_task_scan_table 表数据到 adp.t_task_scan_table
INSERT INTO adp."t_task_scan_table" (
    "id", "task_id", "ds_id", "ds_name", "table_id",
    "table_name", "schema_name", "scan_status",
    "start_time", "end_time", "create_user",
    "scan_params", "scan_result_info", "error_stack"
)
SELECT
    "id", "task_id", "ds_id", "ds_name", "table_id",
    "table_name", "schema_name", "scan_status",
    "start_time", "end_time", "create_user",
    "scan_params", "scan_result_info", "error_stack"
FROM vega."t_task_scan_table"
WHERE NOT EXISTS (
        SELECT 1 FROM adp."t_task_scan_table" t
        WHERE t."id" = vega."t_task_scan_table"."id"
    );

-- 迁移 vega.t_table_scan 表数据到 adp.t_table_scan
INSERT INTO adp."t_table_scan" (
    "f_id", "f_name", "f_advanced_params", "f_description",
    "f_table_rows", "f_data_source_id", "f_data_source_name",
    "f_schema_name", "f_task_id", "f_version", "f_create_time",
    "f_create_user", "f_operation_time", "f_operation_user",
    "f_operation_type", "f_status", "f_status_change", "f_scan_source"
)
SELECT
    "f_id", "f_name", "f_advanced_params", "f_description",
    "f_table_rows", "f_data_source_id", "f_data_source_name",
    "f_schema_name", "f_task_id", "f_version", "f_create_time",
    "f_create_user", "f_operation_time", "f_operation_user",
    "f_operation_type", "f_status", "f_status_change", "f_scan_source"
FROM vega."t_table_scan"
WHERE NOT EXISTS (
        SELECT 1 FROM adp."t_table_scan" t
        WHERE t."f_id" = vega."t_table_scan"."f_id"
    );

-- 迁移 vega.t_table_field_scan 表数据到 adp.t_table_field_scan
INSERT INTO adp."t_table_field_scan" (
    "f_id", "f_field_name", "f_table_id", "f_table_name",
    "f_field_type", "f_field_length", "f_field_precision",
    "f_field_comment", "f_field_order_no", "f_advanced_params",
    "f_version", "f_create_time", "f_create_user",
    "f_operation_time", "f_operation_user", "f_operation_type",
    "f_status_change"
)
SELECT
    "f_id", "f_field_name", "f_table_id", "f_table_name",
    "f_field_type", "f_field_length", "f_field_precision",
    "f_field_comment", "f_field_order_no", "f_advanced_params",
    "f_version", "f_create_time", "f_create_user",
    "f_operation_time", "f_operation_user", "f_operation_type",
    "f_status_change"
FROM vega."t_table_field_scan"
WHERE NOT EXISTS (
        SELECT 1 FROM adp."t_table_field_scan" t
        WHERE t."f_id" = vega."t_table_field_scan"."f_id"
    );

-- 删除vega schema的表
DROP TABLE IF EXISTS vega."data_source" CASCADE;
DROP TABLE IF EXISTS vega."t_data_source_info" CASCADE;
DROP TABLE IF EXISTS vega."t_task_scan" CASCADE;
DROP TABLE IF EXISTS vega."t_task_scan_table" CASCADE;
DROP TABLE IF EXISTS vega."t_table_scan" CASCADE;
DROP TABLE IF EXISTS vega."t_table_field_scan" CASCADE;
