-- Copyright (c) 2026 OpenBKN
-- SPDX-License-Identifier: LicenseRef-OpenBKN
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.
--
-- Audit Ledger Writer uses this global dedup table plus the identical monthly
-- audit_event_YYYYMM template provisioned by the database operator for the
-- current month and the next two UTC months. No producer outbox or DLQ exists.
CREATE DATABASE IF NOT EXISTS bkn_audit;

CREATE TABLE IF NOT EXISTS bkn_audit.audit_event_dedup (
  event_id VARCHAR(128) NOT NULL,
  content_hash CHAR(71) NOT NULL,
  first_recorded_at DATETIME(6) NOT NULL,
  target_table VARCHAR(32) NOT NULL,
  PRIMARY KEY (event_id),
  KEY idx_audit_event_dedup_target_table (target_table, first_recorded_at)
) ENGINE=InnoDB;

-- Monthly tables must be created from the same schema before their UTC month
-- becomes writable; external input is never interpolated into DDL or DML.
