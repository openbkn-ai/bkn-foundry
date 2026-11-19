-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

USE openbkn;

-- Network diagnoses: the latest diagnosis of each project's draft, run in the
-- background: its facts counted over the full tables, the suggestions Ask AI
-- or the server made, and what became of them. It is kept apart from the draft
-- so that recording a run never races an edit.
CREATE TABLE IF NOT EXISTS t_bkn_project_diagnosis (
  f_project_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Project ID',
  f_run_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Run ID: only this run records its outcome',
  f_state VARCHAR(20) NOT NULL DEFAULT '' COMMENT 'State: running, ready or failed',
  f_runner VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Account the counts and Ask AI run as',
  f_runner_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT 'Runner account type',
  f_result LONGTEXT NOT NULL COMMENT 'Metrics, components and suggestions with their statuses, JSON',
  f_error TEXT NOT NULL COMMENT 'Why the run failed',
  f_draft_update_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT 'Update time of the draft the diagnosis holds for',
  f_start_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT 'Start time',
  f_finish_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT 'Finish time',
  PRIMARY KEY (f_project_id)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = 'BKN-project network diagnoses';
