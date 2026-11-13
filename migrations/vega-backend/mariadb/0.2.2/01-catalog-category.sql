-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

USE openbkn;

-- 前置检查：以下查询必须返回零行后再执行首次升级。
-- 若有结果，先恢复对应的连接器类型注册及其真实分类；不得按名称猜测分类，
-- 也不得把无注册信息的 physical Catalog 当作 logical Catalog。
-- SELECT c.f_id, c.f_connector_type
-- FROM t_catalog AS c
-- LEFT JOIN t_connector_type AS ct ON ct.f_type = c.f_connector_type
-- WHERE c.f_type = 'physical'
--   AND (ct.f_type IS NULL OR ct.f_category = '');

ALTER TABLE t_catalog
    ADD COLUMN IF NOT EXISTS f_category VARCHAR(32) NOT NULL DEFAULT '' COMMENT '连接器资源分类，创建时确定且不可修改；logical 为空' AFTER f_connector_type;

-- 只补齐尚未保存分类的物理目录，使用注册元数据，重跑不覆盖任何已有分类。
-- logical 目录保持新增列的空值；缺少注册或注册分类为空的行不会被猜测回填。
UPDATE t_catalog AS c
JOIN t_connector_type AS ct ON ct.f_type = c.f_connector_type
SET c.f_category = ct.f_category
WHERE c.f_type = 'physical'
  AND c.f_category = ''
  AND ct.f_category <> '';

-- 上线前验证：下面查询必须返回零行，否则先修复注册并重跑本脚本。
-- SELECT f_id, f_connector_type FROM t_catalog
-- WHERE f_type = 'physical' AND f_category = '';
