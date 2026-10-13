-- Copyright openbkn.ai
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.
-- Upgrade existing sandbox databases before deploying the result-storage fix (#2029).
USE openbkn;
ALTER TABLE t_sandbox_execution
    MODIFY COLUMN f_stdout MEDIUMTEXT NOT NULL,
    MODIFY COLUMN f_stderr MEDIUMTEXT NOT NULL,
    MODIFY COLUMN f_return_value MEDIUMTEXT NOT NULL;
