// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_target

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	cond "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common/condition"
	oerrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/logics"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
)

// Resolver loads explicitly requested action targets through the existing object API.
type Resolver struct {
	Models  interfaces.OntologyManagerAccess
	Objects interfaces.ObjectTypeService
}

func actionTargetError(ctx context.Context, detail string) error {
	return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ActionExecution_InvalidParameter).WithErrorDetails(detail)
}

func ValidateDefinition(action *interfaces.ActionType) error {
	cfg := action.OperationConfig
	if cfg == nil {
		return fmt.Errorf("operation_config is required; migrate stored action definitions before execution")
	}
	switch cfg.Type {
	case "object":
		if action.ObjectTypeID == "" || (action.ActionType != "add" && action.ActionType != "modify" && action.ActionType != "delete") {
			return fmt.Errorf("object operation requires an object type and add, modify or delete")
		}
	case "relation":
		if action.ObjectTypeID == "" || cfg.RelationTypeID == "" || (cfg.RelationOperation != "create" && cfg.RelationOperation != "delete") || (cfg.AnchorRole != "source" && cfg.AnchorRole != "target") {
			return fmt.Errorf("relation operation requires a relation type, operation and anchor role")
		}
	case "tool":
		if action.ObjectTypeID != "" || action.Condition != nil {
			return fmt.Errorf("tool operations cannot bind object context")
		}
	default:
		return fmt.Errorf("unsupported action operation %q", cfg.Type)
	}
	if cfg.Type != "relation" && (cfg.RelationTypeID != "" || cfg.RelationOperation != "" || cfg.AnchorRole != "" || cfg.OtherObjectCondition != nil) {
		return fmt.Errorf("relation fields require a relation operation")
	}
	return nil
}

// getInstancesForAction only resolves explicitly supplied targets. Conditions
// validate those targets; they never select an implicit batch of objects.
func (s *Resolver) Resolve(ctx context.Context, action *interfaces.ActionType, req *interfaces.ActionExecutionRequest) ([]interfaces.ObjectSystemInfo, []map[string]any, error) {
	if err := ValidateDefinition(action); err != nil {
		return nil, nil, actionTargetError(ctx, err.Error())
	}
	switch action.OperationConfig.Type {
	case "tool":
		if len(req.InstanceIdentities) != 0 || req.RelationTarget != nil || len(req.ObjectInput) != 0 {
			return nil, nil, actionTargetError(ctx, "tool execution does not accept object or relation targets")
		}
		return []interfaces.ObjectSystemInfo{{InstanceIdentity: map[string]any{}}}, []map[string]any{{}}, nil
	case "relation":
		return s.resolveRelationTarget(ctx, action, req)
	}
	if req.RelationTarget != nil {
		return nil, nil, actionTargetError(ctx, "object execution does not accept relation_target")
	}
	object, exists, err := s.Models.GetObjectType(ctx, req.KNID, req.Branch, action.ObjectTypeID)
	if err != nil {
		return nil, nil, err
	}
	if !exists {
		return nil, nil, actionTargetError(ctx, "action object type does not exist")
	}
	if action.ActionType == "add" {
		if len(req.InstanceIdentities) != 0 {
			return nil, nil, actionTargetError(ctx, "new objects use object_input, not existing instance identities")
		}
		data := make(map[string]any, len(req.ObjectInput))
		mapped := mappedObjectInputProperties(action.Parameters)
		for key, value := range req.ObjectInput {
			known := false
			propertyType := ""
			for _, property := range object.DataProperties {
				if property.Name == key {
					known = true
					propertyType = property.Type
					break
				}
			}
			if !known {
				return nil, nil, actionTargetError(ctx, "object_input references an unknown property")
			}
			if !mapped[key] {
				return nil, nil, actionTargetError(ctx, fmt.Sprintf("object_input property %q is not mapped to an execution parameter", key))
			}
			if err := logics.ValidateActionValue(propertyType, value); err != nil {
				return nil, nil, actionTargetError(ctx, "object_input contains a value with an incompatible type")
			}
			data[key] = value
		}
		identity := make(map[string]any)
		for _, key := range object.PrimaryKeys {
			if value := data[key]; value != nil {
				identity[key] = value
			}
		}
		if err := evaluateActionTarget(ctx, data, action.Condition, &object, req.DynamicParams); err != nil {
			return nil, nil, err
		}
		return []interfaces.ObjectSystemInfo{{InstanceIdentity: identity}}, []map[string]any{data}, nil
	}
	if len(req.ObjectInput) != 0 {
		return nil, nil, actionTargetError(ctx, "object_input is only valid for new objects")
	}
	if len(req.InstanceIdentities) == 0 {
		return nil, nil, actionTargetError(ctx, "modify and delete require explicit _instance_identities")
	}
	if len(req.InstanceIdentities) > logics.ActionExecutionMaxObjects {
		return nil, nil, actionTargetError(ctx, "too many action targets")
	}
	instances := make([]interfaces.ObjectSystemInfo, 0, len(req.InstanceIdentities))
	datas := make([]map[string]any, 0, len(req.InstanceIdentities))
	seen := make(map[string]bool)
	for _, identity := range req.InstanceIdentities {
		encoded, err := json.Marshal(identity)
		if err != nil || seen[string(encoded)] {
			return nil, nil, actionTargetError(ctx, "duplicate or invalid action target identity")
		}
		seen[string(encoded)] = true
		instance, data, err := s.loadActionTarget(ctx, req, &object, identity, action.Condition, targetProperties(&object, action.Condition, action.Parameters))
		if err != nil {
			return nil, nil, err
		}
		instances, datas = append(instances, instance), append(datas, data)
	}
	return instances, datas, nil
}

