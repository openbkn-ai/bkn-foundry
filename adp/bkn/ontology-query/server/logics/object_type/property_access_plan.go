// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package object_type

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	cond "ontology-query/common/condition"
	"ontology-query/common/maskrule"
	oerrors "ontology-query/errors"
	"ontology-query/interfaces"
	"ontology-query/logics"
)

type propertyAccessPlan struct {
	properties       map[string]cond.DataProperty
	decisions        map[string]interfaces.PropertyAccessDecision
	returnData       map[string]struct{}
	returnLogic      map[string]struct{}
	operationFields  map[string]struct{}
	dependencyFields map[string]struct{}
	fetchFields      map[string]struct{}
	effective        map[string]interfaces.PropertyAccessLevel
	allPrimaryFull   bool
	displayVisible   bool
	scoreVisible     bool
}

func buildPropertyAccessPlan(ctx context.Context, resolver interfaces.PropertyAccessService,
	objectType interfaces.ObjectType, query *interfaces.ObjectQueryBaseOnObjectType,
	instanceQuery bool) (*propertyAccessPlan, error) {
	plan := &propertyAccessPlan{
		properties:       make(map[string]cond.DataProperty, len(objectType.DataProperties)),
		decisions:        make(map[string]interfaces.PropertyAccessDecision, len(objectType.DataProperties)),
		returnData:       map[string]struct{}{},
		returnLogic:      map[string]struct{}{},
		operationFields:  map[string]struct{}{},
		dependencyFields: map[string]struct{}{},
		fetchFields:      map[string]struct{}{},
		effective:        map[string]interfaces.PropertyAccessLevel{},
		allPrimaryFull:   len(objectType.PrimaryKeys) > 0,
	}
	propertyNames := make([]string, 0, len(objectType.DataProperties))
	for _, property := range objectType.DataProperties {
		plan.properties[property.Name] = property
		propertyNames = append(propertyNames, property.Name)
	}
	if len(propertyNames) > 0 {
		if resolver == nil {
			return nil, propertyDecisionUnavailable(ctx, fmt.Errorf("property access resolver is not configured"))
		}
		entries, err := resolver.ResolvePropertyLevels(ctx, []interfaces.PropertyLevelsRequestItem{{
			ObjectTypeRef: objectType.KNID + "/" + objectType.OTID,
			Properties:    propertyNames,
		}})
		if err != nil {
			return nil, err
		}
		if len(entries) != 1 || entries[0].ObjectTypeRef != objectType.KNID+"/"+objectType.OTID ||
			len(entries[0].Properties) != len(propertyNames) {
			return nil, propertyDecisionUnavailable(ctx, fmt.Errorf("property-level response shape mismatch"))
		}
		for index, name := range propertyNames {
			decision := entries[0].Properties[index]
			if decision.Name != name || !decision.Level.Valid() {
				return nil, propertyDecisionUnavailable(ctx, fmt.Errorf("property-level response decision mismatch"))
			}
			if decision.Level == interfaces.PropertyAccessMasked {
				property := plan.properties[name]
				if err := maskrule.Validate(property.Type, property.MaskRule); err != nil {
					logger.Warnf("Data property [%s/%s/%s] has unusable mask_rule and is downgraded to schema: %v",
						objectType.KNID, objectType.OTID, name, err)
					decision.Level = interfaces.PropertyAccessSchema
				}
			}
			plan.decisions[name] = decision
			if decision.Level != interfaces.PropertyAccessNone {
				plan.effective[name] = decision.Level
			}
		}
	}

	if !instanceQuery {
		return plan, nil
	}

	requestedData, requestedLogic, invalidExplicit := requestedReturnFields(objectType, query)
	if invalidExplicit {
		return nil, unavailablePropertyError(ctx)
	}
	for _, name := range requestedData {
		decision := plan.decisions[name]
		if decision.Level == interfaces.PropertyAccessNone {
			if explicitReturnFields(query) {
				return nil, unavailablePropertyError(ctx)
			}
			continue
		}
		if decision.Level == interfaces.PropertyAccessMasked || decision.Level == interfaces.PropertyAccessFull {
			plan.returnData[name] = struct{}{}
			plan.fetchFields[name] = struct{}{}
		}
	}

	collectOperationFields(query.ActualCondition, plan.operationFields, plan.properties)
	for _, sort := range query.Sort {
		if sort != nil && sort.Field != "" && sort.Field != interfaces.SORT_FIELD_SCORE {
			plan.operationFields[sort.Field] = struct{}{}
		}
	}
	for name := range plan.operationFields {
		decision, exists := plan.decisions[name]
		if !exists || decision.Level != interfaces.PropertyAccessFull {
			return nil, unavailableOperationPropertyError(ctx)
		}
	}

	logicProperties := make(map[string]*interfaces.LogicProperty, len(objectType.LogicProperties))
	for _, property := range objectType.LogicProperties {
		logicProperties[property.Name] = property
	}
	for _, name := range requestedLogic {
		logicProperty := logicProperties[name]
		dependencies, valid := logicPropertyDependencies(logicProperty, plan.decisions)
		if !valid {
			continue
		}
		plan.returnLogic[name] = struct{}{}
		for _, dependency := range dependencies {
			plan.dependencyFields[dependency] = struct{}{}
			plan.fetchFields[dependency] = struct{}{}
		}
	}

	for _, key := range objectType.PrimaryKeys {
		decision, exists := plan.decisions[key]
		if !exists || decision.Level != interfaces.PropertyAccessFull {
			plan.allPrimaryFull = false
		}
	}
	if plan.allPrimaryFull &&
		(!logics.ShouldExcludeSystemProperty(interfaces.SYSTEM_PROPERTY_INSTANCE_ID, query.ExcludeSystemProperties) ||
			!logics.ShouldExcludeSystemProperty(interfaces.SYSTEM_PROPERTY_INSTANCE_IDENTITY, query.ExcludeSystemProperties)) {
		for _, key := range objectType.PrimaryKeys {
			plan.dependencyFields[key] = struct{}{}
			plan.fetchFields[key] = struct{}{}
		}
	}
	if objectType.DisplayKey != "" &&
		!logics.ShouldExcludeSystemProperty(interfaces.SYSTEM_PROPERTY_DISPLAY, query.ExcludeSystemProperties) {
		decision, exists := plan.decisions[objectType.DisplayKey]
		if exists && (decision.Level == interfaces.PropertyAccessFull || decision.Level == interfaces.PropertyAccessMasked) {
			plan.displayVisible = true
			plan.dependencyFields[objectType.DisplayKey] = struct{}{}
			plan.fetchFields[objectType.DisplayKey] = struct{}{}
		}
	}
	plan.scoreVisible = !logics.ShouldExcludeSystemProperty(interfaces.SORT_FIELD_SCORE, query.ExcludeSystemProperties)

	if len(plan.returnData) == 0 && len(plan.returnLogic) == 0 {
		return nil, rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden).
			WithErrorDetails("the request has no returnable properties")
	}
	return plan, nil
}

