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
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PaesslerAG/jsonpath"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/jsoncodec"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/tidwall/sjson"
	"go.opentelemetry.io/otel/attribute"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common"
	cond "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common/condition"
	oerrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/logics"
	permissionlogic "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/logics/permission"
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
	vba            interfaces.VegaBackendAccess
	proxy          interfaces.ProxyContextResolver
	propertyAccess interfaces.PropertyAccessService
	rowFilters     interfaces.RowFilterService
	cursor         *queryCursorCodec
}

type objectAggregateSpec struct {
	measureProperty string
	operation       string
	groupBy         []string
	propertyValue   bool
	having          *interfaces.ObjectAggregateHaving
}

func NewObjectTypeService(appSetting *common.AppSetting) interfaces.ObjectTypeService {
	otServiceOnce.Do(func() {
		otService = &objectTypeService{
			appSetting:     appSetting,
			aoAccess:       logics.AOA,
			mfa:            logics.MFA,
			omAccess:       logics.OMA,
			vba:            logics.VBA,
			proxy:          logics.PCR,
			propertyAccess: permissionlogic.NewPropertyAccessService(appSetting),
			rowFilters:     permissionlogic.NewRowFilterService(appSetting),
			cursor:         newQueryCursorCodec(),
		}
	})
	return otService
}

