-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

-- An event class may name an action type that handles its events. In mode auto, each event a
-- rule check newly finds gets a row here in the transaction that records it, and the platform
-- runs the action type on the object of the event as the class's execution subject. The row
-- keeps how that went: queued, started (with the execution), needs_person (the action type is
-- governed, and only a person can start it) or failed.

USE openbkn;

CREATE TABLE IF NOT EXISTS t_kn_event_handling (
  f_event_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT '事件ID',
  f_kn_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '业务知识网络ID',
  f_event_type VARCHAR(64) NOT NULL DEFAULT '' COMMENT '事件类ID',
  f_action_type_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '处置行动类ID',
  f_target TEXT NOT NULL COMMENT '处置的对象：事件关于的对象的标识，JSON',
  f_parameters TEXT NOT NULL COMMENT '行动类输入的取值：{参数名: {value_from, value}}，JSON',
  f_subject_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT '执行身份ID，即识别的执行身份',
  f_subject_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT '执行身份类型',
  f_status VARCHAR(16) NOT NULL DEFAULT '' COMMENT 'queued 待执行|started 已开始执行|needs_person 需人工发起|failed 未能执行',
  f_attempts INT NOT NULL DEFAULT 0 COMMENT '已尝试次数',
  f_next_at BIGINT(20) NOT NULL DEFAULT 0 COMMENT '下次尝试时间',
  f_execution_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT '行动执行ID',
  f_message VARCHAR(1000) NOT NULL DEFAULT '' COMMENT '未能执行或需人工发起的原因',
  f_create_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT '创建时间',
  f_update_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT '更新时间',
  PRIMARY KEY (f_event_id),
  INDEX idx_kn_event_handling_due (f_status, f_next_at),
  INDEX idx_kn_event_handling_kn (f_kn_id, f_event_type)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = '知识网络事件的自动处置';
