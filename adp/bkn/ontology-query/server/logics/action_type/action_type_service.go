// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package action_type

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/tidwall/sjson"
	"go.opentelemetry.io/otel/attribute"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common"
	oerrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/logics"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/logics/action_target"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/logics/object_type"
	propertyaccess "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/logics/property_access"
)

var (
	atServiceOnce sync.Once
	atService     interfaces.ActionTypeService
)

type actionTypeService struct {
	appSetting *common.AppSetting
	omAccess   interfaces.OntologyManagerAccess
	ots        interfaces.ObjectTypeService
}

func NewActionTypeService(appSetting *common.AppSetting) interfaces.ActionTypeService {
	atServiceOnce.Do(func() {
		atService = &actionTypeService{
			appSetting: appSetting,
			omAccess:   logics.OMA,
			ots:        object_type.NewObjectTypeService(appSetting),
		}
	})
	return atService
}

func (ats *actionTypeService) GetActionsByActionTypeID(ctx context.Context, query *interfaces.ActionQuery) (interfaces.Actions, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "GetActionsByActionTypeID")
	defer span.End()
	var response interfaces.Actions
	action, _, exists, err := ats.omAccess.GetActionType(ctx, query.KNID, query.Branch, query.ActionTypeID)
	if err != nil {
		span.SetAttributes(attribute.Key("at_id").String(query.ActionTypeID))
		otellog.LogError(ctx, fmt.Sprintf("Get Action Type error: %v", err), err)
		if versionErr, ok := logics.KNVersionError(ctx, err); ok {
			return response, versionErr
		}
		return response, httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
			oerrors.OntologyQuery_ObjectType_InternalError_GetObjectTypesByIDFailed).WithErrorDetails(err.Error())
	}
	if !exists {
		return response, httperrors.NewHTTPError(ctx, http.StatusNotFound, oerrors.OntologyQuery_ActionExecution_ActionTypeNotFound)
	}
	resolver := action_target.Resolver{Models: ats.omAccess, Objects: ats.ots}
	schema, err := resolver.ExecutionSchema(ctx, &action, query.KNID, query.Branch)
	if err != nil {
		return response, err
	}
	response.ExecutionSchema, response.ActionSource = schema, action.ActionSource
	if query.IncludeTypeInfo {
		response.ActionType = &action
	}
	if len(query.InstanceIdentities) == 0 && query.RelationTarget == nil && len(query.ObjectInput) == 0 {
		// Definition discovery returns placeholders without requiring runtime values or scanning data.
		definition, err := buildActionFromInstanceData(nil, &action, query.DynamicParams)
		if err != nil {
			return response, err
		}
		response.Actions, response.TotalCount = []interfaces.ActionParam{definition}, 1
		return response, nil
	}
	// A preview may load explicit targets, but input-dependent applicability is
	// enforced only at execution, after the caller has received the schema.
	preview := action
	operation := *action.OperationConfig
	operation.OtherObjectCondition = nil
	preview.OperationConfig, preview.Condition = &operation, nil
	request := &interfaces.ActionExecutionRequest{KNID: query.KNID, Branch: query.Branch,
		InstanceIdentities: query.InstanceIdentities, RelationTarget: query.RelationTarget,
		ObjectInput: query.ObjectInput, DynamicParams: query.DynamicParams}
	instances, data, err := resolver.Resolve(ctx, &preview, request)
	if err != nil {
		return response, err
	}
	response.Actions = make([]interfaces.ActionParam, 0, len(instances))
	for index, instance := range instances {
		item, err := buildActionFromInstanceData(data[index], &action, query.DynamicParams, instance.InstanceIdentity)
		if err != nil {
			return response, err
		}
		item.InstanceID, item.InstanceIdentity, item.Display = instance.InstanceID, instance.InstanceIdentity, instance.Display
		response.Actions = append(response.Actions, item)
	}
	response.TotalCount = len(response.Actions)
	return response, nil
}