func (ots *objectTypeService) GetObjectTypeSchema(ctx context.Context,
	knID, branch, objectTypeID string) (*interfaces.ResourceSchemaResponse, error) {
	if branch != interfaces.MAIN_BRANCH {
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest,
			oerrors.OntologyQuery_ObjectType_InvalidParameter).
			WithErrorDetails("only the published main branch can be queried")
	}
	objectType, exists, err := ots.omAccess.GetObjectType(ctx, knID, branch, objectTypeID)
	if err != nil {
		if versionErr, ok := logics.KNVersionError(ctx, err); ok {
			return nil, versionErr
		}
		return nil, httperrors.NewHTTPError(ctx, http.StatusServiceUnavailable,
			oerrors.OntologyQuery_ObjectType_InternalError_GetObjectTypesByIDFailed).WithErrorDetails(err.Error())
	}
	if !exists {
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, oerrors.OntologyQuery_ObjectType_ObjectTypeNotFound)
	}
	if objectType.KNID != "" && objectType.KNID != knID {
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest,
			oerrors.OntologyQuery_ObjectType_InvalidParameter).WithErrorDetails("object type belongs to another knowledge network")
	}
	objectType.KNID = knID
	plan, err := buildPropertyAccessPlan(ctx, ots.propertyAccess, objectType, nil, false)
	if err != nil {
		return nil, err
	}
	if objectType.DataSource == nil || strings.TrimSpace(objectType.DataSource.ID) == "" ||
		(strings.TrimSpace(objectType.DataSource.Type) != "" &&
			objectType.DataSource.Type != interfaces.DATA_SOURCE_TYPE_RESOURCE) {
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest,
			oerrors.OntologyQuery_ObjectType_InvalidParameter).WithErrorDetails("object type has no published resource data source")
	}
	if ots.proxy == nil {
		return nil, httperrors.NewHTTPError(ctx, http.StatusServiceUnavailable,
			oerrors.OntologyQuery_InternalError_CheckPermissionFailed).
			WithErrorDetails("knowledge network proxy resolver is not configured")
	}
	proxyContext, err := logics.ResolveDataProxy(ctx, ots.proxy, interfaces.TrustedProxyBinding{
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
	// A nil proxy context means the draft or a history version: read as the caller.
	if proxyContext != nil {
		ctx = interfaces.WithTrustedProxyContext(ctx, proxyContext)
	}
	response, err := ots.vba.GetResourceSchema(ctx, objectType.DataSource.ID)
	if err != nil {
		if downstream, ok := interfaces.AsVegaDownstreamError(err); ok && downstream.IsClientError() {
			return nil, httperrors.NewHTTPError(ctx, downstream.StatusCode, proxyDownstreamErrorCode(downstream.StatusCode))
		}
		return nil, httperrors.NewHTTPError(ctx, http.StatusServiceUnavailable,
			oerrors.OntologyQuery_ObjectType_InternalError_GetObjectTypesByIDFailed)
	}
	plan.filterResourceSchema(response)
	return response, nil
}

func (ots *objectTypeService) GetObjectTypeSampleData(ctx context.Context,
	query *interfaces.ObjectQueryBaseOnObjectType) (*interfaces.ObjectTypeSampleData, error) {
	if query == nil {
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter)
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
	query.PagingMode = new(interfaces.ResourceDataPagingModeSingle)
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
		return nil, httperrors.NewHTTPError(ctx, http.StatusServiceUnavailable,
			oerrors.OntologyQuery_ObjectType_InternalError_GetObjectTypesByIDFailed)
	}
	result := &interfaces.ObjectTypeSampleData{
		Columns:              []*interfaces.ObjectTypeSampleDataColumn{},
		Entries:              objects.Datas,
		Name:                 objects.ObjectType.OTName,
		TotalCount:           objects.TotalCount,
		Paging:               objects.Paging,
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

type objectReadMode uint8

const (
	objectReadPage objectReadMode = iota
	objectReadFullScan
)

type objectScan struct {
	consume     func(interfaces.Objects) error
	closeCursor func()
}

func (ots *objectTypeService) GetObjectsByObjectTypeID(ctx context.Context,
	query *interfaces.ObjectQueryBaseOnObjectType) (interfaces.Objects, error) {
	if query != nil {
		mode := interfaces.ResourceDataPagingModeSingle
		if query.PagingMode != nil {
			mode = *query.PagingMode
		}
		if mode != interfaces.ResourceDataPagingModeSingle && mode != interfaces.ResourceDataPagingModeCursor {
			return interfaces.Objects{}, httperrors.NewHTTPError(ctx, http.StatusBadRequest,
				oerrors.OntologyQuery_ObjectType_InvalidParameter).
				WithErrorDetails("paging_mode must be single or cursor")
		}
		if query.Cursor != "" && mode != interfaces.ResourceDataPagingModeCursor {
			return interfaces.Objects{}, httperrors.NewHTTPError(ctx, http.StatusBadRequest,
				oerrors.OntologyQuery_ObjectType_InvalidParameter).
				WithErrorDetails("paging_mode cursor is required when a cursor is supplied")
		}
	}
	return ots.getObjectsByObjectTypeID(ctx, query, objectReadPage, nil, nil)
}

// StreamObjectsByObjectTypeID maps and authorizes each Vega-managed batch
// before handing it to the metric executor. No physical cursor is exposed.
func (ots *objectTypeService) StreamObjectsByObjectTypeID(ctx context.Context,
	query *interfaces.ObjectQueryBaseOnObjectType, consume func(interfaces.Objects) error) error {
	if consume == nil {
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest,
			oerrors.OntologyQuery_ObjectType_InvalidParameter).WithErrorDetails("object stream consumer is required")
	}
	_, err := ots.getObjectsByObjectTypeID(ctx, query, objectReadFullScan, nil, &objectScan{consume: consume})
	return err
}

// ScanObjectsByObjectTypeID reads query page by page so callers that act on a
// whole match set never mistake its first page for all of it. Reading stops
// once more than maxObjects objects are collected: the result then holds
// maxObjects+1 objects, which is how callers learn the set is over their limit
// without loading the rest of it. The first page supplies the type info and
// the total count; the result carries no continuation. Incomplete marks a
// match set that could not be read to the end.
func (ots *objectTypeService) ScanObjectsByObjectTypeID(ctx context.Context,
	query *interfaces.ObjectQueryBaseOnObjectType, maxObjects int) (interfaces.Objects, error) {
	if query == nil || maxObjects < 0 {
		return interfaces.Objects{}, httperrors.NewHTTPError(ctx, http.StatusBadRequest,
			oerrors.OntologyQuery_ObjectType_InvalidParameter).WithErrorDetails("invalid object scan")
	}
	var result interfaces.Objects
	cursor := ""
	scan := &objectScan{}
	defer func() {
		if scan.closeCursor != nil {
			scan.closeCursor()
		}
	}()
	for {
		// Every page is read from a fresh copy: a read rewrites its query (row
		// filter, default sort), and the continuation is bound to the original.
		page := *query
		page.Limit = interfaces.MAX_LIMIT
		page.Cursor = cursor
		objects, err := ots.getObjectsByObjectTypeID(ctx, &page, objectReadFullScan, nil, scan)
		if err != nil {
			return interfaces.Objects{}, err
		}
		if cursor == "" {
			result = objects
			result.Datas = make([]map[string]any, 0, len(objects.Datas))
		}
		result.Datas = append(result.Datas, objects.Datas...)
		if len(result.Datas) > maxObjects {
			result.Datas = result.Datas[:maxObjects+1]
			break
		}
		result.Incomplete = objects.Incomplete
		if objects.Paging != nil && objects.Paging.NextCursor != nil {
			if len(objects.Datas) == 0 {
				result.Incomplete = true
				break
			}
			cursor = *objects.Paging.NextCursor
			continue
		}
		break
	}
	result.ResourceCursor, result.ResourceCursorExpiry, result.Cursor = "", nil, ""
	result.Paging = &interfaces.ObjectPagingResponse{}
	return result, nil
}

// AggregateObjectsByObjectTypeID uses the same proxy, property and row-filter
// decisions as object reads. It is intentionally an internal opt-in operation;
// public callers cannot construct its aggregate specification.
func (ots *objectTypeService) AggregateObjectsByObjectTypeID(ctx context.Context,
	query *interfaces.ObjectQueryBaseOnObjectType, measureProperty, operation string,
	groupBy []string) (interfaces.Objects, error) {
	countRows := operation == "count_rows" && measureProperty == ""
	countPresent := operation == "count_present" && measureProperty != "" && len(groupBy) == 0
	if query == nil || !countRows && !countPresent && (measureProperty == "" || operation != "count" || len(groupBy) > 1) {
		return interfaces.Objects{}, httperrors.NewHTTPError(ctx, http.StatusBadRequest,
			oerrors.OntologyQuery_ObjectType_InvalidParameter).WithErrorDetails("unsupported object aggregate")
	}
	queryCopy := *query
	if countRows {
		// Row count has no property operand. Grouping properties, when present,
		// are still required in full so the aggregate follows the same property
		// authorization and logical-to-physical mapping as object reads.
		queryCopy.Properties = append([]string(nil), groupBy...)
		queryCopy.RequiredFullProperties = append([]string(nil), groupBy...)
		queryCopy.ObjectQueryInfo = nil
		queryCopy.ExcludeSystemProperties = nil
	} else {
		queryCopy.Properties = append([]string{measureProperty}, groupBy...)
		queryCopy.RequiredFullProperties = append([]string(nil), queryCopy.Properties...)
		if countPresent {
			queryCopy.ObjectQueryInfo = nil
			queryCopy.ExcludeSystemProperties = nil
		}
	}
	queryCopy.Cursor = ""
	queryCopy.ResourceCursor = ""
	queryCopy.Offset = 0
	queryCopy.Sort = nil
	return ots.getObjectsByObjectTypeID(ctx, &queryCopy, objectReadPage, &objectAggregateSpec{
		measureProperty: measureProperty, operation: operation, groupBy: append([]string(nil), groupBy...),
	}, nil)
}

// AggregateObjectMetricByObjectTypeID executes one authorized metric aggregate
// through Vega's existing resource-query contract. It deliberately accepts
// logical property names so authorization and physical mapping cannot be
// bypassed by an internal caller.
func (ots *objectTypeService) AggregateObjectMetricByObjectTypeID(ctx context.Context,
	query *interfaces.ObjectQueryBaseOnObjectType, aggregate interfaces.ObjectAggregateQuery) (interfaces.Objects, error) {
	operations := map[string]bool{
		"count_rows": true, "count": true, "count_distinct": true,
		"sum": true, "avg": true, "min": true, "max": true,
	}
	countRows := aggregate.Operation == "count_rows"
	if query == nil || !operations[aggregate.Operation] ||
		(countRows && aggregate.MeasureProperty != "") || (!countRows && aggregate.MeasureProperty == "") {
		return interfaces.Objects{}, httperrors.NewHTTPError(ctx, http.StatusBadRequest,
			oerrors.OntologyQuery_ObjectType_InvalidParameter).WithErrorDetails("unsupported object metric aggregate")
	}
	if aggregate.Having != nil {
		switch aggregate.Having.Operation {
		case cond.OperationEq, cond.OperationNotEq, cond.OperationGt, cond.OperationGte,
			cond.OperationLt, cond.OperationLte:
		default:
			return interfaces.Objects{}, httperrors.NewHTTPError(ctx, http.StatusBadRequest,
				oerrors.OntologyQuery_ObjectType_InvalidParameter).WithErrorDetails("unsupported object metric having")
		}
	}
	queryCopy := *query
	queryCopy.Properties = append([]string(nil), aggregate.GroupBy...)
	if !countRows {
		queryCopy.Properties = append([]string{aggregate.MeasureProperty}, queryCopy.Properties...)
	}
	queryCopy.RequiredFullProperties = append([]string(nil), queryCopy.Properties...)
	queryCopy.ObjectQueryInfo = nil
	queryCopy.ExcludeSystemProperties = nil
	queryCopy.Cursor = ""
	queryCopy.ResourceCursor = ""
	queryCopy.Offset = 0
	queryCopy.Sort = nil
	return ots.getObjectsByObjectTypeID(ctx, &queryCopy, objectReadPage, &objectAggregateSpec{
		measureProperty: aggregate.MeasureProperty,
		operation:       aggregate.Operation,
		groupBy:         append([]string(nil), aggregate.GroupBy...),
		propertyValue:   !countRows,
		having:          aggregate.Having,
	}, nil)
}

// AggregatePropertyByObjectTypeID keeps object authorization, property mapping
// and row filters in the object-query boundary. Vega owns the physical path and
// guarantees a successful aggregate result or returns an unsupported error.
func (ots *objectTypeService) AggregatePropertyByObjectTypeID(ctx context.Context,
	query *interfaces.ObjectQueryBaseOnObjectType, measureProperty, operation string) (interfaces.Objects, error) {
	if query == nil || measureProperty == "" ||
		(operation != "sum" && operation != "avg" && operation != "min" && operation != "max" && operation != "count_distinct") {
		return interfaces.Objects{}, httperrors.NewHTTPError(ctx, http.StatusBadRequest,
			oerrors.OntologyQuery_ObjectType_InvalidParameter).WithErrorDetails("unsupported property aggregate")
	}
	queryCopy := *query
	queryCopy.Properties = []string{measureProperty}
	queryCopy.RequiredFullProperties = []string{measureProperty}
	queryCopy.ObjectQueryInfo = nil
	queryCopy.ExcludeSystemProperties = nil
	queryCopy.Cursor = ""
	queryCopy.ResourceCursor = ""
	queryCopy.Offset = 0
	queryCopy.Sort = nil
	return ots.getObjectsByObjectTypeID(ctx, &queryCopy, objectReadPage, &objectAggregateSpec{
		measureProperty: measureProperty, operation: operation, propertyValue: true,
	}, nil)
}

func (ots *objectTypeService) getObjectsByObjectTypeID(ctx context.Context,
	query *interfaces.ObjectQueryBaseOnObjectType, mode objectReadMode, aggregate *objectAggregateSpec,
	scan *objectScan) (interfaces.Objects, error) {

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "查询对象类的对象数据")
	defer span.End()

	start := time.Now().UnixMilli()

	var resps interfaces.Objects
	if query == nil || query.Branch != interfaces.MAIN_BRANCH {
		return resps, httperrors.NewHTTPError(ctx, http.StatusBadRequest,
			oerrors.OntologyQuery_ObjectType_InvalidParameter).
			WithErrorDetails("only the published main branch can be queried")
	}

	objectType, exists, err := ots.omAccess.GetObjectType(ctx, query.KNID, query.Branch, query.ObjectTypeID)
	if err != nil {
		if versionErr, ok := logics.KNVersionError(ctx, err); ok {
			return resps, versionErr
		}
		span.SetAttributes(attribute.Key("model_id").String(query.ObjectTypeID))
		otellog.LogError(ctx, fmt.Sprintf("Get Object Type error: %v", err), err)

		return resps, httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
			oerrors.OntologyQuery_ObjectType_InternalError_GetObjectTypesByIDFailed).WithErrorDetails(err.Error())
	}
	if !exists {
		logger.Debugf("Object Type %d not found!", query.ObjectTypeID)

		span.SetAttributes(attribute.Key("model_id").String(query.ObjectTypeID))
		httpErr := httperrors.NewHTTPError(ctx, http.StatusNotFound, oerrors.OntologyQuery_ObjectType_ObjectTypeNotFound)
		otellog.LogError(ctx, fmt.Sprintf("Object Type [%s] not found!", query.ObjectTypeID), httpErr)

		return resps, httpErr
	}
	objectType.KNID = query.KNID
	if query.ObjectQueryInfo != nil {
		for _, instanceIdentity := range query.ObjectQueryInfo.InstanceIdentity {
			for _, key := range objectType.PrimaryKeys {
				if _, exists := instanceIdentity[key]; !exists {
					return resps, httperrors.NewHTTPError(ctx, http.StatusBadRequest,
						oerrors.OntologyQuery_ObjectType_InvalidParameter).
						WithErrorDetails("one or more instance identities are invalid")
				}
			}
		}
	}
	// Validate the caller's requested projection, condition and sort before
	// resolving the downstream proxy. Row-filter-only fields are added later as
	// hidden execution dependencies after the query_data authorization succeeds.
	plan, err := buildPropertyAccessPlan(ctx, ots.propertyAccess, objectType, query, true)
	if err != nil {
		return resps, err
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
		return resps, httperrors.NewHTTPError(ctx, http.StatusServiceUnavailable,
			oerrors.OntologyQuery_InternalError_CheckPermissionFailed).
			WithErrorDetails("knowledge network proxy resolver is not configured")
	}
	proxyContext, err := logics.ResolveDataProxy(ctx, ots.proxy, interfaces.TrustedProxyBinding{
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
	// A nil proxy context means the draft or a history version: read as the caller.
	if proxyContext != nil {
		ctx = interfaces.WithTrustedProxyContext(ctx, proxyContext)
	}
	// The version token ties cursors to the version they were read from.
	versionToken := logics.ReadVersionToken(ctx, proxyContext)

	if ots.rowFilters == nil {
		return resps, propertyDecisionUnavailable(ctx, fmt.Errorf("row-filter resolver is not configured"))
	}
	objectTypeRef := query.KNID + "/" + objectType.OTID
	decisions, err := ots.rowFilters.ResolveRowFilters(ctx, []string{objectTypeRef})
	if err != nil {
		return resps, err
	}
	if len(decisions) != 1 || decisions[0].ObjectTypeRef != objectTypeRef ||
		strings.TrimSpace(decisions[0].EffectiveRowFilterDigest) == "" {
		return resps, propertyDecisionUnavailable(ctx, fmt.Errorf("row-filter decision response mismatch"))
	}
	rowCondition, rowFields, noResults, err := compileRowFilter(decisions[0].Predicate, objectType)
	if err != nil {
		return resps, propertyDecisionUnavailable(ctx, err)
	}
	query.EffectiveRowFilterDigest = decisions[0].EffectiveRowFilterDigest
	query.RowFilterNoResults = noResults
	resps.RowFilterApplied = rowCondition != nil || noResults
	query.ActualCondition = andRowFilterCondition(query.ActualCondition, rowCondition)
	for _, name := range rowFields {
		plan.dependencyFields[name] = struct{}{}
		plan.fetchFields[name] = struct{}{}
	}

	// Authorize only caller-selected sort fields. The default sort is an internal
	// stable-pagination detail and must not turn a schema-only primary key into a
	// rejected user operation.
	// A missing or explicitly empty sort both need the stable default used by
	// Vega cursor paging. JSON clients commonly encode an optional empty list
	// as [], which must not bypass this default. Aggregates have no object-row
	// continuation and must not inherit this sort.
	if len(query.Sort) == 0 && aggregate == nil {
		query.Sort = logics.BuildViewSort(objectType)
	}

	// Sort fields can be object type data properties or _score.

	// 3.1 Process the object type and convert it into a view-field to object-type-property mapping.
	// Mapping from view fields to object type properties.
	viewFieldPropMap := plan.fieldPropertyMap()
	if query.Cursor != "" {
		if ots.cursor == nil {
			return resps, invalidQueryCursorError(ctx)
		}
		resourceCursor, err := ots.cursor.decodeResource(ctx, query, versionToken, query.Cursor)
		if err != nil {
			return resps, invalidQueryCursorError(ctx)
		}
		query.ResourceCursor = resourceCursor
	}

	// FALSE is a decision made before any resource read. It is safe to return
	// an empty set directly; this is not post-query filtering.
	if !query.RowFilterNoResults {
		// 3. Request Vega Resource to get data.
		var streamConsumer func(interfaces.Objects) error
		if scan != nil {
			streamConsumer = scan.consume
		}
		err = ots.getObjectsFromResource(ctx, query, objectType, &resps, viewFieldPropMap, plan,
			mode, aggregate, streamConsumer)
		if scan != nil && (err == nil || resps.ResourceCursor != "") {
			scan.closeCursor = nil
			if cursor := resps.ResourceCursor; cursor != "" {
				// Capture the query's proxy binding along with the new cursor.
				scan.closeCursor = func() {
					logics.CloseResourceDataCursor(ctx, ots.vba, objectType.DataSource.ID, cursor)
				}
			}
		}
		if err != nil {
			return resps, err
		}
	} else {
		// A false authorized predicate is complete without reading a resource.
		// No physical query path was chosen, so provenance remains empty.
		resps.Datas = []map[string]any{}
		if query.NeedTotal {
			resps.TotalCount = new(int64(0))
		}
		if aggregate != nil && aggregate.operation == "count_rows" && len(aggregate.groupBy) == 0 {
			resps.Datas = []map[string]any{{"__value": json.Number("0")}}
			resps.TotalCount = new(int64(0))
		}
	}

	if query.IncludeTypeInfo {
		filteredObjectType := plan.filterObjectType(objectType)
		resps.ObjectType = &filteredObjectType
	}
	resps.EffectivePermissions = plan.effective
	if resps.ResourceCursor != "" {
		if ots.cursor == nil {
			return interfaces.Objects{}, propertyDecisionUnavailable(ctx, fmt.Errorf("query cursor codec is not configured"))
		}
		resps.Cursor, err = ots.cursor.encodeResource(ctx, query, versionToken,
			resps.ResourceCursor, resps.ResourceCursorExpiry)
		if err != nil {
			return interfaces.Objects{}, propertyDecisionUnavailable(ctx, err)
		}
	}
	resps.Paging = &interfaces.ObjectPagingResponse{}
	if resps.Cursor != "" {
		expiresAt, expiryErr := ots.cursor.cursorExpiresAt(resps.ResourceCursorExpiry)
		if expiryErr != nil {
			return interfaces.Objects{}, propertyDecisionUnavailable(ctx, expiryErr)
		}
		nextCursor := resps.Cursor
		expiresAtSec := expiresAt.Unix()
		resps.Paging.NextCursor = &nextCursor
		resps.Paging.ExpiresAtSec = &expiresAtSec
	}

	logger.Debugf("从对象类[%s]中获取到的数据条数为[%d],耗时: %dms", objectType.OTID, len(resps.Datas), time.Now().UnixMilli()-start)

	return resps, nil
}

// addLogicProperties builds only outputs whose complete input closure was
// approved by the access plan. The raw dependency values stay in this local
// row and are discarded immediately after projection.
func (*objectTypeService) addLogicProperties(ctx context.Context, object map[string]any,
	logicProperties []*interfaces.LogicProperty) error {

	var err error

	for _, logicProp := range logicProperties {
		switch logicProp.Type {
		case interfaces.LOGIC_PROPERTY_TYPE_TOOL:
			paramsJson := "{}"
			dynamicParamsJson := "{}"
			for _, param := range logicProp.Parameters {
				switch param.ValueFrom {
				case interfaces.LOGIC_PARAMS_VALUE_FROM_PROP:
					value := object[param.Value.(string)]
					paramsJson, err = sjson.Set(paramsJson, param.Name, value)
					if err != nil {
						return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_InternalError_UnMarshalDataFailed).
							WithErrorDetails(fmt.Sprintf("Error setting logic property[%s]'s parameter path %s: %v",
								logicProp.Name, param.Name, err.Error()))
					}

				case interfaces.LOGIC_PARAMS_VALUE_FROM_CONST:
					paramsJson, err = sjson.Set(paramsJson, param.Name, param.Value)
					if err != nil {
						return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_InternalError_UnMarshalDataFailed).
							WithErrorDetails(fmt.Sprintf("Error setting logic property[%s]'s parameter path %s: %v",
								logicProp.Name, param.Name, err.Error()))
					}
				case interfaces.LOGIC_PARAMS_VALUE_FROM_INPUT:
					dynamicParamsJson, err = sjson.Set(dynamicParamsJson, param.Name, param)
					if err != nil {
						return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_InternalError_UnMarshalDataFailed).
							WithErrorDetails(fmt.Sprintf("Error setting logic property[%s]'s dynamic parameter path %s: %v",
								logicProp.Name, param.Name, err.Error()))
					}
				}
			}
			params := map[string]any{}
			err = jsoncodec.Unmarshal([]byte(paramsJson), &params)
			if err != nil {
				return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_InternalError_UnMarshalDataFailed).
					WithErrorDetails(fmt.Sprintf("failed to Unmarshal logic property[%s]'s paramtersJson to map, %s",
						logicProp.Name, err.Error()))
			}

			dynamicParams := map[string]any{}
			err = jsoncodec.Unmarshal([]byte(dynamicParamsJson), &dynamicParams)
			if err != nil {
				return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_InternalError_UnMarshalDataFailed).
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

