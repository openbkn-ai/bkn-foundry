-- Copyright (c) 2026 OpenBKN
-- SPDX-License-Identifier: LicenseRef-OpenBKN
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.
--
-- Evidence-only migration manifest schema. Registration with the shared
-- sessionstore migration list is intentionally owned by Session 0.

CREATE TABLE IF NOT EXISTS bkn_trace_evidence_migration_manifests (
  manifest_id VARCHAR(128) NOT NULL,
  contract_sha CHAR(40) NOT NULL,
  state ENUM('draft','active','closed') NOT NULL,
  source_snapshot_at DATETIME(3) NOT NULL,
  entry_count BIGINT UNSIGNED NOT NULL,
  entries_digest CHAR(64) NOT NULL,
  closure_digest CHAR(64) NULL,
  terminal_counts_json LONGTEXT NULL,
  created_at DATETIME(6) NOT NULL,
  created_by VARCHAR(128) NOT NULL,
  activated_at DATETIME(6) NULL,
  activated_by VARCHAR(128) NULL,
  closed_at DATETIME(6) NULL,
  closed_by VARCHAR(128) NULL,
  row_version BIGINT UNSIGNED NOT NULL DEFAULT 1,
  PRIMARY KEY (manifest_id),
  CONSTRAINT chk_evidence_manifest_digest CHECK (entries_digest REGEXP '^[0-9a-f]{64}$'),
  CONSTRAINT chk_evidence_manifest_closed CHECK ((state <> 'closed') OR (closure_digest IS NOT NULL))
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS bkn_trace_evidence_migration_entries (
  entry_id VARCHAR(128) NOT NULL,
  manifest_id VARCHAR(128) NOT NULL,
  source_service VARCHAR(64) NOT NULL,
  source_table VARCHAR(128) NOT NULL,
  source_primary_key VARCHAR(255) NOT NULL,
  source_status VARCHAR(64) NOT NULL,
  classification ENUM('publish','verify_delivered','coverage_gap') NOT NULL,
  classification_reason VARCHAR(64) NOT NULL,
  event_id VARCHAR(128) NULL,
  payload_hash CHAR(64) NULL,
  producer_id VARCHAR(128) NULL,
  producer_stream_id VARCHAR(255) NULL,
  producer_epoch VARCHAR(20) NULL,
  producer_sequence VARCHAR(20) NULL,
  PRIMARY KEY (entry_id),
  UNIQUE KEY uq_evidence_manifest_entry (manifest_id, entry_id),
  UNIQUE KEY uq_evidence_manifest_source (manifest_id, source_table, source_primary_key),
  UNIQUE KEY uq_evidence_manifest_event (manifest_id, event_id),
  CONSTRAINT fk_evidence_entry_manifest FOREIGN KEY (manifest_id) REFERENCES bkn_trace_evidence_migration_manifests(manifest_id),
  CONSTRAINT chk_evidence_entry_identity CHECK (
    classification='coverage_gap' OR (event_id IS NOT NULL AND payload_hash IS NOT NULL AND producer_id IS NOT NULL AND producer_stream_id IS NOT NULL AND producer_epoch IS NOT NULL AND producer_sequence IS NOT NULL)
  )
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS bkn_trace_evidence_migration_results (
  manifest_id VARCHAR(128) NOT NULL,
  entry_id VARCHAR(128) NOT NULL,
  adjudication ENUM('ledger_committed','conflict','rejected','verified_delivered','coverage_gap') NOT NULL,
  first_observation VARCHAR(32) NOT NULL,
  last_observation VARCHAR(32) NOT NULL,
  reason_code VARCHAR(64) NULL,
  topic VARCHAR(255) NULL,
  partition_id INT NULL,
  offset_id BIGINT NULL,
  ledger_ingest_sequence BIGINT UNSIGNED NULL,
  first_observed_at DATETIME(6) NOT NULL,
  last_observed_at DATETIME(6) NOT NULL,
  attempts BIGINT UNSIGNED NOT NULL,
  PRIMARY KEY (manifest_id, entry_id),
  CONSTRAINT fk_evidence_result_entry FOREIGN KEY (manifest_id, entry_id) REFERENCES bkn_trace_evidence_migration_entries(manifest_id, entry_id),
  CONSTRAINT chk_evidence_result_coordinate CHECK ((topic IS NULL AND partition_id IS NULL AND offset_id IS NULL) OR (topic='openbkn.evidence.v1' AND partition_id >= 0 AND offset_id >= 0))
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS bkn_trace_evidence_migration_result_conflicts (
  conflict_id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  manifest_id VARCHAR(128) NOT NULL,
  entry_id VARCHAR(128) NOT NULL,
  existing_adjudication VARCHAR(32) NOT NULL,
  incoming_adjudication VARCHAR(32) NOT NULL,
  existing_reason_code VARCHAR(64) NOT NULL DEFAULT '',
  incoming_reason_code VARCHAR(64) NOT NULL DEFAULT '',
  detected_at DATETIME(6) NOT NULL,
  PRIMARY KEY (conflict_id),
  UNIQUE KEY uq_evidence_result_conflict (manifest_id, entry_id, existing_adjudication, incoming_adjudication, existing_reason_code, incoming_reason_code),
  CONSTRAINT fk_evidence_result_conflict_entry FOREIGN KEY (manifest_id, entry_id) REFERENCES bkn_trace_evidence_migration_entries(manifest_id, entry_id)
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS bkn_trace_evidence_migration_manifest_audit (
  audit_id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  manifest_id VARCHAR(128) NOT NULL,
  action VARCHAR(32) NOT NULL,
  actor VARCHAR(128) NOT NULL,
  before_digest CHAR(64) NULL,
  after_digest CHAR(64) NULL,
  occurred_at DATETIME(6) NOT NULL,
  PRIMARY KEY (audit_id),
  KEY idx_evidence_manifest_audit (manifest_id, occurred_at),
  CONSTRAINT fk_evidence_manifest_audit FOREIGN KEY (manifest_id) REFERENCES bkn_trace_evidence_migration_manifests(manifest_id)
) ENGINE=InnoDB;

DELIMITER $$
CREATE TRIGGER bkn_trace_evidence_manifest_lifecycle_guard
BEFORE UPDATE ON bkn_trace_evidence_migration_manifests FOR EACH ROW
BEGIN
  IF OLD.state = 'closed' THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='closed Evidence migration manifest is immutable';
  END IF;
  IF OLD.state = 'active' AND (
    NEW.state NOT IN ('active', 'closed') OR
    NEW.contract_sha <> OLD.contract_sha OR
    NEW.source_snapshot_at <> OLD.source_snapshot_at OR
    NEW.entry_count <> OLD.entry_count OR
    NEW.entries_digest <> OLD.entries_digest OR
    NEW.created_at <> OLD.created_at OR
    NEW.created_by <> OLD.created_by OR
    NOT (NEW.activated_at <=> OLD.activated_at) OR
    NOT (NEW.activated_by <=> OLD.activated_by) OR
    (NEW.state = 'active' AND (
      NOT (NEW.closure_digest <=> OLD.closure_digest) OR
      NOT (NEW.terminal_counts_json <=> OLD.terminal_counts_json) OR
      NOT (NEW.closed_at <=> OLD.closed_at) OR
      NOT (NEW.closed_by <=> OLD.closed_by)
    ))
  ) THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='active Evidence migration manifest core fields are immutable';
  END IF;
END$$
CREATE TRIGGER bkn_trace_evidence_manifest_draft_only_delete
BEFORE DELETE ON bkn_trace_evidence_migration_manifests FOR EACH ROW
BEGIN
  IF OLD.state <> 'draft' THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='active or closed Evidence migration manifest cannot be deleted';
  END IF;
END$$
CREATE TRIGGER bkn_trace_evidence_entries_draft_only_insert
BEFORE INSERT ON bkn_trace_evidence_migration_entries FOR EACH ROW
BEGIN
  IF (SELECT state FROM bkn_trace_evidence_migration_manifests WHERE manifest_id=NEW.manifest_id) <> 'draft' THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='active Evidence migration entries are immutable';
  END IF;
END$$
CREATE TRIGGER bkn_trace_evidence_entries_draft_only_update
BEFORE UPDATE ON bkn_trace_evidence_migration_entries FOR EACH ROW
BEGIN
  IF (SELECT state FROM bkn_trace_evidence_migration_manifests WHERE manifest_id=OLD.manifest_id) <> 'draft' THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='active Evidence migration entries are immutable';
  END IF;
END$$
CREATE TRIGGER bkn_trace_evidence_entries_draft_only_delete
BEFORE DELETE ON bkn_trace_evidence_migration_entries FOR EACH ROW
BEGIN
  IF (SELECT state FROM bkn_trace_evidence_migration_manifests WHERE manifest_id=OLD.manifest_id) <> 'draft' THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='active Evidence migration entries are immutable';
  END IF;
END$$
CREATE TRIGGER bkn_trace_evidence_results_open_only_insert
BEFORE INSERT ON bkn_trace_evidence_migration_results FOR EACH ROW
BEGIN
  IF (SELECT state FROM bkn_trace_evidence_migration_manifests WHERE manifest_id=NEW.manifest_id) = 'closed' THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='closed Evidence migration results are immutable';
  END IF;
END$$
CREATE TRIGGER bkn_trace_evidence_results_open_only_update
BEFORE UPDATE ON bkn_trace_evidence_migration_results FOR EACH ROW
BEGIN
  IF (SELECT state FROM bkn_trace_evidence_migration_manifests WHERE manifest_id=OLD.manifest_id) = 'closed' THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='closed Evidence migration results are immutable';
  END IF;
END$$
CREATE TRIGGER bkn_trace_evidence_results_open_only_delete
BEFORE DELETE ON bkn_trace_evidence_migration_results FOR EACH ROW
BEGIN
  IF (SELECT state FROM bkn_trace_evidence_migration_manifests WHERE manifest_id=OLD.manifest_id) = 'closed' THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='closed Evidence migration results are immutable';
  END IF;
END$$
DELIMITER ;
