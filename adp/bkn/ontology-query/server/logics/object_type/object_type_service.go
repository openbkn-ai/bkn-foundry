// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package object_type

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/PaesslerAG/jsonpath"
	"github.com/bytedance/sonic"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/tidwall/sjson"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"ontology-query/common"
	cond "ontology-query/common/condition"
	oerrors "ontology-query/errors"
	"ontology-query/interfaces"
	"ontology-query/locale"
	"ontology-query/logics"
	"ontology-query/logics/metric"
	permissionlogic "ontology-query/logics/permission"
)

var (
	otServiceOnce sync.Once
	otService     interfaces.ObjectTypeService
)

type objectTypeService struct {
	appSetting     *common.AppSetting
	aoAccess       interfaces.AgentOperatorAccess
	mfa            interfaces.ModelFactoryAccess
	omAccess       interfaces.OntologyManagerAccess
	osa            interfaces.OpenSearchAccess
	vba            interfaces.VegaBackendAccess
	mqs            interfaces.MetricQueryService
	proxy          interfaces.ProxyContextResolver
	propertyAccess interfaces.PropertyAccessService
	cursor         *queryCursorCodec
}

func NewObjectTypeService(appSetting *common.AppSetting) interfaces.ObjectTypeService {
	otServiceOnce.Do(func() {
		otService = &objectTypeService{
			appSetting:     appSetting,
			aoAccess:       logics.AOA,
			mfa:            logics.MFA,
			omAccess:       logics.OMA,
			osa:            logics.OSA,
			vba:            logics.VBA,
			mqs:            metric.NewMetricQueryService(appSetting),
			proxy:          logics.PCR,
			propertyAccess: permissionlogic.NewPropertyAccessService(appSetting),
			cursor:         newQueryCursorCodec(),
		}
	})
	return otService
}

func (ots *objectTypeService) GetObjectTypeSchema(ctx context.Context,
	knID, branch, objectTypeID string) (*interfaces.ResourceSchemaResponse, error) {
	if branch != interfaces.MAIN_BRANCH {
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest,
			oerrors.OntologyQuery_ObjectType_InvalidParameter).
			WithErrorDetails("only the published main branch can be queried")
	}
	objectType, exists, err := ots.omAccess.GetObjectType(ctx, knID, branch, objectTypeID)
	if err != nil {
		return nil, rest.NewHTTPError(ctx, http.StatusServiceUnavailable,
			oerrors.OntologyQuery_ObjectType_InternalError_GetObjectTypesByIDFailed).WithErrorDetails(err.Error())
	}
	if !exists {
		return nil, rest.NewHTTPError(ctx, http.StatusNotFound, oerrors.OntologyQuery_ObjectType_ObjectTypeNotFound)
	}
	if objectType.KNID != "" && objectType.KNID != knID {
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest,
			oerrors.OntologyQuery_ObjectType_InvalidParameter).WithErrorDetails("object type belongs to another knowledge network")
	}
	objectType.KNID = knID
	plan, err := buildPropertyAccessPlan(ctx, ots.propertyAccess, objectType, nil, false)
	if err != nil {
		return nil, err
	}
	if objectType.DataSource == nil || objectType.DataSource.Type != interfaces.DATA_SOURCE_TYPE_RESOURCE ||
		strings.TrimSpace(objectType.DataSource.ID) == "" {
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest,
			oerrors.OntologyQuery_ObjectType_InvalidParameter).WithErrorDetails("object type has no published resource data source")
	}
	if ots.proxy == nil {
		return nil, rest.NewHTTPError(ctx, http.StatusServiceUnavailable,
			oerrors.OntologyQuery_InternalError_CheckPermissionFailed).
			WithErrorDetails("knowledge network proxy resolver is not configured")
	}
	proxyContext, err := ots.proxy.Resolve(ctx, interfaces.TrustedProxyBinding{
		KNID:       knID,
		ChildType:  interfaces.PermissionResourceTypeObjectType,
		ChildID:    objectType.OTID,
		TargetType: interfaces.ProxyTargetTypeResource,
		TargetID:   objectType.DataSource.ID,
		Operation:  interfaces.PermissionOperationViewDetail,
	})
	if err != nil {
		return nil, err
	}
	response, err := ots.vba.GetResourceSchema(interfaces.WithTrustedProxyContext(ctx, proxyContext), objectType.DataSource.ID)
	if err != nil {
		if downstream, ok := interfaces.AsVegaDownstreamError(err); ok && downstream.IsClientError() {
			return nil, rest.NewHTTPError(ctx, downstream.StatusCode, proxyDownstreamErrorCode(downstream.StatusCode))
		}
		return nil, rest.NewHTTPError(ctx, http.StatusServiceUnavailable,
			oerrors.OntologyQuery_ObjectType_InternalError_GetObjectTypesByIDFailed)
	}
	plan.filterResourceSchema(response)
	return response, nil
}