func requestedReturnFields(objectType interfaces.ObjectType,
	query *interfaces.ObjectQueryBaseOnObjectType) ([]string, []string, bool) {
	data := make(map[string]struct{}, len(objectType.DataProperties))
	logic := make(map[string]struct{}, len(objectType.LogicProperties))
	for _, property := range objectType.DataProperties {
		data[property.Name] = struct{}{}
	}
	for _, property := range objectType.LogicProperties {
		logic[property.Name] = struct{}{}
	}

	requested := query.Properties
	if query.ObjectQueryInfo != nil {
		requested = query.ObjectQueryInfo.Properties
	}
	if len(requested) == 0 && query.ObjectQueryInfo == nil {
		requested = make([]string, 0, len(objectType.DataProperties))
		for _, property := range objectType.DataProperties {
			requested = append(requested, property.Name)
		}
	}
	dataResult := make([]string, 0, len(requested))
	logicResult := make([]string, 0, len(requested))
	seen := map[string]struct{}{}
	for _, name := range requested {
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		if _, exists := data[name]; exists {
			dataResult = append(dataResult, name)
		} else if _, exists := logic[name]; exists && query.ObjectQueryInfo != nil {
			logicResult = append(logicResult, name)
		} else {
			return nil, nil, true
		}
	}
	if query.ObjectQueryInfo != nil {
		for _, name := range objectType.PrimaryKeys {
			if _, exists := data[name]; !exists {
				continue
			}
			if _, duplicate := seen[name]; !duplicate {
				seen[name] = struct{}{}
				dataResult = append(dataResult, name)
			}
		}
	}
	if query.IncludeLogicParams && query.ObjectQueryInfo == nil {
		for _, property := range objectType.LogicProperties {
			logicResult = append(logicResult, property.Name)
		}
	}
	return dataResult, logicResult, false
}

