// Package auth provides authorization service.
package auth

import (
	"context"
	"net/http"

	oerrors "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
)

const (
	// Permission query related constants.
	MaxInQuerySize   = 1000 // IN queries the maximum number of parameters to avoid database limitations.
	InQueryBatchSize = 200  // Batch size of batch IN query.
)

// CheckCreatePermission Check new permissions.
func (s *authServiceImpl) CheckCreatePermission(ctx context.Context, accessor *interfaces.AuthAccessor, resourceType interfaces.AuthResourceType) error {
	authorized, err := s.OperationCheckAll(ctx, accessor, interfaces.ResourceIDAll, resourceType, interfaces.AuthOperationTypeCreate)
	if err != nil {
		return err
	}
	if !authorized {
		return oerrors.NewHTTPError(ctx, http.StatusForbidden, oerrors.ErrExtCommonAddForbidden, nil)
	}
	return nil
}

// CheckAdminPermission checks super-administrative permissions.
// Determine the reuse of bkn-safe’s safe_admin:console:manage capability bit—that is, the semantics of Enforcer.CanAdmin—.
// Therefore, the execution factory and bkn-safe always have the same determination of "over-management", and there is no need to hardcode the administrator account in this service.
// Used to protect operation and maintenance observation interfaces that return cross-tenant data and are not affiliated with any single business resource.
func (s *authServiceImpl) CheckAdminPermission(ctx context.Context, accessor *interfaces.AuthAccessor) error {
	authorized, err := s.OperationCheckAll(ctx, accessor,
		interfaces.SafeAdminConsoleResourceID,
		interfaces.AuthResourceTypeSafeAdmin,
		interfaces.AuthOperationTypeManage)
	if err != nil {
		return err
	}
	if !authorized {
		return oerrors.NewHTTPError(ctx, http.StatusForbidden, oerrors.ErrExtCommonViewForbidden, nil)
	}
	return nil
}

// CheckModifyPermission Check editing permissions.
func (s *authServiceImpl) CheckModifyPermission(ctx context.Context, accessor *interfaces.AuthAccessor, resourceID string, resourceType interfaces.AuthResourceType) error {
	authorized, err := s.OperationCheckAll(ctx, accessor, resourceID, resourceType, interfaces.AuthOperationTypeModify)
	if err != nil {
		return err
	}
	if !authorized {
		return oerrors.NewHTTPError(ctx, http.StatusForbidden, oerrors.ErrExtCommonEditForbidden, nil)
	}
	return nil
}

// CheckViewPermission Check view permission.
func (s *authServiceImpl) CheckViewPermission(ctx context.Context, accessor *interfaces.AuthAccessor, resourceID string, resourceType interfaces.AuthResourceType) error {
	authorized, err := s.OperationCheckAll(ctx, accessor, resourceID, resourceType, interfaces.AuthOperationTypeView)
	if err != nil {
		return err
	}
	if !authorized {
		return oerrors.NewHTTPError(ctx, http.StatusForbidden, oerrors.ErrExtCommonViewForbidden, nil)
	}
	return nil
}

// CheckDeletePermission Check delete permission.
func (s *authServiceImpl) CheckDeletePermission(ctx context.Context, accessor *interfaces.AuthAccessor, resourceID string, resourceType interfaces.AuthResourceType) error {
	authorized, err := s.OperationCheckAll(ctx, accessor, resourceID, resourceType, interfaces.AuthOperationTypeDelete)
	if err != nil {
		return err
	}
	if !authorized {
		return oerrors.NewHTTPError(ctx, http.StatusForbidden, oerrors.ErrExtCommonDeleteForbidden, nil)
	}
	return nil
}

// CheckPublishPermission Check publishing permission.
func (s *authServiceImpl) CheckPublishPermission(ctx context.Context, accessor *interfaces.AuthAccessor, resourceID string, resourceType interfaces.AuthResourceType) error {
	authorized, err := s.OperationCheckAll(ctx, accessor, resourceID, resourceType, interfaces.AuthOperationTypePublish)
	if err != nil {
		return err
	}
	if !authorized {
		return oerrors.NewHTTPError(ctx, http.StatusForbidden, oerrors.ErrExtCommonPublishForbidden, nil)
	}
	return nil
}

