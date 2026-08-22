// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"strings"

	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

// Operation implications (#1121) close a gap that no enforcement change could:
// resource_manage on a catalog is worthless without view_detail on the same
// catalog, because every management route loads its target first and that load
// is a view_detail judgement. A grant carrying only resource_manage produces a
// 403 that names view_detail — an operator reads it as a bug in the check
// rather than as a hole in the grant they just made.
//
// The rule is applied where grants are WRITTEN, not where they are read. A
// grant is therefore stored complete: the policy table says what the accessor
// holds, and no reader has to replay an implication to know it. It also means
// existing grants are untouched by a deployment picking this version up —
// re-saving one is what repairs it.

// impliedOps expands ops with everything they imply on the same resource type,
// transitively. Order is preserved and the original ops come first, so an
// audit record still reads as "what was asked for, plus what came with it".
//
// A type with no implications (every type but catalog today) returns the input
// unchanged after one query.
func impliedOps(db *gorm.DB, resourceType string, ops []string) ([]string, error) {
	forward, _, err := implicationMaps(db, resourceType)
	if err != nil {
		return nil, err
	}
	return closure(ops, forward), nil
}

// impliedBy expands ops with everything that IMPLIES them, transitively — the
// reverse direction, used when revoking. Dropping view_detail while leaving
// resource_manage behind would rebuild exactly the unusable grant this rule
// exists to prevent, so a revoke takes the implying operation with it.
func impliedBy(db *gorm.DB, resourceType string, ops []string) ([]string, error) {
	_, reverse, err := implicationMaps(db, resourceType)
	if err != nil {
		return nil, err
	}
	return closure(ops, reverse), nil
}

// implicationMaps reads the type's operation rows once and returns the
// implication graph in both directions.
func implicationMaps(db *gorm.DB, resourceType string) (forward, reverse map[string][]string, err error) {
	var rows []model.Operation
	if err := db.Where("resource_type_id = ? AND implied_operation_ids <> ''", resourceType).
		Find(&rows).Error; err != nil {
		return nil, nil, err
	}
	forward = make(map[string][]string, len(rows))
	reverse = make(map[string][]string, len(rows))
	for _, row := range rows {
		for _, implied := range strings.Split(row.ImpliedOperationIDs, ",") {
			implied = strings.TrimSpace(implied)
			if implied == "" {
				continue
			}
			forward[row.ID] = append(forward[row.ID], implied)
			reverse[implied] = append(reverse[implied], row.ID)
		}
	}
	return forward, reverse, nil
}

// closure walks the graph breadth-first from ops. The seed refuses to boot on a
// cyclic implication graph, but the visited set makes this terminate anyway:
// a cycle reaching production must not hang a grant request.
func closure(ops []string, graph map[string][]string) []string {
	if len(graph) == 0 {
		return ops
	}
	out := make([]string, 0, len(ops)+len(graph))
	seen := make(map[string]bool, len(ops)+len(graph))
	queue := append([]string(nil), ops...)
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if seen[cur] {
			continue
		}
		seen[cur] = true
		out = append(out, cur)
		queue = append(queue, graph[cur]...)
	}
	return out
}
