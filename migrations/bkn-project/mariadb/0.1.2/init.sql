-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

USE openbkn;


-- BKN-project release lineage: each release is imported as a new knowledge
-- network; the lineage id is the base network id.
CREATE TABLE IF NOT EXISTS t_bkn_project_release (
  f_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Release ID',
  f_lineage_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Lineage ID: the base knowledge network ID',
  f_network_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Knowledge network ID this release was imported as',
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
  f_lineage_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Lineage ID',
  f_from_network_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Knowledge network ID switched away from',
  f_to_network_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Knowledge network ID switched to',
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
  f_kn_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Knowledge network ID: the lineage ID',
  f_branch VARCHAR(255) NOT NULL DEFAULT '' COMMENT 'Git branch of the lineage',
  f_baseline_network_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Live knowledge network when the project started',
  f_catalog_ids TEXT NOT NULL COMMENT 'Data catalog IDs the project uses, JSON array',
  f_archived TINYINT(1) NOT NULL DEFAULT 0 COMMENT 'Archived: hidden from the default list',
  f_owner VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Owner ID: the creator',
  f_owner_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT 'Owner type',
  f_create_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT 'Create time',
  f_update_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT 'Update time',
  PRIMARY KEY (f_id),
  KEY idx_bkn_project_project_kn (f_kn_id, f_archived),
  KEY idx_bkn_project_project_create (f_create_time, f_id)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = 'BKN-project projects';