func (ots *objectTypeService) GetObjectTypeSampleData(ctx context.Context,
	query *interfaces.ObjectQueryBaseOnObjectType) (*interfaces.ObjectTypeSampleData, error) {
	if query == nil {
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter)
	}
	if query.Limit <= 0 {
		query.Limit = 20
	}
	if query.Limit > 100 {
		query.Limit = 100
	}
	if query.Offset < 0 {
		query.Offset = 0
	}
	query.IncludeTypeInfo = true
	query.IncludeLogicParams = false
	query.ExcludeSystemProperties = []string{
		interfaces.SYSTEM_PROPERTY_INSTANCE_ID,
		interfaces.SYSTEM_PROPERTY_INSTANCE_IDENTITY,
		interfaces.SYSTEM_PROPERTY_DISPLAY,
	}
	objects, err := ots.GetObjectsByObjectTypeID(ctx, query)
	if err != nil {
		return nil, err
	}
	if objects.ObjectType == nil {
		return nil, rest.NewHTTPError(ctx, http.StatusServiceUnavailable,
			oerrors.OntologyQuery_ObjectType_InternalError_GetObjectTypesByIDFailed)
	}
	result := &interfaces.ObjectTypeSampleData{
		Columns:              []*interfaces.ObjectTypeSampleDataColumn{},
		Entries:              objects.Datas,
		Name:                 objects.ObjectType.OTName,
		TotalCount:           objects.TotalCount,
		SearchAfter:          objects.SearchAfter,
		Cursor:               objects.Cursor,
		EffectivePermissions: objects.EffectivePermissions,
	}
	for _, property := range objects.ObjectType.DataProperties {
		if strings.TrimSpace(property.Name) == "" {
			continue
		}
		title := property.DisplayName
		if title == "" {
			title = property.Name
		}
		result.Columns = append(result.Columns, &interfaces.ObjectTypeSampleDataColumn{
			DataIndex: property.Name,
			Title:     title,
		})
	}
	return result, nil
}

func (ots *objectTypeService) GetObjectsByObjectTypeID(ctx context.Context,
	query *interfaces.ObjectQueryBaseOnObjectType) (interfaces.Objects, error) {

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "查询对象类的对象数据")
	defer span.End()

	start := time.Now().UnixMilli()

	var resps interfaces.Objects
	if query == nil || query.Branch != interfaces.MAIN_BRANCH {
		return resps, rest.NewHTTPError(ctx, http.StatusBadRequest,
			oerrors.OntologyQuery_ObjectType_InvalidParameter).
			WithErrorDetails("only the published main branch can be queried")
	}

	objectType, exists, err := ots.omAccess.GetObjectType(ctx, query.KNID, query.Branch, query.ObjectTypeID)
	if err != nil {
		span.SetAttributes(attribute.Key("model_id").String(query.ObjectTypeID))
		otellog.LogError(ctx, fmt.Sprintf("Get Object Type error: %v", err), err)

		return resps, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			oerrors.OntologyQuery_ObjectType_InternalError_GetObjectTypesByIDFailed).WithErrorDetails(err.Error())
	}
	if !exists {
		logger.Debugf("Object Type %d not found!", query.ObjectTypeID)

		span.SetAttributes(attribute.Key("model_id").String(query.ObjectTypeID))
		httpErr := rest.NewHTTPError(ctx, http.StatusNotFound, oerrors.OntologyQuery_ObjectType_ObjectTypeNotFound)
		otellog.LogError(ctx, fmt.Sprintf("Object Type [%s] not found!", query.ObjectTypeID), httpErr)

		return resps, httpErr
	}
	objectType.KNID = query.KNID
	if query.ObjectQueryInfo != nil {
		for _, instanceIdentity := range query.ObjectQueryInfo.InstanceIdentity {
			for _, key := range objectType.PrimaryKeys {
				if _, exists := instanceIdentity[key]; !exists {
					return resps, rest.NewHTTPError(ctx, http.StatusBadRequest,
						oerrors.OntologyQuery_ObjectType_InvalidParameter).
						WithErrorDetails("one or more instance identities are invalid")
				}
			}
		}
	}

	if query.Sort == nil {
		query.Sort = logics.BuildViewSort(objectType)
	}
	plan, err := buildPropertyAccessPlan(ctx, ots.propertyAccess, objectType, query, true)
	if err != nil {
		return resps, err
	}

	// Sort fields can be object type data properties or _score.

	// 3.1 Process the object type and convert it into a view-field to object-type-property mapping.
	// Mapping from view fields to object type properties.
	viewFieldPropMap := plan.fieldPropertyMap()
	// Mapping from object type property names to property names for use in case-to-index queries. Object index field names stay consistent with property names.
	indexPropMap := make(map[string]string, len(plan.fetchFields))
	for _, prop := range objectType.DataProperties {
		if _, needed := plan.fetchFields[prop.Name]; needed {
			indexPropMap[prop.Name] = prop.Name
		}
	}

	dataSourceType := ""
	if objectType.DataSource != nil {
		dataSourceType = objectType.DataSource.Type
	}
	if objectType.DataSource == nil || objectType.DataSource.ID == "" {
		return resps, logics.MissingObjectTypeDataSourceError(ctx, objectType.OTID)
	}
	if dataSourceType != interfaces.DATA_SOURCE_TYPE_RESOURCE {
		return resps, logics.UnsupportedObjectTypeDataSourceError(ctx, objectType.OTID, dataSourceType)
	}
	if ots.proxy == nil {
		return resps, rest.NewHTTPError(ctx, http.StatusServiceUnavailable,
			oerrors.OntologyQuery_InternalError_CheckPermissionFailed).
			WithErrorDetails("knowledge network proxy resolver is not configured")
	}
	proxyContext, err := ots.proxy.Resolve(ctx, interfaces.TrustedProxyBinding{
		KNID:       query.KNID,
		ChildType:  interfaces.PermissionResourceTypeObjectType,
		ChildID:    objectType.OTID,
		TargetType: interfaces.ProxyTargetTypeResource,
		TargetID:   objectType.DataSource.ID,
		Operation:  interfaces.PermissionOperationQueryData,
	})
	if err != nil {
		return resps, err
	}
	ctx = interfaces.WithTrustedProxyContext(ctx, proxyContext)
	if query.Cursor != "" {
		if ots.cursor == nil {
			return resps, invalidQueryCursorError(ctx)
		}
		searchAfter, err := ots.cursor.decode(ctx, query, proxyContext.PublishedModelVersion, query.Cursor)
		if err != nil {
			return resps, invalidQueryCursorError(ctx)
		}
		query.SearchAfter = searchAfter
	}

	// 3. Request Vega Resource to get data.
	err = ots.getObjectsFromResource(ctx, query, objectType, &resps, viewFieldPropMap, plan)
	if err != nil {
		return resps, err
	}

	if query.IncludeTypeInfo {
		filteredObjectType := plan.filterObjectType(objectType)
		resps.ObjectType = &filteredObjectType
	}
	resps.EffectivePermissions = plan.effective
	if len(resps.SearchAfter) > 0 {
		if ots.cursor == nil {
			return interfaces.Objects{}, propertyDecisionUnavailable(ctx, fmt.Errorf("query cursor codec is not configured"))
		}
		resps.Cursor, err = ots.cursor.encode(ctx, query, proxyContext.PublishedModelVersion, resps.SearchAfter)
		if err != nil {
			return interfaces.Objects{}, propertyDecisionUnavailable(ctx, err)
		}
	}

	logger.Debugf("从对象类[%s]中获取到的数据条数为[%d],耗时: %dms", objectType.OTID, len(resps.Datas), time.Now().UnixMilli()-start)

	return resps, nil
}

