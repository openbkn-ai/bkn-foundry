-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

USE openbkn;

-- Retire the execution-factory operator resource. The service no longer reads
-- or writes operators, operator-converted tools, or operator built-in component
-- configs; the owner confirmed no deployment holds operator data, so remaining
-- rows are removed rather than migrated. Every statement can be re-run.

-- Tools converted from operators (source type "operator") have no runtime left.
DELETE FROM t_tool WHERE f_source_type = 'operator';

-- Built-in component configs only accept toolbox and mcp now.
DELETE FROM t_internal_component_config WHERE f_component_type = 'operator';

DROP TABLE IF EXISTS t_operator_release_history;
DROP TABLE IF EXISTS t_operator_release;
DROP TABLE IF EXISTS t_op_registry;
