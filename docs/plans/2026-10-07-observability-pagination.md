# Observability numbered pagination

## Problem and decision

Trace and log HTTP validation previously rejected page numbers above 100.
Removing that validation exposed an existing log-service loop that replayed every
preceding cursor page. A request for page 181 could therefore issue 181 source
searches; using larger skip batches still required 19 sequential searches.

The deployed log service uses the centralized MariaDB audit ledger with
`OperationAuditOnly: true`. Follow the business-provenance list strategy: count
the matching dataset and directly select the requested slice with LIMIT/OFFSET.
Apply public filters, valid operation-audit projection predicates, and per-record
access scope before both COUNT and pagination. Retain authorization checks on
returned records as a second boundary.

The ledger is partitioned by occurred-at UTC month. Count matching months in
descending order, skip entire months using their counts, and query only the
month(s) containing the requested page. The existing maximum 30-day log window
bounds the number of monthly queries independently of the page number. Deep
OFFSET and COUNT still have database costs; all queries share the configured
source timeout and caller cancellation. No deployment or schema changes are
required.

Reject page sizes below 20 for both `page_size` and its `limit` alias, retaining
the upper bound of 200. The Studio log selector exposes 20/50/100/200. Reject
numbered jumps for sources without direct pagination instead of replaying
cursor pages; those sources retain cursor access. An oversized page beyond the
matching total returns an empty page after counting, without executing a deep
OFFSET. Compare page numbers with the total before multiplying offsets.

Cursor requests use the same ledger order and filters, and ignore numbered
page offsets. Their count describes the matching rows remaining after the
cursor boundary. Existing first-page counts become exact for the ledger.

## Related pages

Business provenance already uses direct database paging without a 100-page
limit; its UI requests 20 rows. Its SQL OFFSET has normal database cost, but it
does not replay earlier application pages. System audit also uses direct
LIMIT/OFFSET. Neither page needs the log-service traversal fix.

## Execution checklist

- [x] Remove the trace/log page-100 validation and guard summary integer overflow.
- [x] Reproduce the sequential-source-query failure with counting-source tests.
- [x] Replace log page replay with direct ledger pagination and timeout propagation.
- [x] Cover page 101/181/500, out-of-range and MaxInt pages, cursor continuation,
      caller cancellation, cross-month pages, missing monthly tables, actor
      search, and network-builder scope before COUNT/OFFSET.
- [x] Align log page-size validation and Studio page-size choices.
- [x] Complete final backend and affected frontend verification: full backend tests,
      focused final regressions, lint/vet/build/license/Swagger checks; Studio
      42 affected-file tests, typechecked lint, typecheck/build, formatting,
      license checks and production dependency audit. The initial concurrent
      frontend run timed out in five settings tests; the isolated rerun passed
      all 42 cases with the unchanged 10-second test timeout.
- [x] Requester explicitly authorized committing/pushing this review revision
      and directed subsequent revisions to proceed without repeated confirmation.
- [x] Push the backend revision and update PR #2035 in English; create companion
      Studio PR [#831](https://github.com/openbkn-ai/bkn-studio/pull/831),
      closing Studio issue #830. Local validation passed; remote CI is running.
- [ ] Validate the deployed Studio with other issues, as requested by the user.
