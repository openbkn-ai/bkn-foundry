// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_type

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"

	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
)

func (ats *actionTypeService) checkOperation(ctx context.Context, tx *sql.Tx, at *interfaces.ActionType, batch *interfaces.BatchIDIndex, strict bool) error {
	if err := interfaces.ValidateActionOperation(at); err != nil {
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).WithErrorDetails(err.Error())
	}
	if !strict && at.OperationConfig.Type != interfaces.ActionOperationRelation {
		return nil
	}
	if err := ats.checkOperationDependencies(ctx, tx, at, batch); err != nil {
		if httpErr, ok := err.(*httperrors.HTTPError); ok {
			return httpErr
		}
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).WithErrorDetails(err.Error())
	}
	return nil
}

func (ats *actionTypeService) checkOperationDependencies(ctx context.Context, tx *sql.Tx, at *interfaces.ActionType, batch *interfaces.BatchIDIndex) error {
	if err := interfaces.ValidateActionOperation(at); err != nil {
		return err
	}
	cfg := at.OperationConfig
	if cfg.Type == interfaces.ActionOperationTool {
		return nil
	}
	getObject := func(id string) (*interfaces.ObjectType, error) {
		if batch != nil && batch.ObjectTypes[id] != nil {
			return batch.ObjectTypes[id], nil
		}
		return ats.ots.GetObjectTypeByID(ctx, tx, at.KNID, at.Branch, id)
	}
	current, err := getObject(at.ObjectTypeID)
	if err != nil {
		return err
	}
	if current == nil {
		return fmt.Errorf("object type %q does not exist", at.ObjectTypeID)
	}
	if err := checkConditionSchema(at.Condition, current, at.Parameters); err != nil {
		return err
	}
	for _, p := range at.Parameters {
		if p.ValueFrom == "property" {
			name, _ := p.Value.(string)
			prop := actionProperty(current, name)
			if prop == nil || !actionTypesCompatible(prop.Type, p.Type) {
				return fmt.Errorf("parameter %q references a missing or incompatible property %q", p.Name, name)
			}
		}
	}
	if cfg.Type != interfaces.ActionOperationRelation {
		return nil
	}
	var relation *interfaces.RelationType
	if batch != nil {
		relation = batch.RelationTypes[cfg.RelationTypeID]
	}
	if relation == nil {
		if ats.rta == nil {
			return fmt.Errorf("relation dependency lookup is unavailable")
		}
		relation, err = ats.rta.GetRelationTypeByIDInTx(ctx, tx, at.KNID, at.Branch, cfg.RelationTypeID)
		if err != nil {
			return err
		}
	}
	if relation == nil {
		return fmt.Errorf("relation type %q does not exist", cfg.RelationTypeID)
	}
	otherID, err := resolveActionRelation(cfg, at.ObjectTypeID, relation)
	if err != nil {
		return err
	}
	other := current
	if otherID != current.OTID {
		other, err = getObject(otherID)
	}
	if err != nil {
		return err
	}
	if other == nil {
		return fmt.Errorf("object type %q does not exist", otherID)
	}
	if err := checkConditionSchema(cfg.OtherObjectCondition, other, at.Parameters); err != nil {
		return err
	}
	source, target := current, other
	if cfg.AnchorRole == interfaces.ActionRoleTarget {
		source, target = other, current
	}
	for _, p := range at.Parameters {
		var endpoint *interfaces.ObjectType
		switch p.ValueFrom {
		case interfaces.ActionValueSourceIdentity:
			endpoint = source
		case interfaces.ActionValueTargetIdentity:
			endpoint = target
		default:
			continue
		}
		if len(endpoint.PrimaryKeys) != 1 {
			return fmt.Errorf("identity parameter %q requires exactly one primary key on object type %q", p.Name, endpoint.OTID)
		}
		key := actionProperty(endpoint, endpoint.PrimaryKeys[0])
		if key == nil || !actionTypesCompatible(key.Type, p.Type) {
			return fmt.Errorf("identity parameter %q is incompatible with object type %q primary key", p.Name, endpoint.OTID)
		}
	}
	return nil
}

