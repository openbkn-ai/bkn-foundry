-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

USE openbkn;

-- Projects are no longer archived: they are exported, imported and deleted
-- instead. Every archived project becomes a normal project again; its owner
-- deletes it if it is no longer wanted.
UPDATE t_bkn_project_project SET f_archived = 0 WHERE f_archived = 1;
