-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

-- Regressions of project releases: after a project publishes a release, its
-- scenario questions of the knowledge layer are answered on the new network
-- and judged by rules. One row per release, keyed by the network it created;
-- the questions are kept as they were, so the result reads the same after the
-- specification changes. A regression never blocks or undoes a release.

USE openbkn;

CREATE TABLE IF NOT EXISTS t_bkn_project_regression (
  f_network_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT 'Knowledge network the release created',
  f_project_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Project ID',
  f_version VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Release version',
  f_status VARCHAR(20) NOT NULL DEFAULT '' COMMENT 'Status: running, passed, not_passed, failed or no_questions',
  f_run_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Run ID: only this run records its outcome',
  f_attempt INT NOT NULL DEFAULT 0 COMMENT 'Attempts made in this run',
  f_runner VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Account the questions are answered as',
  f_runner_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT 'Runner account type',
  f_questions LONGTEXT NOT NULL COMMENT 'The questions answered, as the specification had them, JSON',
  f_results LONGTEXT NOT NULL COMMENT 'Outcome of each question, JSON',
  f_error VARCHAR(64) NOT NULL DEFAULT '' COMMENT 'Why the run failed, as a code',
  f_start_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT 'Start time',
  f_finish_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT 'Finish time',
  PRIMARY KEY (f_network_id),
  KEY idx_bkn_project_regression_project (f_project_id, f_start_time),
  KEY idx_bkn_project_regression_status (f_status)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = 'BKN-project regressions of releases';
