package auth

import (
	"context"
	"fmt"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/utils"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
)

// QueryBuilder - Provides a simpler API to use SelectListWithAuthBatch.
// Usage example:
// result, err := NewQueryBuilder[ModelType, *ModelType](ctx, authService, accessor, resourceType, operations).
//
//	SetPage(page, pageSize).
//	SetAll(all).
//	SetQueryFunctions(queryTotal, queryBatch).
//	SetFilteredQueryFunctions(queryTotalWithIDs, queryBatchWithIDs).
//	Execute()
//
// New version features:
// 1. Automatically select query strategy: use batch IN query when the permission ID is small, and use incremental pull when the permission ID is large.
// 2. Ensure the accuracy of sorting and paging.
// 3. Avoid database IN query restriction problems.
// 4. Support cross-database compatibility.
// Parameter explanation:
// ctx: context object, used to cancel operations and pass context information.
// page: current page number (starting from 1)
// pageSize: amount of data per page.
// all: Whether to query all data (without paging)
// queryTotalFunc: function to query the total amount of data.
// queryBatchFunc: function to query data by page.
// queryBatchWithIDsFunc: A function to query data in pages based on the permission ID list (supports IN query)
// queryTotalWithIDsFunc: function to query the total amount of data based on the permission ID list.
// resourceListFunc: function to obtain the list of resource IDs to which the user has permissions.
type QueryBuilder[T any, PT interfaces.PtrBizIdentifiable[T]] struct {
	page                           int
	pageSize                       int
	all                            bool
	queryTotalFunc                 QueryTotalFunc
	queryBatchFunc                 QueryBatchFunc[T, PT]
	queryTotalWithIDsFunc          QueryTotalWithIDsFunc
	queryBatchWithIDsFunc          QueryBatchWithIDsFunc[T, PT]
	resourceListFunc               ResourceListFunc
	businessDomainResourceListFunc BusinessDomainResourceListFunc
}

// QueryTotalFunc is a function that queries the total amount of data.
type QueryTotalFunc func(ctx context.Context) (int64, error)

// QueryBatchFunc function to query data by page.
type QueryBatchFunc[T any, PT interfaces.PtrBizIdentifiable[T]] func(ctx context.Context, pageSize, offset int, cursorValue *T) ([]PT, error)

// QueryTotalWithIDsFunc is a function that queries the total amount of data based on the permission ID list.
type QueryTotalWithIDsFunc func(ctx context.Context, ids []string) (int64, error)

// QueryBatchWithIDsFunc is a function for paging query data based on the permission ID list.
type QueryBatchWithIDsFunc[T any, PT interfaces.PtrBizIdentifiable[T]] func(ctx context.Context, pageSize, offset int, ids []string, cursorValue *T) ([]PT, error)

// ResourceListFunc is a function that obtains the list of resource IDs that the user has permissions for.
type ResourceListFunc func(ctx context.Context) ([]string, error)

// BusinessDomainResourceListFunc Function type to obtain business domain resource ID list.
type BusinessDomainResourceListFunc func(ctx context.Context) ([]string, error)

// NewQueryBuilder creates a new query builder.
func NewQueryBuilder[T any, PT interfaces.PtrBizIdentifiable[T]]() *QueryBuilder[T, PT] {
	return &QueryBuilder[T, PT]{
		page:     1,                          // Default first page.
		pageSize: interfaces.DefaultPageSize, // Default page size.
		all:      false,                      // No paging by default.
	}
}

// SetPage sets paging parameters.
func (b *QueryBuilder[T, PT]) SetPage(page, pageSize int) *QueryBuilder[T, PT] {
	if page > 0 {
		b.page = page
	}
	if pageSize > 0 {
		b.pageSize = pageSize
	}
	return b
}

// SetAll sets whether to return all data.
func (b *QueryBuilder[T, PT]) SetAll(all bool) *QueryBuilder[T, PT] {
	b.all = all
	return b
}

