package logic_view

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/antlr4-go/antlr/v4"
	"github.com/bytedance/sonic"
	"github.com/mitchellh/mapstructure"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"go.opentelemetry.io/otel/codes"

	"vega-backend/common"
	verrors "vega-backend/errors"
	"vega-backend/interfaces"
	"vega-backend/logics/catalog"
	"vega-backend/logics/connector/factory"
	"vega-backend/logics/filter_condition"
	"vega-backend/logics/permission"
	"vega-backend/logics/query"
	"vega-backend/logics/resource"
	lvdsl "vega-backend/logics/resource_data/logic_view/dsl"
	lvsql "vega-backend/logics/resource_data/logic_view/sql"
	"vega-backend/logics/resource_data/logic_view/sql/parsing"
)

var (
	lvServiceOnce sync.Once
	lvService     interfaces.LogicViewService
)

type logicViewService struct {
	appSetting *common.AppSetting
	cf         interfaces.ConnectorFactory
	cs         interfaces.CatalogService
	rs         interfaces.ResourceService
	ps         interfaces.PermissionService
	qs         interfaces.RawQueryService
}

// NewLogicViewService creates a new ResourceDataService.
func NewLogicViewService(appSetting *common.AppSetting) interfaces.LogicViewService {
	lvServiceOnce.Do(func() {
		lvService = &logicViewService{
			appSetting: appSetting,
			cf:         factory.GetFactory(appSetting),
			cs:         catalog.NewCatalogService(appSetting),
			rs:         resource.NewResourceService(appSetting),
			ps:         permission.NewPermissionService(appSetting),
			qs:         query.NewRawQueryService(appSetting),
		}
	})
	return lvService
}

// QueryWithPaging executes composite single-source logic views through
// RawQueryService, so their single and cursor pages have the same contract and
// session ownership as raw queries. Derived and multi-source views retain their
// current execution model.
func (lvs *logicViewService) QueryWithPaging(ctx context.Context, resource *interfaces.Resource,
	params *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
	if params.Paging.Cursor != "" {
		if resource.LogicType == interfaces.LogicType_Derived {
			return query.ExecuteResourceDataCursorContinuation(ctx, accountIDFromContext(ctx), resource, params.Paging.Cursor,
				func(pageCtx context.Context, pageParams *interfaces.ResourceDataQueryParams) ([]map[string]any, int64, error) {
					view := &interfaces.LogicView{Resource: *resource}
					return lvs.queryDerivedLogicView(pageCtx, view, pageParams)
				})
		}
		response, err := lvs.qs.Execute(ctx, &interfaces.RawQueryRequest{
			Paging:                 params.Paging,
			ResourceDataResourceID: resource.ID,
			ResourceDataUpdateTime: resource.UpdateTime,
		})
		if err != nil {
			return nil, err
		}
		return rawQueryResult(response), nil
	}

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Query logic view")
	defer span.End()

	logger.Debugf("Query logic view, resourceID: %s, params: %v",
		resource.ID, params)

	view := &interfaces.LogicView{
		Resource: *resource,
	}

	switch resource.LogicType {
	case interfaces.LogicType_Derived:
		if params.Paging.Mode == interfaces.PagingModeCursor {
			paginationCategory, err := lvs.derivedPaginationCategory(ctx, view)
			if err != nil {
				return nil, err
			}
			if paginationCategory == interfaces.ResourceCategoryIndex {
				if len(params.Sort) == 0 {
					return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
						WithErrorDetails("sort is required for index cursor paging")
				}
				if params.Aggregation != nil || len(params.GroupBy) > 0 || params.Having != nil {
					return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
						WithErrorDetails("cursor paging does not support index aggregation queries")
				}
			}
			return query.ExecuteInitialResourceDataCursorWithCategory(ctx, accountIDFromContext(ctx), resource, paginationCategory, params,
				func(pageCtx context.Context, pageParams *interfaces.ResourceDataQueryParams) ([]map[string]any, int64, error) {
					return lvs.queryDerivedLogicView(pageCtx, view, pageParams)
				})
		}
		entries, total, err := lvs.queryDerivedLogicView(ctx, view, params)
		if err != nil {
			return nil, err
		}
		return &interfaces.ResourceDataQueryResult{Entries: entries, TotalCount: total, Paging: &interfaces.PagingResponse{}}, nil
	case interfaces.LogicType_Composite:
		return lvs.queryCompositeLogicView(ctx, view, params)
	default:
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InternalError_InvalidCategory).
			WithErrorDetails(fmt.Sprintf("The logic type of the custom view '%s' is not supported", resource.ID))
		otellog.LogError(ctx, "Unsupported logic view type", httpErr)
		return nil, httpErr
	}
}