// addLogicProperties builds only outputs whose complete input closure was
// approved by the access plan. The raw dependency values stay in this local
// row and are discarded immediately after projection.
func (*objectTypeService) addLogicProperties(ctx context.Context, object map[string]any,
	objectType interfaces.ObjectType, plan *propertyAccessPlan) error {

	var err error

	for _, logicProp := range objectType.LogicProperties {
		if _, allowed := plan.returnLogic[logicProp.Name]; !allowed {
			continue
		}
		switch logicProp.Type {
		case interfaces.LOGIC_PROPERTY_TYPE_METRIC:
			filters := []interfaces.Filter{}
			dynamicParams := map[string]any{}
			for _, param := range logicProp.Parameters {
				switch param.ValueFrom {
				case interfaces.LOGIC_PARAMS_VALUE_FROM_PROP:
					value := object[param.Value.(string)]
					filters = append(filters, interfaces.Filter{
						Name:      param.Name,
						Operation: param.Operation,
						Value:     value,
					})
				case interfaces.LOGIC_PARAMS_VALUE_FROM_CONST:
					// Fixed parameter and.
					filters = append(filters, interfaces.Filter{
						Name:      param.Name,
						Operation: "==",
						Value:     param.Value,
					})
				case interfaces.LOGIC_PARAMS_VALUE_FROM_INPUT:
					dynamicParams[param.Name] = param
				}
			}

			mProp := interfaces.MetricProperty{
				PropertyType:    logicProp.Type,
				MappingSourceId: logicProp.DataSource.ID,
				Parameters: interfaces.MetricFilters{
					Filters: filters,
				},
				DynamicParams: dynamicParams,
			}
			object[logicProp.Name] = mProp

		case interfaces.LOGIC_PROPERTY_TYPE_TOOL:
			paramsJson := "{}"
			dynamicParamsJson := "{}"
			for _, param := range logicProp.Parameters {
				switch param.ValueFrom {
				case interfaces.LOGIC_PARAMS_VALUE_FROM_PROP:
					value := object[param.Value.(string)]
					paramsJson, err = sjson.Set(paramsJson, param.Name, value)
					if err != nil {
						return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_InternalError_UnMarshalDataFailed).
							WithErrorDetails(fmt.Sprintf("Error setting logic property[%s]'s parameter path %s: %v",
								logicProp.Name, param.Name, err.Error()))
					}

				case interfaces.LOGIC_PARAMS_VALUE_FROM_CONST:
					paramsJson, err = sjson.Set(paramsJson, param.Name, param.Value)
					if err != nil {
						return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_InternalError_UnMarshalDataFailed).
							WithErrorDetails(fmt.Sprintf("Error setting logic property[%s]'s parameter path %s: %v",
								logicProp.Name, param.Name, err.Error()))
					}
				case interfaces.LOGIC_PARAMS_VALUE_FROM_INPUT:
					dynamicParamsJson, err = sjson.Set(dynamicParamsJson, param.Name, param)
					if err != nil {
						return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_InternalError_UnMarshalDataFailed).
							WithErrorDetails(fmt.Sprintf("Error setting logic property[%s]'s dynamic parameter path %s: %v",
								logicProp.Name, param.Name, err.Error()))
					}
				}
			}
			params := map[string]any{}
			err = sonic.Unmarshal([]byte(paramsJson), &params)
			if err != nil {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_InternalError_UnMarshalDataFailed).
					WithErrorDetails(fmt.Sprintf("failed to Unmarshal logic property[%s]'s paramtersJson to map, %s",
						logicProp.Name, err.Error()))
			}

			dynamicParams := map[string]any{}
			err = sonic.Unmarshal([]byte(dynamicParamsJson), &dynamicParams)
			if err != nil {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_InternalError_UnMarshalDataFailed).
					WithErrorDetails(fmt.Sprintf("failed to Unmarshal logic property[%s]'s dynamicParamsJson to map, %s",
						logicProp.Name, err.Error()))
			}

			toolProp := interfaces.ToolProperty{
				PropertyType:  logicProp.Type,
				Parameters:    params,
				DynamicParams: dynamicParams,
			}
			object[logicProp.Name] = toolProp

		default:
			logger.Warnf("系统支持的逻辑属性类型有[metric, tool],当前请求的逻辑属性类型为[%s]，请求将不返回逻辑属性的计算参数", logicProp.Type)
		}
	}
	return nil
}

