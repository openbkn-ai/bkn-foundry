-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

USE openbkn;

-- Process metadata only: source decisions, immutable file/version identities,
-- sample roles and anonymous retention intents. No extracted models or rows.
CREATE TABLE IF NOT EXISTS t_bkn_project_kb_selection (
 f_id VARCHAR(40) NOT NULL COMMENT 'Caller-generated operation UUID',
 f_project_id VARCHAR(40) NOT NULL COMMENT 'Owning project',
 f_state VARCHAR(24) NOT NULL COMMENT 'pending, active, retained, release_pending, released',
 f_current TINYINT NOT NULL DEFAULT 0 COMMENT 'Current source decision',
 f_retained TINYINT NOT NULL DEFAULT 0 COMMENT 'Referenced by a published version',
 f_revision BIGINT NOT NULL DEFAULT 1 COMMENT 'Optimistic process revision',
 f_expected_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Selection this operation replaces',
 f_input_hash CHAR(64) NOT NULL COMMENT 'Canonical request signature without credentials',
 f_data LONGTEXT NOT NULL COMMENT 'Source decisions and file/version/sample metadata, JSON',
 f_holds LONGTEXT NOT NULL COMMENT 'Private retention intents and release outcomes, JSON',
 f_create_time BIGINT NOT NULL COMMENT 'Created at, milliseconds',
 f_update_time BIGINT NOT NULL COMMENT 'Updated at, milliseconds',
 PRIMARY KEY (f_id),
 KEY idx_bkn_project_kb_current (f_project_id,f_current),
 KEY idx_bkn_project_kb_state (f_project_id,f_state)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='BKN-project source selection process';
