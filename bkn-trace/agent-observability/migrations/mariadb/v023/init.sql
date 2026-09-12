-- Copyright (c) 2026 OpenBKN
-- SPDX-License-Identifier: LicenseRef-OpenBKN
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.
CREATE TABLE IF NOT EXISTS bkn_trace_ee_current_explanations (
 interaction_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 input_hash VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 algorithm VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 write_token VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 generated_at VARCHAR(40) CHARACTER SET ascii NOT NULL,
 view_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 view_json MEDIUMBLOB NOT NULL,
 PRIMARY KEY (interaction_id)
) ENGINE=InnoDB;
