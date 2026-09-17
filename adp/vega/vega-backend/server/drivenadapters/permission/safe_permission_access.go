// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package permission

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/bytedance/sonic"

	"vega-backend/common"
	"vega-backend/interfaces"
)

// safeClient talks to bkn-safe's clean authz API (/api/safe/v1/authz/*).
type safeClient struct {
	baseURL string
	http    *http.Client
}

const safeHTTPTimeout = 30 * time.Second

func newSafeClient(baseURL string) *safeClient {
	return &safeClient{baseURL: baseURL, http: &http.Client{Timeout: safeHTTPTimeout}}
}

func uniqueStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func (c *safeClient) checkOne(ctx context.Context, accessorID, rtype, rid, op string) (bool, error) {
	return c.allowedAll(ctx, accessorID, rtype, rid, []string{op})
}

func (c *safeClient) allowedAll(ctx context.Context, accessorID, rtype, rid string, ops []string) (bool, error) {
	if len(ops) == 0 {
		return true, nil
	}
	checks := make([]map[string]any, 0, len(ops))
	for _, op := range ops {
		checks = append(checks, map[string]any{
			"resource":  map[string]string{"type": rtype, "id": rid},
			"operation": op,
		})
	}
	var out struct {
		Allowed bool `json:"allowed"`
	}
	err := c.do(ctx, http.MethodPost, "/api/safe/v1/authz/checks", map[string]any{
		"accessor_id": accessorID,
		"checks":      checks,
	}, &out)
	return out.Allowed, err
}

type safeFilteredResource struct {
	ResourceID string   `json:"resource_id"`
	Operations []string `json:"operations"`
}