func (lvs *logicViewService) derivedPaginationCategory(ctx context.Context, view *interfaces.LogicView) (string, error) {
	for _, node := range view.LogicDefinition {
		if node.Type != interfaces.LogicDefinitionNodeType_Resource {
			continue
		}
		var nodeCfg interfaces.ResourceNodeCfg
		if err := mapstructure.Decode(node.Config, &nodeCfg); err != nil {
			return "", rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError).
				WithErrorDetails(fmt.Sprintf("failed to decode source resource config: %v", err))
		}
		source, err := lvs.rs.GetByID(ctx, nodeCfg.ResourceID)
		if err != nil {
			return "", err
		}
		if source == nil {
			return "", rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Resource_NotFound).
				WithErrorDetails(fmt.Sprintf("source resource %s not found", nodeCfg.ResourceID))
		}
		if _, err := resource.EnsureResourceQueryable(ctx, source); err != nil {
			return "", err
		}
		return source.Category, nil
	}
	return "", rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InternalError_InvalidCategory).
		WithErrorDetails("derived logic view has no source resource")
}

func accountIDFromContext(ctx context.Context) string {
	accountInfo, _ := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	return accountInfo.ID
}

func rawQueryResult(response *interfaces.RawQueryResponse) *interfaces.ResourceDataQueryResult {
	result := &interfaces.ResourceDataQueryResult{
		Entries: response.Entries,
		Paging:  response.Paging,
	}
	if response.TotalCount != nil {
		result.TotalCount = *response.TotalCount
		result.NeedTotal = true
	}
	return result
}

func (lvs *logicViewService) queryDerivedLogicView(ctx context.Context, view *interfaces.LogicView,
	params *interfaces.ResourceDataQueryParams) ([]map[string]any, int64, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Query derived logic view")
	defer span.End()

	var inputNode *interfaces.LogicDefinitionNode
	for _, node := range view.LogicDefinition {
		if node.Type == interfaces.LogicDefinitionNodeType_Resource {
			inputNode = node
			break
		}
	}

	var nodeCfg interfaces.ResourceNodeCfg
	if err := mapstructure.Decode(inputNode.Config, &nodeCfg); err != nil {
		otellog.LogError(ctx, "Decode resource node config failed", err)
		return nil, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError).
			WithErrorDetails(fmt.Sprintf("failed to decode resource node config: %v", err))
	}
	fromResourceFilterCond := nodeCfg.Filters
	// 视图定义里存着的过滤条件是服务端数据，调用方改不了。新的 like 契约拒绝未转义的 %，
	// 直接套到存量定义上会让一次升级把视图查废，因此这里按老行为（当字面量）改写并告警，
	// 只有调用方传进来的条件才硬拒。
	if rewritten := filter_condition.EscapeLegacyLikeWildcards(fromResourceFilterCond); rewritten > 0 {
		otellog.LogWarn(ctx, fmt.Sprintf(
			"View %s has %d stored like/not_like condition(s) using '%%' as a wildcard; matched as a literal. "+
				"Escape it as '\\%%' or switch the condition to [regex] in the view definition.",
			view.ID, rewritten))
	}

	fromResource, err := lvs.rs.GetByID(ctx, nodeCfg.ResourceID)
	if err != nil {
		otellog.LogError(ctx, "Get source resource failed", err)
		return nil, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError).
			WithErrorDetails(fmt.Sprintf("failed to get source resource %s: %v", nodeCfg.ResourceID, err))
	}
	if fromResource == nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Resource_NotFound).
			WithErrorDetails(fmt.Sprintf("source resource %s not found", nodeCfg.ResourceID))
		otellog.LogError(ctx, "Source resource not found", httpErr)
		return nil, 0, httpErr
	}
	if _, err := resource.EnsureResourceQueryable(ctx, fromResource); err != nil {
		return nil, 0, err
	}
	if fromResource.Category == interfaces.ResourceCategoryIndex &&
		params.Aggregation == nil && len(params.GroupBy) == 0 && params.Having == nil {
		paging := rawPaging(params)
		limit := paging.EffectiveLimit()
		if limit <= interfaces.MaxPageLimit && paging.Offset > interfaces.MaxPageLimit-limit {
			return nil, 0, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
				WithErrorDetails(fmt.Sprintf("paging.offset + paging.limit must not exceed %d for OpenSearch queries", interfaces.MaxPageLimit))
		}
	}

	catalog, err := lvs.cs.GetByID(ctx, fromResource.CatalogID, true)
	if err != nil {
		otellog.LogError(ctx, "Get catalog failed", err)
		return nil, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError).
			WithErrorDetails(fmt.Sprintf("failed to get catalog: %v", err))
	}
	if catalog == nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Resource_CatalogNotFound).
			WithErrorDetails(fmt.Sprintf("catalog %s not found", fromResource.CatalogID))
		otellog.LogError(ctx, "Catalog not found", httpErr)
		return nil, 0, httpErr
	}
	if !catalog.Enabled {
		return nil, 0, rest.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Catalog_IsDisabled).
			WithErrorDetails("catalog is disabled")
	}

	fieldMap := map[string]*interfaces.Property{}
	outputFields := make([]string, 0, len(view.SchemaDefinition))
	for _, prop := range view.SchemaDefinition {
		fieldMap[prop.Name] = prop
		outputFields = append(outputFields, prop.Name)
	}
	params.OutputFields = outputFields

	// To merge the FilterCondCfg of resources and queries, it is necessary to determine whether it is nil
	var mergedFilterCond *interfaces.FilterCondCfg
	if fromResourceFilterCond != nil && params.FilterCondCfg != nil {
		mergedFilterCond = &interfaces.FilterCondCfg{
			Operation: filter_condition.OperationAnd,
			SubConds:  []*interfaces.FilterCondCfg{fromResourceFilterCond, params.FilterCondCfg},
		}
	} else if fromResourceFilterCond != nil {
		mergedFilterCond = fromResourceFilterCond
	} else if params.FilterCondCfg != nil {
		mergedFilterCond = params.FilterCondCfg
	}

	// 两半条件的归属不同，状态码必须分开映射：视图定义里存的那半出错是服务端配置问题，
	// 报 400 会让调用方去查自己根本没传的东西，也会把该修的视图定义盖掉。
	if fromResourceFilterCond != nil {
		if _, err := filter_condition.NewFilterCondition(ctx, fromResourceFilterCond, fieldMap); err != nil {
			otellog.LogError(ctx, "Create filter condition from view definition failed", err)
			return nil, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError).
				WithErrorDetails(fmt.Sprintf("view %s has an invalid stored filter condition: %v", view.ID, err))
		}
	}
	if params.FilterCondCfg != nil {
		if _, err := filter_condition.NewFilterCondition(ctx, params.FilterCondCfg, fieldMap); err != nil {
			// 调用方传进来的：字段不存在、值类型不对、算子用法不合法都属于请求错误，
			// 报 500 会把「你传错了」说成「服务坏了」，调用方只能去猜。
			otellog.LogError(ctx, "Create filter condition failed", err)
			return nil, 0, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InvalidParameter).
				WithErrorDetails(err.Error())
		}
	}

	actualFilterCond, err := filter_condition.NewFilterCondition(ctx, mergedFilterCond, fieldMap)
	if err != nil {
		// 两半都单独校验过了，合并后还失败只能是服务端自己的问题
		otellog.LogError(ctx, "Create merged filter condition failed", err)
		return nil, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError).
			WithErrorDetails(err.Error())
	}
	params.ActualFilterCond = actualFilterCond

	// Hand over the Execution PhysicalQuery to handle the SQL push-down
	return lvs.executePhysicalQuery(ctx, catalog, fromResource, params)
}

