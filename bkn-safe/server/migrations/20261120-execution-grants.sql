-- Copyright openbkn.ai
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

-- Back up bkn-safe before applying this additive schema. bkn-safe also creates
-- the table at startup; applying it by hand first is safe and idempotent. No
-- existing identity, credential or grant is changed.
USE safe;

CREATE TABLE IF NOT EXISTS execution_grants (
    id varchar(64) PRIMARY KEY,
    project_id varchar(128) NOT NULL,
    run_id varchar(128) NOT NULL,
    delegator_id varchar(64),
    kn_id varchar(128),
    agent_id varchar(128),
    secret_hash varchar(64),
    expires_at datetime(3),
    status varchar(16) NOT NULL,
    created_at datetime(3),
    updated_at datetime(3),
    UNIQUE KEY uidx_execution_grant_run (project_id, run_id),
    KEY idx_execution_grants_expires_at (expires_at),
    KEY idx_execution_grants_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