func explicitReturnFields(query *interfaces.ObjectQueryBaseOnObjectType) bool {
	return len(query.Properties) > 0 || (query.ObjectQueryInfo != nil && len(query.ObjectQueryInfo.Properties) > 0)
}

func logicPropertyDependencies(property *interfaces.LogicProperty,
	decisions map[string]interfaces.PropertyAccessDecision) ([]string, bool) {
	if property == nil {
		return nil, false
	}
	dependencies := make([]string, 0, len(property.Parameters))
	seen := map[string]struct{}{}
	for _, parameter := range property.Parameters {
		if parameter.ValueFrom != interfaces.LOGIC_PARAMS_VALUE_FROM_PROP {
			continue
		}
		name, ok := parameter.Value.(string)
		decision, exists := decisions[name]
		if !ok || !exists || decision.Level != interfaces.PropertyAccessFull {
			return nil, false
		}
		if _, duplicate := seen[name]; !duplicate {
			seen[name] = struct{}{}
			dependencies = append(dependencies, name)
		}
	}
	return dependencies, true
}

func collectOperationFields(condition *cond.CondCfg, result map[string]struct{}, properties map[string]cond.DataProperty) {
	if condition == nil {
		return
	}
	if condition.Operation == cond.OperationMultiMatch {
		fields, exists := condition.RemainCfg["fields"]
		if !exists {
			for name := range properties {
				result[name] = struct{}{}
			}
		} else {
			for _, name := range stringValues(fields) {
				if name == "*" {
					for propertyName := range properties {
						result[propertyName] = struct{}{}
					}
				} else {
					result[name] = struct{}{}
				}
			}
		}
	} else if condition.Name == "*" {
		for name := range properties {
			result[name] = struct{}{}
		}
	} else if strings.TrimSpace(condition.Name) != "" {
		result[condition.Name] = struct{}{}
	}
	for _, child := range condition.SubConds {
		collectOperationFields(child, result, properties)
	}
}

func stringValues(value any) []string {
	switch values := value.(type) {
	case []string:
		return values
	case []any:
		result := make([]string, 0, len(values))
		for _, value := range values {
			if text, ok := value.(string); ok {
				result = append(result, text)
			}
		}
		return result
	case string:
		return []string{values}
	default:
		return nil
	}
}

func (plan *propertyAccessPlan) fieldPropertyMap() map[string]string {
	result := make(map[string]string, len(plan.fetchFields))
	for name := range plan.fetchFields {
		property := plan.properties[name]
		if property.MappedField.Name != "" {
			result[property.MappedField.Name] = name
		}
	}
	return result
}