// CheckUnpublishPermission Check the removal permission.
func (s *authServiceImpl) CheckUnpublishPermission(ctx context.Context, accessor *interfaces.AuthAccessor, resourceID string, resourceType interfaces.AuthResourceType) error {
	authorized, err := s.OperationCheckAll(ctx, accessor, resourceID, resourceType, interfaces.AuthOperationTypeUnpublish)
	if err != nil {
		return err
	}
	if !authorized {
		return oerrors.NewHTTPError(ctx, http.StatusForbidden, oerrors.ErrExtCommonUnpublishForbidden, nil)
	}
	return nil
}

// CheckAuthorizePermission Check permission management permissions.
func (s *authServiceImpl) CheckAuthorizePermission(ctx context.Context, accessor *interfaces.AuthAccessor, resourceID string, resourceType interfaces.AuthResourceType) error {
	authorized, err := s.OperationCheckAll(ctx, accessor, resourceID, resourceType, interfaces.AuthOperationTypeAuthorize)
	if err != nil {
		return err
	}
	if !authorized {
		return oerrors.NewHTTPError(ctx, http.StatusForbidden, oerrors.ErrExtCommonPermissionForbidden, nil)
	}
	return nil
}

// CheckPublicAccessPermission Check public access permissions.
func (s *authServiceImpl) CheckPublicAccessPermission(ctx context.Context, accessor *interfaces.AuthAccessor, resourceID string, resourceType interfaces.AuthResourceType) error {
	authorized, err := s.OperationCheckAll(ctx, accessor, resourceID, resourceType, interfaces.AuthOperationTypePublicAccess)
	if err != nil {
		return err
	}
	if !authorized {
		return oerrors.NewHTTPError(ctx, http.StatusForbidden, oerrors.ErrExtCommonPublicAccessForbidden, nil)
	}
	return nil
}

// CheckExecutePermission Check usage permissions.
func (s *authServiceImpl) CheckExecutePermission(ctx context.Context, accessor *interfaces.AuthAccessor, resourceID string, resourceType interfaces.AuthResourceType) error {
	authorized, err := s.OperationCheckAll(ctx, accessor, resourceID, resourceType, interfaces.AuthOperationTypeExecute)
	if err != nil {
		return err
	}
	if !authorized {
		return oerrors.NewHTTPError(ctx, http.StatusForbidden, oerrors.ErrExtCommonUseForbidden, nil)
	}
	return nil
}

// MultiCheckOperationPermission Multi-operation permission check.
func (s *authServiceImpl) MultiCheckOperationPermission(ctx context.Context, accessor *interfaces.AuthAccessor, resourceID string,
	resourceType interfaces.AuthResourceType, operations ...interfaces.AuthOperationType) error {
	authorized, err := s.OperationCheckAll(ctx, accessor, resourceID, resourceType, operations...)
	if err != nil {
		return err
	}
	if !authorized {
		return oerrors.NewHTTPError(ctx, http.StatusForbidden, oerrors.ErrExtCommonOperationForbidden, nil)
	}
	return nil
}

// OperationCheckAll Check operation permissions.
func (s *authServiceImpl) OperationCheckAll(
	ctx context.Context,
	accessor *interfaces.AuthAccessor,
	resourceID string,
	resourceType interfaces.AuthResourceType,
	operations ...interfaces.AuthOperationType,
) (bool, error) {
	req := &interfaces.AuthOperationCheckRequest{
		Accessor: accessor,
		Resource: &interfaces.AuthResource{
			ID:   resourceID,
			Type: string(resourceType),
		},
		Operation: operations,
		Method:    interfaces.AuthMethodGet,
	}
	resp, err := s.authorization.OperationCheck(ctx, req)
	if err != nil {
		err := oerrors.NewHTTPError(ctx, http.StatusForbidden, oerrors.ErrExtCommonOperationForbidden, err.Error())
		return false, err
	}
	return resp.Result, nil
}

// OperationCheckAny checks operation permissions.
func (s *authServiceImpl) OperationCheckAny(
	ctx context.Context,
	accessor *interfaces.AuthAccessor,
	resourceID string,
	resourceType interfaces.AuthResourceType,
	operations ...interfaces.AuthOperationType,
) (bool, error) {
	for _, operation := range operations {
		authorized, err := s.OperationCheckAll(ctx, accessor, resourceID, resourceType, operation)
		if err != nil {
			return false, err
		}
		if authorized {
			return true, nil
		}
	}
	return false, nil
}

