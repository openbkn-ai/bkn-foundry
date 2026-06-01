-- 迁移数据从6.1.0 dip_mdl schema 到 6.2.0 adp schema
-- 注意：使用WHERE NOT EXISTS避免重复插入数据
SET SCHEMA adp;

-- 迁移 t_metric_model 表数据
INSERT INTO adp.t_metric_model (
  f_model_id, f_model_name, f_tags, f_comment, f_catalog_id,
  f_catalog_content, f_creator, f_creator_type, f_create_time,
  f_update_time, f_measure_name, f_metric_type, f_data_source,
  f_query_type, f_formula, f_formula_config, f_analysis_dimessions,
  f_order_by_fields, f_having_condition, f_date_field,
  f_measure_field, f_unit_type, f_unit, f_group_id,
  f_builtin, f_calendar_interval
)
SELECT
  f_model_id, f_model_name, f_tags, f_comment, f_catalog_id,
  f_catalog_content, f_creator, f_creator_type, f_create_time,
  f_update_time, f_measure_name, f_metric_type, f_data_source,
  f_query_type, f_formula, f_formula_config, f_analysis_dimessions,
  f_order_by_fields, f_having_condition, f_date_field,
  f_measure_field, f_unit_type, f_unit, f_group_id,
  f_builtin, f_calendar_interval
FROM dip_mdl.t_metric_model
WHERE NOT EXISTS (
    SELECT 1 FROM adp.t_metric_model t
    WHERE t.f_model_id = dip_mdl.t_metric_model.f_model_id
);


-- 迁移 t_metric_model_group 表数据
INSERT INTO adp.t_metric_model_group (
  f_group_id, f_group_name, f_comment,
  f_create_time, f_update_time, f_builtin
)
SELECT
  f_group_id, f_group_name, f_comment,
  f_create_time, f_update_time, f_builtin
FROM dip_mdl.t_metric_model_group
WHERE NOT EXISTS (
    SELECT 1 FROM adp.t_metric_model_group t
    WHERE t.f_group_id = dip_mdl.t_metric_model_group.f_group_id
);


-- 迁移 t_metric_model_task 表数据
INSERT INTO adp.t_metric_model_task (
  f_task_id, f_task_name, f_comment, f_create_time, f_update_time,
  f_module_type, f_model_id, f_schedule, f_variables, f_time_windows,
  f_steps, f_plan_time, f_index_base, f_retrace_duration,
  f_schedule_sync_status, f_execute_status, f_creator, f_creator_type
)
SELECT
  f_task_id, f_task_name, f_comment, f_create_time, f_update_time,
  f_module_type, f_model_id, f_schedule, f_variables, f_time_windows,
  f_steps, f_plan_time, f_index_base, f_retrace_duration,
  f_schedule_sync_status, f_execute_status, f_creator, f_creator_type
FROM dip_mdl.t_metric_model_task
WHERE NOT EXISTS (
    SELECT 1 FROM adp.t_metric_model_task t
    WHERE t.f_task_id = dip_mdl.t_metric_model_task.f_task_id
);


-- 迁移 t_static_metric_index 表数据
INSERT INTO adp.t_static_metric_index (
  f_id, f_base_type, f_split_time
)
SELECT
  f_id, f_base_type, f_split_time
FROM dip_mdl.t_static_metric_index
WHERE NOT EXISTS (
    SELECT 1 FROM adp.t_static_metric_index t
    WHERE t.f_id = dip_mdl.t_static_metric_index.f_id
);

-- 迁移 t_event_model_aggregate_rules 表数据
INSERT INTO adp.t_event_model_aggregate_rules
SELECT *
FROM dip_mdl.t_event_model_aggregate_rules
WHERE NOT EXISTS (
    SELECT 1 FROM adp.t_event_model_aggregate_rules t
    WHERE t.f_aggregate_rule_id = dip_mdl.t_event_model_aggregate_rules.f_aggregate_rule_id
);


-- 迁移 t_event_models 表数据
INSERT INTO adp.t_event_models
SELECT *
FROM dip_mdl.t_event_models
WHERE NOT EXISTS (
    SELECT 1 FROM adp.t_event_models t
    WHERE t.f_event_model_id = dip_mdl.t_event_models.f_event_model_id
);


-- 迁移 t_event_model_detect_rules 表数据
INSERT INTO adp.t_event_model_detect_rules
SELECT *
FROM dip_mdl.t_event_model_detect_rules
WHERE NOT EXISTS (
    SELECT 1 FROM adp.t_event_model_detect_rules t
    WHERE t.f_detect_rule_id = dip_mdl.t_event_model_detect_rules.f_detect_rule_id
);


