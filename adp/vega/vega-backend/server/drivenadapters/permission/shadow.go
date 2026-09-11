// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package permission

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/bytedance/sonic"

	"vega-backend/common"
	"vega-backend/interfaces"
)

// bkn-safe authz cutover (selected by AUTHZ_PROVIDER):
//   - "bkn-safe" : bkn-safe authoritative (full adapter)
//   - "shadow"   : ISF authoritative + bkn-safe queried in parallel, diffs logged
//   - "isf"      : ISF PermissionAccess unchanged; retired, kept as an escape hatch
// "bkn-safe" and "shadow" both need BKN_SAFE_URL. A misspelled value is a
// misconfiguration and refuses to start; an unset value still falls back to ISF
// but says so loudly, because existing deployments carry an explicit empty value
// in their own values overrides and an upgrade must not CrashLoopBackOff.

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

// allowedOps returns the subset of candidate ops the accessor may perform.
func (c *safeClient) allowedOps(ctx context.Context, accessorID, rtype, rid string, cands []string) ([]string, error) {
	out := make([]string, 0, len(cands))
	for _, op := range cands {
		ok, err := c.checkOne(ctx, accessorID, rtype, rid, op)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, op)
		}
	}
	return out, nil
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

// ---- shadow wrapper: ISF authoritative, bkn-safe diff-logged ----

type shadowPermissionAccess struct {
	interfaces.PermissionAccess
	safe *safeClient
}

func (s *shadowPermissionAccess) CheckPermission(ctx context.Context, check interfaces.PermissionCheck) (bool, error) {
	isfOK, isfErr := s.PermissionAccess.CheckPermission(ctx, check)
	safeOK, safeErr := s.safe.allowedAll(ctx, check.Accessor.ID, check.Resource.Type, check.Resource.ID, check.Operations)
	switch {
	case safeErr != nil:
		log.Printf("[authz-shadow] bkn-safe error (ISF authoritative): %s:%s ops=%v err=%v", check.Resource.Type, check.Resource.ID, check.Operations, safeErr)
	case isfErr == nil && isfOK != safeOK:
		log.Printf("[authz-shadow] DIFF: accessor=%s %s:%s ops=%v isf=%v bkn-safe=%v", check.Accessor.ID, check.Resource.Type, check.Resource.ID, check.Operations, isfOK, safeOK)
	}
	return isfOK, isfErr
}

// ---- full bkn-safe adapter: bkn-safe authoritative ----

type safePermissionAccess struct {
	safe *safeClient
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

// supportedAuthzProviders lists every accepted AUTHZ_PROVIDER value, in the
// order the error message should offer them.
var supportedAuthzProviders = []string{"bkn-safe", "shadow", "isf"}

// MaybeShadow applies the AUTHZ_PROVIDER switch.
//
// A misspelled provider, and "bkn-safe" without BKN_SAFE_URL, used to print one
// line and fall back to ISF. ISF is retired, so that fallback turned a typo into
// an authorization surface whose answers are unpredictable, and the single log
// line made it invisible at runtime. Both now report the misconfiguration and
// let the caller refuse to start.
//
// An unset provider keeps the old fallback, loudly: deployments upgraded with
// their own values override still carry an explicit empty value, and refusing to
// start would turn an upgrade into a CrashLoopBackOff. Flipping that to an error
// waits until those deployments are counted.
func MaybeShadow(inner interfaces.PermissionAccess) (interfaces.PermissionAccess, error) {
	provider := strings.TrimSpace(os.Getenv("AUTHZ_PROVIDER"))
	switch provider {
	case "":
		log.Printf("[authz] AUTHZ_PROVIDER is unset, so authorization falls back to the retired ISF; set it to bkn-safe")
		return inner, nil
	case "isf":
		log.Printf("[authz] provider=isf selects the retired authorization service; migrate to bkn-safe")
		return inner, nil
	case "bkn-safe", "shadow":
	default:
		return nil, fmt.Errorf("AUTHZ_PROVIDER=%q is not a supported authorization backend; set it to one of %s",
			provider, strings.Join(supportedAuthzProviders, ", "))
	}
	safeURL := strings.TrimSpace(os.Getenv("BKN_SAFE_URL"))
	if safeURL == "" {
		return nil, fmt.Errorf("AUTHZ_PROVIDER=%s requires BKN_SAFE_URL to be set", provider)
	}
	sc := newSafeClient(safeURL)
	if provider == "shadow" {
		log.Printf("[authz] provider=shadow; ISF authoritative, comparing bkn-safe at %s", safeURL) //nolint:gosec // URL is deployment configuration, not request input.
		return &shadowPermissionAccess{PermissionAccess: inner, safe: sc}, nil
	}
	log.Printf("[authz] provider=bkn-safe (authoritative) at %s", safeURL) //nolint:gosec // URL is deployment configuration, not request input.
	return &safePermissionAccess{safe: sc}, nil
}
