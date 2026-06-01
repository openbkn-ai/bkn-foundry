USE adp;

CREATE TABLE IF NOT EXISTS `data_source` (
    `id` char(36) NOT NULL COMMENT '主键，生成规则:36位uuid',
    `name` varchar(128) NOT NULL COMMENT '数据源展示名称',
    `type_name` varchar(30) NOT NULL COMMENT '数据库类型',
    `bin_data` blob NOT NULL COMMENT '数据源配置信息',
    `comment` varchar(255) DEFAULT NULL COMMENT '描述',
    `created_by_uid` char(36) NOT NULL COMMENT '创建人',
    `created_at` datetime(3) NOT NULL COMMENT '创建时间',
    `updated_by_uid` char(36) DEFAULT NULL COMMENT '修改人',
    `updated_at` datetime(3) DEFAULT NULL COMMENT '更新时间',
    PRIMARY KEY (`id`)
    );

CREATE TABLE IF NOT EXISTS `t_data_source_info` (
      `f_id` varchar(36) NOT NULL COMMENT '主键，生成规则:36位uuid',
      `f_name` varchar(128) NOT NULL COMMENT '数据源展示名称',
      `f_type` varchar(30) NOT NULL COMMENT '数据库类型',
      `f_catalog` varchar(50) COMMENT '数据源catalog名称',
      `f_database` varchar(100) COMMENT '数据库名称',
      `f_schema` varchar(100) COMMENT '数据库模式',
      `f_connect_protocol` varchar(30) NOT NULL COMMENT '连接方式',
      `f_host` varchar(128) NOT NULL COMMENT '地址',
      `f_port` int NOT NULL COMMENT '端口',
      `f_account` varchar(128) COMMENT '账户',
      `f_password` varchar(1024) COMMENT '密码',
      `f_storage_protocol` varchar(30) COMMENT '存储介质',
      `f_storage_base` varchar(1024) COMMENT '存储路径',
      `f_token` varchar(100) COMMENT 'token认证',
      `f_replica_set` varchar(100) COMMENT '副本集名称',
      `f_is_built_in` tinyint NOT NULL DEFAULT '0' COMMENT '是否为内置数据源（0 特殊 1 非内置 2 内置），默认为0',
      `f_comment` varchar(255) COMMENT '描述',
      `f_created_by_uid` varchar(36) COMMENT '创建人',
      `f_created_at` datetime(3) COMMENT '创建时间',
      `f_updated_by_uid` varchar(36) COMMENT '修改人',
      `f_updated_at` datetime(3) COMMENT '更新时间',
      PRIMARY KEY (`f_id`)
);

INSERT INTO `t_data_source_info` (`f_id`,`f_name`,`f_type`,`f_connect_protocol`,`f_host`,`f_port`,`f_is_built_in`,`f_created_at`,`f_updated_at`)
SELECT 'cedb5294-07c3-45b1-a273-17baefa62800','索引库','index_base','http','mdl-index-base-svc',13013,2,current_timestamp(),current_timestamp()
FROM DUAL WHERE NOT EXISTS( SELECT `f_id` FROM `t_data_source_info` WHERE `f_id` = 'cedb5294-07c3-45b1-a273-17baefa62800' AND `f_type` = 'index_base');

CREATE TABLE IF NOT EXISTS `t_task_scan` (
  `id` char(36) NOT NULL COMMENT '唯一id，雪花算法',
  `type` tinyint NOT NULL DEFAULT 0 COMMENT '扫描任务：0 :即时-数据源;1 :即时-数据表;2: 定时-数据源;3: 定时-数据表',
  `name` varchar(128) NOT NULL COMMENT '任务名称',
  `ds_id` char(36) DEFAULT NULL COMMENT '数据源唯一标识',
  `scan_status` tinyint DEFAULT NULL COMMENT '任务状态',
  `start_time` datetime NOT NULL DEFAULT current_timestamp() COMMENT '任务开始时间',
  `end_time` datetime NOT NULL DEFAULT current_timestamp() COMMENT '任务结束时间',
  `create_user` varchar(100) NOT NULL DEFAULT '' COMMENT '创建用户（ID），默认空字符串',
  `task_params_info` text DEFAULT NULL COMMENT '任务执行参数信息',
  `task_process_info` text DEFAULT NULL COMMENT '任务执行进度信息',
  `task_result_info` text DEFAULT NULL COMMENT '任务执行结果信息',
  PRIMARY KEY (`id`),
  KEY `t_task_scan_ds_id_IDX` (`ds_id`)
);

