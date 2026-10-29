-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

USE openbkn;


-- BKN-project release lineage: each release is imported as a new knowledge
-- network; the lineage id is the base network id.
CREATE TABLE IF NOT EXISTS t_bkn_project_release (
  f_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Release ID',
  f_lineage_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT 'Lineage ID: the base knowledge network ID',
  f_network_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT 'Knowledge network ID this release was imported as',
  f_version VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Release version',
  f_base_version VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Version this release was based on; empty for the first release',
  f_project_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Project ID',
  f_publisher VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Publisher ID',
  f_publisher_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT 'Publisher type',
  f_notes TEXT NOT NULL COMMENT 'Release notes',
  f_state VARCHAR(20) NOT NULL DEFAULT 'live' COMMENT 'State: live or archived',
  f_create_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT 'Create time',
  f_update_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT 'Update time',
  PRIMARY KEY (f_id),
  UNIQUE KEY uk_bkn_project_release_network (f_network_id),
  KEY idx_bkn_project_release_lineage (f_lineage_id, f_create_time, f_id)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = 'BKN-project release lineage';

-- Consumer switches of a release lineage: each row moves the lineage's
-- consumers, and the schedules bkn-project can move, from one network to
-- another. The latest row names the network in use; a rollback is a switch
-- back to an earlier network.


CREATE TABLE IF NOT EXISTS t_bkn_project_switch (
  f_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Switch ID',
  f_lineage_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT 'Lineage ID',
  f_from_network_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT 'Knowledge network ID switched away from',
  f_to_network_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT 'Knowledge network ID switched to',
  f_operator VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Operator ID',
  f_operator_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT 'Operator type',
  f_schedules TEXT NOT NULL COMMENT 'Schedules moved, JSON: from_id, to_id, name',
  f_create_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT 'Create time',
  PRIMARY KEY (f_id),
  KEY idx_bkn_project_switch_lineage (f_lineage_id, f_create_time, f_id)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = 'BKN-project consumer switches';

-- Projects: each one changes one knowledge network on its own git branch of
-- the network's lineage. The project's network is the lineage id.


CREATE TABLE IF NOT EXISTS t_bkn_project_project (
  f_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Project ID',
  f_name VARCHAR(255) NOT NULL DEFAULT '' COMMENT 'Project name',
  f_kn_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT 'Knowledge network ID: the lineage ID',
  f_branch VARCHAR(255) NOT NULL DEFAULT '' COMMENT 'Git branch of the lineage',
  f_baseline_network_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT 'Live knowledge network when the project started',
  f_catalog_ids TEXT NOT NULL COMMENT 'Data catalog IDs the project uses, JSON array',
  f_naming_language VARCHAR(16) NOT NULL DEFAULT '' COMMENT 'Naming language: zh-CN or en-US; empty reads as zh-CN',
  f_archived TINYINT(1) NOT NULL DEFAULT 0 COMMENT 'Archived: hidden from the default list',
  f_owner VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Owner ID: the creator',
  f_owner_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT 'Owner type',
  f_create_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT 'Create time',
  f_update_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT 'Update time',
  PRIMARY KEY (f_id),
  KEY idx_bkn_project_project_kn (f_kn_id, f_archived),
  KEY idx_bkn_project_project_create (f_create_time, f_id)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = 'BKN-project projects';

-- Ontology drafts: the current draft of each project, drafted from the table
-- structure of the data catalogs the project uses. A draft is working data of
-- the project and stays out of git until it is confirmed.


CREATE TABLE IF NOT EXISTS t_bkn_project_draft (
  f_project_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Project ID',
  f_status VARCHAR(20) NOT NULL DEFAULT '' COMMENT 'Status: drafting, ready or failed',
  f_items LONGTEXT NOT NULL COMMENT 'Draft items with their evidence, JSON',
  f_error TEXT NOT NULL COMMENT 'Why drafting failed',
  f_drafter VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Account that started drafting',
  f_drafter_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT 'Drafter account type',
  f_create_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT 'Create time',
  f_update_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT 'Update time',
  PRIMARY KEY (f_project_id),
  KEY idx_bkn_project_draft_status (f_status)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = 'BKN-project ontology drafts';

-- Checks of confirmed ontology drafts: the latest check of each project, run in
-- the background after a confirmation against what that confirmation wrote. It
-- is kept apart from the draft so that recording a check never races an edit.

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

-- IDs of a project's items drafted from physical names that are not ASCII: the
-- English short name semantic understanding gave, or a short-hash placeholder
-- when it gave none. Every later draft of the project reuses the registered ID,
-- so it does not change; a placeholder may be replaced until a confirmation
-- locks it.

CREATE TABLE IF NOT EXISTS t_bkn_project_id (
  f_project_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Project ID',
  f_kind VARCHAR(20) NOT NULL DEFAULT '' COMMENT 'Kind: object_types, value_types, relation_types or properties',
  f_source_key VARCHAR(255) NOT NULL DEFAULT '' COMMENT 'Resource ID, or resource ID and field name for a property',
  f_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Registered ID',
  f_origin VARCHAR(20) NOT NULL DEFAULT '' COMMENT 'Origin: semantic or fallback',
  f_basis TEXT NOT NULL COMMENT 'What the ID was made from, JSON',
  f_locked TINYINT(1) NOT NULL DEFAULT 0 COMMENT 'Locked by a confirmation; never replaced again',
  f_create_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT 'Create time',
  f_update_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT 'Update time',
  PRIMARY KEY (f_project_id, f_kind, f_source_key)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = 'BKN-project IDs of items drafted from non-ASCII physical names';

-- Local index builds a confirmation started: one row per confirmed commit and
-- table, with the vega-backend build task it created or found under way, or why
-- it could not. Retrying a confirmation's build replaces its rows.

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
