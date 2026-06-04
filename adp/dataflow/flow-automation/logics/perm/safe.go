package perm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/openbkn-ai/bkn-foundry/adp/dataflow/flow-automation/common"
	aerr "github.com/openbkn-ai/bkn-foundry/adp/dataflow/flow-automation/errors"
	ierr "github.com/openbkn-ai/bkn-foundry/adp/dataflow/flow-automation/libs/go/errors"
	traceLog "github.com/openbkn-ai/bkn-foundry/adp/dataflow/flow-automation/libs/go/telemetry/log"
	"github.com/openbkn-ai/bkn-foundry/adp/dataflow/flow-automation/utils"
)

// safePermPolicy is the bkn-safe-AUTHORITATIVE PermPolicyHandler
// (AUTHZ_PROVIDER=bkn-safe). It reproduces the ISF handler's composition
// (IsDataAdmin/CheckPerm/MinPermList build on the primitives) but every
// decision/grant/list hits bkn-safe's /api/safe/v1/authz/* instead of ISF.
// Revertible: flip AUTHZ_PROVIDER to drop back to ISF (or shadow).
//
// bkn-safe is opaque to flow-automation's resource-id encoding (e.g.
// "dagID:subtype"): ids are sent/returned verbatim, so ListResource round-trips.
type safePermPolicy struct {
	safeURL string
	http    *http.Client
}

var _ PermPolicyHandler = &safePermPolicy{}

// ---- decision ----

func (s *safePermPolicy) OperationCheck(ctx context.Context, accessorID, _, resourceID string, opts ...string) (bool, error) {
	ok, err := s.checkAll(ctx, accessorID, DataFlowResourceType, resourceID, opts)
	if err != nil {
		return false, s.internalErr(ctx, err)
	}
	return ok, nil
}

func (s *safePermPolicy) OperationCheckWithResType(ctx context.Context, accessorID, _, resourceID, resourceType string, opts ...string) error {
	ok, err := s.checkAll(ctx, accessorID, resourceType, resourceID, opts)
	if err != nil {
		return s.internalErr(ctx, err)
	}
	if !ok {
		return ierr.NewPublicRestError(ctx, ierr.PErrorForbidden, aerr.NoPermission, map[string]interface{}{"resource_ids": []interface{}{resourceID}})
	}
	return nil
}

func (s *safePermPolicy) IsDataAdmin(ctx context.Context, userID, userType string) (bool, error) {
	hasDataFlow, err := s.OperationCheck(ctx, userID, userType, DataAdminResourceID, Operations...)
	if err != nil {
		return false, err
	}
	hasO11y, err := s.OperationCheck(ctx, userID, userType, O11yResourceID, DisplayOperation)
	if err != nil {
		return false, err
	}
	return hasDataFlow && hasO11y, nil
}

func (s *safePermPolicy) IsUseAppAccount(ctx context.Context, userid, id, _ string) (bool, error) {
	return s.OperationCheck(ctx, userid, common.User.ToString(), id, RunWithAppOperation)
}

func (s *safePermPolicy) CheckPerm(ctx context.Context, userID, userType string, resourceIDs []string, opts ...string) (bool, error) {
	isDataAdmin, err := s.IsDataAdmin(ctx, userID, userType)
	if err != nil {
		return false, err
	}
	if isDataAdmin {
		return true, nil
	}
	ids, err := s.ResourceFilter(ctx, userID, userType, resourceIDs, opts...)
	if err != nil {
		return false, err
	}
	if _, missing := utils.Arrcmp(resourceIDs, ids); len(missing) > 0 {
		return false, ierr.NewPublicRestError(ctx, ierr.PErrorForbidden, aerr.NoPermission, map[string]interface{}{"resource_ids": missing})
	}
	return true, nil
}

func (s *safePermPolicy) ResourceFilter(ctx context.Context, userID, _ string, resourceIDs []string, opts ...string) ([]string, error) {
	out := make([]string, 0, len(resourceIDs))
	for _, rid := range resourceIDs {
		ok, err := s.checkAll(ctx, userID, DataFlowResourceType, rid, opts)
		if err != nil {
			return nil, s.internalErr(ctx, err)
		}
		if ok {
			out = append(out, rid)
		}
	}
	return out, nil
}

func (s *safePermPolicy) MinPermList(ctx context.Context, userID, userType string, resourceIDs []string) ([]string, error) {
	isDataAdmin, err := s.IsDataAdmin(ctx, userID, userType)
	if err != nil {
		return nil, err
	}
	if isDataAdmin {
		perms := append([]string{}, Operations...)
		return append(perms, DisplayOperation), nil
	}
	if len(resourceIDs) == 0 {
		return []string{}, nil
	}
	var perms []string
	for i, rid := range resourceIDs {
		ops, err := s.allowedOps(ctx, userID, DataFlowResourceType, rid)
		if err != nil {
			return nil, s.internalErr(ctx, err)
		}
		if i == 0 {
			perms = ops
		} else {
			perms = utils.GetIntersection(perms, ops)
		}
	}
	return perms, nil
}

