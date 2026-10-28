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
  named `<base name> · <version>` (ids cut to 128 characters, names to 128
  characters), tagged `v<version>` in git.
- Projects (`t_bkn_project_project`): each changes one network on its own
  branch `project/<id>` of the network's lineage; projects of one network
  share its lineage. A project is visible to callers who can see its network.
- Ontology drafts (`t_bkn_project_draft`): drafting a project's ontology from the
  table structure of its data catalogs, by rules. Entity tables become object
  types with their keys checked by full-table counts; code tables become enums;
  link tables become many-to-many relations; declared foreign keys, and fields
  named for another table's key (also with a role in front, such as
  parent_lot_id), become relations checked by a full-table hit rate. A table
  without a declared key takes an identity-like field that counts unique and
  non-null. Inferred relations that cannot be counted are proposed unverified;
  ones over a field of few values are proposed, not adopted. Personal fields
  (privacy keywords matched as whole words) are excluded. Names and descriptions
  from the catalog's semantic understanding come first. Only counts and
  code-table values leave vega-backend, which is read as the caller.
- Health and readiness endpoints.

## API

Prefix: `/api/bkn-project/v1`. All routes except `/health` and `/ready` need an
OAuth token, verified through Hydra like bkn-backend's external routes. Exports
from bkn-backend run with the caller's token, so its permissions apply.