// getObjectsFromResource queries vega-backend resource data (same row mapping as view path).
// downstreamErrorCode selects the error code by downstream status code.
//
// If the status code is passed through but every error code is mapped to InvalidParameter, then 403 (no permission for the resource),
// 409 (catalog disabled), and 429 (concurrency exceeded) are all described as "parameter error". When the frontend reads 403, it may
// treat it as the user lacking permission; when callers read 429, they may change the query instead of retrying. Error codes must follow status codes.
func downstreamErrorCode(statusCode int) string {
	switch statusCode {
	case http.StatusBadRequest:
		return oerrors.OntologyQuery_ObjectType_InvalidParameter
	case http.StatusUnauthorized:
		return rest.PublicError_Unauthorized
	case http.StatusForbidden:
		return rest.PublicError_Forbidden
	case http.StatusNotFound:
		return rest.PublicError_NotFound
	case http.StatusConflict:
		return rest.PublicError_Conflict
	default:
		// Other 4xx statuses (405/413/422/429, etc.) do not have semantically corresponding public error codes. Do not fall back here to
		// rest.PublicError_BadRequest: its en-US message is "Internal Server Error",
		// English callers would read "Internal Server Error" for a 429, which is exactly the misleading behavior this change avoids.
		// Fall back to this service's parameter error code: messages in both languages are correct and behavior stays consistent with before. The real
		// semantics are carried by the faithfully passed-through status code and the reason returned by downstream.
		return oerrors.OntologyQuery_ObjectType_InvalidParameter
	}
}

// proxyDownstreamErrorCode distinguishes a denied managed principal from a
// denied business caller. Caller authorization has already succeeded before a
// trusted proxy context reaches Vega.
func proxyDownstreamErrorCode(statusCode int) string {
	if statusCode == http.StatusForbidden {
		return oerrors.OntologyQuery_Proxy_PermissionDenied
	}
	return downstreamErrorCode(statusCode)
}

func (ots *objectTypeService) getObjectsFromResource(ctx context.Context, query *interfaces.ObjectQueryBaseOnObjectType,
	objectType interfaces.ObjectType, resps *interfaces.Objects, fieldPropMap map[string]string,
	plan *propertyAccessPlan) error {

	resourceSort, err := logics.MapSortFieldsForDataView(ctx, query.Sort, objectType)
	if err != nil {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
			WithErrorDetails(err.Error())
	}

	viewQuery := interfaces.ViewQuery{
		NeedTotal:         query.NeedTotal,
		Limit:             query.Limit,
		UseSearchAfter:    interfaces.USE_SEARCH_AFTER_TRUE,
		Sort:              resourceSort,
		SearchAfterParams: query.SearchAfterParams,
	}
	if query.ActualCondition != nil {
		rewriteCondition, err := cond.RewriteCondition(ctx, query.ActualCondition,
			logics.TransferPropsToPropMap(objectType.DataProperties),
			logics.MemoizeVectorizer(func(ctx context.Context, property *cond.DataProperty, word string) ([]cond.VectorResp, error) {
				return ots.handlerVector(ctx, property, word)
			}))
		if err != nil {
			return rest.NewHTTPError(ctx, http.StatusBadRequest,
				oerrors.OntologyQuery_InvalidParameter_Condition).
				WithErrorDetails(fmt.Sprintf("failed to rewrite ontology condition for resource, %s", err.Error()))
		}
		viewQuery.Filters = rewriteCondition
	}
	if objectType.DataSource == nil || objectType.DataSource.ID == "" {
		return rest.NewHTTPError(ctx, http.StatusBadRequest,
			oerrors.OntologyQuery_ObjectType_InvalidParameter).
			WithErrorDetails(fmt.Sprintf("object type [%s] has empty data source", objectType.OTID))
	}

	outputFields := make([]string, 0, len(fieldPropMap))
	for k := range fieldPropMap {
		outputFields = append(outputFields, k)
	}
	sort.Strings(outputFields)
	params := &interfaces.ResourceDataQueryParams{
		NeedTotal: query.NeedTotal,
		Paging: interfaces.ResourceDataPagingRequest{
			Mode:   "single",
			Limit:  query.Limit,
			Offset: query.Offset,
		},
		Sort:            resourceSort,
		SearchAfter:     query.SearchAfter,
		FilterCondition: logics.CondCfgToFilterMap(viewQuery.Filters),
		OutputFields:    outputFields,
	}
	resp, err := ots.vba.QueryResourceData(ctx, objectType.DataSource.ID, params)
	if err != nil {
		// When downstream identifies a caller-side issue (4xx), pass through the original status code and carry its reason upward.
		// Upgrading everything to 500 makes self-correctable problems such as unsupported operators or resources without built indexes look
		// like service failures, preventing callers from self-correcting and sending manual investigation in the wrong direction.
		if downstream, ok := interfaces.AsVegaDownstreamError(err); ok && downstream.IsClientError() {
			return rest.NewHTTPError(ctx, downstream.StatusCode,
				proxyDownstreamErrorCode(downstream.StatusCode))
		}
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			oerrors.OntologyQuery_ObjectType_InternalError_GetViewDataByIDFailed).WithErrorDetails(err.Error())
	}
	if resp == nil {
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			oerrors.OntologyQuery_ObjectType_InternalError_GetViewDataByIDFailed).WithErrorDetails("vega resource query returned nil")
	}

	objects := make([]map[string]any, 0, len(resp.Entries))
	for _, col := range resp.Entries {
		rawObject := map[string]any{}
		for k, v := range col {
			if propName, exists := fieldPropMap[k]; exists {
				rawObject[propName] = v
			}
		}
		if err := ots.addLogicProperties(ctx, rawObject, objectType, plan); err != nil {
			return err
		}
		object := plan.projectRow(rawObject, &objectType, query)
		if len(object) > 0 {
			objects = append(objects, object)
		} else {
			logger.Warnf("resource row could not produce a sanitized object for object type [%s/%s]", query.KNID, objectType.OTID)
		}
	}
	resps.TotalCount = resp.TotalCount
	resps.SearchAfter = resp.SearchAfter
	resps.Datas = objects
	return nil
}

