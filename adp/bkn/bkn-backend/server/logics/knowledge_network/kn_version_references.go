// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledge_network

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
)

// CheckReferences lists what a branch references that does not exist: relation ends, action type
// objects, concept group members, the action types of active schedules, and bound functions,
// Skills and knowledge bases. Publish runs it on the draft and activate on the target version.
func (kns *knowledgeNetworkService) CheckReferences(ctx context.Context, knID, branch string) ([]*interfaces.KNVersionProblem, error) {
	kn, err := kns.loadKNForDatasetSnapshot(ctx, knID, branch)
	if err != nil {
		return nil, err
	}
	var members []interfaces.ConceptGroupRelation
	err = kns.runInTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		members, err = kns.cga.ListConceptGroupRelations(ctx, tx, interfaces.ConceptGroupRelationsQueryParams{
			PaginationQueryParameters: interfaces.PaginationQueryParameters{Limit: -1},
			KNID:                      knID, Branch: branch, ConceptType: interfaces.MODULE_TYPE_OBJECT_TYPE,
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	// Schedules are not versioned: they run against whatever version is running.
	var schedules []*interfaces.ActionSchedule
	if logics.ASA != nil {
		schedules, err = logics.ASA.ListSchedules(ctx, interfaces.ActionScheduleQueryParams{
			PaginationQueryParameters: interfaces.PaginationQueryParameters{Limit: -1},
			KNID:                      knID, Branch: interfaces.MAIN_BRANCH, Status: interfaces.ScheduleStatusActive,
		})
		if err != nil {
			return nil, err
		}
	}
	// A binding is missing only when its target is known to be gone; an unreachable execution
	// factory leaves the status empty and does not block.
	bindings, err := kns.cbs.ListCapabilities(ctx, interfaces.CapabilityBindingsQueryParams{
		PaginationQueryParameters: interfaces.PaginationQueryParameters{Limit: -1},
		KNID:                      knID, Branch: branch, ReadAccessMode: interfaces.KN_READ_ACCESS_FULL,
	})
	if err != nil {
		return nil, err
	}
	return brokenReferences(kn, members, schedules, bindings.Entries), nil
}

func brokenReferences(kn *interfaces.KN, members []interfaces.ConceptGroupRelation,
	schedules []*interfaces.ActionSchedule, bindings []*interfaces.CapabilityBinding) []*interfaces.KNVersionProblem {
	problems := []*interfaces.KNVersionProblem{}
	broken := func(kind, id, format string, args ...any) {
		problems = append(problems, &interfaces.KNVersionProblem{Category: interfaces.KNProblemBrokenReference,
			DefinitionType: kind, DefinitionID: id, Description: fmt.Sprintf(format, args...)})
	}
	objects := map[string]bool{}
	for _, ot := range kn.ObjectTypes {
		objects[ot.OTID] = true
	}
	for _, rt := range kn.RelationTypes {
		for _, end := range []string{rt.SourceObjectTypeID, rt.TargetObjectTypeID} {
			if end != "" && !objects[end] {
				broken(interfaces.MODULE_TYPE_RELATION_TYPE, rt.RTID, "object type %s does not exist", end)
			}
		}
	}
	actions := map[string]bool{}
	for _, at := range kn.ActionTypes {
		actions[at.ATID] = true
		if at.ObjectTypeID != "" && !objects[at.ObjectTypeID] {
			broken(interfaces.MODULE_TYPE_ACTION_TYPE, at.ATID, "object type %s does not exist", at.ObjectTypeID)
		}
	}
	for _, m := range members {
		if !objects[m.ConceptID] {
			broken(interfaces.MODULE_TYPE_CONCEPT_GROUP, m.CGID, "member object type %s does not exist", m.ConceptID)
		}
	}
	for _, s := range schedules {
		if !actions[s.ActionTypeID] {
			broken(interfaces.MODULE_TYPE_ACTION_SCHEDULE, s.ID, "action type %s does not exist", s.ActionTypeID)
		}
	}
	for _, b := range bindings {
		if b.Status == interfaces.CAPABILITY_STATUS_MISSING {
			broken(interfaces.RevisionKindCapabilityBinding, b.ID, "%s %s does not exist", b.CapabilityType, b.CapabilityID)
		}
	}
	return problems
}

// BranchReferences lists what one branch uses outside the network: the data resources its object
// types and relation types read, the tools its logic properties, action types and bindings call,
// and the Skills bound to it.
func (kns *knowledgeNetworkService) BranchReferences(ctx context.Context, knID, branch string) (*interfaces.KNBranchReferences, error) {
	kn, err := kns.loadKNForDatasetSnapshot(ctx, knID, branch)
	if err != nil {
		return nil, err
	}
	refs := &interfaces.KNBranchReferences{}
	for _, ot := range kn.ObjectTypes {
		if ot.DataSource != nil && ot.DataSource.Type == interfaces.DATA_SOURCE_TYPE_RESOURCE && ot.DataSource.ID != "" {
			refs.DataResources = append(refs.DataResources, ot.DataSource.ID)
		}
		for _, p := range ot.LogicProperties {
			if p != nil && p.DataSource != nil && p.DataSource.Type == interfaces.LOGIC_PROPERTY_TYPE_TOOL && p.DataSource.ToolID != "" {
				refs.Functions = append(refs.Functions, [2]string{p.DataSource.BoxID, p.DataSource.ToolID})
			}
		}
	}
	for _, rt := range kn.RelationTypes {
		if backing, ok, _ := indirectRelationProxyResource(rt.MappingRules); ok && backing != nil && backing.ID != "" {
			refs.DataResources = append(refs.DataResources, backing.ID)
		}
	}
	for _, at := range kn.ActionTypes {
		if at.ActionSource.Type == interfaces.ACTION_SOURCE_TYPE_TOOL && at.ActionSource.ToolID != "" {
			refs.Functions = append(refs.Functions, [2]string{at.ActionSource.BoxID, at.ActionSource.ToolID})
		}
	}
	for _, capabilityType := range []string{interfaces.CAPABILITY_TYPE_FUNCTION, interfaces.CAPABILITY_TYPE_SKILL} {
		bound, err := kns.cbs.ResolveCapabilities(ctx, knID, branch, capabilityType)
		if err != nil {
			return nil, err
		}
		for _, ref := range bound.Entries {
			if capabilityType == interfaces.CAPABILITY_TYPE_SKILL {
				refs.Skills = append(refs.Skills, ref.CapabilityID)
			} else {
				refs.Functions = append(refs.Functions, [2]string{ref.BoxID, ref.CapabilityID})
			}
		}
	}
	return refs, nil
}
