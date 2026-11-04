-- Copyright openbkn.ai
--
-- Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

USE openbkn;

-- Studio smart QA, Cypher generation, graph explorer and the object metric
-- designer were seeded as drafts, so the features built on them answered
-- "not found or not published" until an administrator published each one.
-- Built-in Agents cannot be unpublished through the API, so a draft row here
-- is always the untouched seed. Publish it; the empty model already falls back
-- to the platform default model. Other settings stay as they are.
UPDATE t_agent
SET f_status = 'published'
WHERE f_agent_id IN ('studio_smart_qa', 'cypher_generation', 'graph_explorer', 'object_metric_designer')
  AND f_status = 'draft';
