-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

USE openbkn;

ALTER TABLE t_kn_proxy_account
  ADD COLUMN IF NOT EXISTS f_pending_model_version VARCHAR(80) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' AFTER f_synced_model_version,
  ADD COLUMN IF NOT EXISTS f_sync_generation BIGINT NOT NULL DEFAULT 0 AFTER f_pending_model_version,
  ADD COLUMN IF NOT EXISTS f_last_sync_started_at BIGINT NOT NULL DEFAULT 0 AFTER f_lock_until,
  ADD COLUMN IF NOT EXISTS f_last_sync_succeeded_at BIGINT NOT NULL DEFAULT 0 AFTER f_last_sync_started_at;

CREATE TABLE IF NOT EXISTS t_kn_proxy_published_grant_source (
  f_kn_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  f_binding_type VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  f_binding_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  f_resource_type VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  f_resource_id VARCHAR(256) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  f_operation VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  f_source_type VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  f_source_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  f_created_at BIGINT NOT NULL DEFAULT 0,
  f_updated_at BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (f_kn_id, f_binding_type, f_binding_id, f_resource_type, f_resource_id, f_operation),
  INDEX idx_kn_proxy_published_grant_source_kn (f_kn_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='Published managed proxy grant snapshot';
