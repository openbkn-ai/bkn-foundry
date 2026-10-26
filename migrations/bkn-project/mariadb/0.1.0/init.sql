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
