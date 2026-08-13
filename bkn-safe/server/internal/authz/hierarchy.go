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

// childIDChunk bounds one IN clause over resource ids — both "children of these
// parents" and the preview's "which of these already have a parent". An
// accessor holding many catalogs, or a full-snapshot preview, would otherwise
// produce a single statement with thousands of bind parameters.
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
	//
	// The result set is as large as the number of owned instances, and this
	// endpoint has never paginated. That is tolerable today because callers
	// probe the type-wide case FIRST (vega's resolveOps and the operator
	// integration both check obj="<type>:*" and skip enumeration when it holds),
	// so this branch is only reached by an accessor holding the wildcard on the
	// PARENT type but not on the type itself — which no seeded role is. #513
	// creates exactly that state if it revokes normal_user's resource:* while
	// leaving catalog:*, and the size question has to be answered there rather
	// than by quietly truncating here: a short list would read as "these are the
	// tables you may see".
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
	// accessibleResources reads GetImplicitPermissionsForUser, which covers the
	// matcher's g() half but NOT its "granted to everyone" half. Check's climb
	// goes through Enforce and sees both, so leaving it out here would make a
	// publicly granted catalog allow a table on the detail page and hide it from
	// the list — the exact divergence this slice exists to close.
	//
	// The union is applied to the ANCESTOR lookup only. accessibleResources is
	// blind to public grants for directly granted instances too, but that is
	// how this endpoint has always answered; widening it would change what
	// today's callers get for resource types that have nothing to do with the
	// hierarchy (the execution factory grants built-in toolboxes to everyone).
	public, err := en.publicInstances(parentType, parentOp)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(parents))
	for _, id := range parents {
		seen[id] = true
	}
	for _, id := range public {
		if !seen[id] {
			seen[id] = true
			parents = append(parents, id)
		}
	}
	if len(parents) == 0 {
		return nil, nil
	}
	return en.childrenOf(resourceType, parentType, parents)
}

