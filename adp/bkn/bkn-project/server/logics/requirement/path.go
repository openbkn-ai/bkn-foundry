// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"slices"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// hop is an adopted relation type read from one of its ends: from the object type it leaves to the
// one it reaches.
type hop struct {
	relation *interfaces.DraftRelationType
	from     string
	to       string
}

// draftGraph is the draft's adopted relation types between object types, each end's in the
// draft's order: the paths a question can follow.
type draftGraph map[string][]hop

func graphOf(draft *interfaces.Draft) draftGraph {
	g := draftGraph{}
	if draft == nil {
		return g
	}
	for _, rt := range draft.Items.RelationTypes {
		if !rt.Adopted || rt.Source == rt.Target {
			continue
		}
		g[rt.Source] = append(g[rt.Source], hop{relation: rt, from: rt.Source, to: rt.Target})
		g[rt.Target] = append(g[rt.Target], hop{relation: rt, from: rt.Target, to: rt.Source})
	}
	return g
}

// route is the shortest way from one object type to another over at most two relations: a
// relation between them, else one through an object type between, the first in the draft's order.
// Nil when there is none.
func (g draftGraph) route(from string, to string, allow func(hop) bool) []hop {
	for _, h := range g[from] {
		if h.to == to && allow(h) {
			return []hop{h}
		}
	}
	for _, first := range g[from] {
		if first.to == to || !allow(first) {
			continue
		}
		for _, second := range g[first.to] {
			if second.to == to && allow(second) {
				return []hop{first, second}
			}
		}
	}
	return nil
}

// questionPath is what a question rests on in the draft beyond the items it names: the relation
// types, and object types between, that join the object types it needs, and the pairs of them no
// adopted path of at most two relations joins. A question needs its object types joined, all of
// them through one another or not: they are judged as groups, so that A joined to B and to C joins
// B and C too. Pairs are tried in the order the question names them. The path is derived from the
// draft whenever needs are read, never stored; needs a person set are theirs, and none is derived.
func questionPath(q *interfaces.RequirementQuestion, draft *interfaces.Draft) ([]interfaces.RequirementItemRef,
	[]interfaces.ScenarioUnconnected) {
	path, unconnected := []interfaces.RequirementItemRef{}, []interfaces.ScenarioUnconnected{}
	if q.NeedsSource == interfaces.REQUIREMENT_NEEDS_FROM_PERSON || draft == nil {
		return path, unconnected
	}
	drafted := map[string]bool{}
	for _, ot := range draft.Items.ObjectTypes {
		drafted[ot.ID] = true
	}
	types := []string{}
	group := map[string]int{}
	for _, need := range q.Needs {
		if need.Kind == interfaces.REQUIREMENT_ITEM_OBJECT_TYPE && drafted[need.ID] && !slices.Contains(types, need.ID) {
			group[need.ID] = len(types)
			types = append(types, need.ID)
		}
	}
	join := func(a string, b string) {
		from, to := group[a], group[b]
		for t, g := range group {
			if g == to {
				group[t] = from
			}
		}
	}
	named := map[interfaces.RequirementItemRef]bool{}
	for _, need := range q.Needs {
		named[need] = true
	}
	// A relation type the question names joins its ends, once adopted: one left out joins nothing.
	for _, rt := range draft.Items.RelationTypes {
		if rt.Adopted && named[interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_RELATION_TYPE, ID: rt.ID}] {
			if _, ok := group[rt.Source]; ok {
				if _, ok := group[rt.Target]; ok {
					join(rt.Source, rt.Target)
				}
			}
		}
	}
	add := func(ref interfaces.RequirementItemRef) {
		if !named[ref] {
			named[ref] = true
			path = append(path, ref)
		}
	}
	g := graphOf(draft)
	adopted := func(hop) bool { return true }
	for i, a := range types {
		for _, b := range types[i+1:] {
			if group[a] == group[b] {
				continue
			}
			r := g.route(a, b, adopted)
			if r == nil {
				continue
			}
			for k, h := range r {
				if k > 0 {
					add(interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: h.from})
				}
				add(interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_RELATION_TYPE, ID: h.relation.ID})
			}
			join(a, b)
		}
	}
	// Each group apart from the first type's is named by its first type, with the first type.
	reported := map[int]bool{}
	for _, t := range types[min(1, len(types)):] {
		if g := group[t]; g != group[types[0]] && !reported[g] {
			reported[g] = true
			unconnected = append(unconnected, interfaces.ScenarioUnconnected{From: types[0], To: t})
		}
	}
	return path, unconnected
}
