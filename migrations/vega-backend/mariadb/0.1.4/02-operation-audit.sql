-- Copyright 2026 openbkn.ai
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.

USE openbkn;

-- Bounded Vega data-resource management audit facts. Request bodies, connector
-- configurations, credentials and execution results are intentionally excluded.
CREATE TABLE IF NOT EXISTS t_vega_operation_audit (
  event_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  event_time DATETIME(6) NOT NULL,
  recorded_at DATETIME(6) NOT NULL,
  tenant_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  business_domain_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  actor_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  actor_name VARCHAR(255) NOT NULL,
  actor_type VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  auth_method VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  request_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  source_channel VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  method VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  action VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  target_type VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  target_id VARCHAR(1024) NOT NULL,
  target_name VARCHAR(1024) NOT NULL,
  outcome VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  failure_code VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
  failure_message VARCHAR(512) NOT NULL DEFAULT '',
  PRIMARY KEY (event_id),
  INDEX idx_vega_audit_tenant_time (tenant_id, event_time, event_id),
  INDEX idx_vega_audit_domain_time (business_domain_id, event_time, event_id),
  INDEX idx_vega_audit_actor_time (actor_id, event_time, event_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='Vega data-resource management operation audit facts';