func selectedObjectLogicProperties(objectType interfaces.ObjectType, plan *propertyAccessPlan) []*interfaces.LogicProperty {
	selected := make([]*interfaces.LogicProperty, 0, len(plan.returnLogic))
	for _, property := range objectType.LogicProperties {
		if _, allowed := plan.returnLogic[property.Name]; allowed {
			selected = append(selected, property)
		}
	}
	return selected
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
		return httperrors.PublicError_Unauthorized
	case http.StatusForbidden:
		return httperrors.PublicError_Forbidden
	case http.StatusNotFound:
		return httperrors.PublicError_NotFound
	case http.StatusConflict:
		return httperrors.PublicError_Conflict
	case http.StatusTooManyRequests:
		return oerrors.OntologyQuery_ObjectType_TooManyRequests
	default:
		// Other 4xx statuses (405/413/422, etc.) do not have semantically corresponding public error codes. Do not fall back here to
		// httperrors.PublicError_BadRequest: its en-US message is "Internal Server Error",
		// English callers would read "Internal Server Error" for a 413, which is exactly the misleading behavior this change avoids.
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

// resourceQueryClientError reports keyword-limit fields in the object's property namespace.
func resourceQueryClientError(ctx context.Context, downstream *interfaces.VegaDownstreamError, properties []cond.DataProperty) *httperrors.HTTPError {
	mapped := *downstream
	metadata := downstream.ClientMetadata()
	if field, ok := metadata["field"].(string); ok {
		name, matches := "", 0
		for _, property := range properties {
			if property.MappedField.Name == field {
				name = property.Name
				matches++
			}
		}
		if matches == 1 && name != "" {
			metadata["field"] = name
		} else {
			// Ambiguous or unknown bindings cannot identify a caller property reliably.
			delete(metadata, "field")
		}
		mapped.Metadata = metadata
	}
	return httperrors.NewHTTPError(ctx, downstream.StatusCode, proxyDownstreamErrorCode(downstream.StatusCode)).
		WithErrorDetails(mapped.ClientMessage()).WithMetadata(metadata)
}

func (ots *objectTypeService) getObjectsFromResource(ctx context.Context, query *interfaces.ObjectQueryBaseOnObjectType,
	objectType interfaces.ObjectType, resps *interfaces.Objects, fieldPropMap map[string]string,
	plan *propertyAccessPlan, mode objectReadMode, aggregate *objectAggregateSpec,
	streamConsumer func(interfaces.Objects) error) error {

	resourceSort, err := logics.MapSortFieldsForDataView(ctx, query.Sort, objectType)
	if err != nil {
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
			WithErrorDetails(err.Error())
	}
	// A schema-only primary key cannot supply a total order for internal scans.
	// An explicit Vega cursor can still use its resource's native paging.
	stable := true
	if query.ResourceCursor == "" && aggregate == nil {
		resourceSort, stable = appendResourceSortTieBreakers(resourceSort, objectType)
	}
	var filterCondition map[string]any
	if query.ActualCondition != nil {
		rewriteCondition, err := cond.RewriteCondition(ctx, query.ActualCondition,
			logics.TransferPropsToPropMap(objectType.DataProperties),
			logics.MemoizeVectorizer(func(ctx context.Context, property *cond.DataProperty, word string) ([]cond.VectorResp, error) {
				return ots.handlerVector(ctx, property, word)
			}))
		if err != nil {
			return httperrors.NewHTTPError(ctx, http.StatusBadRequest,
				oerrors.OntologyQuery_InvalidParameter_Condition).
				WithErrorDetails(fmt.Sprintf("failed to rewrite ontology condition for resource, %s", err.Error()))
		}
		filterCondition = logics.CondCfgToFilterMap(rewriteCondition)
	}
	if objectType.DataSource == nil || objectType.DataSource.ID == "" {
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest,
			oerrors.OntologyQuery_ObjectType_InvalidParameter).
			WithErrorDetails(fmt.Sprintf("object type [%s] has empty data source", objectType.OTID))
	}

	outputFields := make([]string, 0, len(fieldPropMap))
	for k := range fieldPropMap {
		outputFields = append(outputFields, k)
	}
	if plan.scoreVisible {
		outputFields = append(outputFields, interfaces.SORT_FIELD_SCORE)
	}
	sort.Strings(outputFields)
	params := &interfaces.ResourceDataQueryParams{
		NeedTotal:       query.NeedTotal,
		Sort:            resourceSort,
		FilterCondition: filterCondition,
		OutputFields:    outputFields,
	}
	if streamConsumer != nil {
		if !stable {
			return httperrors.NewHTTPError(ctx, http.StatusServiceUnavailable,
				oerrors.OntologyQuery_ObjectType_InternalError_GetViewDataByIDFailed).
				WithErrorDetails("object resource has no stable sort for a complete cursor scan")
		}
		page := *params
		if query.IgnoreLocalIndex {
			page.IgnoreLocalIndex = &query.IgnoreLocalIndex
		}
		page.Paging = interfaces.ResourceDataPagingRequest{
			Mode:         interfaces.ResourceDataPagingModeCursor,
			KeepAliveSec: int(continuousScanCursorTTL / time.Second),
		}
		activeCursor := ""
		var scanned int64
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			response, readErr := ots.vba.QueryResourceData(ctx, objectType.DataSource.ID, &page)
			if readErr != nil {
				return ots.objectResourceQueryError(ctx, readErr, objectType)
			}
			if response == nil {
				return ots.objectResourceQueryError(ctx, fmt.Errorf("vega resource cursor returned no page"), objectType)
			}
			if response.Paging == nil {
				return ots.objectResourceQueryError(ctx, fmt.Errorf("vega resource cursor returned no paging envelope"), objectType)
			}
			activeCursor = ""
			if response.Paging.NextCursor != nil {
				activeCursor = *response.Paging.NextCursor
			}
			if len(response.Entries) == 0 && activeCursor != "" {
				return ots.objectResourceQueryError(ctx, fmt.Errorf("vega resource cursor returned an empty nonterminal page"), objectType)
			}
			decisions, err := ots.rowFilters.ResolveRowFilters(ctx, []string{query.KNID + "/" + objectType.OTID})
			if err != nil {
				return err
			}
			if len(decisions) != 1 || decisions[0].ObjectTypeRef != query.KNID+"/"+objectType.OTID ||
				decisions[0].EffectiveRowFilterDigest != query.EffectiveRowFilterDigest {
				return httperrors.NewHTTPError(ctx, http.StatusConflict, httperrors.PublicError_Conflict).
					WithErrorDetails("row-filter authorization changed during the object scan")
			}
			currentPlan, err := buildPropertyAccessPlan(ctx, ots.propertyAccess, objectType, query, true)
			if err != nil {
				return err
			}
			if !maps.Equal(currentPlan.decisions, plan.decisions) {
				return httperrors.NewHTTPError(ctx, http.StatusForbidden, httperrors.PublicError_Forbidden).
					WithErrorDetails("property authorization changed during the object scan")
			}
			if len(response.Entries) > 0 {
				objects, err := ots.projectResourceObjectEntries(ctx, query, objectType, fieldPropMap, plan, response.Entries)
				if err != nil {
					return err
				}
				batch := interfaces.Objects{Datas: objects,
					RowFilterApplied: resps.RowFilterApplied, EffectivePermissions: plan.effective}
				if query.IncludeTypeInfo {
					filteredObjectType := plan.filterObjectType(objectType)
					batch.ObjectType = &filteredObjectType
				}
				if err := streamConsumer(batch); err != nil {
					return err
				}
				scanned += int64(len(response.Entries))
			}
			if activeCursor == "" {
				if err := ctx.Err(); err != nil {
					return err
				}
				resps.QuerySource = response.QuerySource
				resps.TotalCount = new(scanned)
				return nil
			}
			page = interfaces.ResourceDataQueryParams{Paging: interfaces.ResourceDataPagingRequest{Cursor: activeCursor}}
		}
	}
	var resp *interfaces.DatasetQueryResponse
	var nativePaging bool
	pagingMode := interfaces.ResourceDataPagingModeSingle
	if mode == objectReadPage && query.PagingMode != nil {
		pagingMode = *query.PagingMode
	}
	if aggregate == nil {
		// Preserve the established object-query source preference. Aggregate
		// execution deliberately leaves physical source selection to Vega.
		params.IgnoreLocalIndex = &query.IgnoreLocalIndex
		resp, nativePaging, err = ots.queryResourcePage(ctx, objectType.DataSource.ID, query, params, pagingMode, stable, mode)
	} else if aggregate.operation == "count_rows" && len(aggregate.groupBy) == 0 {
		// Vega's existing need_total contract counts the authorized filtered
		// resource rows. Only one detail row is requested and never returned to
		// the metric caller; the physical count and source remain Vega's choice.
		params.Sort = nil
		params.NeedTotal = true
		params.Paging = interfaces.ResourceDataPagingRequest{
			Mode: interfaces.ResourceDataPagingModeSingle, Limit: 1,
		}
		resp, err = ots.vba.QueryResourceData(ctx, objectType.DataSource.ID, params)
	} else {
		measureField := "*"
		params.Sort = nil
		operation := aggregate.operation
		if operation == "count_rows" {
			operation = "count"
			params.NeedTotal = true
		} else {
			measure, exists := plan.properties[aggregate.measureProperty]
			if !exists || measure.MappedField.Name == "" {
				return unavailableOperationPropertyError(ctx)
			}
			measureField = measure.MappedField.Name
			if operation == "count_present" {
				operation = "count"
			}
		}
		params.Aggregation = map[string]any{"property": measureField,
			"aggr": operation, "alias": "__value"}
		if operation == "count_distinct" && query.AggregateAllowApproximate != nil {
			params.Aggregation["allow_approximate"] = *query.AggregateAllowApproximate
		}
		if len(aggregate.groupBy) == 0 && (aggregate.operation == "count_present" || aggregate.propertyValue) {
			params.Having = map[string]any{"field": "count(*)", "operation": ">", "value": 0}
		}
		params.OutputFields = []string{"__value"}
		for _, name := range aggregate.groupBy {
			property, found := plan.properties[name]
			if !found || property.MappedField.Name == "" {
				return unavailableOperationPropertyError(ctx)
			}
			params.GroupBy = append(params.GroupBy, map[string]any{"property": property.MappedField.Name})
			params.OutputFields = append(params.OutputFields, property.MappedField.Name)
		}
		if len(aggregate.groupBy) > 0 && aggregate.having != nil {
			params.Having = map[string]any{"field": "__value", "operation": aggregate.having.Operation,
				"value": aggregate.having.Value}
		}
		limit := query.Limit
		if len(aggregate.groupBy) > 0 {
			// Public metric page_size is final-result pagination. Read one complete
			// bounded aggregate page and fall back when Vega reports more groups.
			limit = interfaces.MAX_LIMIT
		}
		params.Paging = interfaces.ResourceDataPagingRequest{Mode: interfaces.ResourceDataPagingModeSingle, Limit: limit}
		resp, err = ots.vba.QueryResourceData(ctx, objectType.DataSource.ID, params)
	}
	if err != nil {
		// When downstream identifies a caller-side issue (4xx), pass through the original status code and carry its reason upward.
		// Upgrading everything to 500 makes self-correctable problems such as unsupported operators or resources without built indexes look
		// like service failures, preventing callers from self-correcting and sending manual investigation in the wrong direction.
		if downstream, ok := interfaces.AsVegaDownstreamError(err); ok && downstream.IsClientError() {
			clientErr := resourceQueryClientError(ctx, downstream, objectType.DataProperties)
			if nativePaging && query.ResourceCursor != "" && downstream.StatusCode == http.StatusNotFound {
				return clientErr.WithErrorDetails("Resource or cursor is unavailable. Restart the query without a cursor.")
			}
			return clientErr
		}
		return httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
			oerrors.OntologyQuery_ObjectType_InternalError_GetViewDataByIDFailed).
			WithErrorDetails("object resource query failed")
	}
	if resp == nil {
		return httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
			oerrors.OntologyQuery_ObjectType_InternalError_GetViewDataByIDFailed).WithErrorDetails("vega resource query returned nil")
	}
	resps.Warnings = resp.Warnings
	resps.IsSorted = resp.IsSorted
	if aggregate != nil {
		if resp.IsApproximate != nil {
			resps.IsApproximate = *resp.IsApproximate
		}
		if aggregate.operation == "count_distinct" && resp.IsApproximate == nil {
			if query.AggregateAllowApproximate != nil && !*query.AggregateAllowApproximate {
				return httperrors.NewHTTPError(ctx, http.StatusBadGateway, oerrors.OntologyQuery_ObjectType_InternalError_GetViewDataByIDFailed).
					WithErrorDetails("resource distinct count response is missing accuracy metadata; upgrade Vega before using exact distinct aggregates")
			}
			// Older Vega responses cannot prove exactness; preserve default approximate queries.
			resps.IsApproximate = true
		}
		if aggregate.operation == "count_rows" && len(aggregate.groupBy) == 0 {
			if resp.TotalCount == nil {
				return ots.objectResourceQueryError(ctx, fmt.Errorf("resource count response is missing total_count"), objectType)
			}
			resps.Datas = []map[string]any{{"__value": json.Number(strconv.FormatInt(*resp.TotalCount, 10))}}
			resps.QuerySource = resp.QuerySource
			resps.TotalCount = resp.TotalCount
			return nil
		}
		if len(aggregate.groupBy) > 0 {
			returned := int64(len(resp.Entries))
			if returned > int64(params.Paging.Limit) {
				return httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
					oerrors.OntologyQuery_ObjectType_InternalError_GetViewDataByIDFailed).
					WithErrorDetails("Vega grouped aggregate exceeded the requested page limit")
			}
			if returned == int64(params.Paging.Limit) {
				return httperrors.NewHTTPError(ctx, http.StatusBadRequest,
					oerrors.OntologyQuery_ObjectType_InvalidParameter).
					WithErrorDetails("complete grouped aggregate is unavailable in one Vega page")
			}
		}
		resps.Datas = make([]map[string]any, 0, len(resp.Entries))
		for _, entry := range resp.Entries {
			row := map[string]any{"__value": entry["__value"]}
			for _, name := range aggregate.groupBy {
				row[name] = entry[plan.properties[name].MappedField.Name]
			}
			resps.Datas = append(resps.Datas, row)
		}
		resps.QuerySource = resp.QuerySource
		if len(aggregate.groupBy) > 0 {
			// Resource connectors do not expose one uniform aggregate-total
			// meaning: source tables report groups while local indexes report
			// matched documents. A short single page is complete by the paging
			// contract, and the aggregate boundary exposes its returned groups.
			resps.TotalCount = new(int64(len(resps.Datas)))
		} else {
			resps.TotalCount = resp.TotalCount
		}
		return nil
	}

	if nativePaging {
		if resp.Paging == nil && mode == objectReadFullScan {
			return ots.objectResourceQueryError(ctx, fmt.Errorf("vega resource cursor returned no paging envelope"), objectType)
		}
		if resp.Paging != nil && resp.Paging.NextCursor != nil && *resp.Paging.NextCursor != "" {
			resps.ResourceCursor = *resp.Paging.NextCursor
			resps.ResourceCursorExpiry = resp.Paging.ExpiresAtSec
		}
	}
	objects, err := ots.projectResourceObjectEntries(ctx, query, objectType, fieldPropMap, plan, resp.Entries)
	if err != nil {
		return err
	}
	resps.QuerySource = resp.QuerySource
	resps.TotalCount = resp.TotalCount
	resps.Datas = objects
	if !nativePaging && mode == objectReadFullScan {
		// A bounded single read can prove completeness with a known total or
		// a short page. A full page without a total may hide more matches.
		if query.NeedTotal && resp.TotalCount != nil {
			offset := int64(query.Offset)
			resps.Incomplete = *resp.TotalCount > offset && int64(len(resp.Entries)) < *resp.TotalCount-offset
		} else {
			resps.Incomplete = len(resp.Entries) >= query.Limit
		}
	}
	return nil
}

