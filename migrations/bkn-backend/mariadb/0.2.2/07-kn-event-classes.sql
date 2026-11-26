-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

-- Event classes become object types of their network: an object type whose
-- kinds include event, with the kind's configuration beside it. The event
-- module keeps no event types of its own any more; t_kn_event_type, made by
-- the earlier scripts of this version, is left in place unused. Rows of the
-- rule check table are not converted: they belong to event types of
-- development images, which are not converted either. The new columns are
-- appended last so MariaDB can add them in place.

USE openbkn;

ALTER TABLE t_object_type
  ADD COLUMN IF NOT EXISTS f_kinds VARCHAR(255) NOT NULL DEFAULT '' COMMENT '对象类的种类，JSON 字符串数组，空为普通对象类',
  ADD COLUMN IF NOT EXISTS f_event TEXT DEFAULT NULL COMMENT 'event 种类的配置JSON';

ALTER TABLE t_kn_event_rule_check
  ADD COLUMN IF NOT EXISTS f_last_ok_rule_version VARCHAR(64) NOT NULL DEFAULT '' COMMENT '最近一次成功判断时的规则版本',
  ADD COLUMN IF NOT EXISTS f_dataset_synced TINYINT(1) NOT NULL DEFAULT 0 COMMENT '账本中已有的事件是否已写入数据集';

ALTER TABLE t_kn_event
  ADD INDEX IF NOT EXISTS idx_kn_event_type_occurred (f_kn_id, f_event_type, f_occurred_at);

-- f_event_type keeps its name and now holds the ID of the event class; only
-- its comment changes, as in init.sql.
ALTER TABLE t_kn_event
  MODIFY COLUMN f_event_type VARCHAR(64) NOT NULL DEFAULT '' COMMENT '事件类ID';

ALTER TABLE t_kn_event_rule_check
  MODIFY COLUMN f_event_type VARCHAR(64) NOT NULL DEFAULT '' COMMENT '事件类ID';