// getObjectsFromObjectIndex retrieves object data from the object-type index.
func (ots *objectTypeService) getObjectsFromObjectIndex(ctx context.Context, query *interfaces.ObjectQueryBaseOnObjectType,
	objectType interfaces.ObjectType, resps *interfaces.Objects, indexPropMap map[string]string,
	plan *propertyAccessPlan) error {

	objects := []map[string]any{}

	// Build the DSL filter condition.
	conditionDslStr := "{}"
	if query.ActualCondition != nil {
		condtion, err := cond.NewCondition(ctx, query.ActualCondition, 1, logics.TransferPropsToPropMap(objectType.DataProperties))
		if err != nil {
			return rest.NewHTTPError(ctx, http.StatusBadRequest,
				oerrors.OntologyQuery_InvalidParameter_Condition).
				WithErrorDetails(locale.ValidationDetail(ctx, "QueryConditionInvalid", map[string]any{"error": err.Error()}))
		}

		// Convert the condition to DSL.
		conditionDslStr, err = condtion.Convert(ctx, logics.MemoizeVectorizer(
			func(ctx context.Context, property *cond.DataProperty, word string) ([]cond.VectorResp, error) {
				return ots.handlerVector(ctx, property, word)
			}))
		if err != nil {
			return rest.NewHTTPError(ctx, http.StatusBadRequest,
				oerrors.OntologyQuery_InvalidParameter_Condition).
				WithErrorDetails(locale.ValidationDetail(ctx, "ConditionToDSLFailed", map[string]any{"error": err.Error()}))
		}

	}

	dsl, err := logics.BuildDslQuery(ctx, conditionDslStr, query)
	if err != nil {
		return err
	}
	sourceFields := make([]string, 0, len(indexPropMap))
	for field := range indexPropMap {
		sourceFields = append(sourceFields, field)
	}
	sort.Strings(sourceFields)
	dsl["_source"] = sourceFields
	// Query OpenSearch.
	osHits, err := ots.osa.SearchData(ctx, objectType.Status.Index, dsl)
	if err != nil {
		logger.Errorf("SearchData error: %s", err.Error())
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			oerrors.OntologyQuery_InternalError_SearchDataFromOpensearchFailed).
			WithErrorDetails(fmt.Sprintf("search data from opensearch error: %s", err.Error()))
	}

	// Decide whether to query the total based on NeedTotal.
	if query.NeedTotal {
		total, err := ots.GetTotal(ctx, objectType.Status.Index, dsl)
		if err != nil {
			return err
		}
		resps.TotalCount = total
	}

	// Append each data row to the result.
	for _, hit := range osHits {
		// One row is one object.
		rawObject := map[string]any{}
		for k, v := range hit.Source {
			// k is the view field name, and v is this field's value.
			if propName, exists := indexPropMap[k]; exists {
				// Set the field only when it belongs to requested properties.
				// If a mapping exists, assemble it into object properties.
				rawObject[propName] = v
			}
		}
		// Add the _score field.
		rawObject[interfaces.SORT_FIELD_SCORE] = hit.Score
		if err := ots.addLogicProperties(ctx, rawObject, objectType, plan); err != nil {
			return err
		}
		object := plan.projectRow(rawObject, &objectType, query)

		if len(object) > 0 {
			objects = append(objects, object)
		} else {
			logger.Warnf("OpenSearch row could not produce a sanitized object for object type [%s/%s]", query.KNID, objectType.OTID)
		}
	}

	var searchAfter []any
	if len(osHits) > 0 {
		searchAfter = osHits[len(osHits)-1].Sort
	} else {
		searchAfter = nil
	}
	resps.SearchAfter = searchAfter

	resps.Datas = objects

	return nil
}

func (ots *objectTypeService) GetTotal(ctx context.Context, index string, dsl map[string]any) (total int64, err error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "logic layer: search object type total ")
	defer span.End()

	// delete(dsl, "pit")
	delete(dsl, "from")
	delete(dsl, "size")
	delete(dsl, "sort")
	delete(dsl, "track_scores")
	totalBytes, err := ots.osa.Count(ctx, index, dsl)
	if err != nil {
		otellog.LogError(ctx, "Search total documents count failed", err)
		httpErr := rest.NewHTTPError(ctx, http.StatusInternalServerError, oerrors.OntologyQuery_InternalError).
			WithErrorDetails(err.Error())
		return total, httpErr
	}

	totalNode, err := sonic.Get(totalBytes, "count")
	if err != nil {
		otellog.LogError(ctx, "Get total documents count failed", err)
		httpErr := rest.NewHTTPError(ctx, http.StatusInternalServerError, oerrors.OntologyQuery_InternalError).
			WithErrorDetails(err.Error())
		return total, httpErr
	}

	total, err = totalNode.Int64()
	if err != nil {
		otellog.LogError(ctx, "Convert total documents count to type int64 failed", err)
		httpErr := rest.NewHTTPError(ctx, http.StatusInternalServerError, oerrors.OntologyQuery_InternalError).
			WithErrorDetails(err.Error())
		return total, httpErr
	}

	span.SetStatus(codes.Ok, "")
	return total, nil
}

