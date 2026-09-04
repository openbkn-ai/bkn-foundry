# Ontology Query

## Module overview

Ontology Query is the query service component of the ontology engine. It focuses on efficient and intelligent knowledge-network query capabilities. The module is based on OpenSearch and VEGA virtualized query technology, and supports complex relation queries, semantic search, and multidimensional data retrieval.

## Core features

### 1. Network query

- **Relation path query**: supports complex multi-hop relation path queries
- **Subgraph query**: queries subgraph structures under specific conditions
- **Pattern matching**: intelligent matching queries based on graph patterns
- **Shortest path**: calculates shortest paths between entities

### 2. Semantic search

- **Vector search**: semantic search based on vector similarity
- **Keyword search**: full-text search and fuzzy matching
- **Semantic understanding**: understanding and conversion of natural-language queries
- **Relevance ranking**: intelligent ranking based on semantic relevance

### 3. Data retrieval

- **Multidimensional filtering**: supports combined filters with multiple conditions
- **Pagination**: efficient paginated query support
- **Sorting**: multi-field sorting and custom sorting
- **Field selection**: flexible field selection and projection
- **Statistical queries**: statistical analysis queries over data

### 4. Model query

- **Ontology browsing**: browsing and navigation of ontology models
- **Model search**: keyword-based and semantic model search
- **Model export**: model export and serialization

## Technical architecture

### Technology stack

- **Language**: Go 1.25
- **Web framework**: Gin 1.11
- **Search engine**: OpenSearch 2.x
- **Query language**: custom query language
- **Cache**: supports multiple caching strategies
- **Observability**: OpenTelemetry

### Architecture

```text
server/
├── common/              # Common configuration and utilities
├── config/              # Configuration files
├── drivenadapters/      # Data access layer
│   ├── agent_operator/  # AI Agent data access
│   ├── model_factory/   # Model factory data access
│   ├── ontology_manager/ # Ontology manager data access
│   └── opensearch/      # OpenSearch data access
├── driveradapters/      # Interface adapter layer
├── errors/              # Error definitions
├── interfaces/          # Interface definitions
├── locale/              # Internationalization support
├── logics/              # Business logic layer
└── version/             # Version information
```

## APIs

### Object queries

- **Object data**: `POST /api/ontology-query/v1/knowledge-networks/{kn_id}/object-types/{ot_id}`
- **Object properties**: `POST /api/ontology-query/v1/knowledge-networks/{kn_id}/object-types/{ot_id}/properties`
- **Object subgraph**: `POST /api/ontology-query/v1/knowledge-networks/{kn_id}/subgraph`

### Action queries

- **Action data**: `POST /api/ontology-query/v1/knowledge-networks/{kn_id}/action-types/{at_id}`

### Internal APIs

- Uses the same paths with the prefix changed to `/api/ontology-query/in/v1/`
- Skips OAuth authentication

### System APIs

- **Health check**: `GET /api/ontology-query/v1/health`

For detailed API documentation, see [API documentation](./api_doc/).

## Quick start

### Requirements

- Go 1.25.0+
- OpenSearch 2.x
- Ontology manager module, running on port 13014
- BKN Safe with the `/api/safe/v1/authz/resource-filter` contract enabled

### Local development

1. **Clone the repository**

   ```bash
   git clone https://github.com/your-org/ontology-opensource.git
   cd ontology-opensource/ontology-query
   ```

2. **Configure dependent services**

   Edit `server/config/ontology-query-config.yaml` and configure connection information for OpenSearch and the ontology manager module.

3. **Install dependencies**

   ```bash
   cd server
   go mod download
   ```

4. **Run the service**

   ```bash
   go run main.go
   ```

   The service starts at <http://localhost:13018>.

### Docker deployment

1. **Build the image**

   ```bash
   docker build -t ontology-query:latest -f docker/Dockerfile .
   ```

2. **Run the container**

   ```bash
   docker run -d -p 13018:13018 --name ontology-query ontology-query:latest
   ```

### Kubernetes deployment

Deploy with the provided Helm chart:

```bash
helm3 install ontology-query helm/ontology-query/
```

## Configuration

### Main configuration items

```yaml
# server/config/ontology-query-config.yaml
server:
  http_port: 13018
  read_timeout: 60
  write_timeout: 60
  language: zh-CN
  run_mode: debug
```

