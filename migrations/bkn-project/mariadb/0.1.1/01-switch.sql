-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

-- Consumer switches of a release lineage: each row moves the lineage's
-- consumers, and the schedules bkn-project can move, from one network to
-- another. The latest row names the network in use; a rollback is a switch
-- back to an earlier network.

USE openbkn;

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
