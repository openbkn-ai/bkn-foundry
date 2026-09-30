// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.

package knowledge_network

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	attr "go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"bkn-backend/interfaces"
	"bkn-backend/logics/permission"
)

// NormalizedImportPlan is the persistence-only projection of a knowledge-network import.
// It flattens historical concept-group buckets without changing the request DTO or the
// standalone create APIs. GroupMembers is kept separately so membership does not depend on
// whether an object definition was nested, top-level, or already persisted.
type NormalizedImportPlan struct {
	KNID   string
	Branch string

	ConceptGroups []*interfaces.ConceptGroup
	ObjectTypes   []*interfaces.ObjectType
	RelationTypes []*interfaces.RelationType
	ActionTypes   []*interfaces.ActionType
	RiskTypes     []*interfaces.RiskType
	Metrics       []*interfaces.MetricDefinition

	GroupMembers map[string][]string
	groupOrder   []string
	validObjects map[string]struct{}

	InvalidMemberCount  int
	RestoredMemberCount int
}

func (plan *NormalizedImportPlan) knowledgeNetworkView(source *interfaces.KN) *interfaces.KN {
	view := *source
	view.ConceptGroups = plan.ConceptGroups
	view.ObjectTypes = plan.ObjectTypes
	view.RelationTypes = plan.RelationTypes
	view.ActionTypes = plan.ActionTypes
	view.RiskTypes = plan.RiskTypes
	view.Metrics = plan.Metrics
	return &view
}

type normalizedDefinitionSet[T any] struct {
	items        []*T
	positions    map[string]int
	fingerprints map[string]string
}

func newNormalizedDefinitionSet[T any]() *normalizedDefinitionSet[T] {
	return &normalizedDefinitionSet[T]{
		positions:    make(map[string]int),
		fingerprints: make(map[string]string),
	}
}

func (set *normalizedDefinitionSet[T]) add(id string, item *T, fingerprint string, kind string) error {
	if item == nil {
		return nil
	}
	if _, exists := set.positions[id]; exists {
		if set.fingerprints[id] == fingerprint {
			return nil
		}
		return fmt.Errorf("conflicting %s definitions for id %q", kind, id)
	}
	set.positions[id] = len(set.items)
	set.fingerprints[id] = fingerprint
	set.items = append(set.items, item)
	return nil
}

