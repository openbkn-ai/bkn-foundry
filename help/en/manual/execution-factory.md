# 🛠️ Execution Factory

## 📖 Overview

The **Execution Factory** registers and runs **toolbox tools**, **MCP servers**, and **skills** that agents invoke under policy. It bridges LLM planning to concrete business actions (HTTP tools, code runners, MCP integrations, etc.).

Typical ingress prefix:

| Prefix | Role |
| --- | --- |
| `/api/agent-operator-integration/v1` | Toolbox, MCP, and Skill management and tool execution surface |

## CLI

### Toolboxes and Tools

A toolbox groups tools into one publishable box. An `openapi` box proxies its tools to a service; a `function` box holds platform functions. A tool can be executed only when it is enabled and its toolbox is published.

```bash
# List toolboxes (the id is `box_id`)
openbkn toolbox list

# List the tools in a toolbox
openbkn tool list --toolbox <box_id>

# Inspect one tool: metadata, parameters, usage rule
openbkn tool get <tool_id> --toolbox <box_id>

# Execute an enabled tool in a published toolbox
openbkn tool execute <tool_id> --toolbox <box_id> \
  --body '{"city": "Shanghai", "units": "metric"}'
```

The `openbkn call` command invokes any Execution Factory API by path, acting as a lightweight HTTP client wired into the platform auth and routing:

```bash
openbkn call GET /api/agent-operator-integration/v1/tool-box/list
```

### Skill Management

Skills are packaged bundles of tools, prompts, and configuration that can be registered, discovered from the marketplace, and installed into your workspace.

```bash
# List installed skills
openbkn skill list

# Search the skill marketplace
openbkn skill market
openbkn skill market --query "data analysis"

# Register a new skill from a local SKILL.md
openbkn skill register ./my-skill/SKILL.md

# View skill content (metadata and tool definitions)
openbkn skill content <skill_id>

# Read a file within a skill package
openbkn skill read-file <skill_id> SKILL.md
openbkn skill read-file <skill_id> tools/analyze.py

# Install a skill from the marketplace
openbkn skill install <skill_id>
openbkn skill install <skill_id> --version 1.2.0
```

### End-to-End Example

```bash
# 1. Browse the marketplace for a useful skill
openbkn skill market --query "knowledge graph"

# 2. Install a skill
openbkn skill install skill-kg-analyzer

# 3. Verify it appears in local skills
openbkn skill list

# 4. View what tools the skill provides
openbkn skill content skill-kg-analyzer

# 5. Read the skill's documentation
openbkn skill read-file skill-kg-analyzer SKILL.md

# 6. List the tools of a toolbox
openbkn tool list --toolbox <box_id>

# 7. Execute a tool directly
openbkn tool execute <tool_id> --toolbox <box_id> \
  --body '{"sql": "SELECT COUNT(*) FROM orders WHERE status = '\''pending'\''"}'
```

---

## TypeScript SDK

```typescript
import { createClient } from '@openbkn/bkn-sdk';

const bkn = createClient({ baseUrl: 'https://<access-address>', token: process.env.BKN_TOKEN });

// List toolboxes (generic passthrough)
const toolboxes = await bkn.call('/api/agent-operator-integration/v1/tool-box/list', { method: 'GET' });
console.log('toolboxes:', toolboxes);

// Execute a tool in a published toolbox
const result = await bkn.call(
  '/api/agent-operator-integration/v1/tool-box/<box_id>/proxy/<tool_id>',
  { method: 'POST', body: { body: { city: 'Shanghai', units: 'metric' } } },
);
console.log('result:', result);

// List skills
const skills = await bkn.skills.list();
console.log('skills:', skills);

// Marketplace search
const marketResults = await bkn.skills.market({ query: 'data analysis' });
console.log('market:', marketResults);

// Register a skill from a local directory
const registered = await bkn.skills.register('./my-skill');
console.log('registered:', registered);

// Get skill content
const content = await bkn.skills.content('skill-kg-analyzer');
console.log('content:', content);

// Read a file in a skill
const fileContent = await bkn.skills.readFile('skill-kg-analyzer', 'SKILL.md');
console.log('file:', fileContent);

// Download + install from marketplace into a target directory
await bkn.skills.install('skill-kg-analyzer', './installed/skill-kg-analyzer');
```

---

## curl

```bash
# List toolboxes
curl -sk "https://<access-address>/api/agent-operator-integration/v1/tool-box/list" \
  -H "Authorization: Bearer $(openbkn token)"

# Get toolbox details
curl -sk "https://<access-address>/api/agent-operator-integration/v1/tool-box/<box_id>" \
  -H "Authorization: Bearer $(openbkn token)"

# List the tools in a toolbox
curl -sk "https://<access-address>/api/agent-operator-integration/v1/tool-box/<box_id>/tools/list?page=1&page_size=10" \
  -H "Authorization: Bearer $(openbkn token)"

# Execute an enabled tool in a published toolbox
curl -sk -X POST "https://<access-address>/api/agent-operator-integration/v1/tool-box/<box_id>/proxy/<tool_id>" \
  -H "Authorization: Bearer $(openbkn token)" \
  -H "Content-Type: application/json" \
  -d '{"body": {"city": "Shanghai", "units": "metric"}}'

# Delete a toolbox
curl -sk -X DELETE "https://<access-address>/api/agent-operator-integration/v1/tool-box/<box_id>" \
  -H "Authorization: Bearer $(openbkn token)"

# List skills
curl -sk "https://<access-address>/api/agent-operator-integration/v1/skills" \
  -H "Authorization: Bearer $(openbkn token)"

# Search marketplace
curl -sk "https://<access-address>/api/agent-operator-integration/v1/skills/market?query=data+analysis" \
  -H "Authorization: Bearer $(openbkn token)"

# Get skill content
curl -sk "https://<access-address>/api/agent-operator-integration/v1/skills/skill-kg-analyzer/content" \
  -H "Authorization: Bearer $(openbkn token)"

# Read a file within a skill
curl -sk "https://<access-address>/api/agent-operator-integration/v1/skills/skill-kg-analyzer/files/SKILL.md" \
  -H "Authorization: Bearer $(openbkn token)"

# Install a skill
curl -sk -X POST "https://<access-address>/api/agent-operator-integration/v1/skills/skill-kg-analyzer/install" \
  -H "Authorization: Bearer $(openbkn token)" \
  -H "Content-Type: application/json" \
  -d '{"version": "1.2.0"}'
```
