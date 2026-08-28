ALTER TABLE bkn_trace_ee_provenance_analyses
    ADD COLUMN IF NOT EXISTS locale VARCHAR(16) NOT NULL DEFAULT 'zh-CN';