// filterResources delegates the complete effective decision to bkn-safe. A
// result cannot be reconstructed from an independent wildcard probe and a list
// of concrete grants when an operation has same-resource prerequisites: the
// operation may be type-wide while its prerequisite is instance-specific.
func (c *safeClient) filterResources(ctx context.Context, accessorID string,
	resources []interfaces.PermissionResource, visibility []string, includeOperations bool) ([]safeFilteredResource, error) {
	var out struct {
		Resources *[]safeFilteredResource `json:"resources"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/safe/v1/authz/resource-filter", map[string]any{
		"accessor_id":           accessorID,
		"resources":             resources,
		"visibility_operations": visibility,
		"include_operations":    includeOperations,
	}, &out); err != nil {
		return nil, err
	}
	if out.Resources == nil {
		return nil, fmt.Errorf("bkn-safe resource-filter response is missing a non-null resources field")
	}
	return *out.Resources, nil
}

func (c *safeClient) do(ctx context.Context, method, path string, body, out any) error {
	b, _ := sonic.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range common.BuildTraceHeadersForChildOperation(ctx, "permission.safe", 1) {
		req.Header.Set(key, value)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read bkn-safe %s %s response: %w", method, path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("bkn-safe %s %s: %d: %s", method, path, resp.StatusCode, data)
	}
	if out != nil && len(data) > 0 {
		return sonic.Unmarshal(data, out)
	}
	return nil
}

type safePermissionAccess struct {
	safe *safeClient
}

// bkn-safe caps ResourceParent mutations at 1,000 items per request.
const resourceParentBatchSize = 1000

// NewPermissionAccess creates the bkn-safe authorization adapter from the
// validated application setting.
func NewPermissionAccess(appSetting *common.AppSetting) interfaces.PermissionAccess {
	return &safePermissionAccess{safe: newSafeClient(appSetting.BknSafeURL)}
}

func (s *safePermissionAccess) CheckPermission(ctx context.Context, check interfaces.PermissionCheck) (bool, error) {
	return s.safe.allowedAll(ctx, check.Accessor.ID, check.Resource.Type, check.Resource.ID, check.Operations)
}

func (s *safePermissionAccess) FilterResources(ctx context.Context, filter interfaces.PermissionResourcesFilter) (map[string]interfaces.PermissionResourceOps, error) {
	resources, err := s.safe.filterResources(ctx, filter.Accessor.ID, filter.Resources,
		filter.Operations, filter.AllowOperation)
	if err != nil {
		return nil, err
	}
	out := map[string]interfaces.PermissionResourceOps{}
	for _, r := range resources {
		out[r.ResourceID] = interfaces.PermissionResourceOps{
			ResourceID: r.ResourceID,
			Operations: r.Operations,
		}
	}
	return out, nil
}

func (s *safePermissionAccess) GetResourcesOperations(ctx context.Context, filter interfaces.PermissionResourcesFilter) (map[string]interfaces.PermissionResourceOps, error) {
	resources, err := s.safe.filterResources(ctx, filter.Accessor.ID, filter.Resources, nil, true)
	if err != nil {
		return nil, err
	}
	out := make(map[string]interfaces.PermissionResourceOps, len(filter.Resources))
	for _, r := range filter.Resources {
		out[r.ID] = interfaces.PermissionResourceOps{
			ResourceID: r.ID,
			Operations: []string{},
		}
	}
	for _, r := range resources {
		out[r.ResourceID] = interfaces.PermissionResourceOps{
			ResourceID: r.ResourceID,
			Operations: r.Operations,
		}
	}
	return out, nil
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
	for start := 0; start < len(items); start += resourceParentBatchSize {
		end := start + resourceParentBatchSize
		if end > len(items) {
			end = len(items)
		}
		if err := s.safe.do(ctx, http.MethodPut, "/api/safe/v1/authz/resource-parents", map[string]any{
			"resource_type": resourceType,
			"parent_type":   parentType,
			"items":         items[start:end],
		}, nil); err != nil {
			return err
		}
	}
	return nil
}

func (s *safePermissionAccess) DeleteResourceParents(ctx context.Context, resourceType string, resourceIDs []string) error {
	resourceIDs = uniqueStrings(resourceIDs)
	for start := 0; start < len(resourceIDs); start += resourceParentBatchSize {
		end := start + resourceParentBatchSize
		if end > len(resourceIDs) {
			end = len(resourceIDs)
		}
		if err := s.safe.do(ctx, http.MethodDelete, "/api/safe/v1/authz/resource-parents", map[string]any{
			"resource_type": resourceType,
			"resource_ids":  resourceIDs[start:end],
		}, nil); err != nil {
			return err
		}
	}
	return nil
}

// GetResourceParents reads only the supplied child IDs. The bkn-safe endpoint
// exposes one resource_id filter per request; this is used solely for Resources
// that the final filter did not return, so established parent edges never take
// the legacy Catalog fallback by mistake.
func (s *safePermissionAccess) GetResourceParents(ctx context.Context, resourceType string,
	resourceIDs []string) (map[string]interfaces.PermissionResourceParent, error) {
	result := make(map[string]interfaces.PermissionResourceParent, len(resourceIDs))
	for _, resourceID := range uniqueStrings(resourceIDs) {
		var response struct {
			Items []struct {
				ResourceID string `json:"resource_id"`
				ParentID   string `json:"parent_id"`
			} `json:"items"`
			Total int `json:"total"`
		}
		path := "/api/safe/v1/authz/resource-parents?resource_type=" + url.QueryEscape(resourceType) +
			"&resource_id=" + url.QueryEscape(resourceID)
		if err := s.safe.do(ctx, http.MethodGet, path, nil, &response); err != nil {
			return nil, err
		}
		if response.Total > 1 || len(response.Items) > 1 {
			return nil, fmt.Errorf("bkn-safe returned multiple parent edges for %s:%s", resourceType, resourceID)
		}
		if len(response.Items) == 0 {
			continue
		}
		item := response.Items[0]
		if item.ResourceID != resourceID || item.ParentID == "" {
			return nil, fmt.Errorf("bkn-safe returned invalid parent edge for %s:%s", resourceType, resourceID)
		}
		result[resourceID] = interfaces.PermissionResourceParent{ResourceID: resourceID, ParentID: item.ParentID}
	}
	return result, nil
}