// SetQueryFunctions sets basic query functions.
func (b *QueryBuilder[T, PT]) SetQueryFunctions(
	queryTotal QueryTotalFunc,
	queryBatch QueryBatchFunc[T, PT],
) *QueryBuilder[T, PT] {
	b.queryTotalFunc = queryTotal
	b.queryBatchFunc = queryBatch
	return b
}

// SetAuthFilter sets the permission filtering function.
func (b *QueryBuilder[T, PT]) SetAuthFilter(resourceListFunc ResourceListFunc) *QueryBuilder[T, PT] {
	b.resourceListFunc = resourceListFunc
	return b
}

// SetBusinessDomainFilter sets the business domain resource filtering function.
func (b *QueryBuilder[T, PT]) SetBusinessDomainFilter(businessDomainResourceListFunc BusinessDomainResourceListFunc) *QueryBuilder[T, PT] {
	b.businessDomainResourceListFunc = businessDomainResourceListFunc
	return b
}

// SetFilteredQueryFunctions sets query functions with permission filtering.
func (b *QueryBuilder[T, PT]) SetFilteredQueryFunctions(
	queryTotalWithIDs QueryTotalWithIDsFunc,
	queryBatchWithIDs QueryBatchWithIDsFunc[T, PT],
) *QueryBuilder[T, PT] {
	b.queryTotalWithIDsFunc = queryTotalWithIDs
	b.queryBatchWithIDsFunc = queryBatchWithIDs
	return b
}

// Execute execution permission query.
func (b *QueryBuilder[T, PT]) Execute(ctx context.Context) (*interfaces.QueryResponse[T], error) {
	// Parameter validation.
	if b.queryTotalFunc == nil || b.queryBatchFunc == nil {
		return nil, fmt.Errorf("queryTotalFunc and queryBatchFunc are required")
	}
	// Call SelectListWithAuthBatchWithThresholds to execute the query.
	return b.SelectListWithAuthBatchWithThresholds(ctx)
}

// getFilteredResourceIDs Gets a list of resource IDs that have been double filtered by business domain and permissions.
// Business domain filtering is the first layer, and authority filtering is the second layer.
// Core principle: The business domain is the first layer of filtering restrictions. Even if you have permission to access all resources, if there are no results in the business domain, an empty list must be returned.
func (b *QueryBuilder[T, PT]) getFilteredResourceIDs(ctx context.Context) ([]string, bool, error) {
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, nil)

	var (
		businessDomainIDs      []string
		authorizedIDs          []string
		hasFullAccess          bool
		businessDomainBypassed bool
	)

	// Serial call to business domain resource list function.
	if b.businessDomainResourceListFunc != nil {
		ids, err := b.businessDomainResourceListFunc(ctx)
		if err != nil {
			return nil, false, err
		}
		businessDomainIDs = ids
		for _, id := range ids {
			if id == interfaces.ResourceIDAll {
				businessDomainBypassed = true
				break
			}
		}
	}

	// Early termination strategy: If the business domain does not return any resource ID, an empty list will be returned directly.
	// This is the core embodiment of the first layer of filtering. Regardless of the permissions, when the business domain is empty, empty will be returned directly.
	if len(businessDomainIDs) == 0 && b.businessDomainResourceListFunc != nil && !businessDomainBypassed {
		return []string{}, false, nil
	}

	// Serial call to permission resource list function.
	if b.resourceListFunc != nil {
		ids, err := b.resourceListFunc(ctx)
		if err != nil {
			return nil, false, err
		}
		authorizedIDs = ids
		// Check if you have full permissions.
		for _, id := range ids {
			if id == interfaces.ResourceIDAll {
				hasFullAccess = true
				break
			}
		}
	} else {
		// If no permission filtering function is set, all permissions will be granted by default.
		hasFullAccess = true
	}

	// Calculate the filtered list of resource IDs.
	var filteredIDs []string

	if hasFullAccess {
		// If you have access to all resources, only business domain filtering is applied.
		// The business domain serves as the first layer of filtering. Even if you have permission, you can only access resources within the business domain.
		if businessDomainBypassed {
			return nil, true, nil
		}
		filteredIDs = businessDomainIDs
	} else {
		// In the case of limited permissions, calculate the intersection of business domains and permissions.
		// The utils.CalculateIntersection function internally handles the case of empty lists.
		if businessDomainBypassed {
			filteredIDs = authorizedIDs
		} else {
			filteredIDs = utils.CalculateIntersection(businessDomainIDs, authorizedIDs)
		}
	}

	return filteredIDs, false, nil
}