// Vectorize the query statement.
func (ots *objectTypeService) handlerVector(ctx context.Context, property *cond.DataProperty, word string) ([]cond.VectorResp, error) {
	return nil, fmt.Errorf("ontology-query no longer vectorizes property %q; Vega Resource resolves vector conditions", property.Name)
}

func (ots *objectTypeService) GetObjectPropertyValue(ctx context.Context,
	query *interfaces.ObjectPropertyValueQuery) (interfaces.Objects, error) {

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "查询对象属性值")
	defer span.End()

	var resps interfaces.Objects

	// 1. Build filter conditions from unique identities.
	ukCond := logics.BuildInstanceIdentitiesCondition(query.InstanceIdentities)
	// 2. Retrieve object type instances using conditions built from unique identities.
	objectQuery := &interfaces.ObjectQueryBaseOnObjectType{
		ActualCondition: ukCond,
		PageQuery: interfaces.PageQuery{
			Limit:     interfaces.MAX_LIMIT, // Do not limit the count; fetch all matching records. The view supports up to 10k, so use 10k.
			NeedTotal: true,
		},
		KNID:         query.KNID,
		Branch:       query.Branch,
		ObjectTypeID: query.ObjectTypeID,
		CommonQueryParameters: interfaces.CommonQueryParameters{
			IncludeTypeInfo:         true, // Object type information needs to be returned.
			IncludeLogicParams:      true, // Logical-property calculation parameters need to be returned.
			ExcludeSystemProperties: query.ExcludeSystemProperties,
		},
		ObjectQueryInfo: &interfaces.ObjectQueryInfo{
			InstanceIdentity: query.InstanceIdentities,
			Properties:       query.Properties,
		},
	}
	objects, err := ots.GetObjectsByObjectTypeID(ctx, objectQuery)
	if err != nil {
		return resps, err
	}

	// Convert object type properties into a map.
	dataProperties := map[string]cond.DataProperty{}
	for _, prop := range objects.ObjectType.DataProperties {
		dataProperties[prop.Name] = prop
	}
	logicProperties := map[string]*interfaces.LogicProperty{}
	for _, prop := range objects.ObjectType.LogicProperties {
		logicProperties[prop.Name] = prop
	}

	// Target property.
	propertyNames := map[string]bool{}
	for _, propName := range query.Properties {
		propertyNames[propName] = true
	}
	// Add the primary key.
	for _, key := range objects.ObjectType.PrimaryKeys {
		propertyNames[key] = true
	}

	datas := make([]map[string]any, len(objects.Datas))
	// Step 1: synchronously process data properties for all objects.
	for i, object := range objects.Datas {
		newObject := make(map[string]any)
		for prop, value := range object {
			if !propertyNames[prop] {
				continue
			}
			// Assign data properties directly.
			if _, exist := dataProperties[prop]; exist {
				newObject[prop] = value
			}
		}

		// Excluded fields were already specified in the object data query, so returned data is already filtered; add a field if it exists.
		if _, exist := object[interfaces.SYSTEM_PROPERTY_INSTANCE_ID]; exist {
			newObject[interfaces.SYSTEM_PROPERTY_INSTANCE_ID] = object[interfaces.SYSTEM_PROPERTY_INSTANCE_ID]
		}
		if _, exist := object[interfaces.SYSTEM_PROPERTY_INSTANCE_IDENTITY]; exist {
			newObject[interfaces.SYSTEM_PROPERTY_INSTANCE_IDENTITY] = object[interfaces.SYSTEM_PROPERTY_INSTANCE_IDENTITY]
		}
		if _, exist := object[interfaces.SYSTEM_PROPERTY_DISPLAY]; exist {
			newObject[interfaces.SYSTEM_PROPERTY_DISPLAY] = object[interfaces.SYSTEM_PROPERTY_DISPLAY]
		}

		datas[i] = newObject
	}
	// Step 2: concurrently process all logical properties for all objects.
	var wg sync.WaitGroup
	var mu sync.Mutex
	errChan := make(chan error, len(objects.Datas)*len(logicProperties)) // Large enough buffer.

	for i, object := range objects.Datas {
		for prop, value := range object {
			if !propertyNames[prop] {
				continue
			}

			// Process only logical properties.
			if logicProp, exist := logicProperties[prop]; exist {
				wg.Add(1)
				go func(objIndex int, propName string, propValue any, logicProp *interfaces.LogicProperty) {
					defer wg.Done()

					logger.Debugf("处理对象[%d]的逻辑属性: %s", i, propName)
					resultValue, err := ots.processLogicProperty(ctx, query.KNID, query.Branch, query.ObjectTypeID,
						propName, propValue, logicProp, query.DynamicParams)
					if err != nil {
						detail := locale.ValidationDetail(ctx, "LogicPropertyProcessFailed", map[string]any{
							"index":    objIndex,
							"property": propName,
						})
						errChan <- fmt.Errorf("%s: %w", detail, err)
						return
					}

					// Safely write the result to the corresponding object.
					mu.Lock()
					datas[objIndex][propName] = resultValue
					mu.Unlock()

				}(i, prop, value, logicProp)
			}
		}
	}

	// Wait for all logical-property processing to complete.
	wg.Wait()
	close(errChan)

	// Check errors.
	if len(errChan) > 0 {
		var errors []string
		for err := range errChan {
			errors = append(errors, err.Error())
		}
		return resps, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			oerrors.OntologyQuery_ObjectType_InternalError_ProcessLogicPropertiesFailed).
			WithErrorDetails(strings.Join(errors, "; "))
	}

	resps.Datas = datas
	resps.ObjectType = objects.ObjectType
	resps.TotalCount = objects.TotalCount
	resps.SearchAfter = objects.SearchAfter
	resps.Cursor = objects.Cursor
	resps.EffectivePermissions = objects.EffectivePermissions
	return resps, nil

}

