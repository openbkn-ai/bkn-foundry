-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

-- Why a scenario question got no test questions: the reason the Agent gave,
-- and how many of the test questions it wrote the server left out. Empty and
-- 0 for guidance generated before them.

USE openbkn;

ALTER TABLE t_bkn_project_question_guidance
  ADD COLUMN IF NOT EXISTS f_no_tests_reason VARCHAR(1024) NOT NULL DEFAULT '' COMMENT 'Why the Agent wrote no test questions, as it said' AFTER f_out_of_scope,
  ADD COLUMN IF NOT EXISTS f_left_out_tests INT NOT NULL DEFAULT 0 COMMENT 'Test questions the Agent wrote that the server left out' AFTER f_no_tests_reason;