// ResourceFilterIDs resource filtering.
func (s *authServiceImpl) ResourceFilterIDs(
	ctx context.Context,
	accessor *interfaces.AuthAccessor,
	resourceIDS []string,
	resourceType interfaces.AuthResourceType,
	operations ...interfaces.AuthOperationType,
) ([]string, error) {
	req := &interfaces.AuthResourceFilterRequest{
		Accessor:   accessor,
		Resources:  []*interfaces.AuthResource{},
		Operations: operations,
		Method:     interfaces.AuthMethodGet,
	}

	for _, resourceID := range resourceIDS {
		req.Resources = append(req.Resources, &interfaces.AuthResource{
			ID:   resourceID,
			Type: string(resourceType),
		})
	}

	resp, err := s.authorization.ResourceFilter(ctx, req)
	if err != nil {
		return nil, err
	}
	resourceIDs := make([]string, 0, len(resp))
	for _, resource := range resp {
		resourceIDs = append(resourceIDs, resource.ID)
	}
	return resourceIDs, nil
}

// ResourceListIDs Get the resource list.
func (s *authServiceImpl) ResourceListIDs(
	ctx context.Context,
	accessor *interfaces.AuthAccessor,
	resourceType interfaces.AuthResourceType,
	operations ...interfaces.AuthOperationType,
) ([]string, error) {
	req := &interfaces.ResourceListRequest{
		Accessor: accessor,
		Resource: &interfaces.AuthResource{
			Type: string(resourceType),
		},
		Method:    interfaces.AuthMethodGet,
		Operation: operations,
	}

	resp, err := s.authorization.ResourceList(ctx, req)
	if err != nil {
		return nil, err
	}
	resourceIDs := make([]string, 0, len(resp))
	for _, resource := range resp {
		resourceIDs = append(resourceIDs, resource.ID)
	}
	return resourceIDs, nil
}

// SelectListWithAuth queries the list and performs permission check (independent generic function) -- full filtering.
func SelectListWithAuth[T any, PT interfaces.PtrBizIdentifiable[T]](ctx context.Context,
	page int,
	pageSize int,
	all bool,
	queryOption interfaces.QueryOption[T, PT],
	resourceListFunc interfaces.ResourceListFunc,
) (resp *interfaces.QueryResponse[T], err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	// 1. Execute a query to get all data.
	allData, err := queryOption()
	if err != nil {
		return nil, err
	}

	if allData == nil {
		return &interfaces.QueryResponse[T]{
			Data: []*T{},
			CommonPageResult: interfaces.CommonPageResult{
				TotalCount: 0,
				Page:       page,
				PageSize:   pageSize,
				TotalPage:  0,
				HasNext:    false,
				HasPrev:    false,
			},
		}, nil
	}

	// 2. Get the list of resource IDs that the user has permissions for.
	authorizedIDs, err := resourceListFunc()
	if err != nil {
		return nil, err
	}

	// 3. Permission filtering.
	var filteredData []PT
	if len(authorizedIDs) > 0 {
		authMap := make(map[string]bool, len(authorizedIDs))
		for _, id := range authorizedIDs {
			authMap[id] = true
		}

		if authMap[interfaces.ResourceIDAll] {
			filteredData = allData
		} else {
			for _, item := range allData {
				if item != nil && authMap[item.GetBizID()] {
					filteredData = append(filteredData, item)
				}
			}
		}
	}

	// 4. Paging processing.
	totalCount := len(filteredData)

	var pageData []*T
	if all {
		pageData = make([]*T, totalCount)
		for i, item := range filteredData[:totalCount] {
			pageData[i] = (*T)(item)
		}
		return &interfaces.QueryResponse[T]{
			Data: pageData,
			CommonPageResult: interfaces.CommonPageResult{
				TotalCount: totalCount,
				Page:       1,
				PageSize:   totalCount,
				TotalPage:  1,
				HasNext:    false,
				HasPrev:    false,
			},
		}, nil
	}

	// Set default paging parameters.
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 10
	}

	// Calculate pagination.
	totalPages := (totalCount + pageSize - 1) / pageSize
	hasNext := page < totalPages
	hasPrev := page > 1

	// Calculate data slice range.
	startIndex := (page - 1) * pageSize
	endIndex := startIndex + pageSize

	if startIndex < totalCount {
		if endIndex > totalCount {
			endIndex = totalCount
		}
		sliceData := filteredData[startIndex:endIndex]
		pageData = make([]*T, len(sliceData))
		for i, item := range sliceData {
			pageData[i] = (*T)(item)
		}
	}

	resp = &interfaces.QueryResponse[T]{
		Data: pageData,
		CommonPageResult: interfaces.CommonPageResult{
			TotalCount: totalCount,
			Page:       page,
			PageSize:   pageSize,
			TotalPage:  totalPages,
			HasNext:    hasNext,
			HasPrev:    hasPrev,
		},
	}
	return
}
