// Package resource_data provides resource data query business logic.
package resource_data

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

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
	"vega-backend/logics/dataset"
	"vega-backend/logics/filter_condition"
	"vega-backend/logics/local_index"
	"vega-backend/logics/model_factory"
	querylogic "vega-backend/logics/query"
	"vega-backend/logics/rate"
	resourcelogic "vega-backend/logics/resource"
	"vega-backend/logics/resource_data/logic_view"
)

var (
	rdServiceOnce sync.Once
	rdService     interfaces.ResourceDataService
)

type resourceDataService struct {
	appSetting *common.AppSetting
	cf         interfaces.ConnectorFactory
	ds         interfaces.DatasetService
	lim        interfaces.LocalIndexManager
	cs         interfaces.CatalogService
	rs         interfaces.ResourceService
	lvs        interfaces.LogicViewService
	mfs        interfaces.ModelFactoryService
	cl         rate.ConcurrencyLimiter
}

// NewResourceDataService creates a new ResourceDataService.
func NewResourceDataService(appSetting *common.AppSetting) interfaces.ResourceDataService {
	rdServiceOnce.Do(func() {
		rdService = &resourceDataService{
			appSetting: appSetting,
			cf:         factory.GetFactory(appSetting),
			ds:         dataset.NewDatasetService(appSetting),
			lim:        local_index.NewLocalIndexManager(appSetting),
			cs:         catalog.NewCatalogService(appSetting),
			rs:         resourcelogic.NewResourceService(appSetting),
			lvs:        logic_view.NewLogicViewService(appSetting),
			mfs:        model_factory.NewModelFactoryService(appSetting),
		}

		// Initialize concurrency limiter if enabled
		if appSetting.RateLimitingSetting.Concurrency.Enabled && appSetting.RateLimitingSetting.Concurrency.Global.MaxConcurrentQueries > 0 {
			cfg := rate.ConcurrencyConfig{
				Enabled: appSetting.RateLimitingSetting.Concurrency.Enabled,
				Global: rate.GlobalConcurrencyConfig{
					MaxConcurrentQueries: appSetting.RateLimitingSetting.Concurrency.Global.MaxConcurrentQueries,
				},
			}

			rdService.(*resourceDataService).cl = rate.NewConcurrencyLimiter(cfg)
		}
	})
	return rdService
}

