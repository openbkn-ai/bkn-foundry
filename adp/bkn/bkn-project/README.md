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
  ones over a field of few values (fewer than `draft.minInferredDistinct`, 10 by
  default) are proposed, not adopted. A relation proposed rather than adopted
  says why in `proposed_because`: `not_counted`, `below_suggest_hit_rate`,
  `below_adopt_hit_rate`, `few_values` or `end_not_adopted` (an object type at an
  end waits for a person). Personal fields
  (privacy keywords matched as whole words) are excluded. Names and descriptions
  from the catalog's semantic understanding come first. Items are named in the
  project's naming language: of the candidate names (the name semantic
  understanding gave, the first clause of the description and of the database
  comment) the first in that language, a name holding a Han character reading
  as Chinese; with none in it, the first. The physical name names an item only
  when it has no candidate: a name people read beats an abbreviation. The
  candidates in the other language, never the physical name, become `aliases` of object
  types, properties and relation types, leaving out those BKN refuses: one
  holding a comma, or another property's name or display name. Ids come from physical
  table and field names only, never from names semantic understanding wrote, so
  they do not change with language or with understanding run again: words
  joined by `_`, camelCase split (`customerId` gives `customer_id`). A physical
  name that is not ASCII, which BKN ids cannot hold, takes the English short name
  semantic understanding gave the table or field (`api_name`); until there is
  one, its kind's prefix and a short hash of the name stand in (`ot_…`, `p_…`,
  `vt_…`, `rt_…`). Such ids are registered with the project and every later
  draft gives them again; a placeholder gives way to a short name until a
  confirmation locks it. Registered ids keep their names when a new table
  would clash with them. Name matching
  compares physical names as written. Only counts and
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
| POST | `/projects` | Body `{"name","kn_id"\|"new_kn":{"name","id"},"catalog_ids","naming_language"}`; `naming_language` (`zh-CN` or `en-US`, else 400 `Project.InvalidNamingLanguage`) is the language the project names its items in, by default the caller's language; exactly one of `kn_id` (an existing network the caller can see) and `new_kn` (a network to create). A released network joins the lineage of its base network. Starts the lineage unless it has one and creates the branch `project/<id>`; 201 with the project. The caller owns it |
| GET | `/projects?archived=&name=&limit=&cursor=` | Projects of the networks the caller can see, newest first; cursor paging. `archived` is `false` (default), `true` or `all`; `name` matches names containing it. Each entry has `other_projects`, the other unarchived projects of its network |
| GET | `/projects/{project_id}` | The project; 404 also when the caller cannot see its network |
| POST | `/projects/{project_id}:archive`, `:unarchive` | Hides the project from the default list, or shows it again; only its owner may (403). Releases are not affected |
| PUT | `/projects/{project_id}/catalogs` | Replaces the data catalogs the project drafts from. Body `{"catalog_ids"}` (required, may be empty; ids de-duplicated). Only its owner may (403). The draft is kept; drafting again reads the new catalogs. 400 `Project.InvalidRequestBody`, `Project.InvalidCatalogIDs` |
| PUT | `/projects/{project_id}/naming-language` | Sets the language the project names its ontology items in. Body `{"naming_language"}`: `zh-CN` or `en-US`. Only its owner may (403). The draft keeps its names; drafting again names in the new language. Projects made before naming languages name in `zh-CN`. 400 `Project.InvalidRequestBody`, `Project.InvalidNamingLanguage` |
| GET | `/projects/{project_id}/status` | Where the project stands, computed for the caller from its draft, confirmation and check; nothing of it is stored, and the same data gives the same reply but for `computed_at` and what the caller may do. `stage`: `archived`, `no_draft`, `drafting`, `draft_failed`, `deciding`, `ready_to_confirm`, `check_not_started`, `checking`, `check_needs_attention` or `confirmed`. `blockers` (`code`, `count`, `waiting_for`: `owner`, `editor` or `system`): `no_catalogs`, `needs_identity`, `unadopted_ends`, `nothing_adopted`, `branch_not_v3`, `lineage_not_v3`, `definitions_exist` (read ahead from the project branch for a draft ready to confirm), `check_issues`, `check_partial`, `check_failed`. `undecided` counts the items drafting left out and no one decided on (listed in `pending`). `draft.placeholders` counts the ids in the draft of names that are not ASCII still standing in for a short name; running semantic understanding on their catalog and drafting again gives them one. A placeholder a confirmation locked stays and is not counted. It does not block. `stale`: `confirmation_outdated`, `check_outdated`. `next_actions`: requests to send — `action` (`unarchive`, `set_catalogs`, `generate_draft`, `adopt_items`, `confirm_draft`, `run_check`, `sync_branch`, `commit_drift`, `wait`), `method`, `path` relative to the API root, `body` template, `allowed` for the caller with `reason` when not (`not_owner`, `running` or a blocker code), `retry_after_s` for `wait`, `discards_decisions` for drafting again, `accepts` for a confirmation that accepts what drafting could not read (`partial_draft`) or leaving undecided items out (`undecided_items`). `links` to the draft, its pending items and the check. Codes are for machines; clients word them. 404 as `GET /projects/{project_id}` |
| GET | `/projects/{project_id}/pending` | What waits for a person before the draft can be confirmed — the items confirming checks for (`needs_identity`, `unadopted_ends`), by kind then item ID — each with `options`, requests that settle it: `choose_identity` (with `candidates`: the property drafting counted first, then the other key-typed properties by name; send one in place of `$candidate`; not `allowed`, `reason` `no_key_property`, when no property can identify records), `adopt_end` (not `allowed`, `reason` `end_needs_identity`, while that object type waits for its identity), `unadopt`; these are `blocking`. Then, not blocking, the items drafting left out and no one decided on (`reason` `undecided`, `because`: `needs_identity`, a relation's `proposed_because`, or `not_adopted`), with `adopt` (not `allowed` while the object type waits for its identity or a relation's ends are not adopted) and, for an object type without an identity, `choose_identity`. Bodies carry `$draft_update_time`: send the draft's update time as last read; each edit moves it on. `recommendation` is null until a written rule recommends an option. `evidence` is what drafting counted. A draft not ready lists nothing; 404 `Draft.NotFound` without a draft |
| POST | `/projects/{project_id}/ontology/draft:generate` | Drafts the ontology from the tables of the project's data catalogs, in the background: 202 with the draft, `drafting`. Optional body `{"discard_decisions": true}` agrees to discard the decisions people made on the current draft. 400 `Draft.NoCatalogs`; 409 `Draft.Drafting` while drafting, `Draft.HasDecisions` when the draft has decisions and they are not discarded (a decision on the draft as a whole, `accept_partial`, does not count: drafting again drops it, and the new draft is judged partial anew), `Draft.ProjectArchived`, `Draft.CatalogMissing` when a data catalog of the project no longer exists (vega-backend 404; `error_details` lists them; drafting would otherwise find no tables in it). A catalog the caller may not view in detail (403) still drafts from the tables the caller can read |
| GET | `/projects/{project_id}/ontology/draft` | The draft: `status` (`drafting`, `ready`, `failed` with `error`: `interrupted`, `timeout` or vega-backend's reply) and `items` — `object_types`, `relation_types`, `value_types`, `excluded`, `unreadable`, each with its evidence and whether it is adopted, `decisions`, the changes people made, oldest first, and `inputs`, what the draft was made from — `rules_version`, the rule `settings`, the `naming_language` it named in, `catalog_ids`, each table read with a `fingerprint` of its structure (SHA-256; no data values) and its `rows`, `partial` when some tables could not be read, and `ids` when the project has registered ids for names that are not ASCII: their `count`, how many in the draft are still replaceable `placeholders`, and a `fingerprint` of them. 404 `Draft.NotFound` before the first draft |
| PATCH | `/projects/{project_id}/ontology/draft/items/{kind}/{item_id}` | Edits one item of a ready draft; `kind` is `object_types`, `relation_types` or `value_types`. Body: `update_time` of the draft the edit was made on, and any of `adopted`, `name` (1–128 characters), `description` (up to 1000). Each change is recorded as a decision with what it replaced; not adopting an object type stops adopting the relation types that connect it. Replies the draft. 400 `Draft.MissingUpdateTime`, `Draft.EmptyEdit`, `Draft.NeedsIdentity` (an object type adopted before its identity is chosen), `Draft.InvalidName`, `Draft.InvalidDescription`, `Draft.EndpointNotAdopted` (a relation type whose object types are not both adopted); 404 `Draft.ItemNotFound`; 409 `Draft.Stale` (changed since `update_time`; reload), `Draft.NotReady`, `Draft.ProjectArchived` |
| PUT | `/projects/{project_id}/ontology/draft/object_types/{item_id}/identity` | Chooses the properties that identify an object type's records. Body: `update_time` and `properties`, one or more distinct properties of type `string`, `text`, `integer` or `unsigned integer`. A single-property key is counted over the full table as the caller and kept even with duplicates or nulls, its counts in `key` (`source`: `chosen`); a composite key is not counted. Clears `needs_identity`; adopting stays a separate edit. 400 `Draft.MissingUpdateTime`, `Draft.InvalidIdentity`; 404 `Draft.ItemNotFound`; 409 as for editing |
| POST | `/projects/{project_id}/ontology/draft:restore` | Maps a field the privacy rules excluded back to a property, reading the field from vega-backend as the caller. Body: `update_time`, `object_type`, `field`. 400 `Draft.MissingUpdateTime`, `Draft.NotRestorable` (excluded for its type); 404 `Draft.ItemNotFound`; 409 `Draft.FieldNotFound` (no longer in its table, or not readable), and as for editing |
| POST | `/projects/{project_id}/ontology/draft:confirm` | Confirms a ready draft: its adopted items are written as BKN 3.0 definitions and committed to the project branch, beside what the branch holds. Aliases BKN would refuse as the items are named now, or that name an object type the branch keeps, are left out of the definitions; the draft keeps them. The registered ids of what it writes are locked first, so they never change again; a failure to lock them writes nothing, and a confirmation refused afterwards unlocks the ids it locked. Replies the draft with `items.confirmation` (`commit`, `by`, `time`, `files`, `skipped_value_types`). The draft is confirmed while `items.confirmation.update_time` equals its `update_time`: any later edit moves the draft on, and confirming again replaces or removes what the last confirmation wrote (`files`). Nothing else on the branch is replaced. Drafting again keeps `items.confirmation`, unconfirmed, so a later confirmation can still replace or remove those files. Its commit is then checked in the background (`ontology/check`). Body `{"update_time","accept_partial","accept_undecided"}`: items drafting left out and no one decided on (see `pending`) are left out only with `accept_undecided: true`, recorded as an `accept_undecided` decision listing them as `kind/item_id`, which decides them; without it 409 `Draft.UndecidedNotAccepted` (`error_details`: those items). a draft that could not read every table (`inputs.partial`) is confirmed only with `accept_partial: true`, recorded as an `accept_partial` decision listing those tables; without it 409 `Draft.PartialNotAccepted` (`error_details`: the tables' resource IDs). Only the owner: 403 `Draft.NotOwner`. 400 `Draft.MissingUpdateTime`, `Draft.NothingAdopted`, `Draft.InvalidItem`; 409 `Draft.HasPending` (`error_details`: adopted object types still `needs_identity`, adopted relations whose ends are not both adopted), `Draft.Stale`, `Draft.NotReady`, `Draft.ProjectArchived`, `Lineage.DefinitionsExist` (a definition of the same path is already on the branch, listed), `Lineage.BranchNotV3` (sync the branch first), `Lineage.LineageNotV3` (main is 2.x too: commit its drift, then sync); 400 `Lineage.InvalidDefinitions` when the definitions fail the BKN checks with the branch (`error_details`: `table`, `column`, `code`, `message`). A refused confirmation leaves the draft as it was: once the cause is fixed, confirm again with the same `update_time`. A value type BKN 3.0 would refuse (such as a `text` base type) is left out, listed in `skipped_value_types`; its properties keep their base type |
| GET | `/projects/{project_id}/ontology/check` | The check of the last confirmation, run in the background once it is confirmed, as the person who confirmed: `commit`, `status` (`running`, `passed`, `issues`, `partial`, or `failed` with `error` `interrupted` when the service restarted), `attempt`, `runner`, `start_time`, `finish_time` and `results` — `keys` (each object type's identity: `rows`, `duplicates`, `nulls`), `relations` (each relation's `joins`, one per end it reaches: `rows`, `nulls`, `orphans`, `hit_rate`) `excluded_mapped` (properties mapping a field the privacy rules excluded) and `coverage` (`counted` of `total` identities and joins). Each item has a `state`: `counted`, `not_counted` with a `reason` (`cross_catalog`, `several_fields`, `unmapped`, `no_key`), or `failed` (`query_failed`, `timeout`). `issues` means duplicate or missing identities or an excluded field mapped; `partial` means nothing wrong was found but not every item was counted, so it is never `passed`; orphans are reported, not judged. A failed attempt is retried once, counting only what failed. The results are hints and never undo the confirmation. 404 `Check.NotFound` before the first check |
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
