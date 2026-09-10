// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package authz

import (
	"context"
	"errors"

	"github.com/casbin/casbin/v2/util"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

// ExplanationGrant is the non-sensitive provenance needed to identify a Core
// rule that participated in an explanation. It intentionally omits CreatedBy
// and every Enterprise property-rule payload.
type ExplanationGrant struct {
	GrantID         string          `json:"grant_id"`
	SubjectID       string          `json:"subject_id"`
	SubjectKind     string          `json:"subject_kind"`
	Object          string          `json:"object"`
	Operation       string          `json:"operation"`
	Effect          string          `json:"effect"`
	PolicySource    PolicySource    `json:"policy_source"`
	AuthoritySource AuthoritySource `json:"authority_source"`
	Match           string          `json:"match"`
}

// ExplanationStep is one local lookup in the child-to-parent walk.
type ExplanationStep struct {
	ResourceType    string             `json:"resource_type"`
	ResourceID      string             `json:"resource_id"`
	Operation       string             `json:"operation"`
	Decision        Decision           `json:"decision"`
	Basis           DecisionBasis      `json:"basis"`
	MatchedGrants   []ExplanationGrant `json:"matched_grants"`
	InheritedFrom   *ResourceRef       `json:"inherited_from,omitempty"`
	ParentOperation string             `json:"parent_operation,omitempty"`
}

// RequirementExplanation carries the direct prerequisite's own base trace.
type RequirementExplanation struct {
	Operation string            `json:"operation"`
	Decision  Evaluation        `json:"evaluation"`
	Steps     []ExplanationStep `json:"steps"`
}

// OperationExplanation is generated on demand and is never persisted.
type OperationExplanation struct {
	AccessorID   string                   `json:"accessor_id"`
	ResourceType string                   `json:"resource_type"`
	ResourceID   string                   `json:"resource_id"`
	Operation    string                   `json:"operation"`
	Evaluation   Evaluation               `json:"evaluation"`
	Steps        []ExplanationStep        `json:"steps"`
	Requirements []RequirementExplanation `json:"requirements"`
}

// ExplainOperation returns the final operation decision plus the local,
// wildcard, parent and direct-requirement evidence that produced it.
func (en *Enforcer) ExplainOperation(ctx context.Context, accessorID, resourceType, resourceID, operation string) (OperationExplanation, error) {
	final, err := en.OperationDecision(ctx, accessorID, resourceType, resourceID, operation)
	if err != nil {
		return OperationExplanation{}, err
	}
	idx, err := en.grantIndex(accessorID)
	if err != nil {
		return OperationExplanation{}, err
	}
	steps, err := en.explainBaseWalk(ctx, idx, accessorID, ResourceRef{Type: resourceType, ID: resourceID}, operation)
	if err != nil {
		return OperationExplanation{}, err
	}
	requirements, err := en.DirectRequirements(ctx, resourceType, []string{operation})
	if err != nil {
		return OperationExplanation{}, err
	}
	requirementExplanations := make([]RequirementExplanation, 0, len(requirements[operation]))
	for _, required := range requirements[operation] {
		decision, err := en.baseEffectiveDecisionForExplain(ctx, accessorID, idx,
			ResourceRef{Type: resourceType, ID: resourceID}, required)
		if err != nil {
			return OperationExplanation{}, err
		}
		requiredSteps, err := en.explainBaseWalk(ctx, idx, accessorID,
			ResourceRef{Type: resourceType, ID: resourceID}, required)
		if err != nil {
			return OperationExplanation{}, err
		}
		requirementExplanations = append(requirementExplanations, RequirementExplanation{
			Operation: required, Decision: decision, Steps: requiredSteps,
		})
	}
	return OperationExplanation{
		AccessorID: accessorID, ResourceType: resourceType, ResourceID: resourceID,
		Operation: operation, Evaluation: final, Steps: steps, Requirements: requirementExplanations,
	}, nil
}

func (en *Enforcer) baseEffectiveDecisionForExplain(ctx context.Context, accessorID string, idx *grantIndex,
	resource ResourceRef, operation string) (Evaluation, error) {
	decisions, err := en.baseEffectiveDecisionsWithIndex(ctx, accessorID, idx,
		map[ResourceRef][]string{resource: {operation}})
	if err != nil {
		return Evaluation{}, err
	}
	return decisions[resource][operation], nil
}

func (en *Enforcer) explainBaseWalk(ctx context.Context, idx *grantIndex, accessorID string,
	resource ResourceRef, operation string) ([]ExplanationStep, error) {
	current, currentOperation := resource, operation
	visited := map[ResourceRef]bool{}
	steps := make([]ExplanationStep, 0, 2)
	for depth := 0; depth < maxHierarchyDepth && !visited[current]; depth++ {
		visited[current] = true
		local, err := en.localDecisions(ctx, accessorID, idx, current, []string{currentOperation})
		if err != nil {
			return nil, err
		}
		decision := local[currentOperation]
		grants, err := en.explanationGrants(idx, current, currentOperation)
		if err != nil {
			return nil, err
		}
		step := ExplanationStep{
			ResourceType: current.Type, ResourceID: current.ID, Operation: currentOperation,
			Decision: decision.Decision, Basis: decision.Basis, MatchedGrants: grants,
		}
		steps = append(steps, step)
		if decision.Decision != DecisionNone && !(decision.Decision == DecisionAllow && decision.Basis == BasisWildcard) {
			break
		}
		mapping, err := en.parentOpMap(current.Type)
		if err != nil {
			return nil, err
		}
		parentOperation, inherits := mapping[currentOperation]
		if !inherits {
			break
		}
		var parent model.ResourceParent
		err = en.db.WithContext(ctx).First(&parent,
			"resource_type_id = ? AND resource_id = ?", current.Type, current.ID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			break
		}
		if err != nil {
			return nil, err
		}
		next := ResourceRef{Type: parent.ParentTypeID, ID: parent.ParentID}
		steps[len(steps)-1].InheritedFrom = &next
		steps[len(steps)-1].ParentOperation = parentOperation
		current, currentOperation = next, parentOperation
	}
	return steps, nil
}

func (en *Enforcer) explanationGrants(idx *grantIndex, resource ResourceRef, operation string) ([]ExplanationGrant, error) {
	subjects := make(map[string]string, len(idx.subjects))
	records := make([]PolicyRecord, 0)
	for position, subject := range idx.subjects {
		kind := "role"
		switch subject {
		case PublicAccessorID:
			kind = "public"
		default:
			if position == 0 {
				kind = "user"
			}
		}
		subjects[subject] = kind
		subjectRecords, err := en.PolicyRecords(PolicyFilter{AccessorID: subject})
		if err != nil {
			return nil, err
		}
		records = append(records, subjectRecords...)
	}
	object := obj(resource.Type, resource.ID)
	out := make([]ExplanationGrant, 0)
	for _, record := range records {
		kind, subjectMatch := subjects[record.AccessorID]
		if !subjectMatch || !record.Active || !util.KeyMatch(object, record.Object) {
			continue
		}
		match := "direct"
		operationMatch := record.Operation == operation || record.Operation == ActAll
		if record.PolicySource == PolicySourceCommunityBundle {
			operationMatch = record.Object == object && record.Operation == ActFullBusinessAccess &&
				record.Effect == EffectAllow && communityBundleAllows(resource.Type, operation)
			match = "bundle"
		} else if record.Object != object {
			match = "wildcard"
		}
		if !operationMatch {
			continue
		}
		out = append(out, ExplanationGrant{
			GrantID: record.GrantID, SubjectID: record.AccessorID, SubjectKind: kind,
			Object: record.Object, Operation: record.Operation, Effect: record.Effect,
			PolicySource: record.PolicySource, AuthoritySource: record.AuthoritySource, Match: match,
		})
	}
	return out, nil
}
