-- Copyright openbkn.ai
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

USE openbkn;

-- Process references only. Runtime model bodies, quotes, extracted instances,
-- user tokens and service credentials must never enter this journal.
CREATE TABLE IF NOT EXISTS t_bkn_project_kb_publication (
 f_network_id VARCHAR(128) NOT NULL COMMENT 'Target released knowledge network',
 f_project_id VARCHAR(40) NOT NULL COMMENT 'Origin project; record survives its archive',
 f_generation_id VARCHAR(128) NOT NULL COMMENT 'Dataset generation fenced at publication',
 f_state VARCHAR(24) NOT NULL COMMENT 'Publication process state',
 f_revision BIGINT NOT NULL COMMENT 'Publication process revision',
 f_data LONGTEXT NOT NULL COMMENT 'Private model references, source decisions and holds only, JSON',
 f_create_time BIGINT NOT NULL COMMENT 'Created at, milliseconds',
 f_update_time BIGINT NOT NULL COMMENT 'Updated at, milliseconds',
 PRIMARY KEY (f_network_id),
 KEY idx_bkn_project_kb_publication_generation (f_project_id,f_generation_id,f_state)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='BKN-project released document model process';
