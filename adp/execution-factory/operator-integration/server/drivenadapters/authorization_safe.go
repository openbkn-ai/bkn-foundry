// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces"
)

// bkn-safe authz adapter + cutover switch for exec-factory.
//
// AUTHZ_PROVIDER selects the authz backend (fully revertible — flip the env):
//   - "isf" / unset  : ISF authorization (default, unchanged behaviour)
//   - "shadow"       : ISF authoritative, bkn-safe queried in parallel + diffs logged
//   - "bkn-safe"     : bkn-safe authoritative
// BKN_SAFE_URL points at bkn-safe (e.g. http://bkn-safe:3000) for shadow/bkn-safe.
//
// safeAuthorization implements interfaces.Authorization against bkn-safe's clean
// API (/api/safe/v1/authz/*).

type safeAuthorization struct {
	baseURL string
	http    *http.Client
	logger  interfaces.Logger
}

func newSafeAuthorization(baseURL string, logger interfaces.Logger) *safeAuthorization {
	return &safeAuthorization{baseURL: baseURL, http: &http.Client{Timeout: 5 * time.Second}, logger: logger}
}

// checkOne queries bkn-safe for a single (accessor, type:id, op) decision.
func (s *safeAuthorization) checkOne(ctx context.Context, accessorID, rtype, rid, op string) (bool, error) {
	var out struct {
		Allowed bool `json:"allowed"`
	}
	err := s.post(ctx, "/api/safe/v1/authz/check", map[string]any{
		"accessor_id": accessorID,
		"resource":    map[string]string{"type": rtype, "id": rid},
		"operation":   op,
	}, &out)
	return out.Allowed, err
}

// allowedAll returns true iff the accessor is allowed every op (ISF AND semantics).
func (s *safeAuthorization) allowedAll(ctx context.Context, accessorID, rtype, rid string, ops []interfaces.AuthOperationType) (bool, error) {
	for _, op := range ops {
		ok, err := s.checkOne(ctx, accessorID, rtype, rid, string(op))
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

func (s *safeAuthorization) OperationCheck(ctx context.Context, req *interfaces.AuthOperationCheckRequest) (*interfaces.AuthOperationCheckResponse, error) {
	ok, err := s.allowedAll(ctx, req.Accessor.ID, req.Resource.Type, req.Resource.ID, req.Operation)
	if err != nil {
		return nil, err
	}
	return &interfaces.AuthOperationCheckResponse{Result: ok}, nil
}

// ResourceFilter keeps the resources the accessor is allowed all the operations on.
func (s *safeAuthorization) ResourceFilter(ctx context.Context, req *interfaces.AuthResourceFilterRequest) ([]*interfaces.AuthResourceResult, error) {
	out := make([]*interfaces.AuthResourceResult, 0, len(req.Resources))
	for _, r := range req.Resources {
		ok, err := s.allowedAll(ctx, req.Accessor.ID, r.Type, r.ID, req.Operations)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, &interfaces.AuthResourceResult{ID: r.ID})
		}
	}
	return out, nil
}

// ResourceList enumerates resource-instance IDs the accessor may perform every
// requested operation on. Type-wide grants (type:*) surface as ResourceIDAll;
// multi-op requests intersect per-op enumerations (AND semantics).
func (s *safeAuthorization) ResourceList(ctx context.Context, req *interfaces.ResourceListRequest) ([]*interfaces.AuthResourceResult, error) {
	if req == nil || req.Accessor == nil || req.Resource == nil || len(req.Operation) == 0 {
		return []*interfaces.AuthResourceResult{}, nil
	}

	ok, err := s.allowedAll(ctx, req.Accessor.ID, req.Resource.Type, interfaces.ResourceIDAll, req.Operation)
	if err != nil {
		return nil, err
	}
	if ok {
		return []*interfaces.AuthResourceResult{{ID: interfaces.ResourceIDAll}}, nil
	}

	ids, err := s.accessibleResourceIDsAllOps(ctx, req.Accessor.ID, req.Resource.Type, req.Operation)
	if err != nil {
		return nil, err
	}
	out := make([]*interfaces.AuthResourceResult, 0, len(ids))
	for _, id := range ids {
		out = append(out, &interfaces.AuthResourceResult{ID: id})
	}
	return out, nil
}

func (s *safeAuthorization) accessibleResourceIDs(ctx context.Context, accessorID, rtype, op string) ([]string, error) {
	var out struct {
		IDs []string `json:"ids"`
	}
	q := url.Values{
		"accessor_id":   {accessorID},
		"resource_type": {rtype},
		"operation":     {op},
	}
	if err := s.get(ctx, "/api/safe/v1/authz/resources", q, &out); err != nil {
		return nil, err
	}
	return out.IDs, nil
}

func (s *safeAuthorization) accessibleResourceIDsAllOps(ctx context.Context, accessorID, rtype string, ops []interfaces.AuthOperationType) ([]string, error) {
	ids, err := s.accessibleResourceIDs(ctx, accessorID, rtype, string(ops[0]))
	if err != nil {
		return nil, err
	}
	for _, op := range ops[1:] {
		next, err := s.accessibleResourceIDs(ctx, accessorID, rtype, string(op))
		if err != nil {
			return nil, err
		}
		ids = intersectStringSlices(ids, next)
		if len(ids) == 0 {
			break
		}
	}
	return ids, nil
}