// SelectListWithAuthBatchWithThresholds permission query function with threshold parameters.
func (b *QueryBuilder[T, PT]) SelectListWithAuthBatchWithThresholds(ctx context.Context) (resp *interfaces.QueryResponse[T], err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	// Set default parameters.
	if b.page <= 0 {
		b.page = 1
	}
	if b.pageSize <= 0 {
		b.pageSize = 10
	}

	// Use a unified method to obtain a resource ID list that is double filtered by business domain and permissions.
	filteredIDs, hasFullAccess, err := b.getFilteredResourceIDs(ctx)
	if err != nil {
		return nil, err
	}
	// 2. Select different query strategies based on permission types.
	if hasFullAccess {
		// With full permissions, use paging query directly.
		totalCount, err := b.queryTotalFunc(ctx)
		if err != nil {
			return nil, err
		}
		if b.all {
			// Need to obtain all authorized data.
			var allData []PT
			queryTimes := totalCount / int64(b.pageSize)
			if totalCount%int64(b.pageSize) != 0 {
				queryTimes++
			}
			var processTimes int64
			var cursorValue *T
			for processTimes <= queryTimes {
				// Load a batch of data.
				var batchData []PT
				batchData, err = b.queryBatchFunc(ctx, b.pageSize, 0, cursorValue)
				if err != nil {
					return nil, err
				}

				if len(batchData) == 0 {
					break // no more data.
				}
				allData = append(allData, batchData...)
				cursorValue = allData[len(allData)-1]
				processTimes++
			}
			// Construct response data.
			pageData := make([]*T, len(allData))
			for i, item := range allData {
				pageData[i] = (*T)(item)
			}

			return &interfaces.QueryResponse[T]{
				Data: pageData,
				CommonPageResult: interfaces.CommonPageResult{
					TotalCount: len(pageData),
					Page:       b.page,
					PageSize:   b.pageSize,
					TotalPage:  1,
					HasNext:    false,
					HasPrev:    false,
				},
			}, nil
		}

		// Calculate pagination.
		totalPages := int((totalCount + int64(b.pageSize) - 1) / int64(b.pageSize))
		hasNext := b.page < totalPages
		hasPrev := b.page > 1

		// Calculate offsets and limits.
		offset := (b.page - 1) * b.pageSize
		var data []PT
		data, err = b.queryBatchFunc(ctx, b.pageSize, offset, nil)
		if err != nil {
			return nil, err
		}

		// Construct response data.
		pageData := make([]*T, len(data))
		for i, item := range data {
			pageData[i] = (*T)(item)
		}

		return &interfaces.QueryResponse[T]{
			Data: pageData,
			CommonPageResult: interfaces.CommonPageResult{
				TotalCount: int(totalCount),
				Page:       b.page,
				PageSize:   b.pageSize,
				TotalPage:  totalPages,
				HasNext:    hasNext,
				HasPrev:    hasPrev,
			},
		}, nil
	}

	// 3. Limited authority situation.
	// Returns an empty result when there is no permission ID.
	if len(filteredIDs) == 0 {
		return &interfaces.QueryResponse[T]{
			Data: []*T{},
			CommonPageResult: interfaces.CommonPageResult{
				TotalCount: 0,
				Page:       b.page,
				PageSize:   b.pageSize,
				TotalPage:  0,
				HasNext:    false,
				HasPrev:    false,
			},
		}, nil
	}

	// Select query strategy based on the number of permission IDs.
	if len(filteredIDs) <= MaxInQuerySize {
		// The number of permission IDs is small, use batch IN query.
		return b.selectListWithBatchInQueryWithThresholds(ctx, filteredIDs)
	} else {
		// If the number of permission IDs is large, use incremental pull.
		return b.selectListWithIncrementalFetch(ctx, filteredIDs)
	}
}

