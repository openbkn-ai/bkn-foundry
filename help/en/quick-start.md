# 🚀 Quick start

This walkthrough assumes BKN Foundry is already [installed and deployed](install.md), including the post-install checks on that page. **Full installs assume Linux**; optional **macOS** + kind flow: [`deploy/dev/README.md`](../../deploy/dev/README.md) ([中文](../../deploy/dev/README.zh.md)).

> Before installing on a new host, run **`sudo bash deploy/preflight.sh`** (check / `--fix`) to validate kernel, sysctl, containerd, kubectl, helm, Node and the `openbkn` CLI. After `deploy.sh openbkn install`, run **`sudo bash deploy/onboard.sh`** (Linux — matches `sudo deploy.sh`; macOS dev path uses plain `bash`) to register an LLM + embedding, patch the BKN ConfigMap (only when the default actually changes), and on a full install create the business user **`test`** (Context Loader serves its tools over MCP only and registers no toolbox, so neither onboard nor install has a step for it). Both are documented in [Install — Pre-install host check / fix: `preflight.sh`](install.md#-pre-install-host-check--fix-preflightsh) and [Install — Post-install: `onboard.sh`](install.md#post-install-onboardsh).

> **Model configuration note**: **Register at least one LLM and one embedding (vector) small model** when possible: the LLM powers Agent chat and reasoning; the embedding model powers semantic search and vectorization. Semantic search (Step 4) and Agent chat (Step 5) depend on these; after registering an embedding, complete [Enable BKN semantic search](manual/model.md#enable-bkn-semantic-search) in the cluster (ConfigMap / default small-model name). Other registration details are in [Model management](manual/model.md). A `--minimum` install has no bundled models; see also [Install and deploy — Configure models](install.md#configure-models). Data source connection, knowledge network creation, and conditional queries work without models.

---

## 🎯 Scenario: First semantic search in 5 minutes

**Story**: You just deployed BKN Foundry. You have a MySQL database with ERP data. Your goal is to turn the database into a knowledge network and search it with natural language — "which orders are overdue?"

### Step 1: Authenticate

A **full install** (`./deploy.sh openbkn install`, no `--minimum`, with auth enabled) requires a real user to sign in. Pick **one** of the two paths below to obtain a sign-in account:

#### Path A (recommended): let `bash deploy/onboard.sh` prepare it

On a full install (auth enabled), `onboard.sh` automatically installs / signs in `openbkn` (admin is built into the same CLI), creates the business user **`test`** (password `111111` unless `ONBOARD_TEST_USER_PASSWORD` is set), assigns **every** role from `openbkn admin role list`, and switches local `~/.bkn` to `test`.

```bash
cd deploy
sudo bash ./onboard.sh        # interactive (Linux — matches sudo deploy.sh)
sudo bash ./onboard.sh -y     # non-interactive (defaults)
# macOS dev path:  bash ./dev/mac.sh onboard       # no sudo needed
```

> `sudo` keeps `onboard.sh` reading the same `$HOME/.openbkn-ai/config.yaml` that `sudo deploy.sh` wrote (`/root/.openbkn-ai/`) and writing `openbkn` auth state to the same `$HOME/.bkn`. Skip it on macOS dev. See [Install — Post-install: `onboard.sh`](install.md#post-install-onboardsh).

After it finishes you usually do **nothing more** — jump to [Sign in](#sign-in) below; on a different machine just sign in again. Full sequence: [Install — Post-install: `onboard.sh`](install.md#post-install-onboardsh).

#### Path B (manual): use `openbkn admin` directly

Use this when you want a custom username, custom role set, or simply prefer not to run `onboard.sh`.

```bash
npm install -g @openbkn/bkn-sdk
# admin initial password: generated at install, recorded as bknSafe.initialPassword in ~/.openbkn-ai/config.yaml
openbkn auth login <platform-url> -u admin -p '<initial-password>' -k
openbkn admin role list                                           # all roles and roleIds (e.g. super_admin, normal_user)
openbkn admin user create --login <new-username>                  # initial password is generated and returned ONCE (initial_password); first sign-in forces a change
# Quick start / PoC: assign every roleId from role list to avoid API 403s due to missing roles
openbkn admin user assign-role <userId> <roleId>
# … repeat for each role in role list
openbkn admin user roles <userId>                                 # verify
```

- **Path A default password is `111111`** (set by onboard for `test`); **Path B has no fixed default** — `user create` generates a per-user initial password and returns it once in the response. Use whichever matches the path you took.
- Role / permission notes: [Install — Administrator commands after a full install (`openbkn admin`)](install.md#-administrator-commands-after-a-full-install-openbkn-admin) and [BKN Safe](manual/bkn-safe.md#-administrator-commands-openbkn-admin). In production, grant least privilege; the "every role" pattern is for local / PoC / quick start.
- **Minimum install** (`--minimum`): both paths are unnecessary — use `openbkn auth login <platform-url> --no-auth`.

If you already have a sign-in account from ops, skip both paths and go straight to "Sign in" below.

<a id="sign-in"></a>

#### Sign in

Pick the row matching the path you just took:

| Your situation | Command |
|---|---|
| Ran `onboard.sh` (Path A) | `openbkn auth status` to confirm `~/.bkn` is already `test`; on a different machine: `openbkn auth login <platform-url> -u test -p '<password>' -k` |
| Built a user manually (Path B) | `openbkn auth login <platform-url> -u <new-username> -p '<password>' -k` (first sign-in forces a password change) |
| Minimum install (`--minimum`) | `openbkn auth login <platform-url> --no-auth` |
| Prefer browser OAuth | `openbkn auth login <platform-url> -k` (default; opens local browser on a TTY) |

- `<platform-url>` is the access address printed by `deploy.sh` after installation completes.
- `-k` skips TLS certificate verification — use it with self-signed certificates; omit if you have a valid cert.

**Headless / no-browser sign-in details** (extends the row "Prefer browser OAuth" above when no browser is available — SSH, CI, containers):

| Scenario | What to use |
|----------|-------------|
| **No browser — `--no-browser`** (interactive headless, recommended) | The CLI prints an OAuth URL; open it on another device, sign in, then paste the **full callback URL** from the address bar back into the terminal. |
| **No browser — export & replay** (CI / fully automated) | After `openbkn auth login` on a machine with a browser: the **browser success page** shows **Headless machine** instructions and a one-line `openbkn auth login '<platform-url>' --client-id '…' --client-secret '…' --refresh-token '…'` (often with a **Copy command** button); or run **`openbkn auth export`** (or `--json`) in a terminal. On the **headless** host, run that one-line command to populate `~/.bkn/`. |
| **No browser — HTTP sign-in** | `openbkn auth login … -u <user> -p <password> -k` (optionally `--http-signin`). The CLI calls the platform's `/oauth2/signin` directly — no Node/Chromium required. Omit `-u`/`-p` to be prompted on stdin. This is exactly what `onboard.sh` uses internally. |

After a successful browser login, the page states you can close the tab and explains what to run on a machine **without** a browser (SSH, CI, containers, etc.). **Keep the shown credentials secure** — anyone with the **refresh token** and **client secret** can obtain new access tokens; do not commit them to source control.

- After login, run `openbkn config show` to verify the active platform configuration.

```bash
openbkn config show
```

Context Loader's tools (used by agents to query knowledge networks) are served **over MCP only** — it no longer registers a toolbox into the execution factory. To list the catalog:

```bash
openbkn context info
```

(Use `openbkn context tools <kn-id>` for one knowledge network. Context Loader's tools do not appear in the execution factory's `/tool-box/list`; that is expected.)

> **Note**
>
> - **`openbkn auth whoami`** needs an `id_token` from OAuth login. If you used `openbkn auth login … --no-auth` (or the platform is a minimal / no-auth install), the CLI is in **no-auth** mode and `whoami` will report no `id_token` — **expected**; use `openbkn auth status` to confirm no-auth.

### Step 2: Connect a Database (register a Vega catalog)

```bash
CAT=$(openbkn --json vega catalog create --name "erp" --connector-type mysql \
  --connector-config '{"host":"db.example.com","port":3306,"username":"root","password":"pass123","databases":["erp"]}' \
  | python3 -c 'import json,sys;print(json.load(sys.stdin)["id"])')
echo "$CAT"   # → catalog id
```

The fields inside `connector_config` vary by connector type; `openbkn vega connector-type get <type>`
is authoritative. **The host must be reachable from the network vega-backend runs in** — usually an
internal address.

A catalog is created disabled. Enable it, then discover its tables:

```bash
openbkn vega catalog enable "$CAT"
openbkn vega catalog discover "$CAT" --wait

# Inspect the discovered table resources
openbkn vega catalog list
openbkn vega resource list --catalog-id "$CAT" --category table
```

### Step 3: Create a Knowledge Network

**Option A: CLI one-liner**

```bash
openbkn bkn create-from-catalog "$CAT" \
  --name "erp-supply-chain" \
  --tables "orders,products,customers" \
  --build --embedding-model text-embedding-v4
```

This creates one object type per table, binds each to its Vega resource, and maps the fields.
`--build` additionally submits a search-index build (full text + vector) per resource.

> **`--build` changes the read path**: once a table resource has a local index, Vega serves rows from
> that index and only falls back to the source database while no index exists — later `UPDATE`s in the
> source are invisible until the resource is rebuilt. Omit `--build` to keep reads live, at the cost of
> full-text and vector search.

> **Note**: `create-from-catalog` auto-selects a primary key and display key. If the source table has no
> explicit primary key the choice may be suboptimal (e.g. `status`), merging records that share a value.
> Fix it afterwards with `openbkn bkn object-type update`.

**Option B: Via AI coding assistant**

If you have installed the **openbkn** AI Agent skill (from your organization’s skill bundle), you can use natural language in your AI coding assistant (Cursor, Claude Code, etc.):

```
Create a knowledge network called erp-supply-chain from datasource ds-abc123 using the orders, products, and customers tables
```

Or use the slash command:

```
/openbkn Create a knowledge network from datasource ds-abc123 with tables orders, products, customers, name it erp-supply-chain
```

The skill will automatically invoke the `openbkn` CLI to discover the datasource, create object types, and build indexes.

**Verify**

Regardless of which method you used, verify the result:

```bash
openbkn bkn object-type list <kn_id>
# → orders (ot-1), products (ot-2), customers (ot-3)
```

### Step 4: Semantic Search

> Semantic search requires an embedding model and [Enable BKN semantic search](manual/model.md#enable-bkn-semantic-search). If either is missing, this step may fail. See also [Model management](manual/model.md) and [Install and deploy — Configure models](install.md#configure-models). The **conditional query** below works without semantic search enabled.

```bash
openbkn bkn search <kn_id> "overdue orders"
```

Returns concepts and instances semantically related to "overdue orders". Drill down with a conditional query:

```bash
openbkn bkn object-type query <kn_id> ot-1 \
  '{"limit":10,"condition":{"field":"status","operation":"==","value":"overdue"}}'
```

**Congratulations** — you went from a blank platform to natural-language database search.

---

## 🎯 Scenario: Same thing, with the TypeScript SDK

If you prefer code over CLI, here's the same flow in TypeScript.

> More runnable examples ship with the `@openbkn/bkn-sdk` npm package.

### Create a Client

```typescript
import { createClient } from '@openbkn/bkn-sdk';

// Pass a token explicitly, or omit it to read the CLI session from ~/.bkn/
// (written by `openbkn auth login`).
const bkn = createClient({ baseUrl: 'https://<access-address>', token: process.env.BKN_TOKEN });
```

### Discover Knowledge Networks

```typescript
const knList = await bkn.kn.list({ limit: 10 });
for (const kn of knList) {
  console.log(`${kn.name} (${kn.id})`);
}
```

### Browse the Schema: Object Types, Relations, Actions

```typescript
const knId = knList[0].id;

const objectTypes = await bkn.kn.objectTypes(knId);
for (const ot of objectTypes) {
  console.log(`${ot.name} (${ot.id})`);
}

const relationTypes = await bkn.kn.relationTypes(knId);
for (const rt of relationTypes) {
  console.log(`${rt.source_object_type?.name} —[${rt.name}]→ ${rt.target_object_type?.name}`);
}

const actionTypes = await bkn.kn.actionTypes(knId);
```

### Query Instances & Subgraph Traversal

```typescript
const otId = objectTypes[0].id;

// Conditional query
const instances = await bkn.kn.objectTypeQuery(knId, otId, {
  conditions: [{ field: 'status', op: '==', value: 'overdue' }],
  limit: 5,
});
console.log(instances);

// Subgraph traversal (expand along a relation type)
const rt = relationTypes[0];
const subgraph = await bkn.kn.subgraph(knId, {
  relation_type_paths: [{
    relation_types: [{
      relation_type_id: rt.id,
      source_object_type_id: rt.source_object_type?.id,
      target_object_type_id: rt.target_object_type?.id,
    }],
  }],
  limit: 5,
});
```

### Semantic Search

> Requires a registered embedding and [Enable BKN semantic search](manual/model.md#enable-bkn-semantic-search).

```typescript
const result = await bkn.kn.search(knId, 'overdue orders');
console.log(result);
```

### Context Loader (MCP Layered Retrieval)

```typescript
// Layer 1: Schema search — discover object types by natural language
const schema = await bkn.context.searchSchema(knId, 'orders');

// Layer 2: Instance query — fetch concrete data for an object type
const mcpInstances = await bkn.context.queryObjectInstance(knId, { ot_id: otId, limit: 5 });
```

---

## 🎯 Scenario: Create an agent and chat

**Story**: The knowledge network is built. Now you want to give your business team a natural-language interface — no SQL needed, just ask questions and get answers.

> **Prerequisite**: Agents require an LLM and an embedding; see [Model management](manual/model.md) and [Install and deploy — Configure models](install.md#configure-models). For semantic features, also complete [Enable BKN semantic search](manual/model.md#enable-bkn-semantic-search).

### CLI

```bash
# Check registered LLMs (to get llm_id)
curl -sk "https://<platform-url>/api/mf-model-manager/v1/llm/list?page=1&size=50"

# List available templates (may be empty on --minimum installs)
openbkn agent template-list

# Create an Agent (specify --llm-id)
openbkn agent create \
  --name "Supply Chain Assistant" \
  --profile "Answer supply chain questions" \
  --llm-id <llm_id>

# If templates are available, create from a template config
openbkn agent template-get <template_id> --save-config /tmp/config.json
openbkn agent create \
  --name "Supply Chain Assistant" \
  --profile "Answer supply chain questions" \
  --config /tmp/config-*.json

# Bind the knowledge network
openbkn agent update <agent_id> --knowledge-network-id <kn_id>

# Publish (required before chatting)
openbkn agent publish <agent_id>

# Single-turn chat
openbkn agent chat <agent_id> -m "How many orders are overdue this month?"

# Interactive multi-turn chat
openbkn agent chat <agent_id>
# > Which suppliers have the slowest delivery?
# > What improvements do you suggest?
```

### TypeScript SDK

```typescript
import { createClient } from '@openbkn/bkn-sdk';

const bkn = createClient({ baseUrl: 'https://<access-address>', token: process.env.BKN_TOKEN });

// List agents
const agents = await bkn.agents.list({ limit: 10 });
const agentId = agents[0].id;

// Create an agent (bind the knowledge network in its config), then publish it
const created = await bkn.agents.create({
  name: 'Supply Chain Assistant',
  desc: 'Answer supply chain questions',
});
await bkn.agents.publish(created.id);

// Single-turn chat — resolves with the full answer text
const reply = await bkn.agents.chat(agentId, 'How many orders are overdue this month?');
console.log(reply.text);

// Streaming chat (real-time output) — onDelta receives each new text suffix
await bkn.agents.chat(agentId, 'Which suppliers have the slowest delivery?', {
  stream: true,
  onDelta: (delta) => process.stdout.write(delta),
});

// Conversation history (sessions are keyed by the agent's published key)
const agent = await bkn.agents.get(agentId);
const sessions = await bkn.agents.sessions(agent.key, { size: 5 });
const messages = await bkn.agents.history(agent.key, sessions[0].conversation_id);
```

---

## 🎯 Scenario: Trace the reasoning (BKN Trace)

**Story**: The agent's answer looks wrong. You want to know exactly what data it queried, which tools it called, and how long each step took.

> **Note**: Trace depends on the full backend stack (including Uniquery/DataView components). On a Core-only minimal deployment, the trace endpoint may return HTTP 500; ensure the required services are running.

```bash
# List conversation sessions
openbkn agent sessions <agent_id>

# Get the full trace (agent id + conversation id)
openbkn agent trace <agent_id> <conversation_id> --pretty
```

The trace returns a Span tree ordered by time, showing:
- The agent's planning and reasoning steps
- Tool calls (BKN query, VEGA SQL, external API)
- Inputs, outputs, and latency per step
- Context assembled by Context Loader

```
[HTTP Request] → [Intent Recognition] → [BKN Query] → [SQL Execution] → [Answer Generation]
      ↓                 ↓                    ↓               ↓                  ↓
  User question    "find overdue"       Conditional      3 results         "There are 3..."
   received         identified          ot: orders       from VEGA          composed
```

---

## 🎯 Scenario: Build a knowledge network from CSV files

**Story**: You don't have a database — just a few CSV reports.

CSV still needs a database to land in: load the files, then register a catalog and build.

```bash
# 1. Load the CSVs with the standard mysql client
mysql -h db.example.com -u root -p supply_chain < load_csv.sql

# 2. Register the catalog and discover tables (as in the scenario above)
openbkn vega catalog create --name "supply" --connector-type mysql \
  --connector-config '{"host":"db.example.com","port":3306,"username":"root","password":"pass123","databases":["supply_chain"]}'
openbkn vega catalog enable <catalog_id>
openbkn vega catalog discover <catalog_id> --wait

# 3. Build the knowledge network and its indexes
openbkn bkn create-from-catalog <catalog_id> \
  --name "supply-reports" --build --embedding-model text-embedding-v4

# Verify
openbkn bkn search <kn_id> "zero inventory"
```

> `openbkn bkn create-from-csv <catalog_id> --files ...` still exists in the CLI, but it relies on the
> retired dataflow import path and does not work on current deployments. Use the three steps above.

---

## 🎯 Scenario: Run SQL directly against the data

**Story**: You want to query the underlying data, bypassing the knowledge network.

```bash
# Probe: a catalog listing means the service is up and the token is valid
openbkn vega catalog list --limit 1

# Browse resources in a catalog
openbkn vega catalog resources <catalog_id> --category table

# Sample rows from one resource
openbkn vega resource query <resource_id> --limit 10 --need-total

# Direct SQL: reference a resource with the {{<resource_id>}} placeholder
# instead of hand-writing fully-qualified table names
openbkn vega sql --input-dialect mysql \
  --query "SELECT supplier_name, city FROM {{<resource_id>}} LIMIT 10"

# Multi-table JOINs within one catalog go through the structured query endpoint
openbkn call /api/vega-backend/v1/resources/query -X POST \
  -d '{"tables":[{"resource_id":"<resource_id>"}],"limit":5,"need_total":true}'
```

> Data views (`openbkn dataview`, mdl-uniquery) and the `vega-calculate-coordinator` that ad-hoc SQL
> depended on have both been retired. Use resource queries and `vega sql` instead — see
> [VEGA](manual/vega.md).

---

## 📖 Where to go next

| Goal | Doc |
| --- | --- |
| Full BKN operations (schema, conditional queries, actions) | [bkn.md](manual/bkn.md) |
| Model registration & testing | [Model management](manual/model.md) |
| Enable semantic search in the cluster (ConfigMap) | [Enable BKN semantic search](manual/model.md#enable-bkn-semantic-search) |
| Data virtualization & catalog management | [vega.md](manual/vega.md) |
| MCP layered retrieval | [context-loader.md](manual/context-loader.md) |
| Tools & skill management | [execution-factory.md](manual/execution-factory.md) |
| Trace & evidence chain | [bkn-trace.md](manual/bkn-trace.md) |
| Auth & security governance | [bkn-safe.md](manual/bkn-safe.md) |

Full SDK example code ships with the `@openbkn/bkn-sdk` npm package.