func actionPropertyDependencies(actionType interfaces.ActionType,
	objectType interfaces.ObjectType) ([]string, []string) {
	dataProperties := make(map[string]struct{}, len(objectType.DataProperties))
	knownProperties := make(map[string]struct{}, len(objectType.DataProperties)+len(objectType.LogicProperties))
	for _, property := range objectType.DataProperties {
		dataProperties[property.Name] = struct{}{}
		knownProperties[property.Name] = struct{}{}
	}
	for _, property := range objectType.LogicProperties {
		if property != nil {
			knownProperties[property.Name] = struct{}{}
		}
	}
	returnProperties := make([]string, 0, len(actionType.Parameters))
	fullDependencies := make([]string, 0, len(actionType.Parameters))
	seen := map[string]struct{}{}
	for _, parameter := range actionType.Parameters {
		if parameter.ValueFrom != interfaces.LOGIC_PARAMS_VALUE_FROM_PROP {
			continue
		}
		name, ok := parameter.Value.(string)
		if !ok || name == "" {
			continue
		}
		if _, exists := knownProperties[name]; !exists {
			continue
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		returnProperties = append(returnProperties, name)
		if _, isDataProperty := dataProperties[name]; isDataProperty {
			fullDependencies = append(fullDependencies, name)
		}
	}
	propertyNames := make([]string, 0, len(dataProperties))
	for name := range dataProperties {
		propertyNames = append(propertyNames, name)
	}
	for _, name := range propertyaccess.CollectConditionFields(actionType.Condition, propertyNames) {
		if _, isDataProperty := dataProperties[name]; !isDataProperty {
			continue
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		fullDependencies = append(fullDependencies, name)
	}
	if len(returnProperties) == 0 {
		returnProperties = append(returnProperties, objectType.PrimaryKeys...)
	}
	return returnProperties, fullDependencies
}

// buildActionFromInstanceData builds action data from instance data
func buildActionFromInstanceData(instanceData map[string]any,
	actionType *interfaces.ActionType, requestDynamicParams map[string]any, identities ...map[string]any) (interfaces.ActionParam, error) {

	var action interfaces.ActionParam

	paramsJson := "{}"
	dynamicParamsJson := "{}"
	var err error

	for _, param := range actionType.Parameters {
		switch param.ValueFrom {
		case interfaces.LOGIC_PARAMS_VALUE_FROM_PROP:
			propName, ok := param.Value.(string)
			if !ok {
				return action, fmt.Errorf("parameter %s value_from is property but value is not string", param.Name)
			}
			value := instanceData[propName]
			paramsJson, err = sjson.Set(paramsJson, param.Name, value)
			if err != nil {
				return action, fmt.Errorf("error setting action type[%s]'s parameter path %s: %v",
					actionType.ATName, param.Name, err)
			}
		case interfaces.LOGIC_PARAMS_VALUE_FROM_INPUT:
			val := logics.ActionDynamicParamGetValue(requestDynamicParams, param.Name)
			dynamicParamsJson, err = sjson.Set(dynamicParamsJson, param.Name, val)
			if err != nil {
				return action, fmt.Errorf("error setting action type[%s]'s dynamic parameter path %s: %v",
					actionType.ATName, param.Name, err)
			}

		case "source_identity", "target_identity":
			var value any
			if len(identities) == 1 {
				endpoint := "source"
				if param.ValueFrom == "target_identity" {
					endpoint = "target"
				}
				identity, _ := identities[0][endpoint].(map[string]any)
				for _, item := range identity {
					value = item
				}
			}
			paramsJson, err = sjson.Set(paramsJson, param.Name, value)
			if err != nil {
				return action, err
			}
		case interfaces.LOGIC_PARAMS_VALUE_FROM_CONST:
			paramsJson, err = sjson.Set(paramsJson, param.Name, param.Value)
			if err != nil {
				return action, fmt.Errorf("error setting action type[%s]'s parameter path %s: %v",
					actionType.ATName, param.Name, err)
			}
		}
	}

	params := map[string]any{}
	err = common.UnmarshalPreciseJSON([]byte(paramsJson), &params)
	if err != nil {
		return action, fmt.Errorf("failed to Unmarshal action type[%s]'s paramtersJson to map, %s",
			actionType.ATName, err.Error())
	}

	dynamicParams := map[string]any{}
	err = common.UnmarshalPreciseJSON([]byte(dynamicParamsJson), &dynamicParams)
	if err != nil {
		return action, fmt.Errorf("failed to Unmarshal action type[%s]'s dynamicParamsJson to map, %s",
			actionType.ATName, err.Error())
	}

	action = interfaces.ActionParam{
		Parameters:    params,
		DynamicParams: dynamicParams,
	}

	// Set instance identity from instanceData
	if identity, exist := instanceData[interfaces.SYSTEM_PROPERTY_INSTANCE_IDENTITY]; exist {
		action.InstanceIdentity = identity
	} else {
		// If not found, construct from primary keys
		identityMap := make(map[string]any)
		for k, v := range instanceData {
			identityMap[k] = v
		}
		action.InstanceIdentity = identityMap
	}

	return action, nil
}