-- 迁移 t_event_model_task 表数据
INSERT INTO adp.t_event_model_task
SELECT *
FROM dip_mdl.t_event_model_task
WHERE NOT EXISTS (
    SELECT 1 FROM adp.t_event_model_task t
    WHERE t.f_task_id = dip_mdl.t_event_model_task.f_task_id
);


-- 迁移 t_event_model_task_execution_records 表数据
INSERT INTO adp.t_event_model_task_execution_records
SELECT *
FROM dip_mdl.t_event_model_task_execution_records
WHERE NOT EXISTS (
    SELECT 1 FROM adp.t_event_model_task_execution_records t
    WHERE t.f_run_id = dip_mdl.t_event_model_task_execution_records.f_run_id
);

-- 迁移 t_data_view 表数据
INSERT INTO adp.t_data_view (
  f_view_id, f_view_name, f_technical_name, f_group_id, f_type,
  f_query_type, f_builtin, f_tags, f_comment, f_data_source_type,
  f_data_source_id, f_file_name, f_excel_config, f_data_scope,
  f_fields, f_status, f_metadata_form_id, f_primary_keys, f_sql,
  f_meta_table_name, f_create_time, f_update_time, f_creator, 
  f_creator_type, f_updater, f_updater_type, f_data_source,
  f_field_scope, f_filters, f_open_streaming, f_job_id,
  f_loggroup_filters
)
SELECT
  f_view_id, f_view_name, f_technical_name, f_group_id, f_type,
  f_query_type, f_builtin, f_tags, f_comment, f_data_source_type,
  f_data_source_id, f_file_name, f_excel_config, f_data_scope,
  f_fields, f_status, f_metadata_form_id, f_primary_keys, f_sql,
  f_meta_table_name, f_create_time, f_update_time, f_creator, 
  f_creator_type, f_updater, f_updater_type, f_data_source,
  f_field_scope, f_filters, f_open_streaming, f_job_id,
  f_loggroup_filters
FROM dip_mdl.t_data_view
WHERE NOT EXISTS (
    SELECT 1 FROM adp.t_data_view t
    WHERE t.f_view_id = dip_mdl.t_data_view.f_view_id
);


-- 迁移 t_data_view_group 表数据
INSERT INTO adp.t_data_view_group (
  f_group_id, f_group_name, f_create_time, f_update_time, f_builtin
)
SELECT
  f_group_id, f_group_name, f_create_time, f_update_time, f_builtin
FROM dip_mdl.t_data_view_group
WHERE NOT EXISTS (
    SELECT 1 FROM adp.t_data_view_group t
    WHERE t.f_group_id = dip_mdl.t_data_view_group.f_group_id
);


-- 迁移 t_data_view_row_column_rule 表数据
INSERT INTO adp.t_data_view_row_column_rule (
  f_rule_id, f_rule_name, f_view_id, f_tags, f_comment, f_fields,
  f_row_filters, f_create_time, f_update_time, f_creator,
  f_creator_type, f_updater, f_updater_type
)
SELECT
  f_rule_id, f_rule_name, f_view_id, f_tags, f_comment, f_fields,
  f_row_filters, f_create_time, f_update_time, f_creator,
  f_creator_type, f_updater, f_updater_type
FROM dip_mdl.t_data_view_row_column_rule
WHERE NOT EXISTS (
    SELECT 1 FROM adp.t_data_view_row_column_rule t
    WHERE t.f_rule_id = dip_mdl.t_data_view_row_column_rule.f_rule_id
);


-- 迁移 t_data_dict 表数据
INSERT INTO adp.t_data_dict
SELECT *
FROM dip_mdl.t_data_dict
WHERE NOT EXISTS (
    SELECT 1 FROM adp.t_data_dict t
    WHERE t.f_dict_id = dip_mdl.t_data_dict.f_dict_id
);


-- 迁移 t_data_dict_item 表数据
INSERT INTO adp.t_data_dict_item
SELECT *
FROM dip_mdl.t_data_dict_item
WHERE NOT EXISTS (
    SELECT 1 FROM adp.t_data_dict_item t
    WHERE t.f_item_id = dip_mdl.t_data_dict_item.f_item_id
);


