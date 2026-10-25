-- Copyright (c) 2026 OpenBKN
-- SPDX-License-Identifier: LicenseRef-OpenBKN
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.
CREATE TABLE IF NOT EXISTS bkn_trace_record_integrity_retries (
 interaction_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 source_version BIGINT UNSIGNED NOT NULL,
 retry_status VARCHAR(24) NOT NULL,
 attempts INT NOT NULL,
 first_attempt_at DATETIME(6) NOT NULL,
 last_attempt_at DATETIME(6) NOT NULL,
 next_attempt_at DATETIME(6) NOT NULL,
 lease_until DATETIME(6) NOT NULL,
 lease_token VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 last_failure_reason VARCHAR(64) NOT NULL DEFAULT '',
 reason VARCHAR(64) NOT NULL DEFAULT '',
 PRIMARY KEY(interaction_id,source_version)
) ENGINE=InnoDB;
