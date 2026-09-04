# Vega Backend API Documentation

> OpenAPI 3.1.1 definitions for the Vega Backend HTTP API. Each file focuses
> on one resource concept. Cross-resource convenience operations belong to the
> resource by which they filter, avoiding duplicated schemas and fragmented
> documentation.

## File index

| File | Resource | Endpoints |
|---|---|---|
| [auth-resource.yaml](auth-resource.yaml) | AuthResource | `GET /auth-resources` for resources available for authorization by `resource_type` |
| [catalog.yaml](catalog.yaml) | Catalog | `GET/POST /catalogs`, `GET/PUT/DELETE /catalogs/{id(s)}`, `POST /catalogs/test-connection`, `POST .../enable`, `POST .../disable`, `GET /catalogs/{id}/health-status`, `POST /catalogs/{id}/test-connection` |
| [catalog-health-check-schedule.yaml](catalog-health-check-schedule.yaml) | CatalogHealthCheckSchedule | `GET/PUT /catalogs/{id}/health-check-schedule` |
| [connector-type.yaml](connector-type.yaml) | ConnectorType | `GET/POST /connector-types`, `GET/PUT/DELETE /connector-types/{type}`, `POST .../enable`, `POST .../disable` |
| [discover-task.yaml](discover-task.yaml) | DiscoverTask | `POST /catalogs/{id}/discover`, `GET /discover-tasks`, `GET /discover-tasks/{id}`, `DELETE /discover-tasks/{ids}` |
| [discover-schedule.yaml](discover-schedule.yaml) | DiscoverSchedule | `GET/POST /discover-schedules`, `GET/PUT/DELETE /discover-schedules/{id}`, `POST .../enable`, `POST .../disable` |
| [health.yaml](health.yaml) | Health | `GET /api/vega-backend/v1/health`, returning the platform version |
| [resource.yaml](resource.yaml) | Resource | `GET/POST /resources`, `GET/PUT/DELETE /resources/{id(s)}` |
| [resource-data.yaml](resource-data.yaml) | ResourceData | `POST/PUT /resources/{id}/data`, `GET/PUT/DELETE /resources/{id}/data/{doc_id(s)}` |
| [raw-query.yaml](raw-query.yaml) | RawQuery | `POST /resources/query` |
| [build-task.yaml](build-task.yaml) | BuildTask | `GET/POST /build-tasks`, `GET/DELETE /build-tasks/{id(s)}`, `POST .../start`, `POST .../stop` |
| [semantic-understanding-task.yaml](semantic-understanding-task.yaml) | SemanticUnderstandingTask | `GET/POST /semantic-understanding-tasks`, `GET/DELETE /semantic-understanding-tasks/{id(s)}` |

## Conventions

- **OpenAPI version:** 3.1.1.
- **Error responses:** Every non-2xx response uses the `Error` schema corresponding to `comm-go/rest.BaseError`. Each file currently contains its own definition.
- **Internal APIs:** Most business operations also have `/api/vega-backend/in/v1/...` routes with the same request and response structures. External APIs use an OAuth token; internal APIs use `X-Account-ID` and `X-Account-Type`. This documentation covers only external APIs. The routes registered in `driveradapters/router.go` are authoritative for the internal surface.
- **Cross-resource actions:** `POST /catalogs/{id}/discover` creates a DiscoverTask and is therefore defined in `discover-task.yaml`.