// selectListWithBatchInQueryWithThresholds Batch IN query with threshold parameters.
func (b *QueryBuilder[T, PT]) selectListWithBatchInQueryWithThresholds(ctx context.Context, filteredIDs []string) (resp *interfaces.QueryResponse[T], err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	// If a database filter function is provided, use it directly.
	if b.queryBatchWithIDsFunc != nil && b.queryTotalWithIDsFunc != nil {
		// Query the total number of authorized data.
		authorizedTotalCount, err := b.queryTotalWithIDsFunc(ctx, filteredIDs)
		if err != nil {
			return nil, err
		}

		if b.all {
			// Need to obtain all authorized data.
			var allData []PT
			allData, err = b.queryBatchWithIDsFunc(ctx, int(authorizedTotalCount), 0, filteredIDs, nil)
			if err != nil {
				return nil, err
			}

			// Construct response data.
			pageData := make([]*T, len(allData))
			for i, item := range allData {
				pageData[i] = (*T)(item)
			}

			return &interfaces.QueryResponse[T]{
				Data: pageData,
				CommonPageResult: interfaces.CommonPageResult{
					TotalCount: len(allData),
					Page:       1,
					PageSize:   len(allData),
					TotalPage:  1,
					HasNext:    false,
					HasPrev:    false,
				},
			}, nil
		}

		// Query limited permission data by pagination, calculate pagination.
		totalPages := int((authorizedTotalCount + int64(b.pageSize) - 1) / int64(b.pageSize))
		hasNext := b.page < totalPages
		hasPrev := b.page > 1

		// If the requested page number is out of range, empty data is returned.
		if b.page > totalPages {
			return &interfaces.QueryResponse[T]{
				Data: []*T{},
				CommonPageResult: interfaces.CommonPageResult{
					TotalCount: int(authorizedTotalCount),
					Page:       b.page,
					PageSize:   b.pageSize,
					TotalPage:  totalPages,
					HasNext:    false,
					HasPrev:    true,
				},
			}, nil
		}

		// Calculate offsets and limits.
		offset := (b.page - 1) * b.pageSize

		// Query the data of the specified page, the database layer will ensure the sorting accuracy.
		pageDataList, err := b.queryBatchWithIDsFunc(ctx, b.pageSize, offset, filteredIDs, nil)
		if err != nil {
			return nil, err
		}

		// Construct response data.
		pageData := make([]*T, len(pageDataList))
		for i, item := range pageDataList {
			pageData[i] = (*T)(item)
		}

		return &interfaces.QueryResponse[T]{
			Data: pageData,
			CommonPageResult: interfaces.CommonPageResult{
				TotalCount: int(authorizedTotalCount),
				Page:       b.page,
				PageSize:   b.pageSize,
				TotalPage:  totalPages,
				HasNext:    hasNext,
				HasPrev:    hasPrev,
			},
		}, nil
	}

	// If no database filter function is provided, in order to ensure global sorting consistency, incremental pull is used instead.
	return b.selectListWithIncrementalFetch(ctx, filteredIDs)
}

