package drivenadapters

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces"
)

// bkn-safe authz cutover, shadow stage.
//
// This is the FIRST, fully-revertible step of moving exec-factory's authz from
// ISF to bkn-safe. When AUTHZ_SHADOW_ENABLED=true, every OperationCheck is ALSO
// sent to bkn-safe and the decision is diff-logged — but ISF stays
// AUTHORITATIVE (its result is what's returned). Behaviour is unchanged; revert
// = unset the env var (no redeploy of logic). The other Authorization methods
// delegate straight to ISF.
//
// Once the shadow diffs are clean, a later step flips the authoritative source
// to bkn-safe (then ISF can be retired).

// safeAuthzClient is a minimal bkn-safe authz client: it only implements the
// OperationCheck path needed for shadowing.
type safeAuthzClient struct {
	baseURL string
	http    *http.Client
	logger  interfaces.Logger
}

func newSafeAuthzClient(baseURL string, logger interfaces.Logger) *safeAuthzClient {
	return &safeAuthzClient{
		baseURL: baseURL,
		http:    &http.Client{Timeout: 5 * time.Second},
		logger:  logger,
	}
}

// operationCheckAll returns true iff the accessor is allowed EVERY operation on
// the resource (matching ISF operation-check AND semantics). bkn-safe's /check
// is single-op, so we AND across the requested ops.
func (c *safeAuthzClient) operationCheckAll(ctx context.Context, req *interfaces.AuthOperationCheckRequest) (bool, error) {
	for _, op := range req.Operation {
		ok, err := c.checkOne(ctx, req.Accessor.ID, req.Resource.Type, req.Resource.ID, string(op))
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

func (c *safeAuthzClient) checkOne(ctx context.Context, accessorID, rtype, rid, op string) (bool, error) {
	body, _ := json.Marshal(map[string]any{
		"accessor_id": accessorID,
		"resource":    map[string]string{"type": rtype, "id": rid},
		"operation":   op,
	})
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/safe/v1/authz/check", bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(httpReq)
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

// shadowAuthorization wraps the authoritative (ISF) Authorization and, on
// OperationCheck, also queries bkn-safe and logs any decision divergence.
type shadowAuthorization struct {
	interfaces.Authorization // embedded ISF adapter — provides all methods
	safe                     *safeAuthzClient
	logger                   interfaces.Logger
}

// OperationCheck overrides the embedded ISF method: ISF result is authoritative;
// bkn-safe is queried in parallel only to log diffs.
func (s *shadowAuthorization) OperationCheck(ctx context.Context, req *interfaces.AuthOperationCheckRequest) (*interfaces.AuthOperationCheckResponse, error) {
	isfResp, isfErr := s.Authorization.OperationCheck(ctx, req)

	// Shadow call — never affects the returned decision or error.
	safeAllowed, safeErr := s.safe.operationCheckAll(ctx, req)
	switch {
	case safeErr != nil:
		s.logger.WithContext(ctx).Warnf("[authz-shadow] bkn-safe error (ISF authoritative): accessor=%s resource=%s:%s ops=%v err=%v",
			req.Accessor.ID, req.Resource.Type, req.Resource.ID, req.Operation, safeErr)
	case isfErr == nil && isfResp != nil && isfResp.Result != safeAllowed:
		s.logger.WithContext(ctx).Warnf("[authz-shadow] DIFF: accessor=%s resource=%s:%s ops=%v isf=%v bkn-safe=%v",
			req.Accessor.ID, req.Resource.Type, req.Resource.ID, req.Operation, isfResp.Result, safeAllowed)
	default:
		s.logger.WithContext(ctx).Debugf("[authz-shadow] match: accessor=%s resource=%s:%s ops=%v result=%v",
			req.Accessor.ID, req.Resource.Type, req.Resource.ID, req.Operation, safeAllowed)
	}

	return isfResp, isfErr
}

// maybeShadow wraps the ISF Authorization in a shadow comparator when
// AUTHZ_SHADOW_ENABLED=true and BKN_SAFE_URL is set; otherwise returns isf
// unchanged. This is the single, env-gated, fully-revertible switch point.
func maybeShadow(isf interfaces.Authorization, logger interfaces.Logger) interfaces.Authorization {
	if os.Getenv("AUTHZ_SHADOW_ENABLED") != "true" {
		return isf
	}
	baseURL := os.Getenv("BKN_SAFE_URL")
	if baseURL == "" {
		logger.Warnf("[authz-shadow] AUTHZ_SHADOW_ENABLED but BKN_SAFE_URL empty; shadow disabled")
		return isf
	}
	logger.Infof("[authz-shadow] enabled; ISF authoritative, comparing against bkn-safe at %s", baseURL)
	return &shadowAuthorization{Authorization: isf, safe: newSafeAuthzClient(baseURL, logger), logger: logger}
}
