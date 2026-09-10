// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
)

const vegaWorkloadIdentity = "vega"

// WorkloadAuthenticator derives a service identity from a verifiable request
// credential. Implementations must never trust a caller-supplied service-name
// header as identity.
type WorkloadAuthenticator interface {
	Authenticate(*http.Request) (service string, ok bool)
}

// StaticBearerWorkloadAuthenticator binds one opaque bearer credential to one
// workload identity. An empty configured credential disables authentication
// and therefore fails every local-scope request closed.
type StaticBearerWorkloadAuthenticator struct {
	service string
	digest  [sha256.Size]byte
	enabled bool
}

func NewStaticBearerWorkloadAuthenticator(service, token string) *StaticBearerWorkloadAuthenticator {
	service = strings.TrimSpace(service)
	authenticator := &StaticBearerWorkloadAuthenticator{service: service}
	if service != "" && token != "" && token == strings.TrimSpace(token) {
		authenticator.digest = sha256.Sum256([]byte(token))
		authenticator.enabled = true
	}
	return authenticator
}

func (a *StaticBearerWorkloadAuthenticator) Authenticate(req *http.Request) (string, bool) {
	if a == nil || !a.enabled || req == nil {
		return "", false
	}
	scheme, token, found := strings.Cut(strings.TrimSpace(req.Header.Get("Authorization")), " ")
	if !found || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) != token || token == "" {
		return "", false
	}
	digest := sha256.Sum256([]byte(token))
	if subtle.ConstantTimeCompare(a.digest[:], digest[:]) != 1 {
		return "", false
	}
	return a.service, true
}

var vegaLocalOperations = map[string]map[string]struct{}{
	"catalog": {
		"view_detail": {}, "modify": {}, "delete": {}, "authorize": {},
		"task_manage": {}, "resource_manage": {}, "query_data": {},
	},
	"resource": {
		"view_detail": {}, "query_data": {},
	},
}

// authorizeLocalScope enforces both halves of the local-result trust boundary:
// the request must authenticate as Vega, and every requested decision must be
// inside Vega's Resource/Catalog orchestration contract.
func authorizeLocalScope(c *gin.Context, authenticator WorkloadAuthenticator,
	resources []authz.ResourceRef, operations []string) bool {
	service, ok := "", false
	if authenticator != nil {
		service, ok = authenticator.Authenticate(c.Request)
	}
	if !ok {
		replyPublicError(c, http.StatusUnauthorized)
		return false
	}
	if service != vegaWorkloadIdentity {
		replyPublicError(c, http.StatusForbidden)
		return false
	}
	for _, resource := range resources {
		allowedOperations, allowedType := vegaLocalOperations[resource.Type]
		if !allowedType || resource.ID == "" || strings.Contains(resource.ID, "*") {
			replyPublicError(c, http.StatusForbidden)
			return false
		}
		for _, operation := range operations {
			if _, allowed := allowedOperations[operation]; !allowed {
				replyPublicError(c, http.StatusForbidden)
				return false
			}
		}
	}
	return true
}

func approvedVegaLocalOperations(resourceType string, candidates []string) []string {
	approved := vegaLocalOperations[resourceType]
	out := make([]string, 0, len(candidates))
	for _, operation := range candidates {
		if _, ok := approved[operation]; ok {
			out = append(out, operation)
		}
	}
	return out
}