func (lvs *logicViewService) executePhysicalQuery(ctx context.Context, catalog *interfaces.Catalog, resource *interfaces.Resource,
	params *interfaces.ResourceDataQueryParams) ([]map[string]any, int64, error) {

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Execute physical query")
	defer span.End()

	logger.Debugf("executePhysicalQuery, resourceID: %s, catalogID: %s, params: %v",
		resource.ID, resource.CatalogID, params)

	switch resource.Category {
	case interfaces.ResourceCategoryTable:
		return lvs.executeTableQuery(ctx, catalog, resource, params)
	case interfaces.ResourceCategoryIndex:
		return lvs.executeIndexQuery(ctx, catalog, resource, params)
	default:
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InternalError_InvalidCategory).
			WithErrorDetails(fmt.Sprintf("unsupported resource category: %s", resource.Category))
		otellog.LogError(ctx, "Unsupported resource category", httpErr)
		return nil, 0, httpErr
	}
}

func (lvs *logicViewService) queryCompositeLogicView(ctx context.Context, view *interfaces.LogicView,
	params *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Query composite logic view")
	defer span.End()

	// The category of the input resource determines whether to generate SQL or DSL
	isDSL := false
	catalogMap := map[string]struct{}{}
	refResources := make(map[string]*interfaces.Resource, 0)
	for _, logicNode := range view.LogicDefinition {
		if logicNode.Type == interfaces.LogicDefinitionNodeType_Resource {
			var nodeCfg interfaces.ResourceNodeCfg
			if err := mapstructure.Decode(logicNode.Config, &nodeCfg); err != nil {
				otellog.LogError(ctx, "Decode resource node config failed", err)
				return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError).
					WithErrorDetails(fmt.Sprintf("failed to decode resource node config: %v", err))
			}

			fromResource, err := lvs.rs.GetByID(ctx, nodeCfg.ResourceID)
			if err != nil {
				otellog.LogError(ctx, "Get source resource failed", err)
				return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError).
					WithErrorDetails(fmt.Sprintf("failed to get source resource %s: %v", nodeCfg.ResourceID, err))
			}
			if fromResource == nil {
				httpErr := rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Resource_NotFound).
					WithErrorDetails(fmt.Sprintf("source resource %s not found", nodeCfg.ResourceID))
				otellog.LogError(ctx, "Source resource not found", httpErr)
				return nil, httpErr
			}
			refResources[nodeCfg.ResourceID] = fromResource

			if fromResource.Category == interfaces.ResourceCategoryIndex {
				isDSL = true
			}
			catalogMap[fromResource.CatalogID] = struct{}{}
		}
	}
	view.RefResources = refResources

	view.IsSingleSource = len(catalogMap) == 1

	if isDSL {
		return lvs.executeCompositeViewByDSL(ctx, view, params)
	} else {
		return lvs.executeCompositeViewBySQL(ctx, view, params)
	}
}

