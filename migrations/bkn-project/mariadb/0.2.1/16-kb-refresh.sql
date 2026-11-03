-- Copyright openbkn.ai
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

USE openbkn;

-- One durable process journal and write lease per release lineage. No model,
-- extracted rows, source text, Agent outputs or credentials are stored here.
CREATE TABLE IF NOT EXISTS t_bkn_project_kb_refresh (
 f_lineage_id VARCHAR(40) NOT NULL COMMENT 'Release lineage; serializes shared Dataset writes',
 f_network_id VARCHAR(128) NOT NULL COMMENT 'Network selected by the latest explicit switch',
 f_state VARCHAR(24) NOT NULL COMMENT 'Refresh process state',
 f_revision BIGINT NOT NULL COMMENT 'Process revision',
 f_data LONGTEXT NOT NULL COMMENT 'Manifests, source decisions and task receipts only, JSON',
 f_lease_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT 'Current worker identity',
 f_lease_until BIGINT NOT NULL DEFAULT 0 COMMENT 'Lease expiry, milliseconds',
 f_next_check BIGINT NOT NULL DEFAULT 0 COMMENT 'Next automatic source check, milliseconds',
 f_update_time BIGINT NOT NULL COMMENT 'Updated at, milliseconds',
 PRIMARY KEY (f_lineage_id),
 KEY idx_bkn_project_kb_refresh_due (f_state,f_next_check)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='BKN-project published document refresh process';