// query executes the existing structured resource-data path. Public callers
// use QueryWithPaging so every result has the common paging envelope.
func (rds *resourceDataService) query(ctx context.Context, resource *interfaces.Resource,
	params *interfaces.ResourceDataQueryParams) ([]map[string]any, int64, error) {

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "List resource documents")
	defer span.End()

	logger.Debugf("Query, resourceID: %s, params: %v", resource.ID, params)

	catalog, err := rds.cs.GetByID(ctx, resource.CatalogID, true)
	if err != nil {
		otellog.LogError(ctx, "Get catalog failed", err)
		return nil, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError).
			WithErrorDetails(fmt.Sprintf("failed to get catalog: %v", err))
	}
	if catalog == nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Resource_CatalogNotFound).
			WithErrorDetails(fmt.Sprintf("catalog %s not found", resource.CatalogID))
		otellog.LogError(ctx, "Catalog not found", httpErr)
		return nil, 0, httpErr
	}
	if !catalog.Enabled {
		httpErr := rest.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Catalog_IsDisabled).
			WithErrorDetails("catalog is disabled")
		otellog.LogError(ctx, "Catalog is disabled", httpErr)
		return nil, 0, httpErr
	}

	maxConcurrentQueries := int64(0)
	if resource.Category != interfaces.ResourceCategoryLogicView {
		if concurrent, existsInCatalog := catalog.ConnectorCfg["concurrent"]; existsInCatalog {
			maxConcurrentQueries = int64(concurrent.(float64))
		}
	}

	// Concurrent control
	var release func()
	if rds.cl != nil {
		// Obtain concurrent permission
		var acquireErr error
		release, acquireErr = rds.cl.Acquire(rate.AcquireParams{
			CatalogID:            resource.CatalogID,
			MaxConcurrentQueries: maxConcurrentQueries,
		})
		if acquireErr != nil {
			logger.Warnf("Concurrency limit exceeded: catalog=%s, error=%v",
				resource.CatalogID, acquireErr)

			// Return a rate limiting error
			if rateErr, ok := acquireErr.(*rate.RateLimitError); ok {
				httpErr := rest.NewHTTPError(ctx, rateErr.HTTPStatus, verrors.VegaBackend_Query_ConcurrencyLimitExceeded).
					WithErrorDetails(rateErr.Message)
				otellog.LogError(ctx, "Concurrency limit exceeded", httpErr)
				return nil, 0, httpErr
			}
			httpErr := rest.NewHTTPError(ctx, http.StatusTooManyRequests, verrors.VegaBackend_Query_ConcurrencyLimitExceeded).
				WithErrorDetails("Query concurrency limit exceeded, please retry later")
			otellog.LogError(ctx, "Concurrency limit exceeded", httpErr)
			return nil, 0, httpErr
		}
		defer release() // Release the permission after the query is completed
	}

	fieldMap := map[string]*interfaces.Property{}
	for _, prop := range resource.SchemaDefinition {
		fieldMap[prop.Name] = prop
	}
	// There are also vector fields generated by build tasks in the local index, which are not on the resource schema. Not added.
	// The knn_vector condition will be judged as "Field does not exist" during the field search stage.
	for name, prop := range interfaces.LocalIndexGeneratedFields(resource) {
		fieldMap[name] = prop
	}

	// When a table resource does not have a local index, full-text search has nowhere to go. Here, it is rejected and the reason is explained.
	if err := validateFulltextConditions(resource, params.FilterCondCfg); err != nil {
		otellog.LogError(ctx, "Full-text condition rejected", err)
		return nil, 0, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InvalidParameter).
			WithErrorDetails(err.Error())
	}

	// The conditions for vector retrieval include the query text and the source field name. Here, they are changed to vector and physical vector fields.
	if err := rds.resolveVectorConditions(ctx, resource, params.FilterCondCfg); err != nil {
		otellog.LogError(ctx, "Resolve vector condition failed", err)
		return nil, 0, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InvalidParameter).
			WithErrorDetails(err.Error())
	}
	actualFilterCond, err := filter_condition.NewFilterCondition(ctx, params.FilterCondCfg, fieldMap)
	if err != nil {
		// The condition came from the caller: a missing field, a wrong value type or a misused
		// operator are all request errors, and a 500 would say the service is broken when the
		// request is what needs fixing.
		otellog.LogError(ctx, "Create filter condition failed", err)
		return nil, 0, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InvalidParameter).
			WithErrorDetails(err.Error())
	}
	params.ActualFilterCond = actualFilterCond

	switch resource.Category {
	case interfaces.ResourceCategoryDataset:
		// Call dataset access to list the documents
		documents, total, err := rds.ds.ListDocuments(ctx, resource.ID, resource, params)
		if err != nil {
			otellog.LogError(ctx, "List dataset documents failed", err)
			return nil, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError).
				WithErrorDetails(err.Error())
		}
		return documents, total, nil

	case interfaces.ResourceCategoryTable:
		// Only an available managed index may be queried. Stale index names are
		// retained for diagnostics and must fall back to the source.
		if interfaces.HasAvailableLocalIndex(resource) {
			// Call the local index manager to list the build product documentation
			documents, total, err := rds.lim.ListDocuments(ctx, resource.LocalIndexName, resource, params)
			if err != nil {
				otellog.LogError(ctx, "Query table data from local index failed", err)
				return nil, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError).
					WithErrorDetails(err.Error())
			}

			span.SetStatus(codes.Ok, "")
			return documents, total, nil
		}

		// Prepare the sort parameter
		params = rds.prepareSortParams(resource, params)
		// Prepare output
		params = rds.prepareOutputFieldsParams(resource, params)

		data, total, err := rds.QueryData(ctx, catalog, resource, params)
		if err != nil {
			otellog.LogError(ctx, "Query table data failed", err)
			// QueryData throws the typed errors as they are. Unconditionally repackage it to 500 and it will be there
			// The 400 (not supported by the operator) identified inside is smoothed out, and those mappings are equivalent to not being written.
			var httpErr *rest.HTTPError
			if errors.As(err, &httpErr) {
				return nil, 0, httpErr
			}
			return nil, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError).
				WithErrorDetails(err.Error())
		}

		span.SetStatus(codes.Ok, "")
		return data, total, nil

	case interfaces.ResourceCategoryIndex:
		data, total, err := rds.QueryData(ctx, catalog, resource, params)
		if err != nil {
			otellog.LogError(ctx, "Query index data failed", err)
			// QueryData throws the typed errors as they are. Unconditionally repackage it to 500 and it will be there
			// The 400 (not supported by the operator) identified inside is smoothed out, and those mappings are equivalent to not being written.
			var httpErr *rest.HTTPError
			if errors.As(err, &httpErr) {
				return nil, 0, httpErr
			}
			return nil, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError).
				WithErrorDetails(err.Error())
		}

		span.SetStatus(codes.Ok, "")
		return data, total, nil

	case interfaces.ResourceCategoryLogicView:
		// Prepare the sort parameter
		params = rds.prepareSortParams(resource, params)
		// Prepare output
		params = rds.prepareOutputFieldsParams(resource, params)

		// Query data in a logical view
		result, err := rds.lvs.QueryWithPaging(ctx, resource, params)
		if err != nil {
			otellog.LogError(ctx, "Query logic view data failed", err)
			return nil, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError).
				WithErrorDetails(err.Error())
		}

		span.SetStatus(codes.Ok, "")
		return result.Entries, result.TotalCount, nil

	case interfaces.ResourceCategoryFileset:
		// Prepare the sort parameter
		params = rds.prepareSortParams(resource, params)
		// Prepare output
		params = rds.prepareOutputFieldsParams(resource, params)

		data, total, err := rds.QueryData(ctx, catalog, resource, params)
		if err != nil {
			otellog.LogError(ctx, "Query fileset data failed", err)
			return nil, 0, err
		}

		span.SetStatus(codes.Ok, "")
		return data, total, nil

	default:
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InternalError_InvalidCategory).
			WithErrorDetails(resource.Category)
		otellog.LogError(ctx, "Unsupported resource category", httpErr)
		return nil, 0, httpErr
	}
}

