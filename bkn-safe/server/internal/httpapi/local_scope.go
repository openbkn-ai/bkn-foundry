// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
)

// vegaLocalOperations is the complete request-shape boundary for local
// decisions. It deliberately mirrors the operations configurable at the two
// levels whose Resource -> Catalog relationship Vega owns. Resource lifecycle
// operations are checked on the Catalog as resource_manage, not on Resource.
var vegaLocalOperations = map[string]map[string]struct{}{
	"catalog": {
		"view_detail": {}, "modify": {}, "delete": {}, "authorize": {},
		"task_manage": {}, "resource_manage": {}, "query_data": {},
	},
	"resource": {
		"view_detail": {}, "query_data": {},
	},
}

// validateLocalScope narrows the unauthenticated platform-internal local mode
// to concrete Resource/Catalog decisions. Caller-supplied identity headers are
// intentionally irrelevant: trust comes from the /authz network boundary
// defined by #333 and gated for untrusted workloads by #1289, not from an
// unverifiable service name. Invalid shapes fail with 400 and never fall back
// to effective evaluation.
func validateLocalScope(c *gin.Context, resources []authz.ResourceRef, operations []string) bool {
	for _, resource := range resources {
		allowedOperations, allowedType := vegaLocalOperations[resource.Type]
		if !allowedType || resource.ID == "" || strings.Contains(resource.ID, "*") {
			replyPublicError(c, http.StatusBadRequest)
			return false
		}
		for _, operation := range operations {
			if _, allowed := allowedOperations[operation]; !allowed {
				replyPublicError(c, http.StatusBadRequest)
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
