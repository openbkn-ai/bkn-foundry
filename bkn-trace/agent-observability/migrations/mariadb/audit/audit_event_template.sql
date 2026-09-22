-- Audit monthly-table template, version v031.
-- Operators replace the literal YYYYMM token with a validated UTC month before
-- applying this file; application input is never interpolated into SQL.
CREATE TABLE IF NOT EXISTS bkn_audit.audit_event_YYYYMM (
  event_id VARCHAR(128) NOT NULL,
  source_id VARCHAR(128) NOT NULL,
  content_hash CHAR(71) NOT NULL,
  payload JSON NOT NULL,
  occurred_at DATETIME(6) NOT NULL,
  broker_received_at DATETIME(6) NOT NULL,
  recorded_at DATETIME(6) NOT NULL,
  PRIMARY KEY (event_id),
  KEY idx_audit_event_occurred (occurred_at, event_id),
  KEY idx_audit_event_source (source_id, occurred_at, event_id)
) ENGINE=InnoDB;