func normalizeImportPlan(ctx context.Context, kn *interfaces.KN) (*NormalizedImportPlan, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Normalize knowledge network import")
	defer span.End()
	plan := &NormalizedImportPlan{
		KNID:         kn.KNID,
		Branch:       kn.Branch,
		RiskTypes:    kn.RiskTypes,
		Metrics:      kn.Metrics,
		GroupMembers: make(map[string][]string),
	}
	groups := newNormalizedDefinitionSet[interfaces.ConceptGroup]()
	objects := newNormalizedDefinitionSet[interfaces.ObjectType]()
	relations := newNormalizedDefinitionSet[interfaces.RelationType]()
	actions := newNormalizedDefinitionSet[interfaces.ActionType]()
	groupRefs := make(map[string]*interfaces.ConceptGroup)
	memberSeen := make(map[string]map[string]struct{})

	addMember := func(groupID, objectID string, groupRef *interfaces.ConceptGroup) {
		if groupID == "" || objectID == "" {
			return
		}
		if groupRef != nil {
			if _, exists := groupRefs[groupID]; !exists {
				ref := *groupRef
				ref.ObjectTypes = nil
				ref.RelationTypes = nil
				ref.ActionTypes = nil
				groupRefs[groupID] = &ref
			}
		}
		seen := memberSeen[groupID]
		if seen == nil {
			seen = make(map[string]struct{})
			memberSeen[groupID] = seen
			plan.groupOrder = append(plan.groupOrder, groupID)
		}
		if _, duplicate := seen[objectID]; duplicate {
			return
		}
		seen[objectID] = struct{}{}
		plan.GroupMembers[groupID] = append(plan.GroupMembers[groupID], objectID)
	}

	addObject := func(objectType *interfaces.ObjectType, owner *interfaces.ConceptGroup) error {
		if objectType == nil {
			return nil
		}
		var err error
		objectType.OTID, err = permission.PrepareKNChildResourceID(ctx, objectType.OTID)
		if err != nil {
			return err
		}
		clone := *objectType
		clone.ConceptGroups = nil
		fingerprint, err := importFingerprint(struct {
			interfaces.ObjectTypeWithKeyField
			interfaces.CommonInfo
		}{objectType.ObjectTypeWithKeyField, objectType.CommonInfo})
		if err != nil {
			return err
		}
		if err = objects.add(objectType.OTID, &clone, fingerprint, "object type"); err != nil {
			return err
		}
		if owner != nil {
			addMember(owner.CGID, objectType.OTID, owner)
		}
		for _, group := range objectType.ConceptGroups {
			if group != nil {
				addMember(group.CGID, objectType.OTID, group)
			}
		}
		return nil
	}

	addRelation := func(relationType *interfaces.RelationType) error {
		if relationType == nil {
			return nil
		}
		var err error
		relationType.RTID, err = permission.PrepareKNChildResourceID(ctx, relationType.RTID)
		if err != nil {
			return err
		}
		clone := *relationType
		fingerprint, err := importFingerprint(struct {
			interfaces.RelationTypeWithKeyField
			interfaces.CommonInfo
		}{relationType.RelationTypeWithKeyField, relationType.CommonInfo})
		if err != nil {
			return err
		}
		return relations.add(relationType.RTID, &clone, fingerprint, "relation type")
	}

	addAction := func(actionType *interfaces.ActionType) error {
		if actionType == nil {
			return nil
		}
		var err error
		actionType.ATID, err = permission.PrepareKNChildResourceID(ctx, actionType.ATID)
		if err != nil {
			return err
		}
		clone := *actionType
		fingerprint, err := importFingerprint(struct {
			interfaces.ActionTypeWithKeyField
			interfaces.CommonInfo
		}{actionType.ActionTypeWithKeyField, actionType.CommonInfo})
		if err != nil {
			return err
		}
		return actions.add(actionType.ATID, &clone, fingerprint, "action type")
	}

	// Visit nested definitions before top-level definitions to preserve stable input order.
	// Equivalent duplicates collapse; conflicting definitions fail when the import is applied.
	for _, conceptGroup := range kn.ConceptGroups {
		if conceptGroup == nil {
			continue
		}
		var err error
		conceptGroup.CGID, err = permission.PrepareKNChildResourceID(ctx, conceptGroup.CGID)
		if err != nil {
			return nil, err
		}
		clone := *conceptGroup
		clone.ObjectTypes = nil
		clone.RelationTypes = nil
		clone.ActionTypes = nil
		clone.ObjectTypeIDs = nil
		fingerprint, err := importFingerprint(struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			interfaces.CommonInfo
		}{conceptGroup.CGID, conceptGroup.CGName, conceptGroup.CommonInfo})
		if err != nil {
			return nil, err
		}
		if err = groups.add(conceptGroup.CGID, &clone, fingerprint, "concept group"); err != nil {
			return nil, err
		}
		groupRefs[conceptGroup.CGID] = &clone
		for _, objectID := range conceptGroup.ObjectTypeIDs {
			addMember(conceptGroup.CGID, objectID, conceptGroup)
		}
		for _, objectType := range conceptGroup.ObjectTypes {
			if err = addObject(objectType, conceptGroup); err != nil {
				return nil, err
			}
		}
		for _, relationType := range conceptGroup.RelationTypes {
			if err = addRelation(relationType); err != nil {
				return nil, err
			}
		}
		for _, actionType := range conceptGroup.ActionTypes {
			if err = addAction(actionType); err != nil {
				return nil, err
			}
		}
	}

	for _, objectType := range kn.ObjectTypes {
		if err := addObject(objectType, nil); err != nil {
			return nil, err
		}
	}
	for _, relationType := range kn.RelationTypes {
		if err := addRelation(relationType); err != nil {
			return nil, err
		}
	}
	for _, actionType := range kn.ActionTypes {
		if err := addAction(actionType); err != nil {
			return nil, err
		}
	}

	plan.ConceptGroups = groups.items
	plan.ObjectTypes = objects.items
	plan.RelationTypes = relations.items
	plan.ActionTypes = actions.items

	for _, conceptGroup := range plan.ConceptGroups {
		conceptGroup.ObjectTypeIDs = append([]string(nil), plan.GroupMembers[conceptGroup.CGID]...)
		groupRefs[conceptGroup.CGID] = conceptGroup
	}
	groupsByObject := make(map[string][]*interfaces.ConceptGroup, len(plan.ObjectTypes))
	for _, groupID := range plan.groupOrder {
		groupRef := groupRefs[groupID]
		if groupRef == nil {
			groupRef = &interfaces.ConceptGroup{CGID: groupID}
		}
		for _, objectID := range plan.GroupMembers[groupID] {
			groupsByObject[objectID] = append(groupsByObject[objectID], groupRef)
		}
	}
	for _, objectType := range plan.ObjectTypes {
		objectType.ConceptGroups = append(objectType.ConceptGroups, groupsByObject[objectType.OTID]...)
	}
	memberReferences := 0
	for _, members := range plan.GroupMembers {
		memberReferences += len(members)
	}
	span.SetAttributes(
		attr.Int("concept_group_count", len(plan.ConceptGroups)),
		attr.Int("object_type_count", len(plan.ObjectTypes)),
		attr.Int("relation_type_count", len(plan.RelationTypes)),
		attr.Int("action_type_count", len(plan.ActionTypes)),
		attr.Int("risk_type_count", len(plan.RiskTypes)),
		attr.Int("metric_count", len(plan.Metrics)),
		attr.Int("member_reference_count", memberReferences),
	)
	span.SetStatus(codes.Ok, "")
	return plan, nil
}

func importFingerprint(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
