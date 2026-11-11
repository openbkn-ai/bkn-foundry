-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

-- Each kind of event becomes an object type of its network (its event class),
-- bound to a dataset the event module writes from the ledger.
-- Existing event types keep empty values, which means no class yet; the event
-- module creates it. Existing heads keep 0, so the first round reads the feed
-- from its start. The new columns are appended last so MariaDB can add them
-- in place.

USE openbkn;

ALTER TABLE t_kn_event_type
  ADD COLUMN IF NOT EXISTS f_object_type_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '事件类（对象类）ID，空为尚未建立',
  ADD COLUMN IF NOT EXISTS f_dataset_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '事件数据集ID，空为尚未建立',
  ADD COLUMN IF NOT EXISTS f_dataset_synced TINYINT(1) NOT NULL DEFAULT 0 COMMENT '账本中已有的事件是否已写入数据集';

ALTER TABLE t_kn_event_head
  ADD COLUMN IF NOT EXISTS f_projected_seq BIGINT(20) NOT NULL DEFAULT 0 COMMENT '已写入事件数据集的变更序号';
