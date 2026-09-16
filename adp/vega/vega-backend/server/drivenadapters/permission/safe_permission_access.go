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

func newSafeClient(baseURL string) *safeClient {
	return &safeClient{baseURL: baseURL, http: &http.Client{Timeout: 5 * time.Second}}
}

type localCheckRequest struct {
	AccessorID      string                        `json:"accessor_id"`
	Resource        interfaces.PermissionResource `json:"resource"`
	Operation       string                        `json:"operation"`
	EvaluationScope string                        `json:"evaluation_scope"`
}

type localCheckResponse struct {
	Allowed           *bool                              `json:"allowed"`
	EvaluationScope   string                             `json:"evaluation_scope"`
	Decision          interfaces.PermissionDecision      `json:"decision"`
	Basis             interfaces.PermissionDecisionBasis `json:"basis"`
	Requires          []string                           `json:"requires"`
	DeniedRequirement string                             `json:"denied_requirement"`
	RequirementBasis  interfaces.PermissionDecisionBasis `json:"requirement_basis"`
}

type localFilterRequest struct {
	AccessorID           string   `json:"accessor_id"`
	ResourceType         string   `json:"resource_type"`
	ResourceIDs          []string `json:"resource_ids"`
	VisibilityOperations []string `json:"visibility_operations"`
	CandidateOperations  []string `json:"candidate_operations"`
	EvaluationScope      string   `json:"evaluation_scope"`
}

type localFilterResponse struct {
	Resources *[]localFilterResource `json:"resources"`
}

type localFilterResource struct {
	ResourceType string                                   `json:"resource_type"`
	ResourceID   string                                   `json:"resource_id"`
	Decisions    []interfaces.PermissionOperationDecision `json:"decisions"`
}

func (c *safeClient) localDecision(ctx context.Context, check interfaces.LocalPermissionCheck) (interfaces.PermissionOperationDecision, error) {
	var out localCheckResponse
	err := c.doLocal(ctx, http.MethodPost, "/api/safe/v1/authz/check", localCheckRequest{
		AccessorID: check.Accessor.ID, Resource: check.Resource, Operation: check.Operation,
		EvaluationScope: "local",
	}, &out)
	if err != nil {
		return interfaces.PermissionOperationDecision{}, err
	}
	if out.Allowed == nil {
		return interfaces.PermissionOperationDecision{}, fmt.Errorf("bkn-safe local check omitted the required allowed field")
	}
	allowed := *out.Allowed
	decision := interfaces.PermissionOperationDecision{
		Operation: check.Operation, Decision: out.Decision, Basis: out.Basis,
		Requires: out.Requires, DeniedRequirement: out.DeniedRequirement,
		RequirementBasis: out.RequirementBasis,
	}
	// bkn-safe keeps its effective compatibility response for an unknown or
	// disabled account: {"allowed":false}. Preserve that as an account-level
	// refusal rather than fabricating a local none decision or reporting 500.
	if !allowed && out.EvaluationScope == "" && out.Decision == "" && out.Basis == "" {
		return interfaces.PermissionOperationDecision{}, interfaces.ErrPermissionAccountNotActive
	}
	if out.EvaluationScope != "local" {
		return interfaces.PermissionOperationDecision{}, fmt.Errorf("bkn-safe local check returned evaluation_scope %q", out.EvaluationScope)
	}
	if err := validateLocalDecision(decision); err != nil {
		return interfaces.PermissionOperationDecision{}, err
	}
	if allowed != decision.Allowed() {
		return interfaces.PermissionOperationDecision{}, fmt.Errorf("bkn-safe local check returned inconsistent allowed and decision values")
	}
	return decision, nil
}

