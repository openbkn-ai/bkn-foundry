package authzhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/domain/enum/cdaenum"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/domain/enum/cdapmsenum"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/drivenadapter/httpaccess/authzhttp/authzhttpreq"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/drivenadapter/httpaccess/authzhttp/authzhttpres"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/infra/cmp/icmp"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/infra/common/cenum"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/port/driven/ihttpaccess/iauthzacc"
)

// safeAuthZHttpAcc is the bkn-safe-AUTHORITATIVE implementation of AuthZHttpAcc
// (AUTHZ_PROVIDER=bkn-safe). It talks to bkn-safe's clean authz API instead of
// ISF, and is fully revertible: unset/flip AUTHZ_PROVIDER to drop back to ISF
// (or shadow). The decision/grant/delete/list paths map 1:1 onto bkn-safe; the
// few ISF-only surfaces degrade explicitly:
//   - ResourceList / GetCanUseAgentIDs : global enumeration — bkn-safe has no
//     "list all instance IDs" primitive; no production DA caller. Empty+warn.
//   - Grant*ForAppAdmin / SetResourceType : init-time setup already performed by
//     bkn-safe's seed (catalog + 应用管理员→agent/agent_tpl). No-op+debug.
//   - DenyAgentUsePmsForAllAccessor : bkn-safe is allow-only (deny dropped by
//     design, no DA caller). No-op+warn.
//
// Object/op string values match across systems (ResourceType "agent"/"agent_tpl",
// Operator "use" etc.), so string(...) is the bkn-safe key directly.
type safeAuthZHttpAcc struct {
	safeURL string
	http    *http.Client
	logger  icmp.Logger
}

var _ iauthzacc.AuthZHttpAcc = &safeAuthZHttpAcc{}

// neverExpire is bkn-safe's stand-in expiry (unix 0 = never), matching the
// sentinel DA's ListPolicyRes.FilterByExpiresAt treats as永不过期.
const neverExpire = "1970-01-01T08:00:00+08:00"

// ---- 1. 策略决策接口 ----

func (s *safeAuthZHttpAcc) OperationCheck(ctx context.Context, req *authzhttpreq.SingleCheckReq) (*authzhttpres.SingleCheckResult, error) {
	if req == nil || req.Accessor == nil || req.Resource == nil {
		return &authzhttpres.SingleCheckResult{Result: false}, nil
	}
	ok, err := s.checkAll(ctx, req.Accessor.ID, string(req.Resource.Type), req.Resource.ID, opStrings(req.Operation))
	if err != nil {
		return nil, err
	}
	return &authzhttpres.SingleCheckResult{Result: ok}, nil
}

func (s *safeAuthZHttpAcc) SingleAgentUseCheck(ctx context.Context, accessorID string, _ cenum.PmsTargetObjType, agentID string) (bool, error) {
	return s.checkOne(ctx, accessorID, string(cdaenum.ResourceTypeDataAgent), agentID, string(cdapmsenum.AgentUse))
}

// ResourceFilter keeps resources the accessor may perform ALL requested ops on
// (ISF resource-filter AND semantics).
func (s *safeAuthZHttpAcc) ResourceFilter(ctx context.Context, req *authzhttpreq.ResourceFilterReq) ([]*authzhttpres.ResourceListItem, error) {
	if req == nil || req.Accessor == nil {
		return nil, nil
	}
	ops := opStrings(req.Operation)
	out := make([]*authzhttpres.ResourceListItem, 0, len(req.Resources))
	for _, r := range req.Resources {
		if r == nil {
			continue
		}
		ok, err := s.checkAll(ctx, req.Accessor.ID, string(r.Type), r.ID, ops)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, &authzhttpres.ResourceListItem{ID: r.ID})
		}
	}
	return out, nil
}