// selectListWithIncrementalFetch uses incremental pull for permission filtering.
// Suitable for situations where the number of permission IDs is large (>MaxInQuerySize)
func (b *QueryBuilder[T, PT]) selectListWithIncrementalFetch(ctx context.Context, filteredIDs []string) (resp *interfaces.QueryResponse[T], err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	// Build permission mapping for in-memory filtering.
	authMap := make(map[string]bool, len(filteredIDs))
	for _, id := range filteredIDs {
		authMap[id] = true
	}

	if b.all {
		// Need to obtain all authorized data.
		var totalCount int64
		totalCount, err = b.queryTotalFunc(ctx)
		if err != nil {
			return nil, err
		}

		// Load all data in batches and filter.
		filteredData := make([]PT, 0, interfaces.MaxQuerySize)
		queryTimes := totalCount / int64(interfaces.MaxQuerySize)
		if totalCount%int64(interfaces.MaxQuerySize) != 0 {
			queryTimes++
		}
		var processTimes int64
		var cursorValue *T
		for processTimes <= queryTimes {
			// Load a batch of data.
			var batchData []PT
			batchData, err = b.queryBatchFunc(ctx, interfaces.MaxQuerySize, 0, cursorValue)
			if err != nil {
				return nil, err
			}

			if len(batchData) == 0 {
				break // no more data.
			}
			// Filter out authorized data.
			for _, item := range batchData {
				if item != nil && authMap[item.GetBizID()] {
					filteredData = append(filteredData, item)
				}
			}
			cursorValue = batchData[len(batchData)-1]
			processTimes++

			// Check if the context has been canceled.
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}
		}

		// Construct response data.
		pageData := make([]*T, len(filteredData))
		for i, item := range filteredData {
			pageData[i] = (*T)(item)
		}

		return &interfaces.QueryResponse[T]{
			Data: pageData,
			CommonPageResult: interfaces.CommonPageResult{
				TotalCount: len(filteredData),
				Page:       1,
				PageSize:   len(filteredData),
				TotalPage:  1,
				HasNext:    false,
				HasPrev:    false,
			},
		}, nil
	}

	// Query limited permission data by page.
	targetStart := (b.page - 1) * b.pageSize
	targetEnd := targetStart + b.pageSize
	var pageData []*T

	// For efficient paging, we need to find enough data with permissions.
	// offset := 0
	foundCount := 0 // Number of authorized data found.

	// First get the total number of records in order to calculate the total number of pages.
	allTotalCount, err := b.queryTotalFunc(ctx)
	if err != nil {
		return nil, err
	}

	// Load data in batches until enough authorized data is found or all data is processed.
	queryTimes := allTotalCount / int64(interfaces.MaxQuerySize)
	if allTotalCount%int64(interfaces.MaxQuerySize) != 0 {
		queryTimes++
	}
	var cursorValue *T
	var processTimes int64
	for processTimes <= queryTimes && foundCount < targetEnd {
		// Load a batch of data.
		batchData, err := b.queryBatchFunc(ctx, interfaces.MaxQuerySize, 0, cursorValue)
		if err != nil {
			return nil, err
		}
		if len(batchData) == 0 {
			break // no more data.
		}
		// Filter and log authorized data.
		for i := range batchData {
			item := batchData[i]
			if item != nil && authMap[item.GetBizID()] {
				if foundCount >= targetStart && foundCount < targetEnd {
					// Within the target range, join the result set.
					t := (*T)(item)
					pageData = append(pageData, t)
				}
				foundCount++
			}
			batchData[i] = nil
			cursorValue = (*T)(item)
		}
		processTimes++
		// Check if the context has been canceled.
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
	}

	// Calculate paging information.
	totalPages := (foundCount + b.pageSize - 1) / b.pageSize
	hasNext := b.page < totalPages
	hasPrev := b.page > 1

	return &interfaces.QueryResponse[T]{
		Data: pageData,
		CommonPageResult: interfaces.CommonPageResult{
			TotalCount: foundCount,
			Page:       b.page,
			PageSize:   b.pageSize,
			TotalPage:  totalPages,
			HasNext:    hasNext,
			HasPrev:    hasPrev,
		},
	}, nil
}
