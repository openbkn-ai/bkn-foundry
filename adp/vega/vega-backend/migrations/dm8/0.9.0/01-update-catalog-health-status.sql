-- Split catalog enabled state from health check status.
UPDATE t_catalog
SET f_enabled = 0,
    f_health_check_status = 'unchecked'
WHERE f_health_check_status = 'disabled';

ALTER TABLE t_catalog
    MODIFY f_health_check_status VARCHAR(20 CHAR) NOT NULL DEFAULT 'unchecked';
