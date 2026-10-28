-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

-- Ontology drafts: the current draft of each project, drafted from the table
-- structure of the data catalogs the project uses. A draft is working data of
-- the project and stays out of git until it is confirmed.

USE openbkn;

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
