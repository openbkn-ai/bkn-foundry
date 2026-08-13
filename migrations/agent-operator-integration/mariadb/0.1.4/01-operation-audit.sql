-- Execution Factory management-operation audit facts.
-- Runtime tool/MCP/function/skill execution stays in BKN Trace, not this table.
USE openbkn;

CREATE TABLE IF NOT EXISTS `t_execution_factory_operation_audit` (
  `event_id` varchar(80) NOT NULL,
  `event_time` datetime(6) NOT NULL,
  `recorded_at` datetime(6) NOT NULL,
  `tenant_id` varchar(128) NOT NULL,
  `business_domain_id` varchar(128) NOT NULL,
  `actor_id` varchar(128) NOT NULL,
  `actor_name` varchar(256) NOT NULL,
  `actor_type` varchar(32) NOT NULL,
  `auth_method` varchar(32) NOT NULL,
  `request_id` varchar(160) NOT NULL,
  `source_channel` varchar(32) NOT NULL,
  `method` varchar(16) NOT NULL,
  `action` varchar(64) NOT NULL,
  `target_type` varchar(64) NOT NULL,
  `target_id` varchar(1024) NOT NULL,
  `target_name` varchar(1024) NOT NULL,
  `outcome` varchar(32) NOT NULL,
  `failure_code` varchar(128) NOT NULL DEFAULT '',
  `failure_message` varchar(512) NOT NULL DEFAULT '',
  PRIMARY KEY (`event_id`),
  KEY `idx_execution_audit_scope_time` (`tenant_id`,`business_domain_id`,`event_time`),
  KEY `idx_execution_audit_request` (`request_id`),
  KEY `idx_execution_audit_actor` (`actor_id`,`event_time`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='Execution Factory management operation audit';
