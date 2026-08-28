ALTER TABLE bkn_trace_ee_provenance_analyses
    ADD COLUMN locale VARCHAR(16) NOT NULL DEFAULT 'zh-CN' AFTER agent_id;