// mappedObjectInputProperties identifies the only object values consumed by
// execution parameter binding when an action creates a new instance.
func mappedObjectInputProperties(parameters []interfaces.Parameter) map[string]bool {
	mapped := make(map[string]bool)
	for _, parameter := range parameters {
		if parameter.ValueFrom == "property" {
			if name, ok := parameter.Value.(string); ok && name != "" {
				mapped[name] = true
			}
		}
	}
	return mapped
}

func (s *Resolver) resolveRelationTarget(ctx context.Context, action *interfaces.ActionType, req *interfaces.ActionExecutionRequest) ([]interfaces.ObjectSystemInfo, []map[string]any, error) {
	cfg, target := action.OperationConfig, req.RelationTarget
	if target == nil || len(target.SourceIdentity) == 0 || len(target.TargetIdentity) == 0 || len(req.InstanceIdentities) != 0 || len(req.ObjectInput) != 0 {
		return nil, nil, actionTargetError(ctx, "relation execution requires one explicit relation_target with both identities")
	}
	relation, exists, err := s.Models.GetRelationType(ctx, req.KNID, req.Branch, cfg.RelationTypeID)
	if err != nil {
		return nil, nil, err
	}
	if !exists {
		return nil, nil, actionTargetError(ctx, "relation type does not exist")
	}
	anchorID := relation.SourceObjectTypeID
	if cfg.AnchorRole == "target" {
		anchorID = relation.TargetObjectTypeID
	}
	if anchorID != action.ObjectTypeID {
		return nil, nil, actionTargetError(ctx, "relation direction does not match the action object type")
	}
	endpointIDs := []string{relation.SourceObjectTypeID, relation.TargetObjectTypeID}
	identities := []map[string]any{target.SourceIdentity, target.TargetIdentity}
	conditions := []*cond.CondCfg{action.Condition, cfg.OtherObjectCondition}
	anchor := 0
	if cfg.AnchorRole == "target" {
		conditions[0], conditions[1], anchor = conditions[1], conditions[0], 1
	}
	var anchorData map[string]any
	var anchorInfo interfaces.ObjectSystemInfo
	for index, id := range endpointIDs {
		object, exists, err := s.Models.GetObjectType(ctx, req.KNID, req.Branch, id)
		if err != nil {
			return nil, nil, err
		}
		if !exists || len(object.PrimaryKeys) != 1 {
			return nil, nil, actionTargetError(ctx, "relation endpoints require an existing object type with exactly one primary key")
		}

		var parameters []interfaces.Parameter
		if index == anchor {
			parameters = action.Parameters
		}
		info, data, err := s.loadActionTarget(ctx, req, &object, identities[index], conditions[index], targetProperties(&object, conditions[index], parameters))
		if err != nil {
			return nil, nil, err
		}
		if index == anchor {
			anchorData, anchorInfo = data, info
		}
	}
	// The complete directed pair is carried into execution results and duplicate
	// detection instead of treating the current endpoint as the whole target.
	anchorInfo.InstanceIdentity = map[string]any{"source": target.SourceIdentity, "target": target.TargetIdentity}
	return []interfaces.ObjectSystemInfo{anchorInfo}, []map[string]any{anchorData}, nil
}

