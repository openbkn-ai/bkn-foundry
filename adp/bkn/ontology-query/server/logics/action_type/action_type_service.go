// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package action_type

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/tidwall/sjson"
	"go.opentelemetry.io/otel/attribute"

	"ontology-query/common"
	cond "ontology-query/common/condition"
	oerrors "ontology-query/errors"
	"ontology-query/interfaces"
	"ontology-query/logics"
	"ontology-query/logics/object_type"
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

func (ats *actionTypeService) GetActionsByActionTypeID(ctx context.Context,
	query *interfaces.ActionQuery) (interfaces.Actions, error) {

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "查询行动类的行动数据")
	defer span.End()

	var resps interfaces.Actions

	// 1. Get action type information first.
	actionType, _, exists, err := ats.omAccess.GetActionType(ctx, query.KNID, query.Branch, query.ActionTypeID)
	if err != nil {
		span.SetAttributes(attribute.Key("at_id").String(query.ActionTypeID))
		otellog.LogError(ctx, fmt.Sprintf("Get Action Type error: %v", err), err)

		return resps, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			oerrors.OntologyQuery_ObjectType_InternalError_GetObjectTypesByIDFailed).WithErrorDetails(err.Error())
	}
	if !exists {
		logger.Debugf("Action Type %d not found!", query.ActionTypeID)

		span.SetAttributes(attribute.Key("model_id").String(query.ActionTypeID))
		httpErr := rest.NewHTTPError(ctx, http.StatusNotFound, oerrors.OntologyQuery_ObjectType_ObjectTypeNotFound)
		otellog.LogError(ctx, fmt.Sprintf("Action Type [%s] not found!", query.ActionTypeID), httpErr)

		return resps, httpErr
	}

	// Note: the action recall/preview path, used by get_action_info, does not validate dynamic parameter completeness.
	// This stage returns the executable action definition and parameter schema. The agent must read the schema first to know which dynamic parameters to pass.
	// Forcing validation here would cause a deadlock; see issue #371 and the #291 regression. Dynamic parameter completeness is validated only during ExecuteAction.

	// 2. Check whether an object type is bound.
	isObjectTypeBound := actionType.ObjectTypeID != ""
	var objectType interfaces.ObjectType

	if isObjectTypeBound {
		// Get object type information for condition evaluation.
		var exists bool
		var err error
		objectType, exists, err = ats.omAccess.GetObjectType(ctx, query.KNID, query.Branch, actionType.ObjectTypeID)
		if err != nil {
			logger.Errorf("Get Object Type error: %s", err.Error())
			return resps, rest.NewHTTPError(ctx, http.StatusInternalServerError,
				oerrors.OntologyQuery_ObjectType_InternalError_GetObjectTypesByIDFailed).WithErrorDetails(err.Error())
		}
		if !exists {
			logger.Debugf("Object Type %s not found!", actionType.ObjectTypeID)
			return resps, rest.NewHTTPError(ctx, http.StatusNotFound, oerrors.OntologyQuery_ObjectType_ObjectTypeNotFound)
		}
	} else {
		// Unbound object type case.
		logger.Infof("Action type %s has no bound object type", actionType.ATID)
		if len(query.InstanceIdentities) == 0 {
			// Case 4: unbound object type + without identities → construct a temporary virtual instance.
			logger.Infof("No identities provided, creating virtual instance for action type %s", actionType.ATID)
			virtualAction, err := buildActionFromInstanceData(map[string]any{}, &actionType, query.DynamicParams)
			if err != nil {
				logger.Errorf("Error building virtual action: %v", err)
				return resps, rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_InternalError_UnMarshalDataFailed).
					WithErrorDetails(err.Error())
			}

			respActions := interfaces.Actions{
				ActionSource: actionType.ActionSource,
				Actions:      []interfaces.ActionParam{virtualAction},
				TotalCount:   1,
			}

			if query.IncludeTypeInfo {
				respActions.ActionType = &actionType
			}

			return respActions, nil
		} else {
			// Case 5: unbound object type + with identities → construct instances by identities.
			logger.Infof("Constructing instances from identities for action type %s", actionType.ATID)
			actions := []interfaces.ActionParam{}
			for _, identity := range query.InstanceIdentities {
				action, err := buildActionFromInstanceData(identity, &actionType, query.DynamicParams)
				if err != nil {
					logger.Errorf("Error building action from instance data: %v", err)
					return resps, rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_InternalError_UnMarshalDataFailed).
						WithErrorDetails(err.Error())
				}

				actions = append(actions, action)
			}

			respActions := interfaces.Actions{
				ActionSource: actionType.ActionSource,
				Actions:      actions,
				TotalCount:   len(actions),
			}

			if query.IncludeTypeInfo {
				respActions.ActionType = &actionType
			}

			return respActions, nil
		}
	}

	// 3. Handle special logic for add actions.
	if actionType.ActionType == "add" && len(query.InstanceIdentities) > 0 {
		// First query object instances only by _instance_identities, without action conditions.
		instanceCondition := logics.BuildInstanceIdentitiesCondition(query.InstanceIdentities)
		instanceQuery := &interfaces.ObjectQueryBaseOnObjectType{
			ActualCondition: instanceCondition,
			PageQuery: interfaces.PageQuery{
				Limit:     interfaces.MAX_LIMIT,
				NeedTotal: true,
			},
			KNID:         query.KNID,
			Branch:       query.Branch,
			ObjectTypeID: actionType.ObjectTypeID,
			CommonQueryParameters: interfaces.CommonQueryParameters{
				IncludeTypeInfo:         true,
				IncludeLogicParams:      query.IncludeLogicParams,
				ExcludeSystemProperties: query.ExcludeSystemProperties,
			},
			ObjectQueryInfo: &interfaces.ObjectQueryInfo{
				InstanceIdentity: query.InstanceIdentities,
			},
		}
		instanceObjects, err := ats.ots.GetObjectsByObjectTypeID(ctx, instanceQuery)
		if err != nil {
			return resps, err
		}

		// If the query result is empty, treat _instance_identities as new instances and evaluate whether they satisfy the action condition.
		if len(instanceObjects.Datas) == 0 {
			// Case 2a: if nothing is found, construct instances by identities, apply action conditions, and produce instances when they match.
			logger.Infof("No instances found by identities for add action, constructing instances and evaluating condition")
			actions := []interfaces.ActionParam{}
			for _, instanceIdentity := range query.InstanceIdentities {
				// Evaluate whether the instance satisfies the action condition.
				if actionType.Condition != nil {
					satisfies, err := logics.EvaluateInstanceAgainstCondition(ctx, instanceIdentity, actionType.Condition, &objectType)
					if err != nil {
						logger.Errorf("Error evaluating condition for instance[%v], error: %v", instanceIdentity, err)
						continue
					}
					if !satisfies {
						// Skip when the condition is not satisfied.
						continue
					}
				}

				// When the condition is satisfied, build action data.
				action, err := buildActionFromInstanceData(instanceIdentity, &actionType, query.DynamicParams)
				if err != nil {
					logger.Errorf("Error building action from instance data: %v", err)
					return resps, rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_InternalError_UnMarshalDataFailed).
						WithErrorDetails(err.Error())
				}

				actions = append(actions, action)
			}

			respActions := interfaces.Actions{
				ActionSource: actionType.ActionSource,
				Actions:      actions,
				TotalCount:   len(actions),
			}

			if query.IncludeTypeInfo {
				respActions.ActionType = &actionType
			}

			return respActions, nil
		}
		// Case 2b: if found, filter instances by identities and action conditions, then continue the later logic.
		logger.Infof("Instances found by identities for add action, filtering by identities and action condition")
	}

	// 4. Query object instances by action conditions plus requested unique identities. Action conditions can only target the bound object type, so combine them directly and treat them as applying to this object type.
	// Convert conditions by rewriting unique identities to primary-key filters: join objects with OR, primary keys with AND, then AND the result with action conditions to query object data.
	// instance_identities may be empty.
	condition := logics.BuildInstanceIdentitiesCondition(query.InstanceIdentities)

	if actionType.Condition != nil {
		condition = &cond.CondCfg{
			Operation: "and",
			SubConds:  []*cond.CondCfg{condition, actionType.Condition},
		}
	}

	// 5. Retrieve source object type instances using conditions built from action conditions and unique identities.
	objectQuery := &interfaces.ObjectQueryBaseOnObjectType{
		ActualCondition: condition,
		PageQuery: interfaces.PageQuery{
			Limit:     interfaces.MAX_LIMIT, // Do not limit the count; fetch all matching records. The view supports up to 10k, so use 10k.
			NeedTotal: true,
		},
		KNID:         query.KNID,
		Branch:       query.Branch,
		ObjectTypeID: actionType.ObjectTypeID,
		CommonQueryParameters: interfaces.CommonQueryParameters{
			IncludeTypeInfo:         true,
			IncludeLogicParams:      query.IncludeLogicParams,
			ExcludeSystemProperties: query.ExcludeSystemProperties,
		},
		ObjectQueryInfo: &interfaces.ObjectQueryInfo{
			InstanceIdentity: query.InstanceIdentities,
		},
	}
	objects, err := ats.ots.GetObjectsByObjectTypeID(ctx, objectQuery)
	if err != nil {
		return resps, err
	}

	// 6. Retrieved objects satisfy the condition and should each be instantiated as an action.
	actions := []interfaces.ActionParam{}
	for _, object := range objects.Datas {
		paramsJson := "{}"
		dynamicParamsJson := "{}"
		for _, param := range actionType.Parameters {
			switch param.ValueFrom {
			case interfaces.LOGIC_PARAMS_VALUE_FROM_PROP:
				value := object[param.Value.(string)]
				paramsJson, err = sjson.Set(paramsJson, param.Name, value)
				if err != nil {
					return resps, rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_InternalError_UnMarshalDataFailed).
						WithErrorDetails(fmt.Sprintf("Error setting action type[%s]'s parameter path %s: %v",
							actionType.ATName, param.Name, err.Error()))
				}
			case interfaces.LOGIC_PARAMS_VALUE_FROM_INPUT:
				val := logics.ActionDynamicParamGetValue(query.DynamicParams, param.Name)
				dynamicParamsJson, err = sjson.Set(dynamicParamsJson, param.Name, val)
				if err != nil {
					return resps, rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_InternalError_UnMarshalDataFailed).
						WithErrorDetails(fmt.Sprintf("Error setting action type[%s]'s dynamic parameter path %s: %v",
							actionType.ATName, param.Name, err.Error()))
				}
			case interfaces.LOGIC_PARAMS_VALUE_FROM_CONST:
				paramsJson, err = sjson.Set(paramsJson, param.Name, param.Value)
				if err != nil {
					return resps, rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_InternalError_UnMarshalDataFailed).
						WithErrorDetails(fmt.Sprintf("Error setting action type[%s]'s parameter path %s: %v",
							actionType.ATName, param.Name, err.Error()))
				}
			}
		}
		params := map[string]any{}
		err = json.Unmarshal([]byte(paramsJson), &params)
		if err != nil {
			return resps, rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_InternalError_UnMarshalDataFailed).
				WithErrorDetails(fmt.Sprintf("failed to Unmarshal action type[%s]'s paramtersJson to map, %s",
					actionType.ATName, err.Error()))
		}

		dynamicParams := map[string]any{}
		err = json.Unmarshal([]byte(dynamicParamsJson), &dynamicParams)
		if err != nil {
			return resps, rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_InternalError_UnMarshalDataFailed).
				WithErrorDetails(fmt.Sprintf("failed to Unmarshal action type[%s]'s dynamicParamsJson to map, %s",
					actionType.ATName, err.Error()))
		}

		action := interfaces.ActionParam{
			Parameters:    params,
			DynamicParams: dynamicParams,
		}

		// Excluded fields were already specified in the object data query, so returned data is already filtered; add a field if it exists.
		if _, exist := object[interfaces.SYSTEM_PROPERTY_INSTANCE_ID]; exist {
			action.InstanceID = object[interfaces.SYSTEM_PROPERTY_INSTANCE_ID]
		}
		if _, exist := object[interfaces.SYSTEM_PROPERTY_INSTANCE_IDENTITY]; exist {
			action.InstanceIdentity = object[interfaces.SYSTEM_PROPERTY_INSTANCE_IDENTITY]
		}
		if _, exist := object[interfaces.SYSTEM_PROPERTY_DISPLAY]; exist {
			action.Display = object[interfaces.SYSTEM_PROPERTY_DISPLAY]
		}

		// Returned object data has already been generated by query parameters and system fields have been excluded, so add fields as needed.
		actions = append(actions, action)
	}

	respActions := interfaces.Actions{
		ActionSource: actionType.ActionSource,
		Actions:      actions,
		TotalCount:   len(actions),
	}

	if query.IncludeTypeInfo {
		respActions.ActionType = &actionType
	}

	return respActions, nil
}

// buildActionFromInstanceData builds action data from instance data
func buildActionFromInstanceData(instanceData map[string]any,
	actionType *interfaces.ActionType, requestDynamicParams map[string]any) (interfaces.ActionParam, error) {

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
		case interfaces.LOGIC_PARAMS_VALUE_FROM_CONST:
			paramsJson, err = sjson.Set(paramsJson, param.Name, param.Value)
			if err != nil {
				return action, fmt.Errorf("error setting action type[%s]'s parameter path %s: %v",
					actionType.ATName, param.Name, err)
			}
		}
	}

	params := map[string]any{}
	err = json.Unmarshal([]byte(paramsJson), &params)
	if err != nil {
		return action, fmt.Errorf("failed to Unmarshal action type[%s]'s paramtersJson to map, %s",
			actionType.ATName, err.Error())
	}

	dynamicParams := map[string]any{}
	err = json.Unmarshal([]byte(dynamicParamsJson), &dynamicParams)
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