// ResourceOperation returns, per resource, the ops the accessor may perform.
func (s *safeAuthZHttpAcc) ResourceOperation(ctx context.Context, req *authzhttpreq.ResourceOperationReq) ([]*authzhttpres.ResourceOperationItem, error) {
	if req == nil || req.Accessor == nil {
		return nil, nil
	}
	out := make([]*authzhttpres.ResourceOperationItem, 0, len(req.Resources))
	for _, r := range req.Resources {
		if r == nil {
			continue
		}
		allowed, err := s.allowedOps(ctx, req.Accessor.ID, string(r.Type), r.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, &authzhttpres.ResourceOperationItem{ID: r.ID, Operation: toOperators(allowed)})
	}
	return out, nil
}

func (s *safeAuthZHttpAcc) ResourceList(_ context.Context, _ *authzhttpreq.ResourceListReq) ([]*authzhttpres.ResourceListItem, error) {
	s.logger.Warnf("[authz] bkn-safe: ResourceList (global enumerate) unsupported; returning empty")
	return []*authzhttpres.ResourceListItem{}, nil
}

func (s *safeAuthZHttpAcc) GetCanUseAgentIDs(_ context.Context, _ string) ([]string, error) {
	s.logger.Warnf("[authz] bkn-safe: GetCanUseAgentIDs (global enumerate) unsupported; returning empty")
	return []string{}, nil
}

func (s *safeAuthZHttpAcc) FilterCanUseAgentIDs(ctx context.Context, uid string, agentIDs []string) ([]string, error) {
	out := make([]string, 0, len(agentIDs))
	for _, id := range agentIDs {
		ok, err := s.checkOne(ctx, uid, string(cdaenum.ResourceTypeDataAgent), id, string(cdapmsenum.AgentUse))
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, id)
		}
	}
	return out, nil
}

func (s *safeAuthZHttpAcc) FilterCanUseAgentIDMap(ctx context.Context, uid string, agentIDs []string) (map[string]struct{}, error) {
	ids, err := s.FilterCanUseAgentIDs(ctx, uid, agentIDs)
	if err != nil {
		return nil, err
	}
	m := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		m[id] = struct{}{}
	}
	return m, nil
}

func (s *safeAuthZHttpAcc) GetAgentResourceOpsByUid(ctx context.Context, uid string) (map[cdapmsenum.Operator]bool, error) {
	return s.typeOps(ctx, uid, cdaenum.ResourceTypeDataAgent)
}

func (s *safeAuthZHttpAcc) GetAgentTplResourceOpsByUid(ctx context.Context, uid string) (map[cdapmsenum.Operator]bool, error) {
	return s.typeOps(ctx, uid, cdaenum.ResourceTypeDataAgentTpl)
}

// typeOps returns the type-scope ops the accessor holds, querying the "*"
// instance pattern (matches role grants like agent:*, not per-instance ones).
func (s *safeAuthZHttpAcc) typeOps(ctx context.Context, uid string, rt cdaenum.ResourceType) (map[cdapmsenum.Operator]bool, error) {
	allowed, err := s.allowedOps(ctx, uid, string(rt), "*")
	if err != nil {
		return nil, err
	}
	m := make(map[cdapmsenum.Operator]bool, len(allowed))
	for _, op := range allowed {
		m[cdapmsenum.Operator(op)] = true
	}
	return m, nil
}

// ---- 2. 策略配置接口 ----

func (s *safeAuthZHttpAcc) CreatePolicy(ctx context.Context, reqs []*authzhttpreq.CreatePolicyReq) error {
	for _, req := range reqs {
		if req == nil || req.Accessor == nil || req.Resource == nil || req.Operation == nil {
			continue
		}
		if len(req.Operation.Deny) > 0 {
			s.logger.Warnf("[authz] bkn-safe is allow-only; ignoring %d deny op(s) on %s:%s", len(req.Operation.Deny), req.Resource.Type, req.Resource.ID)
		}
		ops := make([]string, 0, len(req.Operation.Allow))
		for _, a := range req.Operation.Allow {
			ops = append(ops, string(a.ID))
		}
		if len(ops) == 0 {
			continue
		}
		if err := s.grant(ctx, req.Accessor.ID, string(req.Resource.Type), req.Resource.ID, ops); err != nil {
			return err
		}
	}
	return nil
}

