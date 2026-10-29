-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

-- The language a project names its ontology items in. Empty for projects made
-- before it, which read as zh-CN.

USE openbkn;

ALTER TABLE t_bkn_project_project
  ADD COLUMN IF NOT EXISTS f_naming_language VARCHAR(16) NOT NULL DEFAULT '' COMMENT 'Naming language: zh-CN or en-US; empty reads as zh-CN' AFTER f_catalog_ids;