CREATE TABLE IF NOT EXISTS `t_task_scan_table` (
  `id` char(36) NOT NULL COMMENT '唯一id，雪花算法',
  `task_id` char(36) NOT NULL COMMENT '关联任务id',
  `ds_id` char(36) NOT NULL COMMENT '数据源唯一标识',
  `ds_name` varchar(128) NOT NULL COMMENT '数据源名称',
  `table_id` char(36) NOT NULL COMMENT 'table的唯一id',
  `table_name` varchar(128) NOT NULL COMMENT 'table的name',
  `schema_name` varchar(128) NOT NULL COMMENT 'schema的name',
  `scan_status` tinyint DEFAULT NULL COMMENT '任务状态：0 等待;1 进行中;2 成功;3 失败',
  `start_time` datetime NOT NULL DEFAULT current_timestamp() COMMENT '任务开始时间',
  `end_time` datetime  DEFAULT NULL COMMENT '任务结束时间',
  `create_user` varchar(100) NOT NULL DEFAULT '' COMMENT '创建用户（ID），默认空字符串',
  `scan_params` text DEFAULT NULL COMMENT '任务执行参数信息',
  `scan_result_info` text DEFAULT NULL COMMENT '任务执行结果：',
  `error_stack` text DEFAULT NULL COMMENT '异常堆栈信息',
  PRIMARY KEY (`id`),
  KEY `t_task_scan_table_task_id_IDX` (`task_id`),
  KEY `t_task_scan_table_table_id_IDX` (`table_id`)
);

CREATE TABLE IF NOT EXISTS `t_table_scan` (
  `f_id` char(36) NOT NULL COMMENT '唯一id，雪花算法',
  `f_name` varchar(128) NOT NULL COMMENT '表名称',
  `f_advanced_params` text DEFAULT NULL COMMENT '高级参数，格式为"{key(1): value(1), ... , key(n): value(n)}"',
  `f_description` varchar(2048) DEFAULT NULL COMMENT '表注释',
  `f_table_rows` bigint NOT NULL DEFAULT 0 COMMENT '表数据量，默认0',
  `f_data_source_id` char(36) NOT NULL COMMENT '数据源唯一标识',
  `f_data_source_name` varchar(128) NOT NULL COMMENT '冗余字段，数据源名称',
  `f_schema_name` varchar(128) NOT NULL COMMENT '冗余字段，schema名称',
  `f_task_id` char(36) NOT NULL COMMENT '关联任务id',
  `f_version` int NOT NULL DEFAULT 1 COMMENT '版本号',
  `f_create_time` datetime NOT NULL DEFAULT current_timestamp() COMMENT '创建时间',
  `f_create_user` varchar(100) NOT NULL DEFAULT '' COMMENT '创建用户（ID），默认空字符串',
  `f_operation_time` datetime NOT NULL DEFAULT current_timestamp() COMMENT '修改时间，默认当前时间',
  `f_operation_user` varchar(100) NOT NULL DEFAULT '' COMMENT '修改用户（ID），默认空字符串',
  `f_operation_type` tinyint NOT NULL DEFAULT 0 COMMENT '状态：0新增1删除2更新',
  `f_status` tinyint NOT NULL DEFAULT 3 COMMENT '任务状态：0 成功1 失败2 进行中 3 初始化',
  `f_status_change` tinyint NOT NULL DEFAULT 0 COMMENT '状态是否发生变化：0 否1 是',
  `f_scan_source` tinyint(4) DEFAULT NULL COMMENT '扫描来源',
  PRIMARY KEY (`f_id`),
  KEY `t_table_scan_f_data_source_id_IDX` (`f_task_id`)
);

CREATE TABLE IF NOT EXISTS `t_table_field_scan` (
  `f_id` char(36) NOT NULL COMMENT '唯一id，雪花算法',
  `f_field_name` varchar(128) NOT NULL COMMENT '字段名',
  `f_table_id` char(36) NOT NULL COMMENT 'Table唯一标识',
  `f_table_name` varchar(128) NOT NULL COMMENT '表名',
  `f_field_type` varchar(128) DEFAULT NULL COMMENT '字段类型',
  `f_field_length` int DEFAULT NULL COMMENT '字段长度',
  `f_field_precision` int DEFAULT NULL COMMENT '字段精度',
  `f_field_comment` varchar(2048) DEFAULT NULL COMMENT '字段注释',
  `f_field_order_no` int DEFAULT NULL,
  `f_advanced_params` varchar(2048) DEFAULT NULL COMMENT '字段高级参数',
  `f_version` int NOT NULL DEFAULT 1 COMMENT '版本号',
  `f_create_time` datetime NOT NULL DEFAULT current_timestamp() COMMENT '创建时间',
  `f_create_user` varchar(100) NOT NULL DEFAULT '' COMMENT '创建用户（ID），默认空字符串',
  `f_operation_time` datetime NOT NULL DEFAULT current_timestamp() COMMENT '修改时间，默认当前时间',
  `f_operation_user` varchar(100) NOT NULL DEFAULT '' COMMENT '修改用户（ID），默认空字符串',
  `f_operation_type` tinyint NOT NULL DEFAULT 0 COMMENT '状态：0新增1删除2更新',
  `f_status_change` tinyint NOT NULL DEFAULT 0 COMMENT '状态是否发生变化：0 否1 是',
  PRIMARY KEY (`f_id`),
  KEY `t_table_field_scan_f_table_id_IDX` (`f_table_id`)
);