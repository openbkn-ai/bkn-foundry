-- Copyright openbkn.ai
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

USE openbkn;

-- Build process only. Extracted rows, quotes, Agent outputs and user credentials
-- belong to their existing services and must never enter this journal.
CREATE TABLE IF NOT EXISTS t_bkn_project_kb_build (
 f_id VARCHAR(40) NOT NULL COMMENT 'Build operation UUID',
 f_project_id VARCHAR(40) NOT NULL COMMENT 'Owning project',
 f_state VARCHAR(24) NOT NULL COMMENT 'Build process state',
 f_revision BIGINT NOT NULL DEFAULT 1 COMMENT 'Optimistic process revision',
 f_input_hash CHAR(64) NOT NULL COMMENT 'Immutable confirmed input signature',
 f_data LONGTEXT NOT NULL COMMENT 'Exact source identities and process metrics, JSON',
 f_identity LONGTEXT NOT NULL COMMENT 'Private scoped delegation identifiers, JSON; no credentials',
 f_lease_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT 'Worker lease identity',
 f_lease_until BIGINT NOT NULL DEFAULT 0 COMMENT 'Lease expiry, milliseconds',
 f_create_time BIGINT NOT NULL COMMENT 'Created at, milliseconds',
 f_update_time BIGINT NOT NULL COMMENT 'Updated at, milliseconds',
 PRIMARY KEY (f_id),
 KEY idx_bkn_project_kb_build_project (f_project_id,f_create_time),
 KEY idx_bkn_project_kb_build_queue (f_state,f_lease_until)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='BKN-project document build process';

-- Human decisions point to immutable annotations in Vega. No annotation body,
-- source quote or prediction is stored in this process table.
CREATE TABLE IF NOT EXISTS t_bkn_project_kb_reference (
 f_id VARCHAR(40) NOT NULL COMMENT 'Confirmed model and source scope signature',
 f_project_id VARCHAR(40) NOT NULL COMMENT 'Owning project',
 f_revision BIGINT NOT NULL COMMENT 'Optimistic review revision',
 f_data LONGTEXT NOT NULL COMMENT 'Review decisions and Dataset references only, JSON',
 f_update_time BIGINT NOT NULL COMMENT 'Updated at, milliseconds',
 PRIMARY KEY (f_id),
 KEY idx_bkn_project_kb_reference_project (f_project_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='BKN-project acceptance review process';