// processLogicProperty handles a single logical property and wraps the original processing logic.
func (ots *objectTypeService) processLogicProperty(ctx context.Context,
	knID, branch, otID string,
	propName string,
	propValue any,
	logicProp *interfaces.LogicProperty,
	dynamicParams map[string]map[string]any) (any, error) {

	switch logicProp.Type {
	case interfaces.PROPERTY_TYPE_METRIC:
		return ots.handleMetricProperty(ctx, knID, branch, otID, propName, propValue, logicProp, dynamicParams)
	case interfaces.LOGIC_PROPERTY_TYPE_TOOL:
		return ots.handleToolProperty(ctx, knID, otID, propName, propValue, logicProp, dynamicParams)
	default:
		logger.Warnf("不支持的逻辑属性类型: %s", logicProp.Type)
		return nil, nil
	}
}

// handleMetricProperty handles metric-type logical properties using KN MetricDefinition plus the Vega value-computation kernel.
func (ots *objectTypeService) handleMetricProperty(ctx context.Context,
	knID, branch, otID string,
	propName string,
	propValue any,
	logicProp *interfaces.LogicProperty,
	dynamicParams map[string]map[string]any) (interfaces.MetricData, error) {

	var (
		start     int64
		end       int64
		isInstant bool
		step      string
	)

	metricValue := propValue.(interfaces.MetricProperty)
	start = time.Now().Add(-30 * time.Minute).UnixMilli()
	end = time.Now().UnixMilli()
	isInstant = true

	var metricParams interfaces.MetricPropertyDynamicParams
	paramBytes, err := sonic.Marshal(dynamicParams[propName])
	if err != nil {
		return interfaces.MetricData{}, rest.NewHTTPError(ctx, http.StatusBadRequest,
			oerrors.OntologyQuery_ObjectType_InvalidParameter_DynamicParams).
			WithErrorDetails(locale.ValidationDetail(ctx, "DynamicParamDecodeFailed", map[string]any{
				"property": propName, "error": err.Error(),
			}))
	}
	if err = sonic.Unmarshal(paramBytes, &metricParams); err != nil {
		return interfaces.MetricData{}, rest.NewHTTPError(ctx, http.StatusBadRequest,
			oerrors.OntologyQuery_ObjectType_InvalidParameter_DynamicParams).
			WithErrorDetails(locale.ValidationDetail(ctx, "DynamicParamDecodeFailed", map[string]any{
				"property": propName, "error": err.Error(),
			}))
	}

	if metricParams.Start != nil {
		start = *metricParams.Start
	}
	if metricParams.End != nil {
		end = *metricParams.End
		if metricParams.Start == nil {
			start = end - 30*time.Minute.Milliseconds()
		}
	}
	if metricParams.Instant != nil {
		isInstant = *metricParams.Instant
	}
	if metricParams.Step != nil {
		step = *metricParams.Step
	}

	for paramK := range metricValue.DynamicParams {
		switch paramK {
		case "start", "end", "instant", "step":
			continue
		default:
			paramValue, paramExist := dynamicParams[propName][paramK]
			if !paramExist {
				return interfaces.MetricData{}, rest.NewHTTPError(ctx, http.StatusBadRequest,
					oerrors.OntologyQuery_ObjectType_InvalidParameter_DynamicParams).
					WithErrorDetails(locale.ValidationDetail(ctx, "MetricDynamicParamRequired", map[string]any{
						"property": propName, "parameter": paramK,
					}))
			}
			operation := "=="
			for _, configParam := range logicProp.Parameters {
				if configParam.Name == paramK && configParam.Operation != "" {
					operation = configParam.Operation
				}
			}
			metricValue.Parameters.Filters = append(metricValue.Parameters.Filters,
				interfaces.Filter{
					Name:      paramK,
					Operation: operation,
					Value:     paramValue,
				})
		}
	}

	return ots.queryLogicMetricViaKN(ctx, knID, branch, otID, logicProp,
		metricValue.Parameters.Filters, metricParams, start, end, isInstant, step)
}