// publicInstances lists the concrete instances of a type on which the "granted
// to everyone" accessor holds op. These are real policy rows with a fixed
// subject rather than role-derived ones, so casbin's implicit-permission read
// does not report them (filter.go's grantIndex reads them the same way).
func (en *Enforcer) publicInstances(resourceType, op string) ([]string, error) {
	rows, err := en.e.GetFilteredPolicy(0, PublicAccessorID)
	if err != nil {
		return nil, err
	}
	prefix := resourceType + ":"
	seen := map[string]bool{}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if len(row) < 3 {
			continue
		}
		o, act := row[1], row[2]
		if act != op && act != ActAll {
			continue
		}
		if len(o) <= len(prefix) || o[:len(prefix)] != prefix {
			continue
		}
		id := o[len(prefix):]
		if id == "*" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
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

// Ownership flip directions. A preview reports both because a synchroniser
// keys its "safe to push" decision on the total: counting only widening would
// let a re-parenting that REMOVES someone's access report zero.
const (
	FlipGrant  = "grant"
	FlipRevoke = "revoke"
)

// OwnershipFlip is one decision that would change if a proposed set of
// ownership rows were recorded: subject, resource, operation, and which way it
// moves. The subject is reported verbatim — it may be a user or a role, and
// reporting the role rather than expanding it keeps the report the size of the
// grant that causes the change rather than the size of that role's membership.
type OwnershipFlip struct {
	AccessorID string
	ResourceID string
	Operation  string
	Direction  string
}

// PreviewOwnership answers "who would gain and lose what" BEFORE any ownership
// row is written. It exists because recording ownership is the moment
// inheritance starts applying, and there is no flag to stage that (an
// allow-only engine has nothing to tighten), so the confirmation has to happen
// before the write rather than after it.
//
// links maps each proposed resource id to its proposed parent id. limit caps
// the returned slice; the total is always exact, so a caller can say "3 shown
// of 412" rather than implying the list is complete.
//
// Whether a subject reaches the proposed parent is decided by the SAME
// evaluation the enforcer uses, not by reading policy rows on that parent: the
// hierarchy has no depth limit, so a grant two levels up reaches the parent and
// therefore the child. A preview that only read the immediate parent would
// under-report, and a safety preview that under-reports is worse than none.
func (en *Enforcer) PreviewOwnership(resourceType, parentType string, links map[string]string, limit int) ([]OwnershipFlip, int, error) {
	if en.db == nil || len(links) == 0 {
		return nil, 0, nil
	}
	mapping, err := en.parentOpMap(resourceType)
	if err != nil {
		return nil, 0, err
	}
	if len(mapping) == 0 {
		return nil, 0, nil // nothing about this type inherits
	}
	childOps := make([]string, 0, len(mapping))
	for op := range mapping {
		childOps = append(childOps, op)
	}
	sort.Strings(childOps)
	parentOps := distinctSorted(mapping)

	children := make([]string, 0, len(links))
	parentSet := map[string]bool{}
	for child, parent := range links {
		children = append(children, child)
		parentSet[parent] = true
	}
	sort.Strings(children)
	parents := make([]string, 0, len(parentSet))
	for p := range parentSet {
		parents = append(parents, p)
	}
	sort.Strings(parents)

	childRefs := make([]ResourceRef, 0, len(children))
	for _, id := range children {
		childRefs = append(childRefs, ResourceRef{Type: resourceType, ID: id})
	}
	parentRefs := make([]ResourceRef, 0, len(parents))
	for _, id := range parents {
		parentRefs = append(parentRefs, ResourceRef{Type: parentType, ID: id})
	}

	// Which children are being MOVED. Only those can lose anything: a resource
	// that had no parent, or keeps the one it has, cannot stop reaching a grant
	// it already reaches.
	current, err := en.currentParents(resourceType, children)
	if err != nil {
		return nil, 0, err
	}
	moved := map[string]bool{}
	for child, proposed := range links {
		if now, ok := current[child]; ok && now != proposed {
			moved[child] = true
		}
	}

	// Candidates must include whoever reaches the OLD parent as well: those are
	// exactly the subjects a move takes access away from, and they may hold
	// nothing anywhere near the new one.
	lookIn := append([]ResourceRef{}, parentRefs...)
	seenOld := map[string]bool{}
	for child := range moved {
		old := current[child]
		if old == "" || seenOld[old] {
			continue
		}
		seenOld[old] = true
		lookIn = append(lookIn, ResourceRef{Type: parentType, ID: old})
	}
	subjects, err := en.previewCandidates(lookIn)
	if err != nil {
		return nil, 0, err
	}

	out := make([]OwnershipFlip, 0, limit)
	total := 0
	record := func(sub, child, op, dir string) {
		total++
		if len(out) < limit {
			out = append(out, OwnershipFlip{
				AccessorID: sub, ResourceID: child, Operation: op, Direction: dir,
			})
		}
	}

	for _, sub := range subjects {
		// What the subject may do on the proposed parents — through direct
		// grants, roles, wildcards, the grant-to-everyone subject, AND the
		// parents' own ancestors. This is the enforcer's answer, not a row scan.
		onParents, err := en.FilterResourceOps(sub, parentRefs, nil, parentOps)
		if err != nil {
			return nil, 0, err
		}
		heldOnParent := make(map[string]map[string]bool, len(onParents))
		for _, p := range onParents {
			set := make(map[string]bool, len(p.Operations))
			for _, op := range p.Operations {
				set[op] = true
			}
			heldOnParent[p.ID] = set
		}
		// What the subject may do on the children TODAY, including whatever the
		// current ownership rows already confer.
		onChildren, err := en.FilterResourceOps(sub, childRefs, nil, childOps)
		if err != nil {
			return nil, 0, err
		}
		have := make(map[string]map[string]bool, len(onChildren))
		for _, c := range onChildren {
			set := make(map[string]bool, len(c.Operations))
			for _, op := range c.Operations {
				set[op] = true
			}
			have[c.ID] = set
		}

		for _, child := range children {
			gained := map[string]bool{}
			for _, childOp := range childOps {
				if heldOnParent[links[child]][mapping[childOp]] {
					gained[childOp] = true
				}
			}
			for _, op := range childOps {
				if gained[op] && !have[child][op] {
					record(sub, child, op, FlipGrant)
				}
			}
			if !moved[child] {
				continue
			}
			// A move can take access away: what the subject holds today may have
			// come from the OLD parent, and the new one need not confer it. What
			// survives is the grant on the resource itself plus what the new
			// parent confers.
			for _, op := range childOps {
				if !have[child][op] || gained[op] {
					continue
				}
				direct, err := en.e.Enforce(sub, obj(resourceType, child), op)
				if err != nil {
					return nil, 0, err
				}
				if !direct {
					record(sub, child, op, FlipRevoke)
				}
			}
		}
	}
	return out, total, nil
}

// currentParents reads the ownership rows the proposed children have right now,
// so a re-parenting can be told apart from a first-time registration.
func (en *Enforcer) currentParents(resourceType string, children []string) (map[string]string, error) {
	out := map[string]string{}
	for start := 0; start < len(children); start += childIDChunk {
		end := min(start+childIDChunk, len(children))
		var rows []model.ResourceParent
		if err := en.db.Where("resource_type_id = ? AND resource_id IN ?", resourceType, children[start:end]).
			Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, r := range rows {
			out[r.ResourceID] = r.ParentID
		}
	}
	return out, nil
}

// previewCandidates narrows the subjects a preview has to evaluate. Every
// subject in the policy store would be correct but wasteful, so it keeps those
// whose object pattern can reach one of the proposed parents: an exact match, a
// wildcard pattern (which can match anything), or one of the parents' own
// ancestors (a grant up there reaches the parent through the same climb the
// enforcer performs). Being generous here is safe — the evaluation below is
// what decides; this only bounds the work.
func (en *Enforcer) previewCandidates(parentRefs []ResourceRef) ([]string, error) {
	reachable := map[string]bool{}
	for _, r := range parentRefs {
		reachable[obj(r.Type, r.ID)] = true
	}
	ancestors, err := en.ancestorObjects(parentRefs)
	if err != nil {
		return nil, err
	}
	for _, o := range ancestors {
		reachable[o] = true
	}

	rows, err := en.e.GetPolicy()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := make([]string, 0, 16)
	for _, row := range rows {
		if len(row) < 3 || seen[row[0]] {
			continue
		}
		object := row[1]
		hit := hasWildcard(object)
		if !hit {
			hit = reachable[object]
		}
		if !hit {
			continue
		}
		seen[row[0]] = true
		out = append(out, row[0])
	}
	sort.Strings(out)
	return out, nil
}

// ancestorObjects walks up from each ref and returns every ancestor's object
// key. Bounded by the same depth backstop as the enforce-time climb.
func (en *Enforcer) ancestorObjects(refs []ResourceRef) ([]string, error) {
	level := make([]*climber, 0, len(refs))
	for _, r := range refs {
		level = append(level, &climber{origin: r, node: r, visited: map[ResourceRef]bool{r: true}})
	}
	out := make([]string, 0, len(refs))
	for depth := 0; len(level) > 0 && depth < maxHierarchyDepth; depth++ {
		parents, err := en.parentsOf(level)
		if err != nil {
			return nil, err
		}
		next := make([]*climber, 0, len(level))
		for _, c := range level {
			parent, ok := parents[c.node]
			if !ok || c.visited[parent] {
				continue
			}
			out = append(out, obj(parent.Type, parent.ID))
			c.node = parent
			c.visited[parent] = true
			next = append(next, c)
		}
		level = next
	}
	return out, nil
}