// resolveActionRelation infers ordinary directions but never guesses a self-link role.
func resolveActionRelation(cfg *interfaces.ActionOperationConfig, objectTypeID string, relation *interfaces.RelationType) (string, error) {
	isSource, isTarget := relation.SourceObjectTypeID == objectTypeID, relation.TargetObjectTypeID == objectTypeID
	if !isSource && !isTarget {
		return "", fmt.Errorf("relation %q is not connected to object type %q", relation.RTID, objectTypeID)
	}
	if isSource && isTarget && cfg.AnchorRole == "" {
		return "", fmt.Errorf("self relations require an explicit anchor_role")
	}
	if cfg.AnchorRole == "" {
		cfg.AnchorRole = interfaces.ActionRoleSource
		if isTarget {
			cfg.AnchorRole = interfaces.ActionRoleTarget
		}
	}
	if cfg.AnchorRole == interfaces.ActionRoleSource && isSource {
		return relation.TargetObjectTypeID, nil
	}
	if cfg.AnchorRole == interfaces.ActionRoleTarget && isTarget {
		return relation.SourceObjectTypeID, nil
	}
	return "", fmt.Errorf("anchor_role does not match the selected object type")
}

func actionProperty(object *interfaces.ObjectType, name string) *interfaces.DataProperty {
	for _, prop := range object.DataProperties {
		if prop != nil && prop.Name == name {
			return prop
		}
	}
	return nil
}

func actionTypeKind(value string) string {
	switch value = strings.ToLower(value); value {
	case "int", "int8", "int16", "int32", "int64", "uint", "uint32", "uint64", "integer", "long", "short":
		return "integer"
	case "float", "float32", "float64", "double", "decimal", "number":
		return "number"
	case "string", "text", "keyword", "date", "datetime":
		return "string"
	case "bool", "boolean":
		return "boolean"
	default:
		return value
	}
}

func actionTypesCompatible(source, target string) bool {
	source, target = actionTypeKind(source), actionTypeKind(target)
	return source != "" && target != "" && (source == target || source == "integer" && target == "number")
}

func checkConditionSchema(cfg *interfaces.ActionCondCfg, object *interfaces.ObjectType, parameters []interfaces.Parameter) error {
	if cfg == nil {
		return nil
	}
	if cfg.ObjectTypeID != "" && cfg.ObjectTypeID != object.OTID {
		return fmt.Errorf("condition references object type %q instead of %q", cfg.ObjectTypeID, object.OTID)
	}
	cfg.ObjectTypeID = object.OTID
	if len(cfg.SubConds) > 0 {
		for _, sub := range cfg.SubConds {
			if err := checkConditionSchema(sub, object, parameters); err != nil {
				return err
			}
		}
		return nil
	}
	left := actionProperty(object, cfg.Field)
	if left == nil {
		return fmt.Errorf("condition references unknown property %q on %q", cfg.Field, object.OTID)
	}
	var rightType string
	switch cfg.ValueFrom {
	case "property":
		name, _ := cfg.Value.(string)
		right := actionProperty(object, name)
		if right == nil {
			return fmt.Errorf("condition references unknown property %q", name)
		}
		rightType = right.Type
	case "input":
		for _, param := range parameters {
			if param.Name == cfg.Value {
				rightType = param.Type
				break
			}
		}
	default:
		return nil
	}
	switch cfg.Operation {
	case "in", "not_in", "range", "out_range", "between", "before":
		if rightType != "array" {
			return fmt.Errorf("condition %s requires an array reference", cfg.Operation)
		}
	default:
		if !actionTypesCompatible(rightType, left.Type) && !actionTypesCompatible(left.Type, rightType) {
			return fmt.Errorf("condition reference has incompatible type %q for %q", rightType, cfg.Field)
		}
	}
	return nil
}
