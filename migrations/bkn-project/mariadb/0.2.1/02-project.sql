-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

-- Projects: each one changes one knowledge network on its own git branch of
-- the network's lineage. The project's network is the lineage id.

USE openbkn;

CREATE TABLE IF NOT EXISTS t_bkn_project_project (
  f_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Project ID',
  f_name VARCHAR(255) NOT NULL DEFAULT '' COMMENT 'Project name',
  f_kn_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT 'Knowledge network ID: the lineage ID',
  f_branch VARCHAR(255) NOT NULL DEFAULT '' COMMENT 'Git branch of the lineage',
  f_baseline_network_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT 'Live knowledge network when the project started',
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
