-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

USE openbkn;

ALTER TABLE t_bkn_project_release
  MODIFY COLUMN f_lineage_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT 'Lineage ID: the base knowledge network ID',
  MODIFY COLUMN f_network_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT 'Knowledge network ID this release was imported as';

ALTER TABLE t_bkn_project_switch
  MODIFY COLUMN f_lineage_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT 'Lineage ID',
  MODIFY COLUMN f_from_network_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT 'Knowledge network ID switched away from',
  MODIFY COLUMN f_to_network_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT 'Knowledge network ID switched to';

ALTER TABLE t_bkn_project_project
  MODIFY COLUMN f_kn_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT 'Knowledge network ID: the lineage ID',
  MODIFY COLUMN f_baseline_network_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT 'Live knowledge network when the project started';