func (plan *propertyAccessPlan) projectRow(raw map[string]any, objectType *interfaces.ObjectType,
	query *interfaces.ObjectQueryBaseOnObjectType) map[string]any {
	sanitizedValues := make(map[string]any, len(plan.fetchFields))
	for name := range plan.fetchFields {
		value, exists := raw[name]
		if !exists {
			continue
		}
		decision := plan.decisions[name]
		switch decision.Level {
		case interfaces.PropertyAccessFull:
			sanitizedValues[name] = value
		case interfaces.PropertyAccessMasked:
			property := plan.properties[name]
			masked, err := maskrule.Apply(property.Type, property.MaskRule, value)
			if err != nil {
				logger.Warnf("Masking data property [%s/%s/%s] failed and the value was omitted: %v",
					objectType.KNID, objectType.OTID, name, err)
				continue
			}
			sanitizedValues[name] = masked
		}
	}

	result := make(map[string]any, len(plan.returnData)+len(plan.returnLogic)+3)
	for name := range plan.returnData {
		if value, exists := sanitizedValues[name]; exists {
			result[name] = value
		}
	}
	for name := range plan.returnLogic {
		if value, exists := raw[name]; exists {
			result[name] = value
		}
	}
	if plan.allPrimaryFull {
		instanceID, instanceIdentity := logics.GetObjectID(sanitizedValues, objectType)
		if !logics.ShouldExcludeSystemProperty(interfaces.SYSTEM_PROPERTY_INSTANCE_ID, query.ExcludeSystemProperties) {
			result[interfaces.SYSTEM_PROPERTY_INSTANCE_ID] = instanceID
		}
		if !logics.ShouldExcludeSystemProperty(interfaces.SYSTEM_PROPERTY_INSTANCE_IDENTITY, query.ExcludeSystemProperties) {
			result[interfaces.SYSTEM_PROPERTY_INSTANCE_IDENTITY] = instanceIdentity
		}
	}
	if plan.displayVisible && !logics.ShouldExcludeSystemProperty(interfaces.SYSTEM_PROPERTY_DISPLAY, query.ExcludeSystemProperties) {
		if value, exists := sanitizedValues[objectType.DisplayKey]; exists {
			result[interfaces.SYSTEM_PROPERTY_DISPLAY] = value
		}
	}
	if plan.scoreVisible {
		if value, exists := raw[interfaces.SORT_FIELD_SCORE]; exists {
			result[interfaces.SORT_FIELD_SCORE] = value
		}
	}
	return result
}

func (plan *propertyAccessPlan) filterObjectType(objectType interfaces.ObjectType) interfaces.ObjectType {
	filtered := objectType
	filtered.DataProperties = make([]cond.DataProperty, 0, len(objectType.DataProperties))
	for _, property := range objectType.DataProperties {
		decision, exists := plan.decisions[property.Name]
		if exists && decision.Level != interfaces.PropertyAccessNone {
			filtered.DataProperties = append(filtered.DataProperties, property)
		}
	}
	filtered.PrimaryKeys = make([]string, 0, len(objectType.PrimaryKeys))
	for _, name := range objectType.PrimaryKeys {
		if decision, exists := plan.decisions[name]; exists && decision.Level != interfaces.PropertyAccessNone {
			filtered.PrimaryKeys = append(filtered.PrimaryKeys, name)
		}
	}
	if decision, exists := plan.decisions[objectType.DisplayKey]; !exists || decision.Level == interfaces.PropertyAccessNone {
		filtered.DisplayKey = ""
	}
	filtered.LogicProperties = make([]*interfaces.LogicProperty, 0, len(objectType.LogicProperties))
	for _, property := range objectType.LogicProperties {
		if _, visible := logicPropertyDependencies(property, plan.decisions); visible {
			filtered.LogicProperties = append(filtered.LogicProperties, property)
		}
	}
	return filtered
}

func (plan *propertyAccessPlan) filterResourceSchema(response *interfaces.ResourceSchemaResponse) {
	if response == nil {
		return
	}
	visibleFields := map[string]struct{}{}
	for name, decision := range plan.decisions {
		if decision.Level != interfaces.PropertyAccessNone {
			visibleFields[plan.properties[name].MappedField.Name] = struct{}{}
		}
	}
	filtered := make([]map[string]any, 0, len(response.SchemaDefinition))
	for _, definition := range response.SchemaDefinition {
		name, _ := definition["name"].(string)
		if _, visible := visibleFields[name]; visible {
			filtered = append(filtered, definition)
		}
	}
	response.SchemaDefinition = filtered
	response.EffectivePermissions = plan.effective
}

func unavailablePropertyError(ctx context.Context) error {
	return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
		WithErrorDetails("the request contains an unavailable property")
}

func unavailableOperationPropertyError(ctx context.Context) error {
	return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
		WithErrorDetails("the request contains a property unavailable for this operation")
}

func propertyDecisionUnavailable(ctx context.Context, err error) error {
	return rest.NewHTTPError(ctx, http.StatusServiceUnavailable,
		oerrors.OntologyQuery_InternalError_CheckPermissionFailed).WithErrorDetails(err.Error())
}

func invalidQueryCursorError(ctx context.Context) error {
	return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
		WithErrorDetails("query cursor is invalid or expired")
}