// handleToolProperty handles logic properties backed by ToolBox tools.
func (ots *objectTypeService) handleToolProperty(ctx context.Context,
	knID string,
	objectTypeID string,
	propName string,
	propValue any,
	logicProp *interfaces.LogicProperty,
	dynamicParams map[string]map[string]any) (any, error) {

	toolValue := propValue.(interfaces.ToolProperty)
	if _, dynamicParamExist := dynamicParams[propName]; !dynamicParamExist && len(toolValue.DynamicParams) > 0 {
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest,
			oerrors.OntologyQuery_ObjectType_InvalidParameter_DynamicParams).
			WithErrorDetails(locale.ValidationDetail(ctx, "LogicPropertyDynamicParamsRequired", map[string]any{
				"property": propName, "parameters": toolValue.DynamicParams,
			}))
	}

	toolRequest := generateToolExecutionRequest(logicProp.Parameters, toolValue.Parameters, dynamicParams[propName])
	request := interfaces.ToolExecutionRequest{
		Header:  toolRequest.Header,
		Query:   toolRequest.Query,
		Body:    toolRequest.Body,
		Path:    toolRequest.Path,
		Timeout: 300,
	}
	if ots.proxy == nil {
		return nil, rest.NewHTTPError(ctx, http.StatusServiceUnavailable,
			oerrors.OntologyQuery_InternalError_CheckPermissionFailed).
			WithErrorDetails("knowledge network proxy resolver is not configured")
	}
	proxyContext, err := ots.proxy.Resolve(ctx, interfaces.TrustedProxyBinding{
		KNID:       knID,
		ChildType:  interfaces.PermissionResourceTypeLogicProperty,
		ChildID:    logicPropertyBindingID(knID, objectTypeID, propName),
		TargetType: interfaces.ProxyTargetTypeToolBox,
		TargetID:   logicProp.DataSource.BoxID,
		Operation:  interfaces.PermissionOperationExecute,
	})
	if err != nil {
		return nil, err
	}
	toolResult, err := ots.aoAccess.ExecuteToolAsProxy(interfaces.WithTrustedProxyContext(ctx, proxyContext),
		logicProp.DataSource.BoxID, logicProp.DataSource.ToolID, request)
	if err != nil {
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			oerrors.OntologyQuery_ObjectType_InternalError_ExecuteToolFailed).
			WithErrorDetails(locale.ValidationDetail(ctx, "ToolExecutionFailed", map[string]any{
				"property": propName, "toolbox": logicProp.DataSource.BoxID, "tool": logicProp.DataSource.ToolID,
				"error": err.Error(),
			}))
	}

	if logicProp.DataSource.ResultPath != "" {
		toolResult, err = jsonpath.Get(logicProp.DataSource.ResultPath, toolResult)
		if err != nil {
			logger.Warnf("extract tool result with path %q failed for logic property %s: %v",
				logicProp.DataSource.ResultPath, propName, err)
			return nil, nil
		}
	}

	return toolResult, nil
}

// logicPropertyBindingID mirrors BKN's published-model projection key. The
// opaque value keeps the object type/property tuple safe for an HTTP header.
func logicPropertyBindingID(knID, objectTypeID, propertyName string) string {
	propertyKey := strings.Join([]string{objectTypeID, propertyName}, "\x00")
	digest := sha256.Sum256([]byte(strings.Join([]string{
		knID, interfaces.PermissionResourceTypeLogicProperty, propertyKey,
	}, "\x00")))
	return hex.EncodeToString(digest[:])
}

func generateToolExecutionRequest(configParams []interfaces.Parameter, parameters map[string]any,
	dynamicParams map[string]any) interfaces.ToolExecutionRequest {

	toolExecRequest := interfaces.ToolExecutionRequest{
		Header: map[string]any{},
		Query:  map[string]any{},
		Body:   map[string]any{},
		Path:   map[string]any{},
	}

	// First process all parameters and build the base structure.
	for _, param := range configParams {
		var value any

		if param.ValueFrom == interfaces.VALUE_FROM_INPUT {
			// Dynamic input parameters are obtained from dynamicParameterMap.
			value = getNestedValue(dynamicParams, param.Name)
		} else {
			// Fixed-value parameters are obtained from parameterMap.
			value = getNestedValue(parameters, param.Name)
		}

		// Assign to different groups by source.
		switch strings.ToLower(param.Source) {
		case interfaces.PARAMETER_HEADER:
			setNestedValue(toolExecRequest.Header, param.Name, value)
		case interfaces.PARAMETER_QUERY:
			setNestedValue(toolExecRequest.Query, param.Name, value)
		case interfaces.PARAMETER_BODY:
			setNestedValue(toolExecRequest.Body, param.Name, value)
		case interfaces.PARAMETER_PATH:
			setNestedValue(toolExecRequest.Path, param.Name, value)
		}
	}
	return toolExecRequest
}

// getNestedValue gets the value of a nested field from a map.
func getNestedValue(data map[string]any, key string) any {
	if data == nil {
		return nil
	}

	// If the key contains dots, it represents a nested field.
	if strings.Contains(key, ".") {
		parts := strings.Split(key, ".")
		current := data

		for i, part := range parts {
			if i == len(parts)-1 {
				// Last part; return the value.
				return current[part]
			}

			// Middle part; continue descending.
			if next, ok := current[part].(map[string]any); ok {
				current = next
			} else {
				return nil
			}
		}
	}

	return data[key]
}

// setNestedValue sets a nested field value in a map.
func setNestedValue(target map[string]any, key string, value any) {
	if value == nil {
		return
	}

	// If the key contains dots, a nested field needs to be set.
	if strings.Contains(key, ".") {
		parts := strings.Split(key, ".")
		current := target

		for i, part := range parts {
			if i == len(parts)-1 {
				// Last part; set the value.
				current[part] = value
				return
			}

			// Middle part; ensure the map exists.
			if _, exists := current[part]; !exists {
				current[part] = make(map[string]any)
			}

			// Type assert and continue descending.
			if next, ok := current[part].(map[string]any); ok {
				current = next
			} else {
				// If the type does not match, overwrite it with a new map.
				current[part] = make(map[string]any)
				current = current[part].(map[string]any)
			}
		}
	} else {
		// Set simple fields directly.
		target[key] = value
	}
}
