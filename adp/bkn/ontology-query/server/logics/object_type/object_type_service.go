// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package object_type

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
)

var (
	otServiceOnce sync.Once
	otService     interfaces.ObjectTypeService
)

type objectTypeService struct {
	appSetting *common.AppSetting
	aoAccess   interfaces.AgentOperatorAccess
	mfa        interfaces.ModelFactoryAccess
	omAccess   interfaces.OntologyManagerAccess
	osa        interfaces.OpenSearchAccess
	vba        interfaces.VegaBackendAccess
	mqs        interfaces.MetricQueryService
}

func NewObjectTypeService(appSetting *common.AppSetting) interfaces.ObjectTypeService {
	otServiceOnce.Do(func() {
		otService = &objectTypeService{
			appSetting: appSetting,
			aoAccess:   logics.AOA,
			mfa:        logics.MFA,
			omAccess:   logics.OMA,
			osa:        logics.OSA,
			vba:        logics.VBA,
			mqs:        metric.NewMetricQueryService(appSetting),
		}
	})
	return otService
}

func (ots *objectTypeService) GetObjectsByObjectTypeID(ctx context.Context,
	query *interfaces.ObjectQueryBaseOnObjectType) (interfaces.Objects, error) {

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "查询对象类的对象数据")
	defer span.End()

	start := time.Now().UnixMilli()

	var resps interfaces.Objects

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

	// Sort fields can be object type data properties or _score.

	// 3.1 Process the object type and convert it into a view-field to object-type-property mapping.
	// Mapping from view fields to object type properties.
	viewFieldPropMap := map[string]string{
		interfaces.SORT_FIELD_SCORE: interfaces.SORT_FIELD_SCORE, // _score field.
	}
	// Mapping from object type property names to property names for use in case-to-index queries. Object index field names stay consistent with property names.
	indexPropMap := map[string]string{
		interfaces.SORT_FIELD_SCORE: interfaces.SORT_FIELD_SCORE, // _score field.
	}
	// Mapping from object type data property names to object type data properties.
	propMap := map[string]cond.DataProperty{}
	for _, prop := range objectType.DataProperties {
		propMap[prop.Name] = prop
		if len(query.Properties) == 0 { // When no property set is specified, treat it as fetching all properties.
			viewFieldPropMap[prop.MappedField.Name] = prop.Name
			indexPropMap[prop.Name] = prop.Name
		} else {
			for _, requestProp := range query.Properties {
				if prop.Name == requestProp {
					viewFieldPropMap[prop.MappedField.Name] = prop.Name
					indexPropMap[prop.Name] = prop.Name
				}
			}
		}
	}

	// Sort fields must be object-type data properties or _score.
	if len(query.Sort) > 0 {
		for _, sp := range query.Sort {
			if _, exists := indexPropMap[sp.Field]; !exists {
				return resps, rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
					WithErrorDetails(locale.ValidationDetail(ctx, "SortPropertyInvalid", map[string]any{"field": sp.Field}))
			}
		}
	}
	// Requested properties must exist in the object type.
	if len(query.Properties) > 0 {
		for _, prop := range query.Properties {
			if _, exists := propMap[prop]; !exists {
				return resps, rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
					WithErrorDetails(locale.ValidationDetail(ctx, "PropertyNotFound", map[string]any{"property": prop}))
			}
		}
	}

	// Validate data-property query parameters.
	if query.ObjectQueryInfo != nil {
		// Every identity must contain the primary-key fields.
		for i, instanceIdentity := range query.ObjectQueryInfo.InstanceIdentity {
			for _, key := range objectType.PrimaryKeys {
				if _, exist := instanceIdentity[key]; !exist {
					return resps, rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
						WithErrorDetails(locale.ValidationDetail(ctx, "InstanceIdentityFieldRequired", map[string]any{
							"index": i + 1, "field": key,
						}))
				}
			}
		}
		// The property list may contain data or logic properties.
		logicPropMap := make(map[string]bool)
		for _, prop := range objectType.LogicProperties {
			logicPropMap[prop.Name] = true
		}
		for _, prop := range query.ObjectQueryInfo.Properties {
			if _, exist := propMap[prop]; !exist && !logicPropMap[prop] {
				return resps, rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
					WithErrorDetails(locale.ValidationDetail(ctx, "PropertyQueryPropertyNotFound", map[string]any{"property": prop}))
			}
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

	// 2. Build sort fields.
	if query.Sort == nil {
		// Set default values: _score desc and primary key asc.
		query.Sort = logics.BuildViewSort(objectType)
	}
	// 3. Request Vega Resource to get data.
	err = ots.getObjectsFromResource(ctx, query, objectType, &resps, viewFieldPropMap)
	if err != nil {
		return resps, err
	}

	// 4. Assemble logical properties.
	if query.IncludeLogicParams && len(objectType.LogicProperties) > 0 {
		// Process each object's logical properties and set them on the object.
		err = ots.processLogicProperties(ctx, &resps, objectType)
		if err != nil {
			return resps, err
		}
	}

	// resps.Datas = objects

	if query.IncludeTypeInfo {
		resps.ObjectType = &objectType
	}

	logger.Debugf("从对象类[%s]中获取到的数据条数为[%d],耗时: %dms", objectType.OTID, len(resps.Datas), time.Now().UnixMilli()-start)

	return resps, nil
}

// Process each object's logical properties and set them on the object.
func (*objectTypeService) processLogicProperties(ctx context.Context, resps *interfaces.Objects,
	objectType interfaces.ObjectType) error {

	var err error

	// Process each object's logical properties and set them on the object.
	for i, object := range resps.Datas {
		// loop logic prop
		for _, logicProp := range objectType.LogicProperties {
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
				resps.Datas[i][logicProp.Name] = mProp

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
				err = json.Unmarshal([]byte(paramsJson), &params)
				if err != nil {
					return rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_InternalError_UnMarshalDataFailed).
						WithErrorDetails(fmt.Sprintf("failed to Unmarshal logic property[%s]'s paramtersJson to map, %s",
							logicProp.Name, err.Error()))
				}

				dynamicParams := map[string]any{}
				err = json.Unmarshal([]byte(dynamicParamsJson), &dynamicParams)
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
				resps.Datas[i][logicProp.Name] = toolProp

			default:
				logger.Warnf("系统支持的逻辑属性类型有[metric, tool],当前请求的逻辑属性类型为[%s]，请求将不返回逻辑属性的计算参数", logicProp.Type)
			}
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

func (ots *objectTypeService) getObjectsFromResource(ctx context.Context, query *interfaces.ObjectQueryBaseOnObjectType,
	objectType interfaces.ObjectType, resps *interfaces.Objects, fieldPropMap map[string]string) error {

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
				downstreamErrorCode(downstream.StatusCode)).WithErrorDetails(downstream.Message())
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
		object := map[string]any{}
		for k, v := range col {
			if propName, exists := fieldPropMap[k]; exists {
				object[propName] = v
			}
		}
		instanceID, instanceIdentity := logics.GetObjectID(object, &objectType)
		displayValue := object[objectType.DisplayKey]
		if !logics.ShouldExcludeSystemProperty(interfaces.SYSTEM_PROPERTY_INSTANCE_ID, query.ExcludeSystemProperties) {
			object[interfaces.SYSTEM_PROPERTY_INSTANCE_ID] = instanceID
		}
		if !logics.ShouldExcludeSystemProperty(interfaces.SYSTEM_PROPERTY_INSTANCE_IDENTITY, query.ExcludeSystemProperties) {
			object[interfaces.SYSTEM_PROPERTY_INSTANCE_IDENTITY] = instanceIdentity
		}
		if !logics.ShouldExcludeSystemProperty(interfaces.SYSTEM_PROPERTY_DISPLAY, query.ExcludeSystemProperties) {
			object[interfaces.SYSTEM_PROPERTY_DISPLAY] = displayValue
		}
		if len(object) > 0 {
			objects = append(objects, object)
		} else {
			logger.Warnf("resource row could not map to object properties, fieldPropMap: %v", fieldPropMap)
		}
	}
	resps.TotalCount = resp.TotalCount
	resps.SearchAfter = resp.SearchAfter
	resps.Datas = objects
	return nil
}

