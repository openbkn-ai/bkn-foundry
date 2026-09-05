-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

-- Environment-local one-to-one mapping and publication synchronization state.
CREATE TABLE IF NOT EXISTS t_kn_proxy_account (
  f_kn_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  f_proxy_account_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  f_proxy_account_type VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'app',
  f_lifecycle_status VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'active',
  f_version BIGINT NOT NULL DEFAULT 1,
  f_sync_status VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'pending',
  f_published_model_version VARCHAR(80) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
  f_synced_model_version VARCHAR(80) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
  f_last_sync_error VARCHAR(1024) NOT NULL DEFAULT '',
  f_last_grantor_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
  f_lock_owner VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
  f_lock_until BIGINT NOT NULL DEFAULT 0,
  f_created_at BIGINT NOT NULL DEFAULT 0,
  f_updated_at BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (f_kn_id),
  UNIQUE KEY uk_kn_proxy_account_proxy (f_proxy_account_id),
  INDEX idx_kn_proxy_sync (f_sync_status, f_updated_at),
  INDEX idx_kn_proxy_lifecycle (f_lifecycle_status, f_updated_at),
  INDEX idx_kn_proxy_lock (f_lock_until)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='Knowledge network managed proxy mapping';

-- Rollback:
-- DROP TABLE IF EXISTS t_kn_proxy_account;