func (ots *objectTypeService) projectResourceObjectEntries(ctx context.Context,
	query *interfaces.ObjectQueryBaseOnObjectType, objectType interfaces.ObjectType,
	fieldPropMap map[string]string, plan *propertyAccessPlan, entries []map[string]any) ([]map[string]any, error) {
	// The access plan is fixed for this request. Preserve declaration order,
	// but do not recheck every unrequested logical property on every row.
	selectedLogicProperties := selectedObjectLogicProperties(objectType, plan)
	objects := make([]map[string]any, 0, len(entries))
	for _, col := range entries {
		rawObject := map[string]any{}
		for k, v := range col {
			if k == interfaces.SORT_FIELD_SCORE {
				// Score is query metadata; projectRow applies its visibility policy.
				rawObject[k] = v
			} else if propName, exists := fieldPropMap[k]; exists {
				rawObject[propName] = v
			}
		}
		if err := ots.addLogicProperties(ctx, rawObject, selectedLogicProperties); err != nil {
			return nil, err
		}
		object := plan.projectRow(rawObject, &objectType, query)
		if len(object) > 0 {
			objects = append(objects, object)
		} else {
			logger.Warnf("resource row could not produce a sanitized object for object type [%s/%s]", query.KNID, objectType.OTID)
		}
	}
	return objects, nil
}