// getObjectsFromObjectIndex retrieves object data from the object-type index.
func (ots *objectTypeService) getObjectsFromObjectIndex(ctx context.Context, query *interfaces.ObjectQueryBaseOnObjectType,
	objectType interfaces.ObjectType, resps *interfaces.Objects, indexPropMap map[string]string) error {

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
		object := map[string]any{}
		for k, v := range hit.Source {
			// k is the view field name, and v is this field's value.
			if propName, exists := indexPropMap[k]; exists {
				// Set the field only when it belongs to requested properties.
				// If a mapping exists, assemble it into object properties.
				object[propName] = v
			}
		}
		// Add the _score field.
		object[interfaces.SORT_FIELD_SCORE] = hit.Score

		// Add _instance_id, _instance_identity, and _display fields to the object.
		instanceID, instanceIdentity := logics.GetObjectID(object, &objectType)
		displayValue := object[objectType.DisplayKey]

		if !logics.ShouldExcludeSystemProperty(interfaces.SYSTEM_PROPERTY_INSTANCE_ID, query.ExcludeSystemProperties) {
			object[interfaces.SYSTEM_PROPERTY_INSTANCE_ID] = instanceID
		}
		if !logics.ShouldExcludeSystemProperty(interfaces.SYSTEM_PROPERTY_INSTANCE_IDENTITY, query.ExcludeSystemProperties) {
			object[interfaces.SYSTEM_PROPERTY_INSTANCE_IDENTITY] = instanceIdentity
		}
		if !logics.ShouldExcludeSystemProperty(interfaces.SYSTEM_PROPERTY_DISPLAY, query.ExcludeSystemProperties) {
			object[interfaces.SYSTEM_PROPERTY_DISPLAY] = displayValue
		}

		if len(object) > 0 {
			objects = append(objects, object)
		} else {
			logger.Warnf("将视图行数据转成对象时，对象类属性映射的字段没有一个属性能正确映射到视图上，配置的字段属性映射关系为: %v", indexPropMap)
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

	// There is no need to check whether it is enabled; just avoid using the knn operator.
	// knn queries are allowed only when the vector model is enabled in system configuration; return an error if knn is requested while disabled.
	// if !ots.appSetting.ServerSetting.DefaultSmallModelEnabled {
	// 	err := errors.New("defaultSmallModelEnabled is false, does not support knn condition")
	// 	return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
	// 		oerrors.OntologyQuery_ObjectType_InternalError_GetSmallModelByIDFailed).
	// 		WithErrorDetails(err.Error())
	// }

	// First fetch the model configuration by the small model ID in vector index configuration; return an error if it cannot be found.
	model, err := ots.mfa.GetModelByID(ctx, property.IndexConfig.VectorConfig.ModelID)
	if err != nil {
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			oerrors.OntologyQuery_ObjectType_InternalError_GetSmallModelByIDFailed).
			WithErrorDetails(err.Error())
	}
	if model == nil {
		return nil, rest.NewHTTPError(ctx, http.StatusNotFound,
			oerrors.OntologyQuery_ObjectType_SmallModelNotFound).
			WithErrorDetails(locale.ValidationDetail(ctx, "SmallModelNotFound", map[string]any{"modelID": property.IndexConfig.VectorConfig.ModelID}))
	}
	if model.EmbeddingDim == 0 || model.BatchSize == 0 || model.MaxTokens == 0 {
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest,
			oerrors.OntologyQuery_ObjectType_InvalidParameter_SmallModel).
			WithErrorDetails(fmt.Sprintf("model %s has invalid embedding dim, batch size or max tokens", model.ModelID))
	}

	return ots.mfa.GetVector(ctx, model, []string{word})
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
		return ots.handleToolProperty(ctx, propName, propValue, logicProp, dynamicParams)
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
	toolResult, err := ots.aoAccess.ExecuteTool(ctx, logicProp.DataSource.BoxID, logicProp.DataSource.ToolID, request)
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
