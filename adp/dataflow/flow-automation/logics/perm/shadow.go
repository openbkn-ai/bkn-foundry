package perm

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"
)

// bkn-safe authz cutover, shadow stage (fully revertible).
//
// When AUTHZ_PROVIDER=shadow (and BKN_SAFE_URL set), the two decision reads
// (OperationCheck / OperationCheckWithResType) are ALSO sent to bkn-safe
// (/api/safe/v1/authz/check, AND over the op set) and divergence is diff-logged
// — ISF stays AUTHORITATIVE (its result/error is returned). The other
// PermPolicyHandler methods delegate to ISF via embedding. Revert = unset the
// env var (default = pure ISF).

type shadowPermPolicy struct {
	PermPolicyHandler // embedded ISF impl (authoritative)
	safeURL           string
	http              *http.Client
}

func (s *shadowPermPolicy) OperationCheck(ctx context.Context, accessorID, accessorType, resourceID string, opts ...string) (bool, error) {
	isfOK, isfErr := s.PermPolicyHandler.OperationCheck(ctx, accessorID, accessorType, resourceID, opts...)
	s.diff(ctx, accessorID, DataFlowResourceType, resourceID, opts, isfErr == nil && isfOK)
	return isfOK, isfErr
}

func (s *shadowPermPolicy) OperationCheckWithResType(ctx context.Context, accessorID, accessorType, resourceID, resourceType string, opts ...string) error {
	isfErr := s.PermPolicyHandler.OperationCheckWithResType(ctx, accessorID, accessorType, resourceID, resourceType, opts...)
	// ISF's "allowed" for this method = no error.
	s.diff(ctx, accessorID, resourceType, resourceID, opts, isfErr == nil)
	return isfErr
}

// diff queries bkn-safe and logs any divergence from ISF's decision.
func (s *shadowPermPolicy) diff(ctx context.Context, accessorID, rtype, rid string, opts []string, isfAllowed bool) {
	safeOK, err := s.safeAllowedAll(ctx, accessorID, rtype, rid, opts)
	switch {
	case err != nil:
		log.Printf("[authz-shadow] bkn-safe error (ISF authoritative): %s:%s ops=%v err=%v", rtype, rid, opts, err)
	case isfAllowed != safeOK:
		log.Printf("[authz-shadow] DIFF: accessor=%s %s:%s ops=%v isf=%v bkn-safe=%v", accessorID, rtype, rid, opts, isfAllowed, safeOK)
	}
}

func (s *shadowPermPolicy) safeAllowedAll(ctx context.Context, accessorID, rtype, rid string, opts []string) (bool, error) {
	for _, op := range opts {
		ok, err := s.safeCheckOne(ctx, accessorID, rtype, rid, op)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

func (s *shadowPermPolicy) safeCheckOne(ctx context.Context, accessorID, rtype, rid, op string) (bool, error) {
	body, _ := json.Marshal(map[string]any{
		"accessor_id": accessorID,
		"resource":    map[string]string{"type": rtype, "id": rid},
		"operation":   op,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.safeURL+"/api/safe/v1/authz/check", bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.http.Do(req)
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

// MaybeShadow wraps an ISF PermPolicyHandler in a shadow comparator when
// AUTHZ_PROVIDER=shadow and BKN_SAFE_URL is set; otherwise returns it unchanged.
func MaybeShadow(inner PermPolicyHandler) PermPolicyHandler {
	if os.Getenv("AUTHZ_PROVIDER") != "shadow" {
		return inner
	}
	url := os.Getenv("BKN_SAFE_URL")
	if url == "" {
		log.Printf("[authz-shadow] AUTHZ_PROVIDER=shadow but BKN_SAFE_URL empty; shadow disabled")
		return inner
	}
	log.Printf("[authz-shadow] flow-automation enabled; ISF authoritative, comparing bkn-safe at %s", url)
	return &shadowPermPolicy{PermPolicyHandler: inner, safeURL: url, http: &http.Client{Timeout: 5 * time.Second}}
}