func (s *safeAuthZHttpAcc) GrantAgentUsePmsForSingleAccessor(ctx context.Context, accessor *authzhttpreq.PolicyAccessor, agentID string, _ string) error {
	if accessor == nil {
		return nil
	}
	return s.grant(ctx, accessor.ID, string(cdaenum.ResourceTypeDataAgent), agentID, []string{string(cdapmsenum.AgentUse)})
}

func (s *safeAuthZHttpAcc) GrantAgentUsePmsForAccessors(ctx context.Context, accessors []*authzhttpreq.PolicyAccessor, agentID string, _ string) error {
	for _, a := range accessors {
		if a == nil {
			continue
		}
		if err := s.grant(ctx, a.ID, string(cdaenum.ResourceTypeDataAgent), agentID, []string{string(cdapmsenum.AgentUse)}); err != nil {
			return err
		}
	}
	return nil
}

func (s *safeAuthZHttpAcc) GrantAgentUsePmsForAppAdmin(_ context.Context) error {
	s.logger.Debugf("[authz] bkn-safe: GrantAgentUsePmsForAppAdmin no-op (covered by bkn-safe seed)")
	return nil
}

func (s *safeAuthZHttpAcc) GrantMgmtPmsForAppAdmin(_ context.Context) error {
	s.logger.Debugf("[authz] bkn-safe: GrantMgmtPmsForAppAdmin no-op (covered by bkn-safe seed)")
	return nil
}

func (s *safeAuthZHttpAcc) DenyAgentUsePmsForAllAccessor(_ context.Context, agentID string, _ string) error {
	s.logger.Warnf("[authz] bkn-safe is allow-only; DenyAgentUsePmsForAllAccessor no-op (agent=%s)", agentID)
	return nil
}

func (s *safeAuthZHttpAcc) DeletePolicy(ctx context.Context, req *authzhttpreq.PolicyDeleteParams) error {
	if req == nil {
		return nil
	}
	for _, r := range req.Resources {
		if r == nil {
			continue
		}
		if err := s.deleteResource(ctx, string(r.Type), r.ID); err != nil {
			return err
		}
	}
	return nil
}

func (s *safeAuthZHttpAcc) DeleteAgentPolicy(ctx context.Context, agentID string) error {
	return s.deleteResource(ctx, string(cdaenum.ResourceTypeDataAgent), agentID)
}

// ---- 3. 策略查询接口 ----

func (s *safeAuthZHttpAcc) ListPolicy(ctx context.Context, req *authzhttpreq.ListPolicyReq, _ string) (*authzhttpres.ListPolicyRes, error) {
	return s.listPolicyRes(ctx, req)
}

func (s *safeAuthZHttpAcc) ListPolicyAll(ctx context.Context, req *authzhttpreq.ListPolicyReq, _ string) (*authzhttpres.ListPolicyRes, error) {
	return s.listPolicyRes(ctx, req)
}

func (s *safeAuthZHttpAcc) listPolicyRes(ctx context.Context, req *authzhttpreq.ListPolicyReq) (*authzhttpres.ListPolicyRes, error) {
	if req == nil {
		return &authzhttpres.ListPolicyRes{}, nil
	}
	entries, err := s.listPolicies(ctx, string(req.ResourceType), req.ResourceID)
	if err != nil {
		return nil, err
	}
	res := &authzhttpres.ListPolicyRes{Entries: make([]*authzhttpres.PolicyEntry, 0, len(entries))}
	for _, e := range entries {
		allow := make([]*authzhttpres.PolicyOperationItem, 0, len(e.Operations))
		for _, op := range e.Operations {
			allow = append(allow, &authzhttpres.PolicyOperationItem{ID: cdapmsenum.Operator(op)})
		}
		res.Entries = append(res.Entries, &authzhttpres.PolicyEntry{
			Resource:  &authzhttpreq.PolicyResource{ID: req.ResourceID, Type: req.ResourceType},
			Accessor:  &authzhttpres.PolicyAccessor{ID: e.AccessorID},
			Operation: &authzhttpres.PolicyOperation{Allow: allow, Deny: []*authzhttpres.PolicyOperationItem{}},
			Condition: "{}",
			ExpiresAt: neverExpire,
		})
	}
	res.TotalCount = len(res.Entries)
	return res, nil
}

