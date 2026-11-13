-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

USE openbkn;

-- AI-assisted kinetic modeling and business scenarios. The owner's supplement
-- note sits beside the specification, never in it, so running again or starting
-- over leaves it; every kinetic analysis, scenario drafting and Ask AI reads it.
ALTER TABLE t_bkn_project_requirement
  ADD COLUMN IF NOT EXISTS f_supplement TEXT NOT NULL COMMENT 'The owner''s supplement note every kinetic analysis, scenario drafting and Ask AI reads, JSON';

-- Ask AI conversations of the kinetic modeling and business scenarios pages,
-- one row each, kept per page: the questions, the answers, the changes each
-- answer proposed and what became of them. Deleting a conversation deletes its
-- row; what was adopted from it stays in the specification.
CREATE TABLE IF NOT EXISTS t_bkn_project_requirement_conversation (
  f_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Conversation ID',
  f_project_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Project ID',
  f_page VARCHAR(20) NOT NULL DEFAULT '' COMMENT 'Page: kinetic or scenarios',
  f_title VARCHAR(255) NOT NULL DEFAULT '' COMMENT 'Title: the first question, cut short',
  f_turns LONGTEXT NOT NULL COMMENT 'Questions, answers and proposals with their decisions, JSON',
  f_creator VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Account that started it',
  f_creator_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT 'Creator account type',
  f_create_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT 'Create time',
  f_update_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT 'Update time',
  PRIMARY KEY (f_id),
  KEY idx_bkn_project_requirement_conversation_page (f_project_id, f_page, f_update_time)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = 'BKN-project Ask AI conversations';