func (lvs *logicViewService) executeCompositeViewByDSL(ctx context.Context, view *interfaces.LogicView,
	params *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Query composite view data")
	defer span.End()

	// Obtain the index list, the mapping from the view ID to the index list
	_, indices, viewIndicesMap, err := lvs.getIndicesByView(view)
	if err != nil {
		otellog.LogError(ctx, "Get indices failed", err)
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			rest.PublicError_InternalServerError).WithErrorDetails(err.Error())
	}

	// If the index list is empty, return empty data and there is no need to concatenate the dsl below
	if len(indices) == 0 {
		span.SetStatus(codes.Ok, "No indices found")
		return &interfaces.ResourceDataQueryResult{Paging: &interfaces.PagingResponse{}}, nil
	}

	generator := lvdsl.NewlogicViewDSLGenerator(view)
	queryParams := withoutPagingLimit(params)
	dsl, httpErr := generator.BuildDSL(ctx, queryParams, view, viewIndicesMap)
	if httpErr != nil {
		otellog.LogError(ctx, "Convert to DSL failed", httpErr)
		return nil, httpErr
	}

	if view.IsSingleSource {
		// dsl to map
		dslBytes, err := sonic.Marshal(dsl)
		if err != nil {
			otellog.LogError(ctx, "Marshal DSL failed", err)
			return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, rest.PublicError_InternalServerError).
				WithErrorDetails(fmt.Sprintf("failed to marshal dsl: %v", err))
		}

		var dslMap map[string]any
		err = sonic.Unmarshal(dslBytes, &dslMap)
		if err != nil {
			otellog.LogError(ctx, "Unmarshal DSL failed", err)
			return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, rest.PublicError_InternalServerError).
				WithErrorDetails(fmt.Sprintf("failed to unmarshal dsl: %v", err))
		}

		if len(view.RefResources) != 1 {
			return nil, rest.NewHTTPError(ctx, http.StatusNotImplemented, rest.PublicError_NotImplemented).
				WithErrorDetails("OpenSearch logic views with multiple resources are not supported by raw query")
		}
		for resourceID := range view.RefResources {
			dslMap["resource_id"] = resourceID
		}
		paging := rawPaging(params)
		req := interfaces.RawQueryRequest{
			Query:                  dslMap,
			QueryFormat:            interfaces.QueryFormatDSL,
			InputDialect:           "opensearch",
			QueryTimeoutSec:        int(params.Timeout.Seconds()),
			NeedTotal:              params.NeedTotal,
			Paging:                 paging,
			ResourceDataResourceID: view.Resource.ID,
			ResourceDataUpdateTime: view.Resource.UpdateTime,
		}
		res, err := lvs.qs.Execute(ctx, &req)
		if err != nil {
			otellog.LogError(ctx, "Execute raw query failed", err)
			return nil, err
		}
		return rawQueryResult(res), nil
	} else {
		httpErr := rest.NewHTTPError(ctx, http.StatusNotImplemented, rest.PublicError_NotImplemented).
			WithErrorDetails("composite view execution is not implemented")
		otellog.LogError(ctx, "Composite view execution is not implemented", httpErr)
		return nil, httpErr
	}
}

// Obtain the index list from the view and return catalogName, viewIndicesMap (the mapping from view id to index list)
func (lvs *logicViewService) getIndicesByView(view *interfaces.LogicView) (string, []string, map[string][]string, error) {
	var catalog string
	catalogMap := map[string]struct{}{}
	indices := []string{}
	viewIndicesMap := map[string][]string{}
	// Determine whether the catalogs of multiple view nodes are consistent
	for _, ref := range view.RefResources {
		sourceIdentifier := strings.Split(ref.SourceIdentifier, ".")
		indices = append(indices, sourceIdentifier[len(sourceIdentifier)-1])
		viewIndicesMap[ref.ID] = append(viewIndicesMap[ref.ID], sourceIdentifier[len(sourceIdentifier)-1])

		catalog = ref.CatalogID
		catalogMap[catalog] = struct{}{}

	}

	if len(catalogMap) > 1 {
		return "", nil, nil, fmt.Errorf("custom view %s has different catalog %v", view.Name, catalogMap)
	}

	return catalog, indices, viewIndicesMap, nil

}

