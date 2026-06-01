SET SCHEMA adp;

-- 迁移 cache_table 表数据
INSERT INTO adp.cache_table (
    "id", "catalog_name", "schema_name", "table_name", "cts_sql",
    "source_create_sql", "current_view_original_text", "status", "mid_status", "deps",
    "create_time", "update_time"
)
SELECT
    "id", "catalog_name", "schema_name", "table_name", "cts_sql",
    "source_create_sql", "current_view_original_text", "status", "mid_status", "deps",
    "create_time", "update_time"
FROM vega.cache_table
WHERE NOT EXISTS (
    SELECT 1 FROM adp.cache_table t
    WHERE t.id = vega.cache_table.id
);

-- 迁移 client_id 表数据
INSERT INTO adp.client_id (
    "id", "client_name", "client_id", "client_secret", "create_time",
    "update_time"
)
SELECT
    "id", "client_name", "client_id", "client_secret", "create_time",
    "update_time"
FROM vega.client_id
WHERE NOT EXISTS (
    SELECT 1 FROM adp.client_id t
    WHERE t.id = vega.client_id.id
);

-- 迁移 excel_column_type 表数据
INSERT INTO adp.excel_column_type (
    "id", "catalog", "vdm_catalog", "schema_name", "table_name",
    "column_name", "column_comment", "type", "order_no", "create_time",
    "update_time"
)
SELECT
    "id", "catalog", "vdm_catalog", "schema_name", "table_name",
    "column_name", "column_comment", "type", "order_no", "create_time",
    "update_time"
FROM vega.excel_column_type
WHERE NOT EXISTS (
    SELECT 1 FROM adp.excel_column_type t
    WHERE t.id = vega.excel_column_type.id
);

-- 迁移 excel_table_config 表数据
INSERT INTO adp.excel_table_config (
    "id", "catalog", "vdm_catalog", "schema_name", "file_name",
    "table_name", "table_comment", "sheet", "all_sheet", "sheet_as_new_column",
    "start_cell", "end_cell", "has_headers", "create_time", "update_time"
)
SELECT
    "id", "catalog", "vdm_catalog", "schema_name", "file_name",
    "table_name", "table_comment", "sheet", "all_sheet", "sheet_as_new_column",
    "start_cell", "end_cell", "has_headers", "create_time", "update_time"
FROM vega.excel_table_config
WHERE NOT EXISTS (
    SELECT 1 FROM adp.excel_table_config t
    WHERE t.id = vega.excel_table_config.id
);

-- 迁移 query_info 表数据
INSERT INTO adp.query_info (
    "query_id", "result", "msg", "task_id", "state",
    "create_time", "update_time"
)
SELECT
    "query_id", "result", "msg", "task_id", "state",
    "create_time", "update_time"
FROM vega.query_info
WHERE NOT EXISTS (
    SELECT 1 FROM adp.query_info t
    WHERE t.query_id = vega.query_info.query_id
);

-- 迁移 task_info 表数据
INSERT INTO adp.task_info (
    "task_id", "state", "query", "create_time", "update_time",
    "topic", "sub_task_id", "type", "elapsed_time", "update_count",
    "schedule_time", "queued_time", "cpu_time"
)
SELECT
    "task_id", "state", "query", "create_time", "update_time",
    "topic", "sub_task_id", "type", "elapsed_time", "update_count",
    "schedule_time", "queued_time", "cpu_time"
FROM vega.task_info
WHERE NOT EXISTS (
    SELECT 1 FROM adp.task_info t
    WHERE t.task_id = vega.task_info.task_id
);


DROP TABLE IF EXISTS vega.cache_table CASCADE;
DROP TABLE IF EXISTS vega.client_id CASCADE;
DROP TABLE IF EXISTS vega.excel_column_type CASCADE;
DROP TABLE IF EXISTS vega.excel_table_config CASCADE;
DROP TABLE IF EXISTS vega.query_info CASCADE;
DROP TABLE IF EXISTS vega.task_info CASCADE;