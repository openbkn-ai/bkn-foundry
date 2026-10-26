# BKN Project

[中文](README.zh.md) | English

`bkn-project` builds and publishes knowledge networks from projects. Each
release is imported into bkn-backend as a new knowledge network
(`<base_id>_<version>`) instead of overwriting the live one, and the release
history is kept as a lineage. See issue #2520 and epic #2519.

## Current Capability Scope

- Release lineage store (`t_bkn_project_release`) with read-only APIs.
- Health and readiness endpoints.

Publishing, the git store and the drift check follow in later changes. The
Helm chart already mounts a persistent volume at `/data/git` for the git store.

## API

Prefix: `/api/bkn-project/v1`. Release routes need an OAuth token, verified
through Hydra like bkn-backend's external routes.

| Method | Path | Description |
| --- | --- | --- |
| GET | `/health` | Service information |
| GET | `/ready` | 200 when the database is reachable, otherwise 503 |
| GET | `/lineages/{lineage_id}/releases?limit=&cursor=` | Releases of a lineage, newest first; cursor paging |
| GET | `/releases/{network_id}` | The release that published a network |

## Local Development

```bash
cd server
go build ./...
go vet ./...
I18N_MODE_UT=true go test ./...
```

Configuration: `server/config/bkn-project-config.yaml`. The schema is created
by data-migrator from `migrations/bkn-project/`.

Regenerate mocks with `make generate-mock` (needs `mockgen` from
`go.uber.org/mock`).
