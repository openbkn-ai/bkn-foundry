-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

USE openbkn;

ALTER TABLE t_agent_network_setting
  MODIFY COLUMN f_kn_id VARCHAR(128) NOT NULL;
