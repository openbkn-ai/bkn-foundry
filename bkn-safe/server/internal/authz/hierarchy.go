// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package authz

import (
	"errors"
	"log/slog"
	"sort"
	"strings"

	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

// maxHierarchyDepth is a backstop, not a policy. The hierarchy is deliberately
// unbounded in depth (#800 decision 6) and per-climb cycle detection already
// terminates every walk; this only stops a pathological chain from turning one
// authorization decision into unbounded database work.
const maxHierarchyDepth = 64

// climber tracks one resource's walk up the hierarchy. ops maps an operation
// the CALLER asked about to the operation to look for at the node currently
// under examination — the two drift apart as the walk translates through each
// level's mapping ("modify" on a table becomes "resource_manage" on its
// catalog), and the caller's spelling is what the answer must be keyed by.
type climber struct {
	origin  ResourceRef
	node    ResourceRef
	ops     map[string]string
	visited map[ResourceRef]bool
}

// climb resolves, for a batch of resources, which of the still-missing
// operations the accessor holds through an ancestor.
//
// decide answers "which of these ops does the accessor hold on this one node".
// Passing it in is what lets the single-decision path (casbin Enforce) and the
// list-page path (the pre-resolved grant index) share one walk: the two must
// never disagree about inheritance, and the only way to guarantee that is for
// them to run the same code.
//
// The returned map contains only operations that were found — resources with
// nothing inherited are absent, so a caller reads it as an overlay on what it
// already decided.
func (en *Enforcer) climb(
	decide func(ResourceRef, []string) (map[string]bool, error),
	want map[ResourceRef][]string,
) (map[ResourceRef]map[string]bool, error) {
	if en.db == nil || len(want) == 0 {
		return nil, nil
	}
	active := make([]*climber, 0, len(want))
	for r, ops := range want {
		if len(ops) == 0 {
			continue
		}
		pending := make(map[string]string, len(ops))
		for _, op := range ops {
			pending[op] = op // at the resource itself, no translation has happened yet
		}
		active = append(active, &climber{
			origin: r, node: r, ops: pending,
			visited: map[ResourceRef]bool{r: true},
		})
	}

	found := map[ResourceRef]map[string]bool{}
	for level := 0; len(active) > 0 && level < maxHierarchyDepth; level++ {
		// The operation mapping is consulted BEFORE the ownership rows, and the
		// order matters for cost: a type that inherits nothing (every type but
		// one, today) is dismissed by a single indexed lookup on the operations
		// table, so a denied check on an unrelated resource never touches the
		// ownership table at all.
		opMaps := map[string]map[string]string{}
		for _, c := range active {
			if _, done := opMaps[c.node.Type]; done {
				continue
			}
			m, err := en.parentOpMap(c.node.Type)
			if err != nil {
				return nil, err
			}
			opMaps[c.node.Type] = m
		}
		inheriting := make([]*climber, 0, len(active))
		for _, c := range active {
			if len(opMaps[c.node.Type]) > 0 {
				inheriting = append(inheriting, c)
			}
		}
		active = inheriting
		if len(active) == 0 {
			break
		}
		parents, err := en.parentsOf(active)
		if err != nil {
			return nil, err
		}

		// Two resources under the same catalog asking about the same operations
		// are one decision, not two. On a list page that is the common case.
		verdicts := map[string]map[string]bool{}
		next := make([]*climber, 0, len(active))
		for _, c := range active {
			parent, ok := parents[c.node]
			if !ok {
				continue // top of the chain: nothing above to inherit from
			}
			if c.visited[parent] {
				// Only reachable if a type may nest in itself and the pushed rows
				// form a loop. Stop this climb and say so — silently returning
				// "not allowed" would make a data problem look like a policy one.
				slog.Warn("resource hierarchy has a cycle; stopping the climb",
					"origin_type", c.origin.Type, "origin_id", c.origin.ID,
					"repeated_type", parent.Type, "repeated_id", parent.ID)
				continue
			}
			translated := map[string]string{}
			mapping := opMaps[c.node.Type]
			for asked, here := range c.ops {
				// No mapping entry means the operation stops here. Default is NOT
				// to inherit by the same name: a missing entry then costs a
				// permission, while same-name inheritance would grant one — and
				// "modify" means different things on a table and on its catalog.
				if up, ok := mapping[here]; ok && up != "" {
					translated[asked] = up
				}
			}
			if len(translated) == 0 {
				continue
			}

			ask := distinctSorted(translated)
			key := parent.Type + "\x00" + parent.ID + "\x00" + strings.Join(ask, "\x00")
			allowed, cached := verdicts[key]
			if !cached {
				allowed, err = decide(parent, ask)
				if err != nil {
					return nil, err
				}
				verdicts[key] = allowed
			}
			for asked, up := range translated {
				if !allowed[up] {
					continue
				}
				if found[c.origin] == nil {
					found[c.origin] = map[string]bool{}
				}
				found[c.origin][asked] = true
				delete(translated, asked)
			}
			if len(translated) == 0 {
				continue // everything this resource still wanted was answered here
			}
			c.node, c.ops = parent, translated
			c.visited[parent] = true
			next = append(next, c)
		}
		active = next
	}
	if len(active) > 0 {
		slog.Warn("resource hierarchy climb hit the depth backstop; deeper ancestors were not consulted",
			"depth", maxHierarchyDepth, "unfinished", len(active))
	}
	return found, nil
}

// parentsOf loads the single-hop parent of every node the climbers currently
// sit on, batched by type so a list page costs one query per type per level
// rather than one per resource.
func (en *Enforcer) parentsOf(active []*climber) (map[ResourceRef]ResourceRef, error) {
	byType := map[string][]string{}
	seen := map[ResourceRef]bool{}
	for _, c := range active {
		if seen[c.node] {
			continue
		}
		seen[c.node] = true
		byType[c.node.Type] = append(byType[c.node.Type], c.node.ID)
	}
	out := make(map[ResourceRef]ResourceRef, len(seen))
	for rtype, ids := range byType {
		var rows []model.ResourceParent
		if err := en.db.Where("resource_type_id = ? AND resource_id IN ?", rtype, ids).
			Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			out[ResourceRef{Type: row.ResourceTypeID, ID: row.ResourceID}] =
				ResourceRef{Type: row.ParentTypeID, ID: row.ParentID}
		}
	}
	return out, nil
}