// ---- 4. 资源类型配置接口 ----

func (s *safeAuthZHttpAcc) SetResourceType(_ context.Context, resourceTypeID cdaenum.ResourceType, _ *authzhttpreq.ResourceTypeSetReq) error {
	s.logger.Debugf("[authz] bkn-safe: SetResourceType(%s) no-op (catalog seeded in bkn-safe)", resourceTypeID)
	return nil
}

// ---- bkn-safe HTTP helpers ----

func (s *safeAuthZHttpAcc) checkOne(ctx context.Context, accessorID, rtype, rid, op string) (bool, error) {
	var out struct {
		Allowed bool `json:"allowed"`
	}
	err := s.do(ctx, http.MethodPost, "/api/safe/v1/authz/check", map[string]any{
		"accessor_id": accessorID,
		"resource":    map[string]string{"type": rtype, "id": rid},
		"operation":   op,
	}, &out)
	return out.Allowed, err
}

// checkAll is true iff the accessor may perform EVERY op on the resource.
func (s *safeAuthZHttpAcc) checkAll(ctx context.Context, accessorID, rtype, rid string, ops []string) (bool, error) {
	for _, op := range ops {
		ok, err := s.checkOne(ctx, accessorID, rtype, rid, op)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

func (s *safeAuthZHttpAcc) allowedOps(ctx context.Context, accessorID, rtype, rid string) ([]string, error) {
	var out struct {
		Operations []string `json:"operations"`
	}
	err := s.do(ctx, http.MethodPost, "/api/safe/v1/authz/operations", map[string]any{
		"accessor_id": accessorID,
		"resource":    map[string]string{"type": rtype, "id": rid},
	}, &out)
	return out.Operations, err
}

func (s *safeAuthZHttpAcc) grant(ctx context.Context, accessorID, rtype, rid string, ops []string) error {
	return s.do(ctx, http.MethodPost, "/api/safe/v1/authz/policies", map[string]any{
		"accessor_id": accessorID,
		"resource":    map[string]string{"type": rtype, "id": rid},
		"operations":  ops,
	}, nil)
}

func (s *safeAuthZHttpAcc) deleteResource(ctx context.Context, rtype, rid string) error {
	return s.do(ctx, http.MethodDelete, "/api/safe/v1/authz/policies", map[string]any{
		"resource": map[string]string{"type": rtype, "id": rid},
	}, nil)
}

type safePolicyEntry struct {
	AccessorID string   `json:"accessor_id"`
	Operations []string `json:"operations"`
}

func (s *safeAuthZHttpAcc) listPolicies(ctx context.Context, rtype, rid string) ([]safePolicyEntry, error) {
	path := fmt.Sprintf("/api/safe/v1/authz/policies?resource_type=%s&resource_id=%s", url.QueryEscape(rtype), url.QueryEscape(rid))
	var out struct {
		Entries []safePolicyEntry `json:"entries"`
	}
	if err := s.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Entries, nil
}

func (s *safeAuthZHttpAcc) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.safeURL+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.http.Do(req)
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

func opStrings(ops []cdapmsenum.Operator) []string {
	out := make([]string, 0, len(ops))
	for _, op := range ops {
		out = append(out, string(op))
	}
	return out
}

func toOperators(ops []string) []cdapmsenum.Operator {
	out := make([]cdapmsenum.Operator, 0, len(ops))
	for _, op := range ops {
		out = append(out, cdapmsenum.Operator(op))
	}
	return out
}
