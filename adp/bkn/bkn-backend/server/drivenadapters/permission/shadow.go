package permission

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"bkn-backend/interfaces"
)

// bkn-safe authz cutover, shadow stage (fully revertible).
//
// When AUTHZ_PROVIDER=shadow (and BKN_SAFE_URL set), each CheckPermission is
// ALSO sent to bkn-safe (/api/safe/v1/authz/check, AND over the op set) and any
// decision divergence is diff-logged — ISF stays AUTHORITATIVE (its result is
// returned). The other PermissionAccess methods delegate to ISF via embedding.
// Revert = unset the env var (default = pure ISF, no behaviour change).

type shadowPermissionAccess struct {
	interfaces.PermissionAccess // embedded ISF impl (authoritative)
	safeURL                     string
	http                        *http.Client
}

func (s *shadowPermissionAccess) CheckPermission(ctx context.Context, check interfaces.PermissionCheck) (bool, error) {
	isfOK, isfErr := s.PermissionAccess.CheckPermission(ctx, check)
	safeOK, safeErr := s.safeAllowedAll(ctx, check)
	switch {
	case safeErr != nil:
		log.Printf("[authz-shadow] bkn-safe error (ISF authoritative): %s:%s ops=%v err=%v", check.Resource.Type, check.Resource.ID, check.Operations, safeErr)
	case isfErr == nil && isfOK != safeOK:
		log.Printf("[authz-shadow] DIFF: accessor=%s %s:%s ops=%v isf=%v bkn-safe=%v", check.Accessor.ID, check.Resource.Type, check.Resource.ID, check.Operations, isfOK, safeOK)
	}
	return isfOK, isfErr
}

func (s *shadowPermissionAccess) safeAllowedAll(ctx context.Context, check interfaces.PermissionCheck) (bool, error) {
	for _, op := range check.Operations {
		ok, err := s.safeCheckOne(ctx, check.Accessor.ID, check.Resource.Type, check.Resource.ID, op)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

func (s *shadowPermissionAccess) safeCheckOne(ctx context.Context, accessorID, rtype, rid, op string) (bool, error) {
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

// MaybeShadow wraps an ISF PermissionAccess in a shadow comparator when
// AUTHZ_PROVIDER=shadow and BKN_SAFE_URL is set; otherwise returns it unchanged.
func MaybeShadow(inner interfaces.PermissionAccess) interfaces.PermissionAccess {
	if os.Getenv("AUTHZ_PROVIDER") != "shadow" {
		return inner
	}
	url := os.Getenv("BKN_SAFE_URL")
	if url == "" {
		log.Printf("[authz-shadow] AUTHZ_PROVIDER=shadow but BKN_SAFE_URL empty; shadow disabled")
		return inner
	}
	log.Printf("[authz-shadow] enabled; ISF authoritative, comparing bkn-safe at %s", url)
	return &shadowPermissionAccess{PermissionAccess: inner, safeURL: url, http: &http.Client{Timeout: 5 * time.Second}}
}
