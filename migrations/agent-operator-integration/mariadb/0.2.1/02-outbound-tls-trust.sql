-- Copyright openbkn.ai
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
USE openbkn;

CREATE TABLE IF NOT EXISTS `t_outbound_tls_trust` (
    `f_id` varchar(64) NOT NULL,
    `f_registration` varchar(256) NOT NULL,
    `f_origin` varchar(512) NOT NULL,
    `f_fingerprint` varchar(64) NOT NULL DEFAULT '',
    `f_skip_verify` int NOT NULL DEFAULT 0,
    PRIMARY KEY (`f_id`)
) ENGINE=InnoDB COMMENT='Outbound TLS trust scoped to registration and authority';
