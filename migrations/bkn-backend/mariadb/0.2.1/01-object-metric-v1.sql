-- Copyright 2026 openbkn.ai
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.

USE openbkn;

CREATE TABLE IF NOT EXISTS t_object_metric_definition (
  f_kn_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT '业务知识网络ID',
  f_branch VARCHAR(40) NOT NULL DEFAULT '' COMMENT '分支',
  f_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT '对象指标ID',
  f_version INT NOT NULL DEFAULT 1 COMMENT '不可变版本号',
  f_code VARCHAR(128) NOT NULL DEFAULT '' COMMENT '稳定指标编码',
  f_name VARCHAR(128) NOT NULL DEFAULT '' COMMENT '指标名称',
  f_description TEXT NOT NULL COMMENT '业务定义',
  f_owner_object_type_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT '主归属对象类ID',
  f_metric_type VARCHAR(32) NOT NULL COMMENT 'atomic|derived|composite',
  f_status VARCHAR(32) NOT NULL DEFAULT 'draft' COMMENT 'draft|validated|published|deprecated',
  f_definition LONGTEXT NOT NULL COMMENT '对象指标V1定义JSON',
  f_creator VARCHAR(40) NOT NULL DEFAULT '' COMMENT '创建者ID',
  f_creator_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT '创建者类型',
  f_create_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT '创建时间',
  f_updater VARCHAR(40) NOT NULL DEFAULT '' COMMENT '更新者ID',
  f_updater_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT '更新者类型',
  f_update_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT '更新时间',
  PRIMARY KEY (f_kn_id, f_branch, f_id, f_version),
  UNIQUE KEY uk_object_metric_code_version (f_kn_id, f_branch, f_code, f_version),
  INDEX idx_object_metric_owner (f_kn_id, f_branch, f_owner_object_type_id, f_update_time),
  INDEX idx_object_metric_status (f_kn_id, f_branch, f_status, f_update_time)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = '对象指标版本定义';


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