func (ots *objectTypeService) objectResourceQueryError(ctx context.Context, err error,
	objectType interfaces.ObjectType) error {
	var localHTTPError *httperrors.HTTPError
	if errors.As(err, &localHTTPError) {
		return localHTTPError
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if downstream, ok := interfaces.AsVegaDownstreamError(err); ok && downstream.IsClientError() {
		return resourceQueryClientError(ctx, downstream, objectType.DataProperties)
	}
	return httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
		oerrors.OntologyQuery_ObjectType_InternalError_GetViewDataByIDFailed).
		WithErrorDetails("object resource query failed")
}

// Metric-style scans consume cursor pages immediately. A short idle lifetime
// bounds sessions left behind if a scan stops early at its row limit or fails.
const continuousScanCursorTTL = 2 * time.Minute

// queryResourcePage reads one page and reports whether it issued a Vega cursor request.
func (ots *objectTypeService) queryResourcePage(ctx context.Context, resourceID string,
	query *interfaces.ObjectQueryBaseOnObjectType, params *interfaces.ResourceDataQueryParams,
	pagingMode interfaces.ResourceDataPagingMode, stable bool, mode objectReadMode) (*interfaces.DatasetQueryResponse, bool, error) {
	cursorMode := pagingMode == interfaces.ResourceDataPagingModeCursor
	if query.ResourceCursor != "" && (cursorMode || mode != objectReadPage) {
		resp, err := ots.vba.QueryResourceData(ctx, resourceID, &interfaces.ResourceDataQueryParams{
			Paging: interfaces.ResourceDataPagingRequest{Cursor: query.ResourceCursor},
		})
		return resp, true, err
	}
	if cursorMode || mode != objectReadPage && stable {
		keepAlive := continuousScanCursorTTL
		if cursorMode {
			keepAlive = queryCursorTTL
		}
		limit := 0 // Vega chooses the page size for a full scan.
		if cursorMode {
			limit = query.Limit
		}
		cursorParams := *params
		cursorParams.Paging = interfaces.ResourceDataPagingRequest{
			Mode: interfaces.ResourceDataPagingModeCursor, Limit: limit,
			Offset: query.Offset, KeepAliveSec: int(keepAlive / time.Second),
		}
		resp, err := ots.vba.QueryResourceData(ctx, resourceID, &cursorParams)
		if cursorMode || err == nil || !isCursorPagingUnsupported(err) {
			return resp, true, err
		}
	}

	singleParams := *params
	singleParams.Paging = interfaces.ResourceDataPagingRequest{
		Mode: interfaces.ResourceDataPagingModeSingle, Limit: query.Limit, Offset: query.Offset,
	}
	resp, err := ots.vba.QueryResourceData(ctx, resourceID, &singleParams)
	return resp, false, err
}