### Data-query authorization

Set `BKN_SAFE_BASE_URL` to the BKN Safe service root (for example,
`http://bkn-safe:13020`). `BKN_SAFE_URL` is accepted only as a compatibility
fallback. A missing or invalid URL does not enable an unauthenticated mode:
object, relation, action-type, and metric data queries fail closed.

Before reading data, ontology-query resolves dependencies from the published
`main` knowledge-network model and checks `query_data` on canonical resources:

- KN roots use `knowledge_network:{kn_id}`.
- KN children use `{resource_type}:{kn_id}/{child_id}`.
- Bound Vega resource references are validated against the published model but
  are not sent directly to Safe by ontology-query. The query is forwarded to
  Vega as the same caller, and Vega checks `resource:{resource_id}` before any
  physical read, falling back to the owning catalog's `query_data` grant when
  the table itself has no direct grant.

Internal `/api/ontology-query/in/v1` requests must provide both `x-account-id`
and `x-account-type`. Permission denial returns HTTP 403. Missing subjects,
disabled accounts, BKN Safe failures, timeouts, invalid responses, or incomplete
published dependencies prevent the data query from running; authorization
infrastructure failures return HTTP 503.

### Action-execution authorization

When authentication is enabled, both submission and the real external invocation
require all of the following for the authenticated execution subject. There is
no separate action-execution rollout switch:

- `execute` on `action_type:{kn_id}/{action_type_id}`; this operation never
  inherits from the knowledge network;
- `execute` on the referenced `tool_box` or `mcp` resource;
- `query_data` on every object type referenced by the action's target, affect,
  and impact contracts.

The worker rechecks the stored subject's current account state and permissions
immediately before invocation. A missing subject, missing permission snapshot,
incomplete dependency, or BKN Safe failure prevents the external call. See the
[shared authorization contract](../../../docs/api/knowledge-network-authorization.md)
for canonical IDs and error semantics.

## Monitoring and operations

### Health checks

- **Health check endpoint**: `GET /api/ontology-query/v1/health`
- **Readiness check endpoint**: `GET /api/ontology-query/v1/health`

### Logging configuration

Structured log output is supported. The log level and output format are configurable:

```yaml
log:
  logLevel: debug
  developMode: false
  maxAge: 100
  maxBackups: 20
  maxSize: 100
```

## Development conventions

### Code conventions

1. Follow the official Go coding conventions.
2. Follow clean architecture principles.
3. Separate interfaces from implementations.
4. Provide complete error handling.

### Query development

1. **Query building**: use the query builder to build complex queries.
2. **Parameter validation**: strictly validate input parameters.
3. **Performance considerations**: optimize query performance.
4. **Result handling**: use a unified result handling format.
5. **Error handling**: provide detailed error information and handling.

### Testing requirements

1. Unit test coverage > 85%.
2. Integration tests cover main query scenarios.
3. Performance tests validate query performance.
4. Load tests validate system capacity.
5. Chaos tests validate system stability.

## Troubleshooting

### Common issues

1. **OpenSearch connection failure**

   - Check OpenSearch configuration and connection parameters.
   - Verify network connectivity and firewall settings.
   - Confirm the health status of the OpenSearch cluster.
   - Check authentication and permission configuration.

2. **Query performance issues**

   - Check index configuration and mapping settings.
   - Analyze query execution plans and complexity.
   - Optimize query statements and filters.
   - Adjust caching strategies and parameters.

3. **High memory usage**

   - Check query result set size.
   - Optimize aggregate queries and bucket settings.
   - Adjust cache size and TTL.
   - Monitor garbage collection.

4. **Inaccurate query results**

   - Check data synchronization status.
   - Verify index data integrity.
   - Analyze query logic and conditions.
   - Check tokenizer and analyzer configuration.

### Debugging tools

- **Performance profiling**: pprof profiling
- **Log analysis**: runtime log analysis
- **Tracing**: distributed tracing

## Version history

- **v0.1.0**: initial version with vector and semantic search support

## Support and contact

- **Technical support**: AISHU ADP team
- **Documentation updates**: continuously updated
- **Issue feedback**: submit through the internal system

---

**Note**: This is a high-performance query engine. Tune performance and configuration based on actual business scenarios.
