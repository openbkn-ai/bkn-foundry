-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

-- Add the new action definition before the one-time data conversion. Re-running
-- this schema step is safe; it does not invent operation metadata for old rows.
ALTER TABLE t_action_type
  ADD COLUMN IF NOT EXISTS f_operation_config TEXT DEFAULT NULL COMMENT '行动操作定义';
