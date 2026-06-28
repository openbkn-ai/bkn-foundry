# 🛠️ Execution Factory

## 📖 Overview

The **Execution Factory** registers and runs **operators**, **tools**, and **skills** that agents invoke under policy. It bridges LLM planning to concrete business actions (HTTP tools, code runners, MCP integrations, etc.).

Typical ingress prefix:

| Prefix | Role |
| --- | --- |
| `/api/agent-operator-integration/v1` | Operator integration and tool execution surface |

## CLI

### Calling Operator APIs

The `openbkn call` command invokes any registered operator API by path, acting as a lightweight HTTP client wired into the platform auth and routing.

```bash
# GET an operator endpoint
openbkn call GET /api/agent-operator-integration/v1/operators

# POST with a JSON body
openbkn call POST /api/agent-operator-integration/v1/operators/op-weather/invoke \
  --data '{"city": "Shanghai", "units": "metric"}'

# PUT to update an operator definition
openbkn call PUT /api/agent-operator-integration/v1/operators/op-weather \
  --data '{"description": "Updated weather lookup", "timeout": 30}'

# DELETE an operator
openbkn call DELETE /api/agent-operator-integration/v1/operators/op-weather
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

# 6. Call an operator API to list registered operators
openbkn call GET /api/agent-operator-integration/v1/operators

# 7. Invoke an operator directly
openbkn call POST /api/agent-operator-integration/v1/operators/op-sql-query/invoke \
  --data '{"sql": "SELECT COUNT(*) FROM orders WHERE status = '\''pending'\''"}'
```

---

## TypeScript SDK

```typescript
import { createClient } from '@openbkn/bkn-sdk';

const bkn = createClient({ baseUrl: 'https://<access-address>', token: process.env.BKN_TOKEN });

// List operators (operator endpoints have no typed helper — use the generic passthrough)
const operators = await bkn.call('/api/agent-operator-integration/v1/operators', { method: 'GET' });
console.log('operators:', operators);

// Invoke an operator
const result = await bkn.call(
  '/api/agent-operator-integration/v1/operators/op-weather/invoke',
  { method: 'POST', body: { city: 'Shanghai', units: 'metric' } },
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
# List operators
curl -sk "https://<access-address>/api/agent-operator-integration/v1/operators" \
  -H "Authorization: Bearer $(openbkn token)"

# Get operator details
curl -sk "https://<access-address>/api/agent-operator-integration/v1/operators/op-weather" \
  -H "Authorization: Bearer $(openbkn token)"

# Invoke an operator
curl -sk -X POST "https://<access-address>/api/agent-operator-integration/v1/operators/op-weather/invoke" \
  -H "Authorization: Bearer $(openbkn token)" \
  -H "Content-Type: application/json" \
  -d '{"city": "Shanghai", "units": "metric"}'

# Register a new operator
curl -sk -X POST "https://<access-address>/api/agent-operator-integration/v1/operators" \
  -H "Authorization: Bearer $(openbkn token)" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "weather-lookup",
    "type": "http",
    "description": "Look up current weather by city",
    "endpoint": "https://api.weather.example.com/v1/current",
    "method": "GET",
    "parameters": [
      {"name": "city", "type": "string", "required": true},
      {"name": "units", "type": "string", "default": "metric"}
    ]
  }'

# Delete an operator
curl -sk -X DELETE "https://<access-address>/api/agent-operator-integration/v1/operators/op-weather" \
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