func isCursorPagingUnsupported(err error) bool {
	downstream, ok := interfaces.AsVegaDownstreamError(err)
	return ok && downstream.StatusCode == http.StatusNotImplemented
}

// appendResourceSortTieBreakers makes the cursor sort a total order. Vega uses
// search_after internally, so a non-unique caller sort must end with every
// object primary key in its mapped resource-field form.
func appendResourceSortTieBreakers(sorts []*interfaces.SortParams, objectType interfaces.ObjectType) ([]*interfaces.SortParams, bool) {
	fieldByProperty := make(map[string]string, len(objectType.DataProperties))
	for _, property := range objectType.DataProperties {
		fieldByProperty[property.Name] = property.MappedField.Name
	}

	result := append([]*interfaces.SortParams(nil), sorts...)
	present := make(map[string]struct{}, len(result))
	for _, sortParam := range result {
		if sortParam != nil {
			present[sortParam.Field] = struct{}{}
		}
	}
	if len(objectType.PrimaryKeys) == 0 {
		return result, false
	}
	for _, primaryKey := range objectType.PrimaryKeys {
		mappedField := fieldByProperty[primaryKey]
		if mappedField == "" {
			return result, false
		}
		if _, exists := present[mappedField]; exists {
			continue
		}
		result = append(result, &interfaces.SortParams{
			Field:     mappedField,
			Direction: interfaces.ASC_DIRECTION,
		})
		present[mappedField] = struct{}{}
	}
	return result, true
}