func (s *Resolver) loadActionTarget(ctx context.Context, req *interfaces.ActionExecutionRequest, object *interfaces.ObjectType, identity map[string]any, condition *cond.CondCfg, properties []string) (interfaces.ObjectSystemInfo, map[string]any, error) {
	if len(object.PrimaryKeys) == 0 || len(identity) != len(object.PrimaryKeys) {
		return interfaces.ObjectSystemInfo{}, nil, actionTargetError(ctx, "target identity must contain exactly the object primary keys")
	}
	for _, key := range object.PrimaryKeys {
		for _, property := range object.DataProperties {
			if property.Name == key {
				if err := logics.ValidateActionValue(property.Type, identity[key]); err != nil {
					return interfaces.ObjectSystemInfo{}, nil, actionTargetError(ctx, "target primary key has an incompatible type")
				}
			}
		}
		if identity[key] == nil {
			return interfaces.ObjectSystemInfo{}, nil, actionTargetError(ctx, "target identity is missing a primary key value")
		}
	}
	query := &interfaces.ObjectQueryBaseOnObjectType{
		KNID: req.KNID, Branch: req.Branch, ObjectTypeID: object.OTID,
		ActualCondition:       logics.BuildInstanceIdentitiesCondition([]map[string]any{identity}),
		CommonQueryParameters: interfaces.CommonQueryParameters{RequiredFullProperties: properties},
		ObjectQueryInfo:       &interfaces.ObjectQueryInfo{InstanceIdentity: []map[string]any{identity}, Properties: properties},
		PageQuery:             interfaces.PageQuery{Limit: 2, NeedTotal: true},
	}
	objects, err := s.Objects.GetObjectsByObjectTypeID(ctx, query)
	if err != nil {
		return interfaces.ObjectSystemInfo{}, nil, err
	}
	if len(objects.Datas) != 1 || objects.Incomplete {
		return interfaces.ObjectSystemInfo{}, nil, actionTargetError(ctx, "target identity must resolve to exactly one accessible instance")
	}
	data := objects.Datas[0]
	if err := evaluateActionTarget(ctx, data, condition, object, req.DynamicParams); err != nil {
		return interfaces.ObjectSystemInfo{}, nil, err
	}
	return interfaces.ObjectSystemInfo{InstanceIdentity: identity, InstanceID: data[interfaces.SYSTEM_PROPERTY_INSTANCE_ID], Display: data[interfaces.SYSTEM_PROPERTY_DISPLAY]}, data, nil
}

func evaluateActionTarget(ctx context.Context, data map[string]any, condition *cond.CondCfg, object *interfaces.ObjectType, dynamic map[string]any) error {
	if err := validateTargetCondition(condition, object); err != nil {
		return actionTargetError(ctx, err.Error())
	}
	resolved, err := logics.ResolveActionConditionValues(condition, data, dynamic)
	if err != nil {
		return actionTargetError(ctx, err.Error())
	}
	match, err := logics.EvaluateInstanceAgainstCondition(ctx, data, resolved, object)
	if err != nil {
		return actionTargetError(ctx, err.Error())
	}
	if !match {
		return actionTargetError(ctx, "target does not satisfy the action applicability condition")
	}
	return nil
}

// targetProperties asks the existing property-access layer for full access to
// every value consumed by the action, instead of accidentally sending masked data.
func targetProperties(object *interfaces.ObjectType, condition *cond.CondCfg, parameters []interfaces.Parameter) []string {
	fields := append([]string(nil), object.PrimaryKeys...)
	for _, parameter := range parameters {
		if parameter.ValueFrom == "property" {
			if name, ok := parameter.Value.(string); ok {
				fields = append(fields, name)
			}
		}
	}
	var collect func(*cond.CondCfg)
	collect = func(cfg *cond.CondCfg) {
		if cfg == nil {
			return
		}
		if cfg.Name != "" {
			fields = append(fields, cfg.Name)
		}
		if cfg.ValueFrom == "property" {
			if name, ok := cfg.Value.(string); ok {
				fields = append(fields, name)
			}
		}
		for _, child := range cfg.SubConds {
			collect(child)
		}
	}
	collect(condition)
	result := make([]string, 0, len(fields))
	seen := make(map[string]bool)
	for _, field := range fields {
		if field != "" && !seen[field] {
			seen[field] = true
			result = append(result, field)
		}
	}
	return result
}

// validateTargetCondition rejects stale references even when a draft was saved
// without strict dependency validation. Missing properties must not turn != into true.
func validateTargetCondition(cfg *cond.CondCfg, object *interfaces.ObjectType) error {
	if cfg == nil {
		return nil
	}
	if cfg.ObjectTypeID != "" && cfg.ObjectTypeID != object.OTID {
		return fmt.Errorf("condition references another object type")
	}
	if cfg.Operation == "and" || cfg.Operation == "or" {
		if len(cfg.SubConds) == 0 {
			return fmt.Errorf("logical conditions require children")
		}
		for _, child := range cfg.SubConds {
			if child == nil {
				return fmt.Errorf("null condition child")
			}
			if err := validateTargetCondition(child, object); err != nil {
				return err
			}
		}
		return nil
	}
	known := false
	for _, property := range object.DataProperties {
		if property.Name == cfg.Name {
			known = true
			break
		}
	}
	if !known {
		return fmt.Errorf("condition references unknown property %q", cfg.Name)
	}
	if cfg.ValueFrom == "property" {
		name, ok := cfg.Value.(string)
		if !ok || name == "" {
			return fmt.Errorf("condition property reference requires a name")
		}
		known = false
		for _, property := range object.DataProperties {
			if property.Name == name {
				known = true
				break
			}
		}
		if !known {
			return fmt.Errorf("condition references unknown property %q", name)
		}
	}
	return nil
}
