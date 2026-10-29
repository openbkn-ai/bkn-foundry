-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

-- Local index builds a confirmation started: one row per confirmed commit and
-- table, with the vega-backend build task it created or found under way, or why
-- it could not. Retrying a confirmation's build replaces its rows.

USE openbkn;

CREATE TABLE IF NOT EXISTS t_bkn_project_index_build (
  f_project_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Project ID',
  f_commit VARCHAR(64) NOT NULL DEFAULT '' COMMENT 'Commit of the confirmation',
  f_resource_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Resource ID of the table',
  f_object_type_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Object type drafted from the table',
  f_task_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'vega-backend build task created or found under way',
  f_outcome VARCHAR(20) NOT NULL DEFAULT '' COMMENT 'Outcome: created, existing, no_permission or failed',
  f_reason TEXT NOT NULL COMMENT 'Why the build could not start',
  f_runner VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Account the build task was created as',
  f_runner_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT 'Runner account type',
  f_create_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT 'Create time',
  PRIMARY KEY (f_project_id, f_commit, f_resource_id)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = 'BKN-project local index builds started by confirmations';