Every lineage is kept in BKN 3.0: exports ask bkn-backend for `bkn_spec=3.0`, so
`main`, the project branches and what a release reads back share the 3.0 rules,
which hold the value types a draft writes. A network bkn-backend cannot export
as 3.0 is refused with 400, `error_details` naming what to fix, and nothing is
created: `BknProject.Lineage.NetworkNeedsCheck` for action parameters its network
check flags, `BknProject.Lineage.NetworkNotReadableAsV3` for a definition that
does not read back under the 3.0 rules. A lineage
started in 2.x moves to 3.0 when its drift is committed, and its branches when
they sync.

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
| POST | `/projects` | Body `{"name","kn_id"\|"new_kn":{"name","id"},"catalog_ids"}`, exactly one of `kn_id` (an existing network the caller can see) and `new_kn` (a network to create). A released network joins the lineage of its base network. Starts the lineage unless it has one and creates the branch `project/<id>`; 201 with the project. The caller owns it |
| GET | `/projects?archived=&name=&limit=&cursor=` | Projects of the networks the caller can see, newest first; cursor paging. `archived` is `false` (default), `true` or `all`; `name` matches names containing it. Each entry has `other_projects`, the other unarchived projects of its network |
| GET | `/projects/{project_id}` | The project; 404 also when the caller cannot see its network |
| POST | `/projects/{project_id}:archive`, `:unarchive` | Hides the project from the default list, or shows it again; only its owner may (403). Releases are not affected |
| PUT | `/projects/{project_id}/catalogs` | Replaces the data catalogs the project drafts from. Body `{"catalog_ids"}` (required, may be empty; ids de-duplicated). Only its owner may (403). The draft is kept; drafting again reads the new catalogs. 400 `Project.InvalidRequestBody`, `Project.InvalidCatalogIDs` |
| POST | `/projects/{project_id}/ontology/draft:generate` | Drafts the ontology from the tables of the project's data catalogs, in the background: 202 with the draft, `drafting`. Optional body `{"discard_decisions": true}` agrees to discard the decisions people made on the current draft. 400 `Draft.NoCatalogs`; 409 `Draft.Drafting` while drafting, `Draft.HasDecisions` when the draft has decisions and they are not discarded, `Draft.ProjectArchived`, `Draft.CatalogMissing` when a data catalog of the project no longer exists (vega-backend 404; `error_details` lists them; drafting would otherwise find no tables in it). A catalog the caller may not view in detail (403) still drafts from the tables the caller can read |
| GET | `/projects/{project_id}/ontology/draft` | The draft: `status` (`drafting`, `ready`, `failed` with `error`: `interrupted`, `timeout` or vega-backend's reply) and `items` — `object_types`, `relation_types`, `value_types`, `excluded`, `unreadable`, each with its evidence and whether it is adopted, and `decisions`, the changes people made, oldest first. 404 `Draft.NotFound` before the first draft |
| PATCH | `/projects/{project_id}/ontology/draft/items/{kind}/{item_id}` | Edits one item of a ready draft; `kind` is `object_types`, `relation_types` or `value_types`. Body: `update_time` of the draft the edit was made on, and any of `adopted`, `name` (1–128 characters), `description` (up to 1000). Each change is recorded as a decision with what it replaced; not adopting an object type stops adopting the relation types that connect it. Replies the draft. 400 `Draft.MissingUpdateTime`, `Draft.EmptyEdit`, `Draft.NeedsIdentity` (an object type adopted before its identity is chosen), `Draft.InvalidName`, `Draft.InvalidDescription`, `Draft.EndpointNotAdopted` (a relation type whose object types are not both adopted); 404 `Draft.ItemNotFound`; 409 `Draft.Stale` (changed since `update_time`; reload), `Draft.NotReady`, `Draft.ProjectArchived` |
| PUT | `/projects/{project_id}/ontology/draft/object_types/{item_id}/identity` | Chooses the properties that identify an object type's records. Body: `update_time` and `properties`, one or more distinct properties of type `string`, `text`, `integer` or `unsigned integer`. A single-property key is counted over the full table as the caller and kept even with duplicates or nulls, its counts in `key` (`source`: `chosen`); a composite key is not counted. Clears `needs_identity`; adopting stays a separate edit. 400 `Draft.MissingUpdateTime`, `Draft.InvalidIdentity`; 404 `Draft.ItemNotFound`; 409 as for editing |
| POST | `/projects/{project_id}/ontology/draft:restore` | Maps a field the privacy rules excluded back to a property, reading the field from vega-backend as the caller. Body: `update_time`, `object_type`, `field`. 400 `Draft.MissingUpdateTime`, `Draft.NotRestorable` (excluded for its type); 404 `Draft.ItemNotFound`; 409 `Draft.FieldNotFound` (no longer in its table, or not readable), and as for editing |
| POST | `/projects/{project_id}/ontology/draft:confirm` | Confirms a ready draft: its adopted items are written as BKN 3.0 definitions and committed to the project branch, beside what the branch holds; replies the draft with `items.confirmation` (`commit`, `by`, `time`, `files`, `skipped_value_types`). The draft is confirmed while `items.confirmation.update_time` equals its `update_time`: any later edit moves the draft on, and confirming again replaces or removes what the last confirmation wrote (`files`). Nothing else on the branch is replaced. Drafting again keeps `items.confirmation`, unconfirmed, so a later confirmation can still replace or remove those files. Its commit is then checked in the background (`ontology/check`). Body `{"update_time"}`. Only the owner: 403 `Draft.NotOwner`. 400 `Draft.MissingUpdateTime`, `Draft.NothingAdopted`, `Draft.InvalidItem`; 409 `Draft.HasPending` (`error_details`: adopted object types still `needs_identity`, adopted relations whose ends are not both adopted), `Draft.Stale`, `Draft.NotReady`, `Draft.ProjectArchived`, `Lineage.DefinitionsExist` (a definition of the same path is already on the branch, listed), `Lineage.BranchNotV3` (sync the branch first), `Lineage.LineageNotV3` (main is 2.x too: commit its drift, then sync); 400 `Lineage.InvalidDefinitions` when the definitions fail the BKN checks with the branch (`error_details`: `table`, `column`, `code`, `message`). A refused confirmation leaves the draft as it was: once the cause is fixed, confirm again with the same `update_time`. A value type BKN 3.0 would refuse (such as a `text` base type) is left out, listed in `skipped_value_types`; its properties keep their base type |
| GET | `/projects/{project_id}/ontology/check` | The check of the last confirmation, run in the background once it is confirmed, as the person who confirmed: `commit`, `status` (`running`, `passed`, `issues`, or `failed` with `error` `interrupted` when the service restarted), `attempt`, `runner`, `start_time`, `finish_time` and `results` — `keys` (each object type's identity: `rows`, `duplicates`, `nulls`), `relations` (each relation's `joins`, one per end it reaches: `rows`, `nulls`, `orphans`, `hit_rate`) and `excluded_mapped` (properties mapping a field the privacy rules excluded). Each item has a `state`: `counted`, `not_counted` with a `reason` (`cross_catalog`, `several_fields`, `unmapped`, `no_key`), or `failed` (`query_failed`, `timeout`). `issues` means duplicate or missing identities or an excluded field mapped; orphans are reported, not judged. A failed attempt is retried once, counting only what failed. The results are hints and never undo the confirmation. 404 `Check.NotFound` before the first check |
| POST | `/projects/{project_id}/ontology/check:run` | Checks the last confirmation again, all of it, as the caller; replies 202 with the check, `running`. Only the owner: 403 `Check.NotOwner`. 409 `Draft.ProjectArchived`, `Check.NothingConfirmed`, `Check.Running` (that confirmation is being checked), `Check.ConfirmAgain` (the draft changed after it was confirmed and that confirmation was never checked: confirm again) |

## Local Development

```bash
cd server
go build ./...
go vet ./...
I18N_MODE_UT=true go test ./...
```

Configuration: `server/config/bkn-project-config.yaml`. The schema is created
by data-migrator from `migrations/bkn-project/`.

Drafting rules are under `draft` (defaults in brackets): `codeTableMaxRows` (500)
and `codeTableMaxColumns` (3) bound a code table; `adoptHitRate` (0.95) and
`suggestHitRate` (0.8) grade relations by hit rate; `privacyKeywords` match
personal fields by name or comment; `queryTimeout` (60 s) bounds each count and
`timeout` (10 min) the whole drafting. Drafting runs in the service process: a
restart fails drafts left drafting, as `interrupted`.

Regenerate mocks with `make generate-mock` (needs `mockgen` from
`go.uber.org/mock`).
