-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

-- IDs of a project's items drafted from physical names that are not ASCII: the
-- English short name semantic understanding gave, or a short-hash placeholder
-- when it gave none. Every later draft of the project reuses the registered ID,
-- so it does not change; a placeholder may be replaced until a confirmation
-- locks it.

USE openbkn;

CREATE TABLE IF NOT EXISTS t_bkn_project_id (
  f_project_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Project ID',
  f_kind VARCHAR(20) NOT NULL DEFAULT '' COMMENT 'Kind: object_types, value_types, relation_types or properties',
  f_source_key VARCHAR(255) NOT NULL DEFAULT '' COMMENT 'Resource ID, or resource ID and field name for a property',
  f_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Registered ID',
  f_origin VARCHAR(20) NOT NULL DEFAULT '' COMMENT 'Origin: semantic or fallback',
  f_basis TEXT NOT NULL COMMENT 'What the ID was made from, JSON',
  f_locked TINYINT(1) NOT NULL DEFAULT 0 COMMENT 'Locked by a confirmation; never replaced again',
  f_create_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT 'Create time',
  f_update_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT 'Update time',
  PRIMARY KEY (f_project_id, f_kind, f_source_key)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = 'BKN-project IDs of items drafted from non-ASCII physical names';
