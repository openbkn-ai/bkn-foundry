-- Copyright openbkn.ai
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

-- Back up bkn-safe before applying this additive schema. Apply manually before
-- rolling out the build-delegation API; no existing identity or grant is changed.
USE safe;

CREATE TABLE IF NOT EXISTS build_grants (
    id varchar(64) PRIMARY KEY,
    project_id varchar(128) NOT NULL,
    build_id varchar(128) NOT NULL,
    principal_id varchar(64),
    delegator_id varchar(64),
    model_fingerprint varchar(64),
    manifest_hash varchar(64),
    revision bigint unsigned NOT NULL,
    status varchar(16) NOT NULL,
    created_at datetime(3),
    updated_at datetime(3),
    UNIQUE KEY uidx_build_grant_job (project_id, build_id),
    KEY idx_build_grants_principal_id (principal_id),
    KEY idx_build_grants_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS build_grant_versions (
    grant_id varchar(64) NOT NULL,
    knowledge_base_id varchar(128) NOT NULL,
    file_id varchar(128) NOT NULL,
    content_version_id varchar(128) NOT NULL,
    PRIMARY KEY (grant_id, knowledge_base_id, file_id, content_version_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