func (lvs *logicViewService) executeCompositeViewBySQL(ctx context.Context, view *interfaces.LogicView,
	params *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
	// Ideal state: Obtain the SQL builder directly from the generator
	ldGenerator := lvsql.NewlogicDefinitionSQLGenerator(view)
	builder, err := ldGenerator.NewQueryBuilder(ctx, view)
	if err != nil {
		otellog.LogError(ctx, "Initialize query builder failed", err)
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, rest.PublicError_InternalServerError).
			WithErrorDetails(fmt.Sprintf("failed to initialize query builder: %v", err))
	}

	// Uniformly apply query parameters (filtering, sorting, pagination)
	queryParams := withoutPagingLimit(params)
	if err := builder.ApplyParams(ctx, &queryParams, view); err != nil {
		otellog.LogError(ctx, "Apply query parameters failed", err)
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, rest.PublicError_InternalServerError).
			WithErrorDetails(fmt.Sprintf("failed to apply query parameters: %v", err))
	}

	finalSql := builder.Build()
	logger.Infof("executeCompositeViewBySQL Final SQL: [%s]", query.SafeQuerySummary(finalSql))

	if view.IsSingleSource {
		paging := rawPaging(params)
		req := interfaces.RawQueryRequest{
			Query:       finalSql,
			QueryFormat: interfaces.QueryFormatSQL,
			// lvsql emits MySQL-style quoted identifiers (backticks). Raw Query
			// transpiles it when the resolved Catalog uses another SQL dialect.
			InputDialect:           "mysql",
			QueryTimeoutSec:        int(params.Timeout.Seconds()),
			NeedTotal:              params.NeedTotal,
			Paging:                 paging,
			ResourceDataResourceID: view.Resource.ID,
			ResourceDataUpdateTime: view.Resource.UpdateTime,
		}
		res, err := lvs.qs.Execute(ctx, &req)
		if err != nil {
			otellog.LogError(ctx, "Execute raw query failed", err)
			return nil, err
		}
		return rawQueryResult(res), nil
	} else {
		httpErr := rest.NewHTTPError(ctx, http.StatusNotImplemented, rest.PublicError_NotImplemented).
			WithErrorDetails("composite view execution is not implemented")
		otellog.LogError(ctx, "Composite view execution is not implemented", httpErr)
		return nil, httpErr
	}
}

// withoutPagingLimit leaves filtering and sorting with the logic-view
// generator, while RawQueryService owns offset/size for both single and cursor
// pages. This prevents a generated LIMIT/from from competing with cursor state.
func withoutPagingLimit(params *interfaces.ResourceDataQueryParams) interfaces.ResourceDataQueryParams {
	copy := *params
	copy.Offset = 0
	copy.Limit = 0
	return copy
}

func rawPaging(params *interfaces.ResourceDataQueryParams) interfaces.PagingRequest {
	paging := params.Paging
	if paging.Mode == "" && paging.Cursor == "" && paging.Limit == 0 && (params.Offset != 0 || params.Limit != 0) {
		paging.Offset = params.Offset
		paging.Limit = params.Limit
	}
	return paging
}

func (lvs *logicViewService) executeIndexQuery(ctx context.Context, catalog *interfaces.Catalog, resource *interfaces.Resource,
	params *interfaces.ResourceDataQueryParams) ([]map[string]any, int64, error) {

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Execute index query")
	defer span.End()

	connector, err := lvs.cf.CreateConnectorInstance(ctx, catalog.ConnectorType, catalog.ConnectorCfg)
	if err != nil {
		otellog.LogError(ctx, "Create connector failed", err)
		return nil, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError).
			WithErrorDetails(fmt.Sprintf("failed to create connector: %v", err))
	}

	if err := connector.Connect(ctx); err != nil {
		otellog.LogError(ctx, "Connect to data source failed", err)
		return nil, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError).
			WithErrorDetails(fmt.Sprintf("failed to connect to data source: %v", err))
	}
	defer func() { _ = connector.Close(ctx) }()

	indexConnector, ok := connector.(interfaces.IndexConnector)
	if !ok {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InternalError_InvalidCategory).
			WithErrorDetails(fmt.Sprintf("connector %s does not support index operations", catalog.ConnectorType))
		otellog.LogError(ctx, "Connector does not support index operations", httpErr)
		return nil, 0, httpErr
	}

	result, err := indexConnector.ExecuteQuery(ctx, resource.Name, resource, params)
	if err != nil {
		otellog.LogError(ctx, "Execute query failed", err)
		if unsupported, ok := filter_condition.AsUnsupportedOperationError(err); ok {
			return nil, 0, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
				WithErrorDetails(unsupported.Error())
		}
		return nil, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError).
			WithErrorDetails(fmt.Sprintf("failed to execute query: %v", err))
	}
	params.SearchAfter = append([]any(nil), result.SearchAfter...)
	return result.Entries, result.Total, nil
}

