// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	coresocket "github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/rowfilter"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/directory"
	internalrowfilter "github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/rowfilter"
)

// rowFilterRequest deliberately carries only the authenticated caller's id
// and the object type. Roles, departments and predicate fragments are always
// loaded by bkn-safe from trusted local services.
type rowFilterRequest struct {
	AccessorID     string   `json:"accessor_id" binding:"required"`
	ObjectTypeRefs []string `json:"object_type_refs" binding:"required"`
}

// rowFilterValue uses explicit type metadata so string, integer and boolean
// values retain their exact semantics while crossing the S2S boundary.
type rowFilterValue struct {
	Type    coresocket.ValueType `json:"type"`
	String  *string              `json:"string,omitempty"`
	Integer *int64               `json:"integer,omitempty"`
	Boolean *bool                `json:"boolean,omitempty"`
}

type rowFilterPredicate struct {
	Kind       coresocket.PredicateKind `json:"kind"`
	Property   string                   `json:"property,omitempty"`
	Values     []rowFilterValue         `json:"values,omitempty"`
	Predicates []rowFilterPredicate     `json:"predicates,omitempty"`
}

type rowFilterResponseEntry struct {
	ObjectTypeRef            string             `json:"object_type_ref"`
	Predicate                rowFilterPredicate `json:"predicate"`
	EffectiveRowFilterDigest string             `json:"effective_row_filter_digest"`
}

type rowFilterResponse struct {
	Entries []rowFilterResponseEntry `json:"entries"`
}

// registerRowFilter adds the tokenless ClusterIP-only S2S batch decision
// endpoint. It resolves the real caller from bkn-safe's directory and role
// graph exactly once before invoking the EE policy socket. Low-edition
// binaries receive the documented TRUE fallback; active EE with no resolver
// and all resolver failures are unavailable decisions, never permissive ones.
func registerRowFilter(group *gin.RouterGroup, enforcer *authz.Enforcer, directoryService *directory.Service, maxDepartmentScopeValues int) {
	if maxDepartmentScopeValues <= 0 {
		maxDepartmentScopeValues = directory.DefaultRowFilterDepartmentScopeLimit
	}
	group.POST("/row-filters", func(c *gin.Context) {
		var body rowFilterRequest
		if !bind(c, &body) {
			return
		}
		if !validRowFilterRequest(body) {
			replyPublicError(c, http.StatusBadRequest)
			return
		}
		caller, err := internalrowfilter.NewTrustedCallerResolverWithDepartmentLimit(directoryService, enforcer, maxDepartmentScopeValues).Resolve(c.Request.Context(), body.AccessorID)
		if err != nil {
			if errors.Is(err, internalrowfilter.ErrCallerInvalid) {
				replyPublicError(c, http.StatusForbidden)
				return
			}
			replyPublicError(c, http.StatusServiceUnavailable)
			return
		}
		decision, err := coresocket.Resolve(c.Request.Context(), coresocket.Request{
			ObjectTypeRefs: body.ObjectTypeRefs,
			Caller:         caller,
		})
		if err != nil {
			replyPublicError(c, http.StatusServiceUnavailable)
			return
		}
		response := rowFilterResponse{Entries: make([]rowFilterResponseEntry, 0, len(decision.Entries))}
		for _, entry := range decision.Entries {
			response.Entries = append(response.Entries, rowFilterResponseEntry{
				ObjectTypeRef:            entry.ObjectTypeRef,
				Predicate:                rowFilterPredicateFor(entry.Plan.Predicate),
				EffectiveRowFilterDigest: entry.EffectiveRowFilterDigest,
			})
		}
		c.JSON(http.StatusOK, response)
	})
}

func validRowFilterRequest(request rowFilterRequest) bool {
	if request.AccessorID == "" || len(request.ObjectTypeRefs) == 0 || len(request.ObjectTypeRefs) > coresocket.MaxObjectTypesPerRequest {
		return false
	}
	seen := make(map[string]struct{}, len(request.ObjectTypeRefs))
	for _, reference := range request.ObjectTypeRefs {
		if !coresocket.ValidObjectTypeRef(reference) {
			return false
		}
		if _, duplicate := seen[reference]; duplicate {
			return false
		}
		seen[reference] = struct{}{}
	}
	return true
}

func rowFilterPredicateFor(predicate coresocket.Predicate) rowFilterPredicate {
	result := rowFilterPredicate{
		Kind:     predicate.Kind,
		Property: predicate.Property,
	}
	if len(predicate.Values) > 0 {
		result.Values = make([]rowFilterValue, 0, len(predicate.Values))
		for _, value := range predicate.Values {
			result.Values = append(result.Values, rowFilterValueFor(value))
		}
	}
	if len(predicate.Predicates) > 0 {
		result.Predicates = make([]rowFilterPredicate, 0, len(predicate.Predicates))
		for _, child := range predicate.Predicates {
			result.Predicates = append(result.Predicates, rowFilterPredicateFor(child))
		}
	}
	return result
}

func rowFilterValueFor(value coresocket.Value) rowFilterValue {
	result := rowFilterValue{Type: value.Type}
	switch value.Type {
	case coresocket.ValueString:
		value := value.String
		result.String = &value
	case coresocket.ValueInteger:
		value := value.Integer
		result.Integer = &value
	case coresocket.ValueBoolean:
		value := value.Boolean
		result.Boolean = &value
	}
	return result
}
