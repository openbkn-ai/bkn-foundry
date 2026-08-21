CREATE TABLE IF NOT EXISTS bkn_trace_ee_provenance_analyses (
    analysis_id VARCHAR(64) NOT NULL,
    interaction_id VARCHAR(64) NOT NULL,
    markdown_snapshot LONGTEXT NOT NULL,
    agent_id VARCHAR(128) NOT NULL,
    status VARCHAR(16) NOT NULL,
    result_payload LONGTEXT NULL,
    failure_code VARCHAR(64) NULL,
    failure_message VARCHAR(512) NULL,
    started_at DATETIME(6) NOT NULL,
    finished_at DATETIME(6) NULL,
    PRIMARY KEY (analysis_id),
    INDEX idx_provenance_analysis_interaction (interaction_id, started_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
