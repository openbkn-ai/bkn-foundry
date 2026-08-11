-- Copyright 2026 openbkn.ai
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.

USE openbkn;

-- Unify the three Vega task state vocabularies:
-- pending/running/completed/failed/cancelled.
UPDATE t_build_task
SET f_status = 'pending'
WHERE f_status = 'init';

UPDATE t_semantic_understanding_task
SET f_status = 'completed'
WHERE f_status = 'succeeded';
