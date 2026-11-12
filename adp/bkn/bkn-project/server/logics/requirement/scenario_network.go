// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// Scenarios are drafted from the whole model: the object and relation types the draft holds, the
// network's own among them, and the kinetic items. Beside the specification's kinetic needs, the
// action types, functions, Skills and metrics the network the project started on already has go
// to scenario drafting as kinetic needs too, by networkItemPrefix and their place, so questions
// may rest on them. A question that does becomes a need of the specification linked to the item.

// scenarioNetworkItems are the items of the network the project started on that no kinetic need
// of base links to yet: those the specification's needs do not already stand for. None on a
// network the project created; a list that cannot be read leaves its items out, as scenarios are
// drafted all the same.
func (s *requirementService) scenarioNetworkItems(ctx context.Context, token string, lineageID string,
	base *interfaces.RequirementSpec) []*interfaces.LogicNetworkItem {
	if s.rla == nil || s.bba == nil {
		return nil
	}
	items, err := s.kineticNetworkItems(ctx, token, lineageID)
	if err != nil {
		logger.Warnf("Read the kinetic items of network %s for scenarios failed: %v", lineageID, err)
		return nil
	}
	return slices.DeleteFunc(items, func(item *interfaces.LogicNetworkItem) bool {
		return linkedNeed(base, item) != nil
	})
}

// linkedNeed is the kinetic need of spec linked to item, nil when none is.
func linkedNeed(spec *interfaces.RequirementSpec, item *interfaces.LogicNetworkItem) *interfaces.RequirementLogicNeed {
	for _, n := range spec.LogicNeeds {
		if item.Implements(n.Link) {
			return n
		}
	}
	return nil
}

// networkItemAt is the item a network id of the message names, by its place in items.
func networkItemAt(items []*interfaces.LogicNetworkItem, id string) *interfaces.LogicNetworkItem {
	rest, ok := strings.CutPrefix(strings.TrimSpace(id), networkItemPrefix)
	if !ok {
		return nil
	}
	i, err := strconv.Atoi(rest)
	if err != nil || i < 1 || i > len(items) {
		return nil
	}
	return items[i-1]
}

// needsOfNetworkItems gives each item of the network a question of out rests on a kinetic need of
// spec linked to it, by caller at now, and has the question name that need. It returns the needs it
// added, for leaveUnasked to take back those no question kept.
func needsOfNetworkItems(out *draftOutput, spec *interfaces.RequirementSpec, items []*interfaces.LogicNetworkItem,
	draft *interfaces.Draft, caller interfaces.AccountInfo, now int64) []*interfaces.RequirementLogicNeed {
	added := []*interfaces.RequirementLogicNeed{}
	adopted := adoptedRefs(draft)
	for i := range out.Questions {
		q := &out.Questions[i]
		for j, id := range q.LogicNeedIDs {
			item := networkItemAt(items, id)
			if item == nil {
				continue
			}
			need := linkedNeed(spec, item)
			if need == nil {
				need = &interfaces.RequirementLogicNeed{
					ID: nextID(spec, "n", spec.LogicNeeds, logicNeedID), Kind: networkNeedKind(item.Type),
					Name: item.Name, Definition: item.Description, ObjectTypeIDs: []string{},
					Properties: []interfaces.RequirementPropertyRef{}, ConceptIDs: []string{}, ScenarioIDs: []string{},
					Citations: []interfaces.RequirementCitation{}, Source: interfaces.LOGIC_NEED_FROM_SCENARIOS,
					Link: &interfaces.RequirementLogicLink{Type: item.Type, ID: item.ID, BoxID: item.BoxID, By: caller, At: now},
				}
				owner := interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: item.OwnerObjectTypeID}
				if item.OwnerObjectTypeID != "" && adopted[owner] {
					need.ObjectTypeIDs = append(need.ObjectTypeIDs, item.OwnerObjectTypeID)
				}
				spec.LogicNeeds = append(spec.LogicNeeds, need)
				added = append(added, need)
			}
			q.LogicNeedIDs[j] = keptPrefix + need.ID
		}
	}
	return added
}

// leaveUnasked takes out of spec the needs of added that no question of it rests on: a question
// drafting dropped does not leave the need it named behind.
func leaveUnasked(spec *interfaces.RequirementSpec, added []*interfaces.RequirementLogicNeed) {
	if len(added) == 0 {
		return
	}
	_, asked := questionRefs(spec.Questions)
	spec.LogicNeeds = slices.DeleteFunc(spec.LogicNeeds, func(n *interfaces.RequirementLogicNeed) bool {
		return slices.Contains(added, n) && !asked[n.ID]
	})
}