// QueryWithPaging is the sole public resource-data query entrypoint.
func (rds *resourceDataService) QueryWithPaging(ctx context.Context, resource *interfaces.Resource,
	params *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
	if _, err := resourcelogic.EnsureResourceQueryable(ctx, resource); err != nil {
		return nil, err
	}
	if resource.Category == interfaces.ResourceCategoryLogicView {
		return rds.lvs.QueryWithPaging(ctx, resource, params)
	}
	paginationCategory := resourceDataPaginationCategory(resource)
	if params.Paging.Cursor != "" {
		if !resourceDataCursorSupported(resource.Category) {
			return nil, rest.NewHTTPError(ctx, http.StatusNotImplemented, verrors.VegaBackend_Query_InvalidParameter).
				WithErrorDetails("cursor paging is not implemented for this resource category")
		}
		return querylogic.ExecuteResourceDataCursorContinuation(ctx, accountIDFromContext(ctx), resource, params.Paging.Cursor,
			func(pageCtx context.Context, pageParams *interfaces.ResourceDataQueryParams) ([]map[string]any, int64, error) {
				return rds.query(pageCtx, resource, pageParams)
			})
	}
	if paginationCategory == interfaces.ResourceCategoryIndex && !isIndexAggregateQuery(params) {
		limit := params.Paging.EffectiveLimit()
		if limit <= interfaces.MaxPageLimit && params.Paging.Offset > interfaces.MaxPageLimit-limit {
			return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
				WithErrorDetails(fmt.Sprintf("paging.offset + paging.limit must not exceed %d for OpenSearch queries", interfaces.MaxPageLimit))
		}
	}
	if params.Paging.Mode == interfaces.PagingModeCursor {
		if resourceDataCursorSupported(resource.Category) {
			if paginationCategory == interfaces.ResourceCategoryIndex && len(params.Sort) == 0 {
				return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
					WithErrorDetails("sort is required for index cursor paging")
			}
			if paginationCategory == interfaces.ResourceCategoryIndex && isIndexAggregateQuery(params) {
				return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
					WithErrorDetails("cursor paging does not support index aggregation queries")
			}
			return querylogic.ExecuteInitialResourceDataCursorWithCategory(ctx, accountIDFromContext(ctx), resource, paginationCategory, params,
				func(pageCtx context.Context, pageParams *interfaces.ResourceDataQueryParams) ([]map[string]any, int64, error) {
					return rds.query(pageCtx, resource, pageParams)
				})
		}
		return nil, rest.NewHTTPError(ctx, http.StatusNotImplemented, verrors.VegaBackend_Query_InvalidParameter).
			WithErrorDetails("cursor paging is not implemented for this resource category")
	}

	entries, total, err := rds.query(ctx, resource, params)
	if err != nil {
		return nil, err
	}
	return &interfaces.ResourceDataQueryResult{Entries: entries, TotalCount: total, Paging: &interfaces.PagingResponse{}}, nil
}

