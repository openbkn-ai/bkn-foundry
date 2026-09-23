-- Copyright (c) 2026 OpenBKN
-- SPDX-License-Identifier: LicenseRef-OpenBKN
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.
--
-- Audit schema namespace: v031. v026-v030 are reserved by the evidence and
-- policy workstreams and must not be reused by Audit.
CREATE DATABASE IF NOT EXISTS bkn_audit;

CREATE TABLE IF NOT EXISTS bkn_audit.audit_event_dedup (
  event_id VARCHAR(128) NOT NULL,
  content_hash CHAR(71) NOT NULL,
  first_recorded_at DATETIME(6) NOT NULL,
  target_table VARCHAR(32) NOT NULL,
  PRIMARY KEY (event_id),
  KEY idx_audit_event_dedup_target_table (target_table, first_recorded_at)
) ENGINE=InnoDB;
