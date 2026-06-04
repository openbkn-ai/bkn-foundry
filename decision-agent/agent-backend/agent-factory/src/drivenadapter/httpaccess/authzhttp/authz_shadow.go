package authzhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/drivenadapter/httpaccess/authzhttp/authzhttpreq"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/drivenadapter/httpaccess/authzhttp/authzhttpres"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/infra/cmp/icmp"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/port/driven/ihttpaccess/iauthzacc"
)

// bkn-safe authz cutover for DA (decision-agent), shadow stage — fully
// revertible. When AUTHZ_PROVIDER=shadow (and BKN_SAFE_URL set), each
// OperationCheck is ALSO sent to bkn-safe and decision divergence is diff-
// logged; ISF stays AUTHORITATIVE. SingleAgentUseCheck routes through
// OperationCheck, so it's covered too. All other AuthZHttpAcc methods delegate
// to ISF via embedding. Revert = unset the env (default = pure ISF).
//
// DA's AuthZHttpAcc surface is large (~20 methods incl Grant*/SetResourceType/
// ListPolicy); a bkn-safe-authoritative adapter for DA is a later step. This
// slice only shadows the decision path to collect diffs safely.

type shadowAuthZHttpAcc struct {
	iauthzacc.AuthZHttpAcc // embedded ISF impl (authoritative)
	safeURL                string
	http                   *http.Client
	logger                 icmp.Logger
}

// OperationCheck overrides the embedded ISF method: ISF result is returned;
// bkn-safe is queried in parallel only to log diffs.
func (s *shadowAuthZHttpAcc) OperationCheck(ctx context.Context, req *authzhttpreq.SingleCheckReq) (*authzhttpres.SingleCheckResult, error) {
	isfRes, isfErr := s.AuthZHttpAcc.OperationCheck(ctx, req)

	safeOK, safeErr := s.safeAllowedAll(ctx, req)
	rtype := ""
	rid := ""
	if req.Resource != nil {
		rtype = string(req.Resource.Type)
		rid = req.Resource.ID
	}
	accID := ""
	if req.Accessor != nil {
		accID = req.Accessor.ID
	}
	switch {
	case safeErr != nil:
		s.logger.Warnf("[authz-shadow] bkn-safe error (ISF authoritative): %s:%s ops=%v err=%v", rtype, rid, req.Operation, safeErr)
	case isfErr == nil && isfRes != nil && isfRes.Result != safeOK:
		s.logger.Warnf("[authz-shadow] DIFF: accessor=%s %s:%s ops=%v isf=%v bkn-safe=%v", accID, rtype, rid, req.Operation, isfRes.Result, safeOK)
	default:
		s.logger.Debugf("[authz-shadow] match: %s:%s ops=%v result=%v", rtype, rid, req.Operation, safeOK)
	}
	return isfRes, isfErr
}

// safeAllowedAll returns true iff bkn-safe allows the accessor EVERY op on the
// resource (ISF operation-check AND semantics).
func (s *shadowAuthZHttpAcc) safeAllowedAll(ctx context.Context, req *authzhttpreq.SingleCheckReq) (bool, error) {
	if req.Accessor == nil || req.Resource == nil {
		return false, nil
	}
	for _, op := range req.Operation {
		ok, err := s.safeCheckOne(ctx, req.Accessor.ID, string(req.Resource.Type), req.Resource.ID, string(op))
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

func (s *shadowAuthZHttpAcc) safeCheckOne(ctx context.Context, accessorID, rtype, rid, op string) (bool, error) {
	body, _ := json.Marshal(map[string]any{
		"accessor_id": accessorID,
		"resource":    map[string]string{"type": rtype, "id": rid},
		"operation":   op,
	})
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.safeURL+"/api/safe/v1/authz/check", bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := s.http.Do(httpReq)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	var out struct {
		Allowed bool `json:"allowed"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return false, err
	}
	return out.Allowed, nil
}

// MaybeShadow wraps the ISF AuthZHttpAcc in a shadow comparator when
// AUTHZ_PROVIDER=shadow and BKN_SAFE_URL is set; otherwise returns impl
// unchanged. The single, env-gated, fully-revertible cutover point for DA.
func MaybeShadow(impl iauthzacc.AuthZHttpAcc, logger icmp.Logger) iauthzacc.AuthZHttpAcc {
	if os.Getenv("AUTHZ_PROVIDER") != "shadow" {
		return impl
	}
	baseURL := os.Getenv("BKN_SAFE_URL")
	if baseURL == "" {
		logger.Warnf("[authz-shadow] AUTHZ_PROVIDER=shadow but BKN_SAFE_URL empty; shadow disabled")
		return impl
	}
	logger.Infof("[authz-shadow] DA enabled; ISF authoritative, comparing bkn-safe at %s", baseURL)
	return &shadowAuthZHttpAcc{
		AuthZHttpAcc: impl,
		safeURL:      baseURL,
		http:         &http.Client{Timeout: 5 * time.Second},
		logger:       logger,
	}
}