// Vectorize the query statement.
func (ots *objectTypeService) handlerVector(_ context.Context, property *cond.DataProperty, _ string) ([]cond.VectorResp, error) {
	return nil, fmt.Errorf("ontology-query no longer vectorizes property %q; Vega Resource resolves vector conditions", property.Name)
}

const maxConcurrentLogicProperties = 16

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
	// Step 2: process tool-backed logical properties with bounded concurrency.
	// A page may contain thousands of objects, and one goroutine per property
	// would overload the downstream execution service.
	var wg sync.WaitGroup
	var mu sync.Mutex
	var processErrors []error
	semaphore := make(chan struct{}, maxConcurrentLogicProperties)

	launchProperty := func(i int, prop string, value any, logicProp *interfaces.LogicProperty) {
		semaphore <- struct{}{}
		wg.Add(1)
		go func(objIndex int, propName string, propValue any, logicProp *interfaces.LogicProperty) {
			defer wg.Done()
			defer func() { <-semaphore }()

			logger.Debugf("Processing logical property of object [%d]: %s", objIndex, propName)
			resultValue, err := ots.processLogicProperty(ctx, query.KNID, query.Branch, query.ObjectTypeID,
				propName, propValue, logicProp, query.DynamicParams)
			if err != nil {
				localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx),
					"OntologyQuery.Validation.Detail.LogicPropertyProcessFailed", map[string]any{
						"index":    objIndex,
						"property": propName,
					})
				if renderErr != nil {
					logger.Errorf("localized message rendering failed: %v", renderErr)
				}
				mu.Lock()
				processErrors = append(processErrors, fmt.Errorf("%s: %w", localizedText, err))
				mu.Unlock()
				return
			}

			// Safely write the result to the corresponding object.
			mu.Lock()
			datas[objIndex][propName] = resultValue
			mu.Unlock()

		}(i, prop, value, logicProp)
	}
	for i, object := range objects.Datas {
		for prop, value := range object {
			if logicProp := logicProperties[prop]; propertyNames[prop] && logicProp != nil {
				launchProperty(i, prop, value, logicProp)
			}
		}
	}

	// Wait for all logical-property processing to complete.
	wg.Wait()

	// Check errors.
	if len(processErrors) > 0 {
		errors := make([]string, 0, len(processErrors))
		for _, err := range processErrors {
			errors = append(errors, err.Error())
		}
		return resps, httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
			oerrors.OntologyQuery_ObjectType_InternalError_ProcessLogicPropertiesFailed).
			WithErrorDetails(strings.Join(errors, "; "))
	}

	resps.Datas = datas
	resps.ObjectType = objects.ObjectType
	resps.TotalCount = objects.TotalCount
	resps.QuerySource = objects.QuerySource
	resps.IsSorted = objects.IsSorted
	resps.RowFilterApplied = objects.RowFilterApplied
	resps.Paging = objects.Paging
	resps.Cursor = objects.Cursor
	resps.EffectivePermissions = objects.EffectivePermissions
	return resps, nil

}