func (lvs *logicViewService) executeTableQuery(ctx context.Context, catalog *interfaces.Catalog, resource *interfaces.Resource,
	params *interfaces.ResourceDataQueryParams) ([]map[string]any, int64, error) {

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Execute table query")
	defer span.End()

	connector, err := lvs.cf.CreateConnectorInstance(ctx, catalog.ConnectorType, catalog.ConnectorCfg)
	if err != nil {
		otellog.LogError(ctx, "Create connector failed", err)
		return nil, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError).
			WithErrorDetails(fmt.Sprintf("failed to create connector: %v", err))
	}

	if err := connector.Connect(ctx); err != nil {
		otellog.LogError(ctx, "Connect to data source failed", err)
		return nil, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError).
			WithErrorDetails(fmt.Sprintf("failed to connect to data source: %v", err))
	}
	defer func() { _ = connector.Close(ctx) }()

	tableConnector, ok := connector.(interfaces.TableConnector)
	if !ok {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InternalError_InvalidCategory).
			WithErrorDetails(fmt.Sprintf("connector %s does not support table operations", catalog.ConnectorType))
		otellog.LogError(ctx, "Connector does not support table operations", httpErr)
		return nil, 0, httpErr
	}

	result, err := tableConnector.ExecuteQuery(ctx, resource, params)
	if err != nil {
		otellog.LogError(ctx, "Execute query failed", err)
		if unsupported, ok := filter_condition.AsUnsupportedOperationError(err); ok {
			return nil, 0, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
				WithErrorDetails(unsupported.Error())
		}
		return nil, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError).
			WithErrorDetails(fmt.Sprintf("failed to execute query: %v", err))
	}
	return result.Entries, result.Total, nil
}

// FieldInfo represents the field information output by the SQL query
type FieldInfo struct {
	Name      string `json:"name"`       // Field name or expression.
	Alias     string `json:"alias"`      // Field alias, empty when no alias is present.
	IsStar    bool   `json:"is_star"`    // Whether the field is a wildcard.
	IsComplex bool   `json:"is_complex"` // Whether the field is a complex expression, such as a function or CASE.
}

// QueryAnalysis represents the analysis results of SQL queries
type QueryAnalysis struct {
	Fields       []FieldInfo `json:"fields"`
	HasStar      bool        `json:"has_star"`
	HasUnion     bool        `json:"has_union"`
	HasJoin      bool        `json:"has_join"`
	HasAggregate bool        `json:"has_aggregate"`
	HasSubquery  bool        `json:"has_subquery"`
	HasCase      bool        `json:"has_case"`
	Error        error       `json:"error,omitempty"`
}

// String returns the string representation of the analysis result
func (q *QueryAnalysis) String() string {
	if q.Error != nil {
		return fmt.Sprintf("Analysis error: %v", q.Error)
	}

	result := fmt.Sprintf("Query fields (%d):\n", len(q.Fields))
	for i, field := range q.Fields {
		fieldDesc := field.Name
		if field.Alias != "" {
			fieldDesc = fmt.Sprintf("%s AS %s", field.Name, field.Alias)
		}
		if field.IsStar {
			fieldDesc = "*"
		}
		result += fmt.Sprintf("  %d. %s\n", i+1, fieldDesc)
	}

	result += "\nQuery features:\n"
	result += fmt.Sprintf("  - Contains UNION: %t\n", q.HasUnion)
	result += fmt.Sprintf("  - Contains JOIN: %t\n", q.HasJoin)
	result += fmt.Sprintf("  - Contains aggregate functions: %t\n", q.HasAggregate)
	result += fmt.Sprintf("  - Contains subqueries: %t\n", q.HasSubquery)
	result += fmt.Sprintf("  - Contains CASE expressions: %t\n", q.HasCase)

	return result
}

// SQLFieldParser SQL field parser
type SQLFieldParser struct {
	listener *sqlFieldListener
}

// NewSQLFieldParser creates a new SQL field parser
func NewSQLFieldParser() *SQLFieldParser {
	return &SQLFieldParser{
		listener: newSqlFieldListener(),
	}
}