func intersectStringSlices(a, b []string) []string {
	if len(a) == 0 || len(b) == 0 {
		return []string{}
	}
	set := make(map[string]struct{}, len(a))
	for _, id := range a {
		set[id] = struct{}{}
	}
	out := make([]string, 0, len(b))
	seen := make(map[string]struct{}, len(b))
	for _, id := range b {
		if _, ok := set[id]; !ok {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// CreatePolicy grants each accessor the allowed ops on its resource instance.
func (s *safeAuthorization) CreatePolicy(ctx context.Context, reqs []*interfaces.AuthCreatePolicyRequest) error {
	for _, req := range reqs {
		ops := make([]string, 0)
		if req.Operation != nil {
			for _, a := range req.Operation.Allow {
				ops = append(ops, a.ID)
			}
		}
		if err := s.post(ctx, "/api/safe/v1/authz/policies", map[string]any{
			"accessor_id": req.Accessor.ID,
			"resource":    map[string]string{"type": req.Resource.Type, "id": req.Resource.ID},
			"operations":  ops,
		}, nil); err != nil {
			return err
		}
	}
	return nil
}

// DeletePolicy drops all policies on each resource instance.
func (s *safeAuthorization) DeletePolicy(ctx context.Context, req *interfaces.AuthDeletePolicyRequest) error {
	for _, r := range req.Resources {
		if err := s.del(ctx, "/api/safe/v1/authz/policies", map[string]any{
			"resource": map[string]string{"type": r.Type, "id": r.ID},
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *safeAuthorization) post(ctx context.Context, path string, body, out any) error {
	return s.do(ctx, http.MethodPost, path, body, out)
}
func (s *safeAuthorization) get(ctx context.Context, path string, query url.Values, out any) error {
	target := s.baseURL + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("bkn-safe GET %s: %d: %s", path, resp.StatusCode, data)
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}
func (s *safeAuthorization) del(ctx context.Context, path string, body any) error {
	return s.do(ctx, http.MethodDelete, path, body, nil)
}

func (s *safeAuthorization) do(ctx context.Context, method, path string, body, out any) error {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, method, s.baseURL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
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

// shadowAuthorization wraps the authoritative (ISF) adapter and, on
// OperationCheck, also queries bkn-safe and logs decision divergence.
type shadowAuthorization struct {
	interfaces.Authorization // embedded ISF adapter (authoritative)
	safe                     *safeAuthorization
	logger                   interfaces.Logger
}

func (s *shadowAuthorization) OperationCheck(ctx context.Context, req *interfaces.AuthOperationCheckRequest) (*interfaces.AuthOperationCheckResponse, error) {
	isfResp, isfErr := s.Authorization.OperationCheck(ctx, req)
	safeOK, safeErr := s.safe.allowedAll(ctx, req.Accessor.ID, req.Resource.Type, req.Resource.ID, req.Operation)
	switch {
	case safeErr != nil:
		s.logger.WithContext(ctx).Warnf("[authz-shadow] bkn-safe error (ISF authoritative): %s:%s ops=%v err=%v", req.Resource.Type, req.Resource.ID, req.Operation, safeErr)
	case isfErr == nil && isfResp != nil && isfResp.Result != safeOK:
		s.logger.WithContext(ctx).Warnf("[authz-shadow] DIFF: accessor=%s %s:%s ops=%v isf=%v bkn-safe=%v", req.Accessor.ID, req.Resource.Type, req.Resource.ID, req.Operation, isfResp.Result, safeOK)
	default:
		s.logger.WithContext(ctx).Debugf("[authz-shadow] match: %s:%s ops=%v result=%v", req.Resource.Type, req.Resource.ID, req.Operation, safeOK)
	}
	return isfResp, isfErr
}

// selectAuthz applies the AUTHZ_PROVIDER switch. Default/unknown => ISF (the
// single, env-gated, fully-revertible cutover point).
func selectAuthz(isf interfaces.Authorization, logger interfaces.Logger) interfaces.Authorization {
	provider := os.Getenv("AUTHZ_PROVIDER")
	if provider == "" || provider == "isf" {
		return isf
	}
	baseURL := os.Getenv("BKN_SAFE_URL")
	if baseURL == "" {
		logger.Warnf("[authz] AUTHZ_PROVIDER=%s but BKN_SAFE_URL empty; falling back to ISF", provider)
		return isf
	}
	safe := newSafeAuthorization(baseURL, logger)
	switch provider {
	case "bkn-safe":
		logger.Infof("[authz] provider=bkn-safe (authoritative) at %s", baseURL)
		return safe
	case "shadow":
		logger.Infof("[authz] provider=shadow; ISF authoritative, comparing bkn-safe at %s", baseURL)
		return &shadowAuthorization{Authorization: isf, safe: safe, logger: logger}
	default:
		logger.Warnf("[authz] unknown AUTHZ_PROVIDER=%s; using ISF", provider)
		return isf
	}
}