// processLogicProperty handles a single logical property and wraps the original processing logic.
func (ots *objectTypeService) processLogicProperty(ctx context.Context,
	knID, _, otID string,
	propName string,
	propValue any,
	logicProp *interfaces.LogicProperty,
	dynamicParams map[string]map[string]any) (any, error) {

	switch logicProp.Type {
	case interfaces.LOGIC_PROPERTY_TYPE_TOOL:
		return ots.handleToolProperty(ctx, knID, otID, propName, propValue, logicProp, dynamicParams)
	default:
		logger.Warnf("不支持的逻辑属性类型: %s", logicProp.Type)
		return nil, nil
	}
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
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "OntologyQuery.Validation.Detail.LogicPropertyDynamicParamsRequired", map[string]any{"property": propName, "parameters": toolValue.DynamicParams})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest,
			oerrors.OntologyQuery_ObjectType_InvalidParameter_DynamicParams).
			WithErrorDetails(localizedText)
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
		return nil, httperrors.NewHTTPError(ctx, http.StatusServiceUnavailable,
			oerrors.OntologyQuery_InternalError_CheckPermissionFailed).
			WithErrorDetails("knowledge network proxy resolver is not configured")
	}
	proxyContext, err := logics.ResolveDataProxy(ctx, ots.proxy, interfaces.TrustedProxyBinding{
		KNID:      knID,
		ChildType: interfaces.PermissionResourceTypeLogicProperty,
		ChildID:   logicPropertyBindingID(knID, objectTypeID, propName),
		TargetID:  logicProp.DataSource.BoxID,
		Operation: interfaces.PermissionOperationExecute,
	})
	if err != nil {
		return nil, err
	}
	var toolResult any
	if proxyContext == nil {
		// The draft or a history version: the tool runs with the caller's own permissions.
		toolResult, err = ots.aoAccess.ExecuteTool(ctx, logicProp.DataSource.BoxID, logicProp.DataSource.ToolID, request)
	} else {
		toolResult, err = ots.aoAccess.ExecuteToolAsProxy(interfaces.WithTrustedProxyContext(ctx, proxyContext),
			logicProp.DataSource.BoxID, logicProp.DataSource.ToolID, request)
	}
	if err != nil {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "OntologyQuery.Validation.Detail.ToolExecutionFailed", map[string]any{
			"property": propName, "toolbox": logicProp.DataSource.BoxID, "tool": logicProp.DataSource.ToolID,
			"error": "tool execution failed",
		})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError, oerrors.OntologyQuery_ObjectType_InternalError_ExecuteToolFailed).WithErrorDetails(localizedText)
	}

	if logicProp.DataSource.ResultPath != "" {
		toolResult, err = jsonpath.Get(logicProp.DataSource.ResultPath, toolResult)
		if err != nil {
			logger.Warnf("extract tool result with path %q failed for logic property %s",
				logicProp.DataSource.ResultPath, propName)
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