// parentOpMap returns the type's operation translation: operation on this type
// -> operation to look for on its parent. Operations that do not inherit are
// absent from the map.
func (en *Enforcer) parentOpMap(resourceType string) (map[string]string, error) {
	var rows []model.Operation
	if err := en.db.Where("resource_type_id = ? AND parent_operation_id <> ''", resourceType).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		out[row.ID] = row.ParentOperationID
	}
	return out, nil
}

// childIDChunk bounds the size of one "children of these parents" IN clause.
// An accessor holding many catalogs would otherwise produce a single statement
// with thousands of bind parameters.
const childIDChunk = 500

// inheritedResources lists the instances of resourceType the accessor reaches
// only through an ancestor — the enumeration counterpart of the climb that
// Check performs. Where Check walks UP from one resource, this walks DOWN from
// the ancestors the accessor can act on, because the question is inverted:
// "which tables may I touch" rather than "may I touch this table".
//
// visitedTypes keeps the recursion finite. The type graph is validated acyclic
// when the catalog is seeded, so this is a second belt rather than the only one.
func (en *Enforcer) inheritedResources(accessorID, resourceType, op string, visitedTypes map[string]bool) ([]string, error) {
	if en.db == nil || visitedTypes[resourceType] {
		return nil, nil
	}
	visitedTypes[resourceType] = true

	mapping, err := en.parentOpMap(resourceType)
	if err != nil {
		return nil, err
	}
	parentOp, inherits := mapping[op]
	if !inherits {
		return nil, nil
	}
	parentType, err := en.parentTypeOf(resourceType)
	if err != nil || parentType == "" {
		return nil, err
	}

	// A type-wide grant on the parent ("every catalog") reaches every instance
	// that has a parent at all. It has to be handled separately because
	// AccessibleResources deliberately reports concrete instances only, so the
	// recursive call below would return nothing for it.
	wide, err := en.e.Enforce(accessorID, obj(parentType, "*"), parentOp)
	if err != nil {
		return nil, err
	}
	if wide {
		return en.childrenOf(resourceType, parentType, nil)
	}

	parents, err := en.accessibleResources(accessorID, parentType, parentOp, visitedTypes)
	if err != nil {
		return nil, err
	}
	if len(parents) == 0 {
		return nil, nil
	}
	return en.childrenOf(resourceType, parentType, parents)
}

// childrenOf lists the ids of resourceType sitting under the given parents. A
// nil parent list means "under any parent of that type".
func (en *Enforcer) childrenOf(resourceType, parentType string, parentIDs []string) ([]string, error) {
	q := en.db.Model(&model.ResourceParent{}).
		Where("resource_type_id = ? AND parent_type_id = ?", resourceType, parentType)
	if parentIDs == nil {
		var ids []string
		if err := q.Pluck("resource_id", &ids).Error; err != nil {
			return nil, err
		}
		return ids, nil
	}
	out := make([]string, 0, len(parentIDs))
	for start := 0; start < len(parentIDs); start += childIDChunk {
		end := min(start+childIDChunk, len(parentIDs))
		var ids []string
		if err := en.db.Model(&model.ResourceParent{}).
			Where("resource_type_id = ? AND parent_type_id = ? AND parent_id IN ?",
				resourceType, parentType, parentIDs[start:end]).
			Pluck("resource_id", &ids).Error; err != nil {
			return nil, err
		}
		out = append(out, ids...)
	}
	return out, nil
}

// parentTypeOf returns the type this one hangs under, or "" when it is a root.
func (en *Enforcer) parentTypeOf(resourceType string) (string, error) {
	var rt model.ResourceType
	err := en.db.First(&rt, "id = ?", resourceType).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return rt.ParentTypeID, nil
}

// distinctSorted returns the map's distinct values in a stable order, so the
// per-node decision cache key is the same for two callers asking the same thing.
func distinctSorted(m map[string]string) []string {
	seen := make(map[string]bool, len(m))
	out := make([]string, 0, len(m))
	for _, v := range m {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// enforceOn is the single-decision evaluator handed to climb: it asks casbin
// about one ancestor node, exactly as Check asks about the resource itself.
func (en *Enforcer) enforceOn(accessorID string) func(ResourceRef, []string) (map[string]bool, error) {
	return func(node ResourceRef, ops []string) (map[string]bool, error) {
		out := make(map[string]bool, len(ops))
		for _, op := range ops {
			ok, err := en.e.Enforce(accessorID, obj(node.Type, node.ID), op)
			if err != nil {
				return nil, err
			}
			if ok {
				out[op] = true
			}
		}
		return out, nil
	}
}

// inheritedOps reports which of the missing operations the accessor holds on an
// ancestor of the resource. The single-resource entry point behind Check and
// AllowedOps.
func (en *Enforcer) inheritedOps(accessorID, resourceType, resourceID string, missing []string) (map[string]bool, error) {
	if len(missing) == 0 {
		return nil, nil
	}
	r := ResourceRef{Type: resourceType, ID: resourceID}
	found, err := en.climb(en.enforceOn(accessorID), map[ResourceRef][]string{r: missing})
	if err != nil {
		return nil, err
	}
	return found[r], nil
}
