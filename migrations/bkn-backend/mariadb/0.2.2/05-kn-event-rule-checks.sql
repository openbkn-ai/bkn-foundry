-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

-- A rule event type may name a rule tool and a check schedule; the event
-- module then checks the rule itself, as the person who saved them. Existing
-- types keep empty values: they have no rule tool and are checked as before,
-- by the applications. The new columns are appended last so MariaDB can add
-- them in place.

USE openbkn;

ALTER TABLE t_kn_event_type
  ADD COLUMN IF NOT EXISTS f_rule_tool TEXT DEFAULT NULL COMMENT '规则工具JSON（box_id、tool_id、parameters），空为由应用检查',
  ADD COLUMN IF NOT EXISTS f_check_cron VARCHAR(128) NOT NULL DEFAULT '' COMMENT '检查时间（五段cron）',
  ADD COLUMN IF NOT EXISTS f_check_timezone VARCHAR(64) NOT NULL DEFAULT '' COMMENT '检查时区（IANA）',
  ADD COLUMN IF NOT EXISTS f_check_subject_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT '执行身份ID',
  ADD COLUMN IF NOT EXISTS f_check_subject_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT '执行身份类型',
  ADD COLUMN IF NOT EXISTS f_next_check_at BIGINT(20) NOT NULL DEFAULT 0 COMMENT '下次检查时间，0为不由平台检查',
  ADD COLUMN IF NOT EXISTS f_check_failures INT(11) NOT NULL DEFAULT 0 COMMENT '连续无法判断的检查次数',
  ADD INDEX IF NOT EXISTS idx_kn_event_type_next_check (f_next_check_at);