// Parse parses SQL statements and returns the results of field analysis
func (p *SQLFieldParser) Parse(sql string) *QueryAnalysis {
	// Create an input stream
	input := antlr.NewInputStream(sql)

	// Create a lexical analyzer
	lexer := parsing.NewSqlBaseLexer(input)

	// Create a token stream
	stream := antlr.NewCommonTokenStream(lexer, antlr.TokenDefaultChannel)

	// Create a parser
	parser := parsing.NewSqlBaseParser(stream)

	// Add an error listener
	parser.RemoveErrorListeners()
	errorListener := newErrorListener()
	parser.AddErrorListener(errorListener)

	// Build the parsing tree - Adjust the starting rule according to the actual grammar rules
	tree := parser.Query()

	// Traverse the parse tree
	antlr.ParseTreeWalkerDefault.Walk(p.listener, tree)

	analysis := p.listener.getAnalysis()
	if errorListener.hasErrors() {
		analysis.Error = fmt.Errorf("SQL syntax error: %s", strings.Join(errorListener.getErrors(), "; "))
	}

	return analysis
}

// sqlFieldListener Custom field parsing listener
type sqlFieldListener struct {
	*parsing.BaseSqlBaseListener
	analysis          *QueryAnalysis
	currentQueryLevel int
	inSelectClause    bool
	currentField      *FieldInfo
}

// newSqlFieldListener creates a new field listener
func newSqlFieldListener() *sqlFieldListener {
	return &sqlFieldListener{
		analysis: &QueryAnalysis{
			Fields: make([]FieldInfo, 0),
		},
		currentQueryLevel: 0,
		inSelectClause:    false,
	}
}

// getAnalysis to obtain the analysis results
func (l *sqlFieldListener) getAnalysis() *QueryAnalysis {
	return l.analysis
}

// Enter the query with EnterQuery
func (l *sqlFieldListener) EnterQuery(ctx *parsing.QueryContext) {
	l.currentQueryLevel++
}

// ExitQuery exits the query
func (l *sqlFieldListener) ExitQuery(ctx *parsing.QueryContext) {
	l.currentQueryLevel--
}

// Enter the query specification (SELECT statement)
func (l *sqlFieldListener) EnterQuerySpecification(ctx *parsing.QuerySpecificationContext) {
	l.inSelectClause = true
}

// Exitquery Specification ExitQuerySpecification (SELECT statement)
func (l *sqlFieldListener) ExitQuerySpecification(ctx *parsing.QuerySpecificationContext) {
	l.inSelectClause = false
}

// EnterSelectSingle handles the selection of a single field
func (l *sqlFieldListener) EnterSelectSingle(ctx *parsing.SelectSingleContext) {
	if !l.inSelectClause || l.currentQueryLevel > 1 {
		// Only process the outermost SELECT field
		return
	}

	// Get the text of the options
	itemText := l.getText(ctx)

	// Create field information
	l.currentField = &FieldInfo{
		Name:      itemText,
		IsStar:    l.isStarExpression(itemText),
		IsComplex: l.isComplexExpression(itemText),
	}

	if l.isStarExpression(itemText) {
		l.analysis.HasStar = true
	}

	// Check if there is an alias
	if alias := l.extractAlias(ctx); alias != "" {
		l.currentField.Alias = alias
		l.currentField.Name = strings.TrimSuffix(l.currentField.Name, " AS "+alias)
		l.currentField.Name = strings.TrimSuffix(l.currentField.Name, " "+alias)
	}

	// Add to the analysis results
	l.analysis.Fields = append(l.analysis.Fields, *l.currentField)
	l.currentField = nil
}

// The isStarExpression checks if it is a * wildcard
func (l *sqlFieldListener) isStarExpression(expr string) bool {
	return strings.TrimSpace(expr) == "*"
}

// isComplexExpression reports whether an expression is complex.
func (l *sqlFieldListener) isComplexExpression(expr string) bool {
	trimmed := strings.ToUpper(strings.TrimSpace(expr))

	// Check the function call
	if strings.Contains(trimmed, "(") && strings.Contains(trimmed, ")") {
		return true
	}

	// Check the CASE expression
	if strings.HasPrefix(trimmed, "CASE") {
		l.analysis.HasCase = true
		return true
	}

	// Check the aggregation function
	if l.isAggregateFunction(trimmed) {
		l.analysis.HasAggregate = true
		return true
	}

	// Check arithmetic expressions
	if strings.ContainsAny(trimmed, "+-*/%") {
		return true
	}

	return false
}

// The isAggregateFunction checks whether it is an aggregation function
func (l *sqlFieldListener) isAggregateFunction(expr string) bool {
	upperExpr := strings.ToUpper(expr)
	aggregateFuncs := []string{
		"COUNT(", "SUM(", "AVG(", "MIN(", "MAX(",
		"GROUP_CONCAT(", "ARRAY_AGG(", "STRING_AGG(",
	}

	for _, funcName := range aggregateFuncs {
		if strings.Contains(upperExpr, funcName) {
			return true
		}
	}
	return false
}

