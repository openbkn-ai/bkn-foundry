-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

-- 资源增加语义理解给出的英文短名，可用作机器标识；只写一次，之后不再覆盖。
USE openbkn;

ALTER TABLE t_resource
    ADD COLUMN IF NOT EXISTS f_api_name VARCHAR(64) NOT NULL DEFAULT '' COMMENT '语义理解给出的英文短名（snake_case），只写一次' AFTER f_source_identifier;
