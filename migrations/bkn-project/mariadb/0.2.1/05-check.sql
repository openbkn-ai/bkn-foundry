-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

-- Checks of confirmed ontology drafts: the latest check of each project, run in
-- the background after a confirmation against what that confirmation wrote. It
-- is kept apart from the draft so that recording a check never races an edit.

USE openbkn;

CREATE TABLE IF NOT EXISTS t_bkn_project_check (
  f_project_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Project ID',
  f_commit VARCHAR(64) NOT NULL DEFAULT '' COMMENT 'Commit of the confirmation checked',
  f_status VARCHAR(20) NOT NULL DEFAULT '' COMMENT 'Status: running, passed, issues or failed',
  f_run_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Run ID: only this run records its outcome',
  f_attempt INT NOT NULL DEFAULT 0 COMMENT 'Attempts made in this run',
  f_runner VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Account the counts run as',
  f_runner_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT 'Runner account type',
  f_input LONGTEXT NOT NULL COMMENT 'What the confirmation wrote, as the check reads it, JSON',
  f_results LONGTEXT NOT NULL COMMENT 'Results of each check item, JSON',
  f_error TEXT NOT NULL COMMENT 'Why the run failed',
  f_start_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT 'Start time',
  f_finish_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT 'Finish time',
  PRIMARY KEY (f_project_id),
  KEY idx_bkn_project_check_status (f_status)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = 'BKN-project checks of confirmed drafts';
