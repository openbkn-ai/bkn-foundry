// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package permission

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// safeClient talks to bkn-safe's clean authz API (/api/safe/v1/authz/*).
type safeClient struct {
	baseURL string
	http    *http.Client
}

// safeHTTPStatusError preserves the response status as structured data so
// callers never need to inspect a potentially sensitive dependency error
// string to decide whether a compatibility fallback is available.
type safeHTTPStatusError struct {
	method     string
	path       string
	statusCode int
}

func (e *safeHTTPStatusError) Error() string {
	return fmt.Sprintf("bkn-safe %s %s returned status %d", e.method, e.path, e.statusCode)
}

// concreteResourceOperations is used only when talking to an older bkn-safe
// that predates the any_operation query flag. Create is deliberately absent:
// it is a type-level capability and never identifies an existing resource.
var concreteResourceOperations = []string{
	interfaces.OPERATION_TYPE_VIEW_DETAIL,
	interfaces.OPERATION_TYPE_MODIFY,
	interfaces.OPERATION_TYPE_DELETE,
	interfaces.OPERATION_TYPE_QUERY_DATA,
	interfaces.OPERATION_TYPE_AUTHORIZE,
	interfaces.OPERATION_TYPE_EXECUTE,
	interfaces.OPERATION_TYPE_FULL_BUSINESS_ACCESS,
}

func newSafeClient(baseURL string) *safeClient {
	return &safeClient{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		http:    &http.Client{Timeout: 5 * time.Second},
	}
}

func (c *safeClient) allowedAll(ctx context.Context, accessorID, rtype, rid string, ops []string) (bool, error) {
	if len(ops) == 0 {
		return true, nil
	}
	checks := make([]interfaces.PermissionRequirement, 0, len(ops))
	for _, op := range ops {
		checks = append(checks, interfaces.PermissionRequirement{
			Resource:  interfaces.PermissionResource{Type: rtype, ID: rid},
			Operation: op,
		})
	}
	out, err := c.checkPermissions(ctx, interfaces.PermissionChecksRequest{AccessorID: accessorID, Checks: checks})
	if err != nil {
		return false, err
	}
	return out.Allowed, nil
}

func (c *safeClient) checkPermissions(ctx context.Context,
	request interfaces.PermissionChecksRequest) (interfaces.PermissionChecksResponse, error) {

	var response interfaces.PermissionChecksResponse
	if len(request.Checks) == 0 {
		response.Allowed = true
		response.Results = []interfaces.PermissionCheckResult{}
		return response, nil
	}
	if err := c.do(ctx, http.MethodPost, "/api/safe/v1/authz/checks", request, &response); err != nil {
		return response, err
	}
	if response.Results == nil || len(response.Results) != len(request.Checks) {
		return response, fmt.Errorf("invalid bkn-safe checks response")
	}
	allAllowed := true
	for index, check := range request.Checks {
		result := response.Results[index]
		if result.ResourceType != check.Resource.Type || result.ResourceID != check.Resource.ID ||
			result.Operation != check.Operation {
			return response, fmt.Errorf("invalid bkn-safe checks response")
		}
		allAllowed = allAllowed && result.Allowed
	}
	if response.Allowed != allAllowed {
		return response, fmt.Errorf("invalid bkn-safe checks response")
	}
	return response, nil
}