func (s *safePermPolicy) ListResource(ctx context.Context, userID, _ string, _ string, opts ...string) (*ResourceList, error) {
	ids := &ResourceList{}
	op := ListOperation
	if len(opts) > 0 {
		op = opts[0]
	}
	res, err := s.accessibleResources(ctx, userID, DataFlowResourceType, op)
	if err != nil {
		return ids, s.internalErr(ctx, err)
	}
	*ids = append(*ids, res...)
	return ids, nil
}

// ---- write ----

func (s *safePermPolicy) CreatePolicy(ctx context.Context, userID, _, _, resourceID, _ string, allowOpts, denyOpts []string) error {
	if len(denyOpts) > 0 {
		traceLog.WithContext(ctx).Warnf("[authz] bkn-safe is allow-only; ignoring %d deny op(s) on %s:%s", len(denyOpts), DataFlowResourceType, resourceID)
	}
	if len(allowOpts) == 0 {
		return nil
	}
	if err := s.grant(ctx, userID, DataFlowResourceType, resourceID, allowOpts); err != nil {
		return s.internalErr(ctx, err)
	}
	return nil
}

func (s *safePermPolicy) DeletePolicy(ctx context.Context, resourceIDs ...string) error {
	for _, rid := range resourceIDs {
		if err := s.deleteResource(ctx, DataFlowResourceType, rid); err != nil {
			return s.internalErr(ctx, err)
		}
	}
	return nil
}

// UpdatePolicy has no production caller in flow-automation, and bkn-safe has no
// stable policy-ID concept (policies are Casbin rows). No-op + warn; revert to
// ISF if a policy-ID-based update is ever needed.
func (s *safePermPolicy) UpdatePolicy(ctx context.Context, policyIDs []string, _, _ []string) error {
	traceLog.WithContext(ctx).Warnf("[authz] bkn-safe: UpdatePolicy(%v) no-op (no policy-id concept; no flow-automation caller)", policyIDs)
	return nil
}

// HandlePolicyNameChange notified ISF so it could update a policy's display
// name. bkn-safe stores no display names, so there is nothing to update.
func (s *safePermPolicy) HandlePolicyNameChange(id, name, rType string) {}

// ---- bkn-safe HTTP helpers ----

func (s *safePermPolicy) checkOne(ctx context.Context, accessorID, rtype, rid, op string) (bool, error) {
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

func (s *safePermPolicy) checkAll(ctx context.Context, accessorID, rtype, rid string, opts []string) (bool, error) {
	for _, op := range opts {
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

func (s *safePermPolicy) allowedOps(ctx context.Context, accessorID, rtype, rid string) ([]string, error) {
	var out struct {
		Operations []string `json:"operations"`
	}
	err := s.do(ctx, http.MethodPost, "/api/safe/v1/authz/operations", map[string]any{
		"accessor_id": accessorID,
		"resource":    map[string]string{"type": rtype, "id": rid},
	}, &out)
	return out.Operations, err
}

func (s *safePermPolicy) accessibleResources(ctx context.Context, accessorID, rtype, op string) ([]string, error) {
	path := fmt.Sprintf("/api/safe/v1/authz/resources?accessor_id=%s&resource_type=%s&operation=%s",
		url.QueryEscape(accessorID), url.QueryEscape(rtype), url.QueryEscape(op))
	var out struct {
		IDs []string `json:"ids"`
	}
	err := s.do(ctx, http.MethodGet, path, nil, &out)
	return out.IDs, err
}

func (s *safePermPolicy) grant(ctx context.Context, accessorID, rtype, rid string, ops []string) error {
	return s.do(ctx, http.MethodPost, "/api/safe/v1/authz/policies", map[string]any{
		"accessor_id": accessorID,
		"resource":    map[string]string{"type": rtype, "id": rid},
		"operations":  ops,
	}, nil)
}

func (s *safePermPolicy) deleteResource(ctx context.Context, rtype, rid string) error {
	return s.do(ctx, http.MethodDelete, "/api/safe/v1/authz/policies", map[string]any{
		"resource": map[string]string{"type": rtype, "id": rid},
	}, nil)
}

func (s *safePermPolicy) do(ctx context.Context, method, path string, body, out any) error {
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

// internalErr wraps a bkn-safe transport error the same way the ISF handler
// wraps its adapter errors, so callers see an identical public error shape.
func (s *safePermPolicy) internalErr(ctx context.Context, err error) error {
	traceLog.WithContext(ctx).Warnf("[authz] bkn-safe request failed: %s", err.Error())
	return ierr.NewPublicRestError(ctx, ierr.PErrorInternalServerError, ierr.PErrorInternalServerError, err.Error())
}