// extractAlias extracts field aliases
func (l *sqlFieldListener) extractAlias(ctx antlr.ParserRuleContext) string {
	// Extract aliases based on actual grammar rules
	// Here is a general implementation. You may need to adjust it according to your g4 syntax

	children := ctx.GetChildren()
	for _, child := range children {
		if terminal, ok := child.(antlr.TerminalNode); ok {
			text := terminal.GetText()
			upperText := strings.ToUpper(text)
			if upperText == "AS" {
				// Find the keyword "AS", and the next sibling node should be an alias
				return l.getNextSiblingText(child)
			}
		}
	}

	// If there is no "AS" keyword, check whether the last child node might be an alias
	// This needs to be adjusted according to specific grammar rules
	return ""
}

// getNextSiblingText gets the text of the next sibling node
func (l *sqlFieldListener) getNextSiblingText(node antlr.Tree) string {
	parent := getParent(node)
	if parent == nil {
		return ""
	}

	children := parent.GetChildren()
	found := false
	for _, child := range children {
		if found {
			return l.getText(child)
		}
		if child == node {
			found = true
		}
	}
	return ""
}

// getParent gets the parent node of the node
func getParent(node antlr.Tree) antlr.Tree {
	if node == nil {
		return nil
	}

	switch n := node.(type) {
	case antlr.ParserRuleContext:
		return n.GetParent()
	case antlr.TerminalNode:
		return n.GetParent()
	default:
		return nil
	}
}

// getText securely acquires the text content of nodes
func (l *sqlFieldListener) getText(node antlr.Tree) string {
	if node == nil {
		return ""
	}

	switch ctx := node.(type) {
	case antlr.ParserRuleContext:
		return ctx.GetText()
	case antlr.TerminalNode:
		return ctx.GetText()
	default:
		return fmt.Sprintf("%v", node)
	}
}

// EnterSetOperation handles UNION operations
func (l *sqlFieldListener) EnterSetOperation(ctx *parsing.SetOperationContext) {
	l.analysis.HasUnion = true
}

// EnterJoinRelation handles the JOIN relationship
func (l *sqlFieldListener) EnterJoinRelation(ctx *parsing.JoinRelationContext) {
	l.analysis.HasJoin = true
}

// EnterSubquery handles subqueries
func (l *sqlFieldListener) EnterSubquery(ctx *parsing.SubqueryContext) {
	if l.currentQueryLevel > 0 {
		l.analysis.HasSubquery = true
	}
}

// errorListener Custom error listener
type errorListener struct {
	*antlr.DefaultErrorListener
	errors []string
}

// newErrorListener creates an error listener
func newErrorListener() *errorListener {
	return &errorListener{
		errors: make([]string, 0),
	}
}

// SyntaxError handles syntax errors
func (l *errorListener) SyntaxError(
	recognizer antlr.Recognizer,
	offendingSymbol interface{},
	line, column int,
	msg string,
	e antlr.RecognitionException,
) {
	errorMsg := fmt.Sprintf("line %d, column %d: %s", line, column, msg)
	l.errors = append(l.errors, errorMsg)
}

// hasErrors checks for errors
func (l *errorListener) hasErrors() bool {
	return len(l.errors) > 0
}

// getErrors gets all errors
func (l *errorListener) getErrors() []string {
	return l.errors
}

// GetFieldNames gets all field names (aliases are preferred; if there are no aliases, field names are used)
func (q *QueryAnalysis) GetFieldNames() []string {
	names := make([]string, len(q.Fields))
	for i, field := range q.Fields {
		if field.Alias != "" {
			names[i] = field.Alias
		} else if field.IsStar {
			names[i] = "*"
		} else {
			names[i] = field.Name
		}
	}
	return names
}

// HasComplexFields checks whether complex fields are included
func (q *QueryAnalysis) HasComplexFields() bool {
	for _, field := range q.Fields {
		if field.IsComplex {
			return true
		}
	}
	return false
}

// GetSimpleFieldNames gets simple field names (excluding complex expressions and *)
func (q *QueryAnalysis) GetSimpleFieldNames() []string {
	var names []string
	for _, field := range q.Fields {
		if !field.IsComplex && !field.IsStar {
			if field.Alias != "" {
				names = append(names, field.Alias)
			} else {
				names = append(names, field.Name)
			}
		}
	}
	return names
}

// FormatAsJSON formats the parsing result into JSON
func (info *QueryAnalysis) FormatAsJSON() string {
	jsonData, err := sonic.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Sprintf(`{"error": "JSON formatting failed: %v"}`, err)
	}
	return string(jsonData)
}
