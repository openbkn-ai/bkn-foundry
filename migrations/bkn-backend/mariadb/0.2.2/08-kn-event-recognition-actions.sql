-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

-- An event class of source rule is checked by its recognition action: an
-- action type whose operation is event, run on its own schedule. The action
-- type keeps who it checks as and the version of its rule; the platform keeps
-- one schedule row for each recognition action of the running version; and an
-- event's history notes the execution that recorded it. Existing rows take the
-- defaults: none of them is a recognition action. The new columns are
-- appended last so MariaDB can add them in place.

USE openbkn;

ALTER TABLE t_action_type
  ADD COLUMN IF NOT EXISTS f_execution_subject VARCHAR(40) NOT NULL DEFAULT '' COMMENT '执行身份ID，记录事件的行动类',
  ADD COLUMN IF NOT EXISTS f_execution_subject_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT '执行身份类型',
  ADD COLUMN IF NOT EXISTS f_rule_version VARCHAR(64) NOT NULL DEFAULT '' COMMENT '规则版本，记录事件的行动类';

ALTER TABLE t_action_schedule
  ADD COLUMN IF NOT EXISTS f_source VARCHAR(16) NOT NULL DEFAULT '' COMMENT '空为用户建立的定时；action_type 为平台按记录事件的行动类维护';

ALTER TABLE t_kn_event_change
  ADD COLUMN IF NOT EXISTS f_execution_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT '写入这次变更的行动执行ID';
