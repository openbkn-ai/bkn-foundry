-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

USE openbkn;

-- 知识网络事件类型（识别配置）
CREATE TABLE IF NOT EXISTS t_kn_event_type (
  f_kn_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '业务知识网络ID',
  f_code VARCHAR(64) NOT NULL DEFAULT '' COMMENT '事件类型编码',
  f_name VARCHAR(128) NOT NULL DEFAULT '' COMMENT '事件类型名称',
  f_source VARCHAR(16) NOT NULL DEFAULT '' COMMENT 'rule|report',
  f_subject VARCHAR(128) NOT NULL DEFAULT '' COMMENT '事件涉及的对象类ID',
  f_severity VARCHAR(16) NOT NULL DEFAULT '' COMMENT '默认严重程度',
  f_merge_hours DOUBLE NOT NULL DEFAULT 0 COMMENT '登记事件的合并窗口（小时）',
  f_rule LONGTEXT NOT NULL COMMENT '规则定义JSON',
  f_rule_version VARCHAR(64) NOT NULL DEFAULT '' COMMENT '规则版本',
  f_creator VARCHAR(40) NOT NULL DEFAULT '' COMMENT '创建者ID',
  f_creator_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT '创建者类型',
  f_create_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT '创建时间',
  f_updater VARCHAR(40) NOT NULL DEFAULT '' COMMENT '更新者ID',
  f_updater_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT '更新者类型',
  f_update_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT '更新时间',
  PRIMARY KEY (f_kn_id, f_code)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = '知识网络事件类型';


-- 知识网络事件账本
CREATE TABLE IF NOT EXISTS t_kn_event (
  f_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT '事件ID',
  f_kn_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '业务知识网络ID',
  f_event_type VARCHAR(64) NOT NULL DEFAULT '' COMMENT '事件类型编码',
  f_title VARCHAR(256) NOT NULL DEFAULT '' COMMENT '标题',
  f_status VARCHAR(16) NOT NULL DEFAULT '' COMMENT 'unverified|verified|rejected|duplicate|closed',
  f_severity VARCHAR(16) NOT NULL DEFAULT '' COMMENT '严重程度',
  f_source VARCHAR(16) NOT NULL DEFAULT '' COMMENT 'rule|report',
  f_rule_version VARCHAR(64) NOT NULL DEFAULT '' COMMENT '规则版本',
  f_object_type VARCHAR(128) NOT NULL DEFAULT '' COMMENT '对象类ID',
  f_object_key VARCHAR(250) NOT NULL DEFAULT '' COMMENT '对象键',
  f_object_name VARCHAR(256) NOT NULL DEFAULT '' COMMENT '对象显示名',
  f_object_attributes LONGTEXT NOT NULL COMMENT '写入方提供的对象属性值JSON',
  f_occurred_at BIGINT(20) NOT NULL DEFAULT 0 COMMENT '发生时间',
  f_recorded_at BIGINT(20) NOT NULL DEFAULT 0 COMMENT '记录时间',
  f_ended_at BIGINT(20) NOT NULL DEFAULT 0 COMMENT '结束时间',
  f_first_detected_at BIGINT(20) NOT NULL DEFAULT 0 COMMENT '首次发现时间',
  f_last_checked_at BIGINT(20) NOT NULL DEFAULT 0 COMMENT '最近检测时间',
  f_check_status VARCHAR(16) NOT NULL DEFAULT '' COMMENT 'ok|unknown',
  f_detail LONGTEXT NOT NULL COMMENT '详情JSON',
  f_backlog TINYINT(1) NOT NULL DEFAULT 0 COMMENT '规则首次检查发现的存量',
  f_merged_into VARCHAR(40) NOT NULL DEFAULT '' COMMENT '合并到的事件ID',
  f_reported_by VARCHAR(40) NOT NULL DEFAULT '' COMMENT '登记人ID',
  f_reported_by_name VARCHAR(128) NOT NULL DEFAULT '' COMMENT '登记人显示名',
  f_verified_by VARCHAR(40) NOT NULL DEFAULT '' COMMENT '核实人ID',
  f_verified_by_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT '核实人类型',
  f_verified_by_name VARCHAR(128) NOT NULL DEFAULT '' COMMENT '核实人显示名',
  f_verified_at BIGINT(20) NOT NULL DEFAULT 0 COMMENT '核实时间',
  f_revision INT NOT NULL DEFAULT 1 COMMENT '修订号',
  f_create_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT '创建时间',
  f_update_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT '更新时间',
  PRIMARY KEY (f_id),
  INDEX idx_kn_event_recorded (f_kn_id, f_recorded_at, f_id),
  INDEX idx_kn_event_type_object (f_kn_id, f_event_type, f_object_key)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = '知识网络事件';


-- 知识网络事件变更记录，按网络连续编号，兼作变更流
CREATE TABLE IF NOT EXISTS t_kn_event_change (
  f_kn_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '业务知识网络ID',
  f_seq BIGINT(20) NOT NULL COMMENT '网络内变更序号',
  f_event_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT '事件ID',
  f_action VARCHAR(32) NOT NULL DEFAULT '' COMMENT '变更动作',
  f_from_status VARCHAR(16) NOT NULL DEFAULT '' COMMENT '变更前状态',
  f_to_status VARCHAR(16) NOT NULL DEFAULT '' COMMENT '变更后状态',
  f_actor VARCHAR(40) NOT NULL DEFAULT '' COMMENT '操作者ID',
  f_actor_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT '操作者类型',
  f_actor_name VARCHAR(128) NOT NULL DEFAULT '' COMMENT '操作者显示名',
  f_note VARCHAR(512) NOT NULL DEFAULT '' COMMENT '原因或说明',
  f_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT '变更时间',
  PRIMARY KEY (f_kn_id, f_seq),
  INDEX idx_kn_event_change_event (f_event_id, f_seq),
  INDEX idx_kn_event_change_action_time (f_action, f_time)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = '知识网络事件变更记录';


-- 知识网络事件规则检查（各事件类型最近一次）
CREATE TABLE IF NOT EXISTS t_kn_event_rule_check (
  f_kn_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '业务知识网络ID',
  f_event_type VARCHAR(64) NOT NULL DEFAULT '' COMMENT '事件类型编码',
  f_status VARCHAR(16) NOT NULL DEFAULT '' COMMENT 'ok|unknown',
  f_rule_version VARCHAR(64) NOT NULL DEFAULT '' COMMENT '规则版本',
  f_hits INT NOT NULL DEFAULT 0 COMMENT '命中对象数',
  f_message VARCHAR(1024) NOT NULL DEFAULT '' COMMENT '无法判断的原因',
  f_last_ok BIGINT(20) NOT NULL DEFAULT 0 COMMENT '最近一次成功判断的时间',
  f_checked_by VARCHAR(40) NOT NULL DEFAULT '' COMMENT '检查人ID',
  f_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT '检查时间',
  PRIMARY KEY (f_kn_id, f_event_type)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = '知识网络事件规则检查';


-- 每个知识网络一行，记最新变更序号；写入事件时锁住它，保证变更按提交顺序编号
CREATE TABLE IF NOT EXISTS t_kn_event_head (
  f_kn_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '业务知识网络ID',
  f_last_seq BIGINT(20) NOT NULL DEFAULT 0 COMMENT '最新变更序号',
  PRIMARY KEY (f_kn_id)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = '知识网络事件序号';
