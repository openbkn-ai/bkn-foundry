// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package permission

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"bkn-backend/interfaces"
)

// safeClient talks to bkn-safe's clean authz API (/api/safe/v1/authz/*).
type safeClient struct {
	baseURL string
	http    *http.Client
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
	var response interfaces.PermissionResourceScope
	query := url.Values{}
	query.Set("accessor_id", accessorID)
	query.Set("resource_type", resourceType)
	query.Set("operation", operation)
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
		return fmt.Errorf("bkn-safe %s %s returned status %d", method, path, resp.StatusCode)
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
