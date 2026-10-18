-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

-- 文件目录（fc-1）的内容版本与文件修订表（openbkn-ai/openbkn-ee#304）。表由 core 创建、社区版保持为空；
-- 写入方是企业版的 S3 兼容对象存储连接器。只建缺失的表，不修改已有表。
USE openbkn;

-- ==========================================
-- 12. t_file_catalog_content_version 文件目录内容版本表（fc-1 ContentVersion）
-- ==========================================
-- 内容版本 c-n 是文件目录在一次完整列举后的不可变快照；下游解析、理解都固定引用某个版本。
-- 版本内的文件清单不在本表复制，而由 t_file_catalog_file 的修订区间推出。
CREATE TABLE IF NOT EXISTS t_file_catalog_content_version (
    f_catalog_id              VARCHAR(40) NOT NULL COMMENT '文件目录所属 Catalog ID',
    f_version                 INT NOT NULL COMMENT '内容版本序号 n，对外表示为 c-n，按目录从 1 递增',
    f_parent_version          INT NOT NULL DEFAULT 0 COMMENT '上一内容版本序号；c-1 为 0',
    f_snapshot_digest         VARCHAR(40) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT 'fc-1 列举快照摘要 cvd_…；与最新版本相同的重扫不产生新版本',
    f_file_count              INT NOT NULL DEFAULT 0 COMMENT '该版本包含的文件数',
    f_added_count             INT NOT NULL DEFAULT 0 COMMENT '相对上一版本新增的文件数',
    f_changed_count           INT NOT NULL DEFAULT 0 COMMENT '相对上一版本内容变化的文件数',
    f_removed_count           INT NOT NULL DEFAULT 0 COMMENT '相对上一版本删除的文件数',
    f_discover_task_id        VARCHAR(40) NOT NULL DEFAULT '' COMMENT '产生该版本的资源发现任务 ID',
    f_create_time             BIGINT(20) NOT NULL DEFAULT 0 COMMENT '创建时间（Unix 毫秒时间戳）',
    PRIMARY KEY (f_catalog_id, f_version),
    INDEX idx_catalog_digest (f_catalog_id, f_snapshot_digest)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT='文件目录内容版本（fc-1 ContentVersion），不可变';

-- ==========================================
-- 13. t_file_catalog_file 文件目录文件修订表（fc-1 File）
-- ==========================================
-- 每行是一个对象的一次修订（object_key + etag/size/mtime），在内容版本区间 [f_from_version, f_to_version) 内有效，
-- f_to_version = 0 表示至今有效。版本 v 的文件清单为 f_from_version <= v AND (f_to_version = 0 OR f_to_version > v)。
CREATE TABLE IF NOT EXISTS t_file_catalog_file (
    f_catalog_id              VARCHAR(40) NOT NULL COMMENT '文件目录所属 Catalog ID',
    f_object_key_hash         CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT 'object_key 的 SHA-256 十六进制，用于主键与索引',
    f_from_version            INT NOT NULL COMMENT '该修订首次出现的内容版本序号',
    f_to_version              INT NOT NULL DEFAULT 0 COMMENT '该修订被新修订替换或文件被删除时的内容版本序号；0 表示至今有效',
    f_object_key              VARCHAR(2048) NOT NULL COMMENT '对象存储中的对象键',
    f_resource_id             VARCHAR(40) NOT NULL DEFAULT '' COMMENT '对应的 fileset 资源 ID',
    f_etag                    VARCHAR(256) NOT NULL DEFAULT '' COMMENT '对象 ETag，原样保存（含引号）',
    f_size                    BIGINT(20) NOT NULL DEFAULT 0 COMMENT '对象大小（字节）',
    f_mtime                   VARCHAR(40) NOT NULL DEFAULT '' COMMENT '对象修改时间（RFC 3339）',
    f_mime                    VARCHAR(255) NOT NULL DEFAULT '' COMMENT '对象 MIME 类型',
    f_support_status          VARCHAR(16) NOT NULL DEFAULT 'supported' COMMENT '处理状态: supported, excluded',
    f_exclude_reason          VARCHAR(32) NOT NULL DEFAULT '' COMMENT '排除原因: encrypted, corrupt, unsupported_format；supported 时为空',
    PRIMARY KEY (f_catalog_id, f_object_key_hash, f_from_version),
    INDEX idx_catalog_version_range (f_catalog_id, f_from_version, f_to_version)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_bin COMMENT='文件目录文件修订（fc-1 File），按内容版本区间有效';