// safeResource is one { type, id } pair in the batch filter request.
type safeResource struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// filterResources runs one batched decision for a whole page: visibility decides
// which resources come back. When requested, bkn-safe returns complete
// effective operations for each returned resource.
// One round trip regardless of resource or operation count — the per-resource,
// per-operation loop it replaces made list pages scale as N x M (#357).
func (c *safeClient) filterResources(ctx context.Context, accessorID string,
	resources []safeResource, visibility []string, includeOperations bool) (map[string][]string, error) {

	out := map[string][]string{}
	if len(resources) == 0 {
		return out, nil
	}
	var resp struct {
		Resources *[]struct {
			ResourceID string   `json:"resource_id"`
			Operations []string `json:"operations"`
		} `json:"resources"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/safe/v1/authz/resource-filter", map[string]any{
		"accessor_id":           accessorID,
		"resources":             resources,
		"visibility_operations": visibility,
		"include_operations":    includeOperations,
	}, &resp); err != nil {
		return nil, err
	}
	if resp.Resources == nil {
		return nil, fmt.Errorf("invalid bkn-safe resource-filter response")
	}
	for _, r := range *resp.Resources {
		out[r.ResourceID] = r.Operations
	}
	return out, nil
}

func (c *safeClient) listAccessibleResources(ctx context.Context, accessorID, resourceType,
	operation string) (interfaces.PermissionResourceScope, error) {
	return c.listAccessibleResourceScope(ctx, accessorID, resourceType, operation, false)
}

func (c *safeClient) listAccessibleResourcesWithAnyOperation(ctx context.Context, accessorID,
	resourceType string) (interfaces.PermissionResourceScope, error) {
	scope, err := c.listAccessibleResourceScope(ctx, accessorID, resourceType, "", true)
	if err == nil || !isUnsupportedAnyOperation(err) {
		return scope, err
	}

	// bkn-safe 0.2.0 accepts the same resource listing endpoint but not its
	// any_operation flag. Preserve the OR semantics by joining the concrete
	// scopes instead of silently reducing visibility to view_detail.
	return c.listAccessibleResourcesByConcreteOperations(ctx, accessorID, resourceType)
}

func isUnsupportedAnyOperation(err error) bool {
	var statusErr *safeHTTPStatusError
	return errors.As(err, &statusErr) && statusErr.statusCode == http.StatusBadRequest
}

func (c *safeClient) listAccessibleResourcesByConcreteOperations(ctx context.Context, accessorID,
	resourceType string) (interfaces.PermissionResourceScope, error) {
	seen := make(map[string]struct{})
	result := interfaces.PermissionResourceScope{ResourceIDs: []string{}}
	for _, operation := range concreteResourceOperations {
		scope, err := c.listAccessibleResourceScope(ctx, accessorID, resourceType, operation, false)
		if err != nil {
			return interfaces.PermissionResourceScope{}, err
		}
		if scope.Unrestricted {
			return scope, nil
		}
		result.RequiresCandidateFilter = result.RequiresCandidateFilter || scope.RequiresCandidateFilter
		for _, resourceID := range scope.ResourceIDs {
			if _, exists := seen[resourceID]; exists {
				continue
			}
			seen[resourceID] = struct{}{}
			result.ResourceIDs = append(result.ResourceIDs, resourceID)
		}
	}
	return result, nil
}

func (c *safeClient) listAccessibleResourceScope(ctx context.Context, accessorID, resourceType,
	operation string, anyOperation bool) (interfaces.PermissionResourceScope, error) {
	var response interfaces.PermissionResourceScope
	query := url.Values{}
	query.Set("accessor_id", accessorID)
	query.Set("resource_type", resourceType)
	if anyOperation {
		query.Set("any_operation", "true")
	} else {
		query.Set("operation", operation)
	}
	if err := c.do(ctx, http.MethodGet, "/api/safe/v1/authz/resources?"+query.Encode(), nil, &response); err != nil {
		return response, err
	}
	if response.ResourceIDs == nil {
		return response, fmt.Errorf("invalid bkn-safe resources response")
	}
	return response, nil
}

func (c *safeClient) upsertResourceParents(ctx context.Context, resourceType, parentType string,
	items []interfaces.PermissionResourceParent) error {
	if len(items) == 0 {
		return nil
	}
	return c.do(ctx, http.MethodPut, "/api/safe/v1/authz/resource-parents", map[string]any{
		"resource_type": resourceType,
		"parent_type":   parentType,
		"items":         items,
	}, nil)
}

func (c *safeClient) deleteResourceParents(ctx context.Context, resourceType string, resourceIDs []string) error {
	if len(resourceIDs) == 0 {
		return nil
	}
	return c.do(ctx, http.MethodDelete, "/api/safe/v1/authz/resource-parents", map[string]any{
		"resource_type": resourceType,
		"resource_ids":  resourceIDs,
	}, nil)
}

func (c *safeClient) do(ctx context.Context, method, path string, body, out any) error {
	var requestBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		requestBody = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, requestBody)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read bkn-safe response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &safeHTTPStatusError{method: method, path: path, statusCode: resp.StatusCode}
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

type safePermissionAccess struct {
	safe *safeClient
}

// NewPermissionAccess creates the bkn-safe authorization adapter.
func NewPermissionAccess(baseURL string) interfaces.PermissionAccess {
	return &safePermissionAccess{safe: newSafeClient(baseURL)}
}

func (s *safePermissionAccess) CheckPermission(ctx context.Context, check interfaces.PermissionCheck) (bool, error) {
	return s.safe.allowedAll(ctx, check.Accessor.ID, check.Resource.Type, check.Resource.ID, check.Operations)
}

func (s *safePermissionAccess) CheckPermissions(ctx context.Context,
	request interfaces.PermissionChecksRequest) (interfaces.PermissionChecksResponse, error) {
	return s.safe.checkPermissions(ctx, request)
}

func (s *safePermissionAccess) ResolvePropertyLevels(ctx context.Context,
	request interfaces.PropertyLevelsRequest) (interfaces.PropertyLevelsResponse, error) {
	var response interfaces.PropertyLevelsResponse
	if err := s.safe.do(ctx, http.MethodPost, "/api/safe/v1/authz/property-levels", request, &response); err != nil {
		return response, err
	}
	if response.Entries == nil {
		return response, fmt.Errorf("invalid bkn-safe property-levels response")
	}
	return response, nil
}

func (s *safePermissionAccess) ResolveRowFilters(ctx context.Context,
	request interfaces.RowFiltersRequest) (interfaces.RowFiltersResponse, error) {
	var response interfaces.RowFiltersResponse
	if err := s.safe.do(ctx, http.MethodPost, "/api/safe/v1/authz/row-filters", request, &response); err != nil {
		return response, err
	}
	if response.Entries == nil {
		return response, fmt.Errorf("invalid bkn-safe row-filters response")
	}
	return response, nil
}

// filterBatch is the shared body of FilterResources.
func (s *safePermissionAccess) filterBatch(ctx context.Context,
	filter interfaces.PermissionResourcesFilter, visibility []string,
	includeOperations bool) (map[string]interfaces.PermissionResourceOps, error) {

	resources := make([]safeResource, 0, len(filter.Resources))
	for _, r := range filter.Resources {
		resources = append(resources, safeResource{Type: r.Type, ID: r.ID})
	}
	ops, err := s.safe.filterResources(ctx, filter.Accessor.ID, resources, visibility, includeOperations)
	if err != nil {
		return nil, err
	}
	out := make(map[string]interfaces.PermissionResourceOps, len(ops))
	for id, allowed := range ops {
		out[id] = interfaces.PermissionResourceOps{ResourceID: id, Operations: allowed}
	}
	return out, nil
}

func (s *safePermissionAccess) FilterResources(ctx context.Context, filter interfaces.PermissionResourcesFilter) (map[string]interfaces.PermissionResourceOps, error) {
	return s.filterBatch(ctx, filter, filter.Operations, filter.IncludeOperations)
}

func (s *safePermissionAccess) ListAccessibleResources(ctx context.Context, accessor interfaces.PermissionAccessor,
	resourceType, operation string) (interfaces.PermissionResourceScope, error) {
	return s.safe.listAccessibleResources(ctx, accessor.ID, resourceType, operation)
}

func (s *safePermissionAccess) ListAccessibleResourcesWithAnyOperation(ctx context.Context,
	accessor interfaces.PermissionAccessor, resourceType string) (interfaces.PermissionResourceScope, error) {
	return s.safe.listAccessibleResourcesWithAnyOperation(ctx, accessor.ID, resourceType)
}

func (s *safePermissionAccess) CreateResources(ctx context.Context, policies []interfaces.PermissionPolicy) error {
	for _, p := range policies {
		ops := make([]string, 0, len(p.Operations.Allow))
		for _, a := range p.Operations.Allow {
			ops = append(ops, a.Operation)
		}
		if err := s.safe.do(ctx, http.MethodPost, "/api/safe/v1/authz/policies", map[string]any{
			"accessor_id": p.Accessor.ID,
			"resource":    map[string]string{"type": p.Resource.Type, "id": p.Resource.ID},
			"operations":  ops,
		}, nil); err != nil {
			return err
		}
	}
	return nil
}

func (s *safePermissionAccess) DeleteResources(ctx context.Context, resources []interfaces.PermissionResource) error {
	for _, r := range resources {
		if err := s.safe.do(ctx, http.MethodDelete, "/api/safe/v1/authz/policies", map[string]any{
			"resource": map[string]string{"type": r.Type, "id": r.ID},
		}, nil); err != nil {
			return err
		}
	}
	return nil
}

func (s *safePermissionAccess) UpsertResourceParents(ctx context.Context, resourceType, parentType string,
	items []interfaces.PermissionResourceParent) error {
	return s.safe.upsertResourceParents(ctx, resourceType, parentType, items)
}

func (s *safePermissionAccess) DeleteResourceParents(ctx context.Context, resourceType string, resourceIDs []string) error {
	return s.safe.deleteResourceParents(ctx, resourceType, resourceIDs)
}
