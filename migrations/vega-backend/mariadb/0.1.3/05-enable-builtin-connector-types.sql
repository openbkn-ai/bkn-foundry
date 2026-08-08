-- Copyright 2026 openbkn.ai
-- Copyright The kweaver.ai Authors.
--
-- Licensed under the Apache License, Version 2.0.

-- Turn the built-in connector types back on, once.
--
-- Every INSERT that seeds a built-in type is guarded by
-- "WHERE NOT EXISTS (... f_type = ...)", so it creates and never corrects. That
-- is right for what an operator configures, and wrong for what the product
-- ships: a row created once with f_enabled = 0 stays off through every upgrade,
-- and no migration can reach it. It happens easily — POST /connector-types
-- takes f_enabled straight from the request body, so a caller that simply omits
-- the field registers a built-in type disabled, permanently.
--
-- The symptom is a connector the product clearly supports showing up greyed out
-- as "unavailable", with the implementation compiled in and the licence in
-- order. Nothing in the logs says why.
--
-- Runs ONCE, which is the whole design. Deliberately disabling a connector has
-- to keep working, so this must not be turned into a rule that re-asserts
-- itself on every upgrade — that would take the switch away from the operator
-- rather than fix it being stuck.
--
-- Scoped to the types this product seeds itself — the exact list the INSERTs in
-- 0.1.0/init.sql and 0.1.3/{init,04-add-sqlserver-connector}.sql create. A type
-- an operator registered is theirs, including whether it is on.
--
-- oracle is deliberately absent: the connector exists in the code but no
-- migration seeds a row for it, so there is nothing here to correct.
USE openbkn;

UPDATE t_connector_type
   SET f_enabled = TRUE
 WHERE f_type IN ('mysql', 'mariadb', 'postgresql', 'sqlserver',
                  'opensearch', 'anyshare')
   AND f_enabled = FALSE;
