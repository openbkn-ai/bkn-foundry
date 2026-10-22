-- Copyright 2026 openbkn.ai
--
-- Licensed under the Apache License, Version 2.0.

USE openbkn;

CREATE TABLE IF NOT EXISTS t_object_metric_reference (
  f_id BIGINT NOT NULL AUTO_INCREMENT COMMENT '引用记录ID',
  f_kn_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT '业务知识网络ID',
  f_branch VARCHAR(40) NOT NULL DEFAULT '' COMMENT '分支',
  f_source_type VARCHAR(24) NOT NULL COMMENT 'metric_version|logic_property',
  f_source_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT '来源指标或对象类ID',
  f_source_version INT NOT NULL DEFAULT 0 COMMENT '来源指标版本；逻辑属性为0',
  f_source_field VARCHAR(128) NOT NULL DEFAULT '' COMMENT '引用字段路径',
  f_source_property_name VARCHAR(40) NOT NULL DEFAULT '' COMMENT '逻辑属性名称',
  f_target_metric_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT '目标指标ID',
  f_target_version INT NOT NULL DEFAULT 1 COMMENT '目标指标版本',
  PRIMARY KEY (f_id),
  UNIQUE KEY uk_object_metric_reference (
    f_kn_id, f_branch, f_source_type, f_source_id, f_source_version,
    f_source_field, f_source_property_name, f_target_metric_id, f_target_version
  ),
  INDEX idx_object_metric_reference_target (
    f_kn_id, f_branch, f_target_metric_id, f_target_version
  ),
  INDEX idx_object_metric_reference_source (
    f_kn_id, f_branch, f_source_type, f_source_id, f_source_version
  ),
  CONSTRAINT fk_object_metric_reference_target
    FOREIGN KEY (f_kn_id, f_branch, f_target_metric_id, f_target_version)
    REFERENCES t_object_metric_definition (f_kn_id, f_branch, f_id, f_version)
    ON UPDATE RESTRICT ON DELETE RESTRICT
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = '对象指标版本引用索引';

INSERT IGNORE INTO t_object_metric_reference (
  f_kn_id, f_branch, f_source_type, f_source_id, f_source_version,
  f_source_field, f_source_property_name, f_target_metric_id, f_target_version
)
SELECT
  f_kn_id, f_branch, 'metric_version', f_id, f_version,
  'specification.base_metric', '',
  JSON_UNQUOTE(JSON_EXTRACT(f_definition, '$.specification.base_metric.metric_id')),
  CAST(JSON_UNQUOTE(JSON_EXTRACT(f_definition, '$.specification.base_metric.version')) AS UNSIGNED)
FROM t_object_metric_definition
WHERE JSON_UNQUOTE(JSON_EXTRACT(f_definition, '$.specification.base_metric.metric_id')) <> '';

INSERT IGNORE INTO t_object_metric_reference (
  f_kn_id, f_branch, f_source_type, f_source_id, f_source_version,
  f_source_field, f_source_property_name, f_target_metric_id, f_target_version
)
SELECT
  metric.f_kn_id, metric.f_branch, 'metric_version', metric.f_id, metric.f_version,
  CONCAT('specification.dependencies[', dependency.ordinality - 1, '].metric_ref'), '',
  dependency.metric_id, dependency.metric_version
FROM t_object_metric_definition AS metric
JOIN JSON_TABLE(
  metric.f_definition,
  '$.specification.dependencies[*]' COLUMNS (
    ordinality FOR ORDINALITY,
    metric_id VARCHAR(40) PATH '$.metric_ref.metric_id',
    metric_version INT PATH '$.metric_ref.version'
  )
) AS dependency
WHERE dependency.metric_id <> '' AND dependency.metric_version > 0;

INSERT IGNORE INTO t_object_metric_reference (
  f_kn_id, f_branch, f_source_type, f_source_id, f_source_version,
  f_source_field, f_source_property_name, f_target_metric_id, f_target_version
)
SELECT
  object_type.f_kn_id, object_type.f_branch, 'logic_property', object_type.f_id, 0,
  CASE
    WHEN property.data_source_id REGEXP '^object_metric_v1:[^@]+@[1-9][0-9]*$'
      THEN CONCAT('logic_properties.', property.property_name, '.data_source.id')
    ELSE CONCAT('logic_properties.', property.property_name, '.metric_binding.metric_ref')
  END,
  property.property_name,
  CASE
    WHEN property.data_source_id REGEXP '^object_metric_v1:[^@]+@[1-9][0-9]*$'
      THEN SUBSTRING_INDEX(SUBSTRING(property.data_source_id, 18), '@', 1)
    ELSE property.binding_metric_id
  END,
  CASE
    WHEN property.data_source_id REGEXP '^object_metric_v1:[^@]+@[1-9][0-9]*$'
      THEN CAST(SUBSTRING_INDEX(property.data_source_id, '@', -1) AS UNSIGNED)
    ELSE property.binding_metric_version
  END
FROM t_object_type AS object_type
JOIN JSON_TABLE(
  COALESCE(object_type.f_logic_properties, '[]'),
  '$[*]' COLUMNS (
    property_name VARCHAR(40) PATH '$.name',
    data_source_id VARCHAR(128) PATH '$.data_source.id',
    binding_metric_id VARCHAR(40) PATH '$.metric_binding.metric_ref.metric_id',
    binding_metric_version INT PATH '$.metric_binding.metric_ref.version'
  )
) AS property
WHERE property.data_source_id REGEXP '^object_metric_v1:[^@]+@[1-9][0-9]*$'
  OR (property.binding_metric_id <> '' AND property.binding_metric_version > 0);