func (c *safeClient) localResourceDecisions(ctx context.Context,
	filter interfaces.LocalPermissionFilter) (map[string]map[string]interfaces.PermissionOperationDecision, error) {

	ids := uniqueStrings(filter.ResourceIDs)
	operations := uniqueStrings(filter.Operations)
	if len(ids) == 0 || len(operations) == 0 {
		return map[string]map[string]interfaces.PermissionOperationDecision{}, nil
	}
	var out localFilterResponse
	err := c.doLocal(ctx, http.MethodPost, "/api/safe/v1/authz/resource-filter", localFilterRequest{
		AccessorID: filter.Accessor.ID, ResourceType: filter.ResourceType, ResourceIDs: ids,
		VisibilityOperations: []string{}, CandidateOperations: operations, EvaluationScope: "local",
	}, &out)
	if err != nil {
		return nil, err
	}
	if out.Resources == nil {
		return nil, fmt.Errorf("bkn-safe local filter omitted the required resources field")
	}
	resources := *out.Resources
	// With non-empty input, an empty result is bkn-safe's compatibility response
	// for an unknown or disabled account. A partial result remains a protocol
	// error below so omitted resources cannot be confused with deny or none.
	if len(resources) == 0 {
		return nil, interfaces.ErrPermissionAccountNotActive
	}
	expectedIDs := make(map[string]bool, len(ids))
	for _, id := range ids {
		expectedIDs[id] = true
	}
	result := make(map[string]map[string]interfaces.PermissionOperationDecision, len(ids))
	for _, resource := range resources {
		if resource.ResourceType != filter.ResourceType || !expectedIDs[resource.ResourceID] {
			return nil, fmt.Errorf("bkn-safe local filter returned unexpected resource %s:%s", resource.ResourceType, resource.ResourceID)
		}
		if _, duplicate := result[resource.ResourceID]; duplicate {
			return nil, fmt.Errorf("bkn-safe local filter returned duplicate resource %s:%s", resource.ResourceType, resource.ResourceID)
		}
		decisions := make(map[string]interfaces.PermissionOperationDecision, len(resource.Decisions))
		for _, decision := range resource.Decisions {
			if _, duplicate := decisions[decision.Operation]; duplicate {
				return nil, fmt.Errorf("bkn-safe local filter returned duplicate decision for %s:%s operation %s",
					resource.ResourceType, resource.ResourceID, decision.Operation)
			}
			if err := validateLocalDecision(decision); err != nil {
				return nil, err
			}
			decisions[decision.Operation] = decision
		}
		for _, operation := range operations {
			decision, ok := decisions[operation]
			if !ok {
				return nil, fmt.Errorf("bkn-safe local filter omitted %s:%s operation %s",
					resource.ResourceType, resource.ResourceID, operation)
			}
			for _, requirement := range decision.Requires {
				if _, ok := decisions[requirement]; !ok {
					return nil, fmt.Errorf("bkn-safe local filter omitted %s:%s required operation %s",
						resource.ResourceType, resource.ResourceID, requirement)
				}
			}
		}
		result[resource.ResourceID] = decisions
	}
	for _, id := range ids {
		if _, ok := result[id]; !ok {
			return nil, fmt.Errorf("bkn-safe local filter omitted requested resource %s:%s", filter.ResourceType, id)
		}
	}
	return result, nil
}

