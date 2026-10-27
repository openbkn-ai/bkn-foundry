# BKN Project

[中文](README.zh.md) | English

`bkn-project` builds and publishes knowledge networks from projects. Each
release is imported into bkn-backend as a new knowledge network
(`<base_id>_<version>`) instead of overwriting the live one, and the release
history is kept as a lineage. See issue #2520 and epic #2519.

## Current Capability Scope

- Release lineage store (`t_bkn_project_release`).
- Git lineages: one bare repository per lineage under `/data/git` (a
  persistent volume in the Helm chart). `main` mirrors the live network;
  projects work on their own branches and sync `main` into them with a
  semantic three-way merge.
- Drift check between the live network and `main`.
- Publishing a project branch as a new network `<lineage_id>_v<major>_<minor>_<patch>`
  named `<base name> · <version>` (ids cut to 40 characters, names to 40
  characters), tagged `v<version>` in git.
- Health and readiness endpoints.

## API

Prefix: `/api/bkn-project/v1`. All routes except `/health` and `/ready` need an
OAuth token, verified through Hydra like bkn-backend's external routes. Exports
from bkn-backend run with the caller's token, so its permissions apply.

| Method | Path | Description |
| --- | --- | --- |
| GET | `/health` | Service information |
| GET | `/ready` | 200 when the database is reachable, otherwise 503 |
| GET | `/lineages/{lineage_id}/releases?limit=&cursor=` | Releases of a lineage, newest first; cursor paging. The newest release has `state` `live`, earlier ones `archived`; switching consumers changes no state |
| GET | `/releases/{network_id}` | The release that published a network |
| POST | `/lineages/{lineage_id}/releases` | Body `{"branch","version","project_id","notes"}`. Publishes the branch: 201 `{"release","network_id","tag","commit"}`. 409 `BranchBehind` (sync the branch first), `Drift` (the paths in `error_details`; commit the drift and sync), `VersionExists`, or `CapabilitiesSkipped` (the network was imported but the release is not recorded; publishing the same version from the same branch head again overwrites it) |
| POST | `/lineages` | Body `{"kn_id"}`. Exports the live network and starts its lineage on `main`; 201 `{"lineage_id","commit"}`, 409 if it exists |
| POST | `/lineages/{lineage_id}/branches` | Body `{"name","from"}` (`from` defaults to `main`); 201 `{"name","commit"}`, 409 if it exists |
| PUT | `/lineages/{lineage_id}/branches/{branch}/definitions?message=` | Body: a BKN tar as bkn-backend exports it (`application/octet-stream`, at most 64 MiB). Replaces the branch definitions; `{"commit","changed":{"added","changed","removed"}}`. `main` is refused with 403 |
| POST | `/lineages/{lineage_id}/branches/{branch}/sync` | Brings `main` into the branch; `{"result":"up_to_date"\|"fast_forward"\|"merged","commit","summary"}`, 409 with the conflicts in `error_details` |
| POST | `/lineages/{lineage_id}/drift?commit=` | Body `{"kn_id"}`. Compares the live network with `main`; `{"lineage_id","kn_id","added","changed","removed","commit"}`. With `commit=true` the live definitions are committed to `main` |
| GET | `/lineages/{lineage_id}/switch-plan?to=` | What switching the lineage's consumers to network `to` would do: `{"lineage_id","from_network_id","to_network_id","schedules"}`, the active schedules of the network in use. Agents, MCP, workshop, eval and permissions on `to` are switched by their owners. 400 `InvalidSwitchTarget` (not a network of the lineage), 409 `AlreadyInUse` |
| POST | `/lineages/{lineage_id}/switches` | Body `{"to_network_id"}`. Turns each active schedule on on the target, where it runs as the caller, and off on the network in use, then records the switch: 201 with the switch. A rollback is a switch back to an earlier network; the counterparts it left off are turned back on |

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
