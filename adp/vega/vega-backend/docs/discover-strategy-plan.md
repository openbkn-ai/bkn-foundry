# Discover Strategy Improvement Plan

## Background

Vega discover needs an explicit business-level strategy for deciding how resource changes are synchronized from a physical catalog into Vega resources.

The intended business strategies are mutually exclusive presets:

- `full_sync`: discover all source resources, create missing resources, refresh existing resources, and mark missing source resources as stale.
- `create_only`: only create newly discovered resources that do not exist in Vega.
- `cleanup_only`: only mark Vega resources as stale when their source resource no longer exists.

Because these are business presets rather than freely combinable operations, the external API and persisted configuration should use a singular `strategy` field. The worker implementation may convert the strategy into internal actions for reconciliation.

## Naming Decision

Use `strategy` for API, domain objects, and database persistence.

Use internal actions only inside worker/reconcile logic:

- `create`
- `refresh`
- `mark_stale`

Recommended mapping:

| Strategy | Business meaning | Internal actions |
| --- | --- | --- |
| `full_sync` | Full discover and synchronization | `create`, `refresh`, `mark_stale` |
| `create_only` | Only add newly discovered resources | `create` |
| `cleanup_only` | Only clean up resources missing from the source | `mark_stale` |

Do not expose action arrays in API unless the product later needs custom user-defined combinations.

## Target API Shape

Manual discover:

```json
{
  "strategy": "full_sync"
}
```

Schedule discover:

```json
{
  "name": "nightly full discover",
  "catalog_id": "catalog-id",
  "cron_expr": "0 2 * * *",
  "strategy": "full_sync",
  "enabled": true
}
```

Default behavior:

- Missing or empty `strategy` should be normalized to `full_sync`.
- Invalid strategy should return HTTP 400.

## Domain Model Changes

Add discover strategy constants in `server/interfaces`, for example:

```go
const (
	DiscoverStrategyFullSync    = "full_sync"
	DiscoverStrategyCreateOnly  = "create_only"
	DiscoverStrategyCleanupOnly = "cleanup_only"
)
```

Replace strategy arrays with a singular field:

```go
Strategy string `json:"strategy"`
```

Affected domain/request models:

- `DiscoverSchedule`
- `DiscoverScheduleRequest`
- `DiscoverTask`
- any discover task creation request introduced by the service layer

Introduce a typed task creation request instead of variadic strings:

```go
type CreateDiscoverTaskRequest struct {
	CatalogID    string
	TriggerType  string
	ScheduleID   string
	Strategy     string
}
```

Change the service interface from positional variadic arguments to:

```go
Create(ctx context.Context, req *CreateDiscoverTaskRequest) (string, error)
```

## Database Changes

Prefer a new single-value column:

```sql
f_strategy varchar(32) not null default 'full_sync'
```

Apply this to:

- `t_discover_schedule`
- `t_discover_task`

Migration from the old array field should normalize known values:

| Existing value | New value |
| --- | --- |
| empty string | `full_sync` |
| `[]` | `full_sync` |
| `["insert"]` | `create_only` |
| `["delete"]` | `cleanup_only` |
| `["insert","delete","update"]` or equivalent all-actions set | `full_sync` |
| unsupported mixed subsets | decide explicitly during migration; safest default is `full_sync` |

If backward compatibility is required for one release, keep reading `f_strategies` as a fallback when `f_strategy` is empty, but write only `f_strategy`.

## Handler Changes

Manual discover handler should:

1. Bind an optional request body.
2. Normalize missing strategy to `full_sync`.
3. Validate strategy against the supported enum.
4. Create a discover task with the normalized strategy.
5. Keep the response shape stable, returning the task id.

Schedule handlers should:

1. Accept `strategy` in create/update requests.
2. Normalize and validate it.
3. Persist it on the schedule.
4. Pass it directly to task creation during schedule execution.

## Worker Design

Convert strategy into internal actions at the worker boundary:

```go
type DiscoverActions struct {
	Create    bool
	Refresh   bool
	MarkStale bool
}
```

Expected conversion:

```go
func ActionsFromDiscoverStrategy(strategy string) DiscoverActions {
	switch strategy {
	case DiscoverStrategyCreateOnly:
		return DiscoverActions{Create: true}
	case DiscoverStrategyCleanupOnly:
		return DiscoverActions{MarkStale: true}
	case DiscoverStrategyFullSync, "":
		return DiscoverActions{Create: true, Refresh: true, MarkStale: true}
	default:
		return DiscoverActions{Create: true, Refresh: true, MarkStale: true}
	}
}
```

All discover categories should use the same action gates:

- table
- index
- fileset

Behavior rules:

- If `Create` is false, source resources missing in Vega must not be created.
- If `Refresh` is false, existing Vega resources must not have metadata refreshed and stale resources must not be reactivated.
- If `MarkStale` is false, Vega resources missing from the source must not be marked stale.

## Implementation Order

1. Add strategy constants, normalizer, validator, and internal action conversion helper.
2. Add typed `CreateDiscoverTaskRequest` and refactor `DiscoverTaskService.Create`.
3. Update discover task persistence from `strategies` array to singular `strategy`.
4. Update discover schedule persistence from `strategies` array to singular `strategy`.
5. Add manual discover API request body support for `strategy`.
6. Update schedule create/update/execute flows to use `strategy`.
7. Refactor worker reconcile code so table, index, and fileset all consume the same internal actions.
8. Add migration scripts for MariaDB and DM8.
9. Update generated mocks if needed.
10. Add or update tests.

## Test Plan

API and validation:

- Manual discover without body defaults to `full_sync`.
- Manual discover with `strategy: create_only` creates a task with `create_only`.
- Manual discover with an invalid strategy returns HTTP 400.
- Schedule create/update persists the selected strategy.
- Schedule execution creates a task with the schedule strategy.

Persistence:

- Discover task create/get/list round-trips `strategy`.
- Discover schedule create/get/list/update round-trips `strategy`.
- Migration maps old strategy-array values to the new singular strategy.

Worker behavior by category:

- `full_sync` creates missing resources, refreshes existing resources, reactivates stale resources found in source, and marks missing source resources stale.
- `create_only` only creates missing resources.
- `cleanup_only` only marks resources stale when they are missing from source.

Run category coverage for:

- table
- index
- fileset

## Open Decisions

- Whether to support one-release backward compatibility for API requests containing old `strategies`.
- How to map unsupported historical subsets such as `["insert","delete"]`; safest default is `full_sync`, but a product decision may prefer a new explicit strategy if that combination matters.
- Whether strategy names should be user-facing English values or localized display labels should be handled only by frontend/i18n.