func validateLocalDecision(decision interfaces.PermissionOperationDecision) error {
	if decision.Operation == "" {
		return fmt.Errorf("bkn-safe local decision omitted operation")
	}
	switch decision.Decision {
	case interfaces.PermissionDecisionAllow, interfaces.PermissionDecisionDeny:
		if decision.Basis != interfaces.PermissionBasisDirect &&
			decision.Basis != interfaces.PermissionBasisBundle &&
			decision.Basis != interfaces.PermissionBasisWildcard {
			return fmt.Errorf("bkn-safe local decision for operation %s returned invalid basis %q",
				decision.Operation, decision.Basis)
		}
	case interfaces.PermissionDecisionNone:
		if decision.Basis != interfaces.PermissionBasisNone {
			return fmt.Errorf("bkn-safe local none decision for operation %s returned basis %q",
				decision.Operation, decision.Basis)
		}
	default:
		return fmt.Errorf("bkn-safe local decision for operation %s returned invalid decision %q",
			decision.Operation, decision.Decision)
	}
	if decision.DeniedRequirement != "" || decision.RequirementBasis != "" {
		return fmt.Errorf("bkn-safe local decision for operation %s unexpectedly enforced requirements", decision.Operation)
	}
	return nil
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

type safeFilteredResource struct {
	ResourceID string   `json:"resource_id"`
	Operations []string `json:"operations"`
}

// filterResources delegates the complete effective decision to bkn-safe. A
// result cannot be reconstructed from an independent wildcard probe and a list
// of concrete grants when an operation has same-resource prerequisites: the
// operation may be type-wide while its prerequisite is instance-specific.
func (c *safeClient) filterResources(ctx context.Context, accessorID string,
	resources []interfaces.PermissionResource, visibility, candidates []string) ([]safeFilteredResource, error) {
	var out struct {
		Resources *[]safeFilteredResource `json:"resources"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/safe/v1/authz/resource-filter", map[string]any{
		"accessor_id":           accessorID,
		"resources":             resources,
		"visibility_operations": visibility,
		"candidate_operations":  candidates,
	}, &out); err != nil {
		return nil, err
	}
	if out.Resources == nil {
		return nil, fmt.Errorf("bkn-safe resource-filter response is missing a non-null resources field")
	}
	return *out.Resources, nil
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
	return c.doWithHeaders(ctx, method, path, body, out, nil, false)
}

func (c *safeClient) doLocal(ctx context.Context, method, path string, body, out any) error {
	return c.doWithHeaders(ctx, method, path, body, out, map[string]string{"x-caller-service": "vega"}, true)
}

func (c *safeClient) doWithHeaders(ctx context.Context, method, path string, body, out any,
	extraHeaders map[string]string, requireResponseBody bool) error {
	b, _ := sonic.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range common.BuildTraceHeadersForChildOperation(ctx, "permission.shadow", 1) {
		req.Header.Set(key, value)
	}
	for key, value := range extraHeaders {
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
	if requireResponseBody && out != nil && len(data) == 0 {
		return fmt.Errorf("bkn-safe %s %s returned an empty response body", method, path)
	}
	if out != nil && len(data) > 0 {
		return sonic.Unmarshal(data, out)
	}
	return nil
}

type safePermissionAccess struct {
	safe *safeClient
}

// NewPermissionAccess creates the bkn-safe authorization adapter from the
// validated application setting.
func NewPermissionAccess(appSetting *common.AppSetting) interfaces.PermissionAccess {
	return &safePermissionAccess{safe: newSafeClient(appSetting.BknSafeURL)}
}

func (s *safePermissionAccess) CheckPermission(ctx context.Context, check interfaces.PermissionCheck) (bool, error) {
	return s.safe.allowedAll(ctx, check.Accessor.ID, check.Resource.Type, check.Resource.ID, check.Operations)
}

func (s *safePermissionAccess) LocalDecision(ctx context.Context, check interfaces.LocalPermissionCheck) (interfaces.PermissionOperationDecision, error) {
	return s.safe.localDecision(ctx, check)
}

func (s *safePermissionAccess) LocalResourceDecisions(ctx context.Context,
	filter interfaces.LocalPermissionFilter) (map[string]map[string]interfaces.PermissionOperationDecision, error) {
	return s.safe.localResourceDecisions(ctx, filter)
}

// reportOps is the set the answer names back. Callers that state no candidates
// get the visibility operations, which is what they asked about.
func reportOps(filter interfaces.PermissionResourcesFilter) []string {
	if len(filter.CandidateOperations) > 0 {
		return filter.CandidateOperations
	}
	return filter.Operations
}

func (s *safePermissionAccess) FilterResources(ctx context.Context, filter interfaces.PermissionResourcesFilter) (map[string]interfaces.PermissionResourceOps, error) {
	resources, err := s.safe.filterResources(ctx, filter.Accessor.ID, filter.Resources,
		filter.Operations, reportOps(filter))
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
	resources, err := s.safe.filterResources(ctx, filter.Accessor.ID, filter.Resources,
		nil, reportOps(filter))
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
	if len(items) == 0 {
		return nil
	}
	return s.safe.do(ctx, http.MethodPut, "/api/safe/v1/authz/resource-parents", map[string]any{
		"resource_type": resourceType,
		"parent_type":   parentType,
		"items":         items,
	}, nil)
}

func (s *safePermissionAccess) DeleteResourceParents(ctx context.Context, resourceType string, resourceIDs []string) error {
	if len(resourceIDs) == 0 {
		return nil
	}
	return s.safe.do(ctx, http.MethodDelete, "/api/safe/v1/authz/resource-parents", map[string]any{
		"resource_type": resourceType,
		"resource_ids":  uniqueStrings(resourceIDs),
	}, nil)
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
