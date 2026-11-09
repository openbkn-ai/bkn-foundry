-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

USE openbkn;

-- Releases go into one versioned knowledge network (bkn-foundry#1229): a release
-- is prepared in the network's draft, then published as a network version.
-- Consumers no longer switch between networks.

ALTER TABLE t_bkn_project_release
  DROP INDEX uk_bkn_project_release_network,
  MODIFY COLUMN f_state VARCHAR(20) NOT NULL DEFAULT 'prepared' COMMENT 'State: prepared or published',
  ADD COLUMN f_kn_version INT NOT NULL DEFAULT 0 COMMENT 'Network version publishing made; 0 while prepared' AFTER f_state,
  ADD COLUMN f_branch_commit VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Commit holding the prepared definitions' AFTER f_kn_version,
  ADD COLUMN f_draft_revision BIGINT(20) NOT NULL DEFAULT 0 COMMENT 'Draft revision once the definitions were written' AFTER f_branch_commit,
  ADD COLUMN f_capabilities MEDIUMTEXT NOT NULL COMMENT 'Function and Skill files publishing commits, JSON' AFTER f_draft_revision,
  ADD UNIQUE KEY uk_bkn_project_release_version (f_lineage_id, f_version);

ALTER TABLE t_bkn_project_regression
  CHANGE COLUMN f_network_id f_release_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Release ID';

DROP TABLE IF EXISTS t_bkn_project_switch;
