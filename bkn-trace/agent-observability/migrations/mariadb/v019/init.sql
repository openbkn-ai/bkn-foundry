CREATE TABLE IF NOT EXISTS bkn_trace_ee_historical_provenance_projections (
    interaction_id VARCHAR(64) NOT NULL,
    facts_hash CHAR(64) NOT NULL,
    tenant_id VARCHAR(64) NOT NULL,
    business_domain_id VARCHAR(64) NOT NULL,
    resolver_version VARCHAR(64) NOT NULL,
    resolved_at DATETIME(6) NULL,
    status VARCHAR(16) NOT NULL,
    graph_payload LONGTEXT NULL,
    markdown_snapshot LONGTEXT NULL,
    content_hash CHAR(64) NULL,
    failure_code VARCHAR(64) NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (interaction_id, facts_hash),
    UNIQUE KEY uq_provenance_projection_interaction (interaction_id),
    UNIQUE KEY uq_provenance_projection_interaction_facts (interaction_id, facts_hash),
    INDEX idx_provenance_projection_scope (tenant_id, business_domain_id, interaction_id),
    INDEX idx_provenance_projection_status (status, updated_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS bkn_trace_ee_historical_provenance_tombstones (
    interaction_id VARCHAR(64) NOT NULL,
    deleted_at DATETIME(6) NOT NULL,
    PRIMARY KEY (interaction_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
