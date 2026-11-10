-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

USE openbkn;

-- Cypher functions (#2670): a function tool whose script type is cypher runs one
-- read-only Cypher statement against one knowledge network. Python functions
-- leave both columns empty. Every statement can be re-run.
ALTER TABLE t_metadata_function
  ADD COLUMN IF NOT EXISTS f_kn_id varchar(128) NOT NULL DEFAULT '' COMMENT 'Cypher 函数查询的知识网络ID';
ALTER TABLE t_metadata_function
  ADD COLUMN IF NOT EXISTS f_result_shape varchar(16) NOT NULL DEFAULT '' COMMENT 'Cypher 函数返回形状：value / table';
