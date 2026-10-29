-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

-- Requirement documents a project's owner uploaded: Markdown or plain text,
-- kept with the blocks they were split into, which drafted items quote. They
-- are project material and stay out of git.

USE openbkn;

CREATE TABLE IF NOT EXISTS t_bkn_project_requirement_document (
  f_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Document ID',
  f_project_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Project ID',
  f_name VARCHAR(255) NOT NULL DEFAULT '' COMMENT 'File name',
  f_doc_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT 'Document type: prd, policy, standard or other',
  f_format VARCHAR(20) NOT NULL DEFAULT '' COMMENT 'Format: markdown or text',
  f_content LONGTEXT NOT NULL COMMENT 'Text of the document, UTF-8',
  f_blocks LONGTEXT NOT NULL COMMENT 'Blocks the text was split into, JSON',
  f_sha256 CHAR(64) NOT NULL DEFAULT '' COMMENT 'SHA-256 of the content',
  f_size BIGINT(20) NOT NULL DEFAULT 0 COMMENT 'Size in bytes',
  f_uploader VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Account that uploaded it',
  f_uploader_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT 'Uploader account type',
  f_create_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT 'Create time',
  f_update_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT 'Update time',
  PRIMARY KEY (f_id),
  UNIQUE KEY uk_bkn_project_requirement_document_sha (f_project_id, f_sha256),
  KEY idx_bkn_project_requirement_document_project (f_project_id, f_create_time)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = 'BKN-project requirement documents';

-- Requirement specifications: the current specification of each project, rq-n,
-- drafted from its documents by bkn-agent and checked against them. Scenarios,
-- candidate concepts, scenario questions, business rules, conflicts and
-- modelling choices are kept together as one JSON document.

CREATE TABLE IF NOT EXISTS t_bkn_project_requirement (
  f_project_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Project ID',
  f_version INT NOT NULL DEFAULT 0 COMMENT 'Specification version: n of rq-n',
  f_status VARCHAR(20) NOT NULL DEFAULT '' COMMENT 'Status: drafting, ready or failed',
  f_spec LONGTEXT NOT NULL COMMENT 'The specification, JSON',
  f_inputs TEXT NOT NULL COMMENT 'What the specification was drafted from, JSON',
  f_error VARCHAR(64) NOT NULL DEFAULT '' COMMENT 'Why drafting failed, as a code',
  f_run_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Run ID: only this run records its outcome',
  f_agent_task_id VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'bkn-agent task of the drafting',
  f_drafter VARCHAR(40) NOT NULL DEFAULT '' COMMENT 'Account that started drafting',
  f_drafter_type VARCHAR(20) NOT NULL DEFAULT '' COMMENT 'Drafter account type',
  f_create_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT 'Create time',
  f_update_time BIGINT(20) NOT NULL DEFAULT 0 COMMENT 'Update time',
  PRIMARY KEY (f_project_id),
  KEY idx_bkn_project_requirement_status (f_status)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT = 'BKN-project requirement specifications';