func isIndexAggregateQuery(params *interfaces.ResourceDataQueryParams) bool {
	return params.Aggregation != nil || len(params.GroupBy) > 0 || params.Having != nil
}

func resourceDataPaginationCategory(resource *interfaces.Resource) string {
	if resource == nil {
		return ""
	}
	if resource.Category == interfaces.ResourceCategoryDataset ||
		resource.Category == interfaces.ResourceCategoryIndex ||
		(resource.Category == interfaces.ResourceCategoryTable && interfaces.HasAvailableLocalIndex(resource)) {
		return interfaces.ResourceCategoryIndex
	}
	return resource.Category
}

func resourceDataCursorSupported(category string) bool {
	return category == interfaces.ResourceCategoryTable ||
		category == interfaces.ResourceCategoryIndex ||
		category == interfaces.ResourceCategoryDataset ||
		category == interfaces.ResourceCategoryFileset
}

func accountIDFromContext(ctx context.Context) string {
	accountInfo, _ := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	return accountInfo.ID
}

func (rds *resourceDataService) QueryData(ctx context.Context, catalog *interfaces.Catalog, resource *interfaces.Resource,
	params *interfaces.ResourceDataQueryParams) ([]map[string]any, int64, error) {

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Query data")
	defer span.End()

	logger.Debugf("QueryData, resourceID: %s, catalogID: %s, params: %v",
		resource.ID, resource.CatalogID, params)

	connector, err := rds.cf.CreateConnectorInstance(ctx, catalog.ConnectorType, catalog.ConnectorCfg)
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

	switch resource.Category {
	case interfaces.ResourceCategoryTable:
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

		span.SetStatus(codes.Ok, "")
		return result.Entries, result.Total, nil

	case interfaces.ResourceCategoryIndex:
		indexConnector, ok := connector.(interfaces.IndexConnector)
		if !ok {
			httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InternalError_InvalidCategory).
				WithErrorDetails(fmt.Sprintf("connector %s does not support index operations", catalog.ConnectorType))
			otellog.LogError(ctx, "Connector does not support index operations", httpErr)
			return nil, 0, httpErr
		}

		result, err := indexConnector.ExecuteQuery(ctx, resource.SourceIdentifier, resource, params)
		if err != nil {
			otellog.LogError(ctx, "Execute query failed", err)
			if unsupported, ok := filter_condition.AsUnsupportedOperationError(err); ok {
				return nil, 0, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
					WithErrorDetails(unsupported.Error())
			}
			return nil, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError).
				WithErrorDetails(fmt.Sprintf("failed to execute query: %v", err))
		}

		span.SetStatus(codes.Ok, "")
		params.SearchAfter = append([]any(nil), result.SearchAfter...)
		return result.Entries, result.Total, nil

	case interfaces.ResourceCategoryFileset:
		fc, ok := connector.(interfaces.FilesetConnector)
		if !ok {
			httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InternalError_InvalidCategory).
				WithErrorDetails(fmt.Sprintf("connector %s does not support fileset operations", catalog.ConnectorType))
			otellog.LogError(ctx, "Connector does not support fileset operations", httpErr)
			return nil, 0, httpErr
		}

		// Use ExecuteQuery to obtain the list of files
		result, err := fc.ExecuteQuery(ctx, resource, params)
		if err != nil {
			otellog.LogError(ctx, "Fileset query failed", err)
			// The same typing as the table/index branches: The unimplemented operators of anyshare are problems on the request side
			// The caller can pass by simply changing the operator. If everything is uniformly packaged as 500, ontology-query will be judged as a dependency fault.
			if unsupported, ok := filter_condition.AsUnsupportedOperationError(err); ok {
				return nil, 0, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
					WithErrorDetails(unsupported.Error())
			}
			return nil, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError).
				WithErrorDetails(err.Error())
		}

		span.SetStatus(codes.Ok, "")
		return result.Entries, result.Total, nil

	default:
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InternalError_InvalidCategory).
			WithErrorDetails(connector.GetCategory())
		otellog.LogError(ctx, "Connector does not support table operations", httpErr)
		return nil, 0, httpErr
	}

}

