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

// opAccess 是某个资源类型下、单个操作的授权解析结果：要么持类型级通配授权
// （覆盖该类型全部实例），要么是具体可访问 id 集合。
type opAccess struct {
	all bool
	ids map[string]bool
}

// resolveOps 批量解析某资源类型下每个候选操作的授权：先用一次 obj="*" 的 check
// 判定类型级/超管通配（命中则该 op 覆盖全部实例、无需再取集合），否则向 bkn-safe
// 取该 (accessor, 类型, 操作) 的可访问 id 集。
//
// 往返次数只与「操作数」相关，与待过滤的资源数无关——这正是大目录不再超时的原因
// （#357：原实现逐资源鉴权，持全量授权的账号约 5.6k 资源即 40s+ 超时）。
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

// resolveFilter 解析 filter 中出现的每个资源类型的每 op 授权，供调用方在内存里
// 逐资源判定，避免逐资源发起鉴权请求。
func (s *safePermissionAccess) resolveFilter(ctx context.Context,
	filter interfaces.PermissionResourcesFilter) (map[string]map[string]opAccess, error) {

	byType := make(map[string]map[string]opAccess)
	for _, r := range filter.Resources {
		if _, done := byType[r.Type]; done {
			continue
		}
		access, err := s.resolveOps(ctx, filter.Accessor.ID, r.Type, filter.Operations)
		if err != nil {
			return nil, err
		}
		byType[r.Type] = access
	}
	return byType, nil
}

// allowedFrom 从已解析的每 op 授权里挑出该资源上实际持有的操作。
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
		if ops := allowedFrom(byType[r.Type], filter.Operations, r.ID); len(ops) > 0 {
			out[r.ID] = interfaces.PermissionResourceOps{ResourceID: r.ID, Operations: ops}
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
			Operations: allowedFrom(byType[r.Type], filter.Operations, r.ID),
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
