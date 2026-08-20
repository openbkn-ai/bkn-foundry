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
	"net/url"
	"os"
	"time"

	"github.com/bytedance/sonic"

	"vega-backend/common"
	"vega-backend/interfaces"
)

// bkn-safe authz cutover (revertible via AUTHZ_PROVIDER):
//   - unset / "isf" : ISF PermissionAccess unchanged (default)
//   - "shadow"      : ISF authoritative + bkn-safe queried in parallel, diffs logged
//   - "bkn-safe"    : bkn-safe authoritative (full adapter)
// BKN_SAFE_URL points at bkn-safe. Flip the env to revert; ISF impl untouched.

// safeClient talks to bkn-safe's clean authz API (/api/safe/v1/authz/*).
type safeClient struct {
	baseURL string
	http    *http.Client
}

func newSafeClient(baseURL string) *safeClient {
	return &safeClient{baseURL: baseURL, http: &http.Client{Timeout: 5 * time.Second}}
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

// accessibleIDs returns the concrete resource ids of rtype the accessor may
// perform op on (type-wide "*" grants are excluded by bkn-safe; the caller
// detects those via a separate obj="*" check). One bulk round-trip.
func (c *safeClient) accessibleIDs(ctx context.Context, accessorID, rtype, op string) ([]string, error) {
	q := url.Values{}
	q.Set("accessor_id", accessorID)
	q.Set("resource_type", rtype)
	q.Set("operation", op)
	var out struct {
		IDs []string `json:"ids"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/safe/v1/authz/resources?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return out.IDs, nil
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
	b, _ := sonic.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range common.BuildTraceHeadersForChildOperation(ctx, "permission.shadow", 1) {
		req.Header.Set(key, value)
	}
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

// opAccess is the authorization resolution result of a single operation under a certain resource type: either it holds type-level wildcard authorization
// (Covering all instances of this type), or a specific collection of accessible ids.
type opAccess struct {
	all bool
	ids map[string]bool
}

// resolveOps batch resolves the authorization of each candidate operation under a certain resource type: first, use a check with obj="*"
// Determine type-level/over-pipe matching (if hit, this op covers all instances and no further collection is required); otherwise, proceed to bkn-safe
// Take the set of accessible ids of this (accessor, type, operation).
//
// The number of round trips is only related to the "operand" and has nothing to do with the number of resources to be filtered - this is precisely why large directories no longer time out
// (#357: In the original implementation of resource-by-resource authentication, accounts with full authorization had approximately 5.6k resources, which led to a timeout of over 40 seconds.)
func (s *safePermissionAccess) resolveOps(ctx context.Context, accessorID, rtype string, ops []string) (map[string]opAccess, error) {
	out := make(map[string]opAccess, len(ops))
	for _, op := range ops {
		wild, err := s.safe.checkOne(ctx, accessorID, rtype, "*", op)
		if err != nil {
			return nil, err
		}
		if wild {
			out[op] = opAccess{all: true}
			continue
		}
		ids, err := s.safe.accessibleIDs(ctx, accessorID, rtype, op)
		if err != nil {
			return nil, err
		}
		set := make(map[string]bool, len(ids))
		for _, id := range ids {
			set[id] = true
		}
		out[op] = opAccess{ids: set}
	}
	return out, nil
}

// resolveFilter parses each op authorization of each resource type that appears in the filter for the caller to store in memory
// Determine each resource one by one to avoid initiating authentication requests resource by resource.
func (s *safePermissionAccess) resolveFilter(ctx context.Context,
	filter interfaces.PermissionResourcesFilter) (map[string]map[string]opAccess, error) {

	byType := make(map[string]map[string]opAccess)
	for _, r := range filter.Resources {
		if _, done := byType[r.Type]; done {
			continue
		}
		access, err := s.resolveOps(ctx, filter.Accessor.ID, r.Type, filterOps(filter))
		if err != nil {
			return nil, err
		}
		byType[r.Type] = access
	}
	return byType, nil
}

// filterOps is every operation the answer has to know about: the ones that
// decide visibility, plus the ones the answer reports on. They are separate
// axes — a resource is listed because the caller may view it, while the
// operations travelling back with it are what the caller may then do — so
// resolving only the first leaves the second empty and every button dark.
func filterOps(filter interfaces.PermissionResourcesFilter) []string {
	out := make([]string, 0, len(filter.Operations)+len(filter.CandidateOperations))
	seen := make(map[string]bool, cap(out))
	for _, group := range [][]string{filter.Operations, filter.CandidateOperations} {
		for _, op := range group {
			if seen[op] {
				continue
			}
			seen[op] = true
			out = append(out, op)
		}
	}
	return out
}

// reportOps is the set the answer names back. Callers that state no candidates
// get the visibility operations, which is what they asked about.
func reportOps(filter interfaces.PermissionResourcesFilter) []string {
	if len(filter.CandidateOperations) > 0 {
		return filter.CandidateOperations
	}
	return filter.Operations
}

// allowedFrom selects the actual operations held on the resource from each op authorization that has been parsed.
func allowedFrom(access map[string]opAccess, ops []string, id string) []string {
	out := make([]string, 0, len(ops))
	for _, op := range ops {
		if a := access[op]; a.all || a.ids[id] {
			out = append(out, op)
		}
	}
	return out
}

func (s *safePermissionAccess) FilterResources(ctx context.Context, filter interfaces.PermissionResourcesFilter) (map[string]interfaces.PermissionResourceOps, error) {
	byType, err := s.resolveFilter(ctx, filter)
	if err != nil {
		return nil, err
	}
	out := map[string]interfaces.PermissionResourceOps{}
	for _, r := range filter.Resources {
		// Visibility and reporting are decided separately: the first says whether
		// the resource belongs in the answer at all, the second says what comes
		// back with it.
		if visible := allowedFrom(byType[r.Type], filter.Operations, r.ID); len(visible) > 0 {
			out[r.ID] = interfaces.PermissionResourceOps{
				ResourceID: r.ID,
				Operations: allowedFrom(byType[r.Type], reportOps(filter), r.ID),
			}
		}
	}
	return out, nil
}

func (s *safePermissionAccess) GetResourcesOperations(ctx context.Context, filter interfaces.PermissionResourcesFilter) (map[string]interfaces.PermissionResourceOps, error) {
	byType, err := s.resolveFilter(ctx, filter)
	if err != nil {
		return nil, err
	}
	out := map[string]interfaces.PermissionResourceOps{}
	for _, r := range filter.Resources {
		out[r.ID] = interfaces.PermissionResourceOps{
			ResourceID: r.ID,
			Operations: allowedFrom(byType[r.Type], reportOps(filter), r.ID),
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

// MaybeShadow applies the AUTHZ_PROVIDER switch. Default/unknown => ISF (inner).
func MaybeShadow(inner interfaces.PermissionAccess) interfaces.PermissionAccess {
	provider := os.Getenv("AUTHZ_PROVIDER")
	if provider == "" || provider == "isf" {
		return inner
	}
	url := os.Getenv("BKN_SAFE_URL")
	if url == "" {
		log.Printf("[authz] AUTHZ_PROVIDER=%s but BKN_SAFE_URL empty; using ISF", provider)
		return inner
	}
	sc := newSafeClient(url)
	switch provider {
	case "bkn-safe":
		log.Printf("[authz] provider=bkn-safe (authoritative) at %s", url)
		return &safePermissionAccess{safe: sc}
	case "shadow":
		log.Printf("[authz] provider=shadow; ISF authoritative, comparing bkn-safe at %s", url)
		return &shadowPermissionAccess{PermissionAccess: inner, safe: sc}
	default:
		log.Printf("[authz] unknown AUTHZ_PROVIDER=%s; using ISF", provider)
		return inner
	}
}
