-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

USE openbkn;

-- Releases go into one versioned knowledge network (bkn-foundry#1229): a release
-- is prepared in the network's draft, then published as a network version.
-- Consumers no longer switch between networks.

-- Releases of the one-network-per-release scheme stay as records under their own
-- state: their versions remain taken, and none of them is a version of a
-- versioned network.
UPDATE t_bkn_project_release SET f_state = 'legacy' WHERE f_state IN ('live', 'archived');

ALTER TABLE t_bkn_project_release
  DROP INDEX uk_bkn_project_release_network,
  MODIFY COLUMN f_state VARCHAR(20) NOT NULL DEFAULT 'prepared' COMMENT 'State: prepared, published, or legacy for one-network-per-release records',
  ADD COLUMN f_kn_version INT NOT NULL DEFAULT 0 COMMENT 'Network version publishing made; 0 while prepared' AFTER f_state,
  ADD COLUMN f_branch_commit VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Commit holding the prepared definitions' AFTER f_kn_version,
  ADD COLUMN f_draft_revision BIGINT(20) NOT NULL DEFAULT 0 COMMENT 'Draft revision once the definitions were written' AFTER f_branch_commit,
  ADD COLUMN f_capabilities MEDIUMTEXT NOT NULL COMMENT 'Function and Skill files publishing commits, JSON' AFTER f_draft_revision,
  ADD COLUMN f_publish_request MEDIUMTEXT NOT NULL COMMENT 'Publish sent with its idempotency key, JSON; empty until sent' AFTER f_capabilities,
  ADD UNIQUE KEY uk_bkn_project_release_version (f_lineage_id, f_version);

-- Regressions were keyed by the network a release created; key them by that
-- release, and drop those no recorded release created, before the column is
-- narrowed to a release id.
UPDATE t_bkn_project_regression g
  JOIN t_bkn_project_release r ON r.f_network_id = g.f_network_id
  SET g.f_network_id = r.f_id;
DELETE FROM t_bkn_project_regression
  WHERE f_network_id NOT IN (SELECT f_id FROM t_bkn_project_release);

ALTER TABLE t_bkn_project_regression
  CHANGE COLUMN f_network_id f_release_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Release ID';

DROP TABLE IF EXISTS t_bkn_project_switch;
