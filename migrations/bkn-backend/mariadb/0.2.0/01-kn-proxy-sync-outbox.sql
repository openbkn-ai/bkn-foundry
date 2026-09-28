-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

USE openbkn;

ALTER TABLE t_kn_proxy_account
  ADD COLUMN IF NOT EXISTS f_published_generation BIGINT NOT NULL DEFAULT 0 AFTER f_sync_generation;

CREATE TABLE IF NOT EXISTS t_kn_proxy_planned_grant_source (
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
  INDEX idx_kn_proxy_planned_grant_source_kn (f_kn_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='Planned managed proxy grant snapshot';

CREATE TABLE IF NOT EXISTS t_kn_proxy_sync_outbox (
  f_id VARCHAR(40) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  f_kn_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  f_proxy_account_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  f_generation BIGINT NOT NULL,
  f_base_version VARCHAR(80) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
  f_target_version VARCHAR(80) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
  f_status VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'pending',
  f_attempt_count INT NOT NULL DEFAULT 0,
  f_next_retry_at BIGINT NOT NULL DEFAULT 0,
  f_lease_owner VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
  f_lease_until BIGINT NOT NULL DEFAULT 0,
  f_last_error VARCHAR(1024) NOT NULL DEFAULT '',
  f_payload LONGTEXT NOT NULL,
  f_created_at BIGINT NOT NULL DEFAULT 0,
  f_updated_at BIGINT NOT NULL DEFAULT 0,
  f_completed_at BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (f_id),
  UNIQUE KEY uk_kn_proxy_outbox_generation (f_kn_id, f_generation),
  INDEX idx_kn_proxy_outbox_ready (f_status, f_next_retry_at, f_created_at),
  INDEX idx_kn_proxy_outbox_cleanup (f_status, f_completed_at, f_id),
  INDEX idx_kn_proxy_outbox_proxy_status (f_proxy_account_id, f_status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='Durable managed proxy grant publication queue';

-- A stopped upgrade must repair non-ready mappings before this migration. Only
-- ready rows have a trustworthy published snapshot that can seed the plan.
UPDATE t_kn_proxy_account
SET f_published_generation = f_sync_generation
WHERE f_sync_status = 'ready';

INSERT IGNORE INTO t_kn_proxy_planned_grant_source (
  f_kn_id, f_binding_type, f_binding_id, f_resource_type, f_resource_id, f_operation,
  f_source_type, f_source_id, f_created_at, f_updated_at
)
SELECT p.f_kn_id, p.f_binding_type, p.f_binding_id, p.f_resource_type, p.f_resource_id, p.f_operation,
       p.f_source_type, p.f_source_id, p.f_created_at, p.f_updated_at
FROM t_kn_proxy_published_grant_source p
JOIN t_kn_proxy_account m ON m.f_kn_id = p.f_kn_id
WHERE m.f_sync_status = 'ready';

-- Rollback is permitted only before new outbox events exist:
-- DROP TABLE IF EXISTS t_kn_proxy_sync_outbox;
-- DROP TABLE IF EXISTS t_kn_proxy_planned_grant_source;
-- ALTER TABLE t_kn_proxy_account DROP COLUMN IF EXISTS f_published_generation;
