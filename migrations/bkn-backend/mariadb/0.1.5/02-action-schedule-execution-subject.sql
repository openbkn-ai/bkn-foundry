-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

-- Add the current execution subject used to authorize Schedule triggers.
ALTER TABLE t_action_schedule
  ADD COLUMN IF NOT EXISTS f_execution_subject VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Current execution subject ID' AFTER f_update_time,
  ADD COLUMN IF NOT EXISTS f_execution_subject_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT 'Current execution subject type' AFTER f_execution_subject;

-- Existing schedules retain their historical creator as an explicit subject.
-- A later executable configuration update or inactive-to-active transition
-- replaces the subject with the current, fully authorized caller.
UPDATE t_action_schedule
SET f_execution_subject = f_creator,
    f_execution_subject_type = f_creator_type
WHERE f_execution_subject = '' OR f_execution_subject_type = '';

-- Rollback:
-- ALTER TABLE t_action_schedule
--   DROP COLUMN IF EXISTS f_execution_subject_type,
--   DROP COLUMN IF EXISTS f_execution_subject;
