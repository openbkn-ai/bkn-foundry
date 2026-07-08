// authz-shadow: a shadow-comparison harness for the ISF -> bkn-safe authz
// cutover. Fires the same authorization requests at ISF's operation-check and
// bkn-safe's check, diffs the decisions, and reports divergence. stdlib only.
module bkn-safe/cmd/authz-shadow

go 1.25.0
