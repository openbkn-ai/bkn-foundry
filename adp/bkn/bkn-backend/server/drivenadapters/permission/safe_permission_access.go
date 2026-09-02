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

func (c *safeClient) checkOne(ctx context.Context, accessorID, rtype, rid, op string) (bool, error) {
	var out struct {
		Allowed bool `json:"allowed"`
	}
	err := c.do(ctx, http.MethodPost, "/api/safe/v1/authz/check", map[string]any{
		"accessor_id": accessorID,
		"resource":    map[string]string{"type": rtype, "id": rid},
		"operation":   op,
	}, &out)
	return out.Allowed, err
}

// safeResource is one { type, id } pair in the batch filter request.
type safeResource struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// filterResources runs one batched decision for a whole page: visibility decides
// which resources come back, candidates decide which operations each carries.
// One round trip regardless of resource or operation count — the per-resource,
// per-operation loop it replaces made list pages scale as N x M (#357).
func (c *safeClient) filterResources(ctx context.Context, accessorID string,
	resources []safeResource, visibility, candidates []string) (map[string][]string, error) {

	out := map[string][]string{}
	if len(resources) == 0 {
		return out, nil
	}
	var resp struct {
		Resources []struct {
			ResourceID string   `json:"resource_id"`
			Operations []string `json:"operations"`
		} `json:"resources"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/safe/v1/authz/resource-filter", map[string]any{
		"accessor_id":           accessorID,
		"resources":             resources,
		"visibility_operations": visibility,
		"candidate_operations":  candidates,
	}, &resp); err != nil {
		return nil, err
	}
	for _, r := range resp.Resources {
		out[r.ResourceID] = r.Operations
	}
	return out, nil
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

func (c *safeClient) allowedAll(ctx context.Context, accessorID, rtype, rid string, ops []string) (bool, error) {
	for _, op := range ops {
		ok, err := c.checkOne(ctx, accessorID, rtype, rid, op)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

func (c *safeClient) do(ctx context.Context, method, path string, body, out any) error {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("bkn-safe %s %s: %d: %s", method, path, resp.StatusCode, data)
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

// filterBatch is the shared body of FilterResources and GetResourcesOperations:
// both project the candidate operations onto a set of resources; they differ
// only in whether the visibility operations filter the result.
func (s *safePermissionAccess) filterBatch(ctx context.Context,
	filter interfaces.PermissionResourcesFilter, visibility []string) (map[string]interfaces.PermissionResourceOps, error) {

	resources := make([]safeResource, 0, len(filter.Resources))
	for _, r := range filter.Resources {
		resources = append(resources, safeResource{Type: r.Type, ID: r.ID})
	}
	// Callers that only pass a visibility list keep the old behaviour: the
	// operations they asked about are also the ones projected back.
	candidates := filter.CandidateOperations
	if len(candidates) == 0 {
		candidates = filter.Operations
	}
	ops, err := s.safe.filterResources(ctx, filter.Accessor.ID, resources, visibility, candidates)
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
	return s.filterBatch(ctx, filter, filter.Operations)
}

// GetResourcesOperations answers "what may I do on each of these", so no
// visibility filter — every requested resource comes back, possibly with an
// empty operation set.
func (s *safePermissionAccess) GetResourcesOperations(ctx context.Context, filter interfaces.PermissionResourcesFilter) (map[string]interfaces.PermissionResourceOps, error) {
	return s.filterBatch(ctx, filter, nil)
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