// prepareSortParams prepares sort parameters to only include fields defined in resource SchemaDefinition
func (rds *resourceDataService) prepareSortParams(resource *interfaces.Resource, params *interfaces.ResourceDataQueryParams) *interfaces.ResourceDataQueryParams {
	if resource == nil || params == nil {
		return params
	}

	// Create a field map for quick lookup
	fieldMap := make(map[string]bool)
	for _, prop := range resource.SchemaDefinition {
		fieldMap[prop.Name] = true
	}

	// Add aggregation alias to field map if aggregation is present
	if params.Aggregation != nil && params.Aggregation.Alias != "" {
		fieldMap[params.Aggregation.Alias] = true
	}
	// Add __value to field map for aggregation queries
	if params.Aggregation != nil {
		fieldMap["__value"] = true
	}
	// Add GROUP BY fields to field map for aggregation queries
	if params.GroupBy != nil {
		for _, groupByItem := range params.GroupBy {
			fieldMap[groupByItem.Property] = true
		}
	}

	filteredParams := params

	// Filter Sort fields to only include fields defined in SchemaDefinition
	if params.Sort != nil {
		filteredSort := []*interfaces.SortField{}
		for _, sortField := range params.Sort {
			if fieldMap[sortField.Field] {
				filteredSort = append(filteredSort, sortField)
			}
		}
		filteredParams.Sort = filteredSort
	}

	return filteredParams
}

// prepareOutputFieldsParams filters output fields to only include fields defined in resource SchemaDefinition.
func (rds *resourceDataService) prepareOutputFieldsParams(resource *interfaces.Resource, params *interfaces.ResourceDataQueryParams) *interfaces.ResourceDataQueryParams {
	if resource == nil || params == nil || len(params.OutputFields) == 0 {
		return params
	}

	fieldMap := make(map[string]bool, len(resource.SchemaDefinition))
	for _, prop := range resource.SchemaDefinition {
		if prop == nil || prop.Name == "" {
			continue
		}
		fieldMap[prop.Name] = true
	}

	filteredOutputFields := make([]string, 0, len(params.OutputFields))
	for _, field := range params.OutputFields {
		if fieldMap[field] || (field == "_score" && resource.Category == interfaces.ResourceCategoryIndex) {
			filteredOutputFields = append(filteredOutputFields, field)
		}
	}
	params.OutputFields = filteredOutputFields

	return params
}