-- 迁移 t_data_connection 表数据
INSERT INTO adp.t_data_connection
SELECT *
FROM dip_mdl.t_data_connection
WHERE NOT EXISTS (
    SELECT 1 FROM adp.t_data_connection t
    WHERE t.f_connection_id = dip_mdl.t_data_connection.f_connection_id
);


-- 迁移 t_data_connection_status 表数据
INSERT INTO adp.t_data_connection_status
SELECT *
FROM dip_mdl.t_data_connection_status
WHERE NOT EXISTS (
    SELECT 1 FROM adp.t_data_connection_status t
    WHERE t.f_connection_id = dip_mdl.t_data_connection_status.f_connection_id
);


-- 迁移 t_trace_model 表数据
INSERT INTO adp.t_trace_model (
  f_model_id, f_model_name, f_tags, f_comment, f_creator, f_creator_type,
  f_create_time, f_update_time, f_span_source_type, f_span_config,
  f_enabled_related_log, f_related_log_source_type, f_related_log_config
)
SELECT
  f_model_id, f_model_name, f_tags, f_comment, f_creator, f_creator_type,
  f_create_time, f_update_time, f_span_source_type, f_span_config,
  f_enabled_related_log, f_related_log_source_type, f_related_log_config
FROM dip_mdl.t_trace_model
WHERE NOT EXISTS (
    SELECT 1 FROM adp.t_trace_model t
    WHERE t.f_model_id = dip_mdl.t_trace_model.f_model_id
);


-- 迁移 t_data_model_job 表数据
INSERT INTO adp.t_data_model_job (
  f_job_id, f_creator, f_creator_type, f_create_time, f_update_time,
  f_job_type, f_job_config, f_job_status, f_job_status_details
)
SELECT
  f_job_id, f_creator, f_creator_type, f_create_time, f_update_time,
  f_job_type, f_job_config, f_job_status, f_job_status_details
FROM dip_mdl.t_data_model_job
WHERE NOT EXISTS (
    SELECT 1 FROM adp.t_data_model_job t
    WHERE t.f_job_id = dip_mdl.t_data_model_job.f_job_id
);


-- 迁移 t_objective_model 表数据
INSERT INTO adp.t_objective_model (
  f_model_id, f_model_name, f_tags, f_comment, f_creator, f_creator_type,
  f_create_time, f_update_time, f_objective_type, f_objective_config
)
SELECT
  f_model_id, f_model_name, f_tags, f_comment, f_creator, f_creator_type,
  f_create_time, f_update_time, f_objective_type, f_objective_config
FROM dip_mdl.t_objective_model
WHERE NOT EXISTS (
    SELECT 1 FROM adp.t_objective_model t
    WHERE t.f_model_id = dip_mdl.t_objective_model.f_model_id
);


DROP TABLE IF EXISTS dip_mdl.t_metric_model CASCADE;
DROP TABLE IF EXISTS dip_mdl.t_metric_model_group CASCADE;
DROP TABLE IF EXISTS dip_mdl.t_metric_model_task CASCADE;
DROP TABLE IF EXISTS dip_mdl.t_static_metric_index CASCADE;
DROP TABLE IF EXISTS dip_mdl.t_event_model_aggregate_rules CASCADE;
DROP TABLE IF EXISTS dip_mdl.t_event_models CASCADE;
DROP TABLE IF EXISTS dip_mdl.t_event_model_detect_rules CASCADE;
DROP TABLE IF EXISTS dip_mdl.t_event_model_task CASCADE;
DROP TABLE IF EXISTS dip_mdl.t_event_model_task_execution_records CASCADE;
DROP TABLE IF EXISTS dip_mdl.t_data_view CASCADE;
DROP TABLE IF EXISTS dip_mdl.t_data_view_group CASCADE;
DROP TABLE IF EXISTS dip_mdl.t_data_view_row_column_rule CASCADE;
DROP TABLE IF EXISTS dip_mdl.t_data_dict CASCADE;
DROP TABLE IF EXISTS dip_mdl.t_data_dict_item CASCADE;
DROP TABLE IF EXISTS dip_mdl.t_data_connection CASCADE;
DROP TABLE IF EXISTS dip_mdl.t_data_connection_status CASCADE;
DROP TABLE IF EXISTS dip_mdl.t_trace_model CASCADE;
DROP TABLE IF EXISTS dip_mdl.t_data_model_job CASCADE;
DROP TABLE IF EXISTS dip_mdl.t_objective_model CASCADE;
DROP TABLE IF EXISTS dip_mdl.t_scan_record CASCADE;