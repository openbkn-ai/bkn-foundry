// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package object_type

import (
	"context"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	oerrors "ontology-query/errors"
)

func Test_downstreamErrorCode(t *testing.T) {
	// The status code is already passed through correctly, so the error code must follow it: mapping 403 to "parameter error" makes the frontend read it as the user
	// having no permission, and mapping 429 to "parameter error" makes callers change the query instead of retrying.
	cases := map[int]string{
		http.StatusBadRequest:   oerrors.OntologyQuery_ObjectType_InvalidParameter,
		http.StatusUnauthorized: rest.PublicError_Unauthorized,
		http.StatusForbidden:    rest.PublicError_Forbidden,
		http.StatusNotFound:     rest.PublicError_NotFound,
		http.StatusConflict:     rest.PublicError_Conflict,
		// When there is no semantically corresponding public code, fall back to this service's parameter error code instead of Public.BadRequest.
		// The latter's en-US message is "Internal Server Error".
		http.StatusTooManyRequests:       oerrors.OntologyQuery_ObjectType_InvalidParameter,
		http.StatusRequestEntityTooLarge: oerrors.OntologyQuery_ObjectType_InvalidParameter,
	}
	for status, want := range cases {
		if got := downstreamErrorCode(status); got != want {
			t.Fatalf("status %d: got %q, want %q", status, got, want)
		}
	}
}

// rest.NewHTTPError calls logger.Fatalf for unregistered error codes; tests that only compare the mapping table cannot catch
// a code added to the switch but not to allErrs. That kind of error appears only when it is first hit online,
// and it terminates the process. Construct each code here and check that messages in both languages are non-empty.
func Test_downstreamErrorCodeIsRegisteredInEveryLanguage(t *testing.T) {
	statuses := []int{
		http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusNotFound,
		http.StatusConflict,
		http.StatusMethodNotAllowed,
		http.StatusRequestTimeout,
		http.StatusGone,
		http.StatusRequestEntityTooLarge,
		http.StatusUnsupportedMediaType,
		http.StatusUnprocessableEntity,
		http.StatusTooManyRequests,
	}
	for lang := range rest.Languages {
		for _, status := range statuses {
			ctx := context.WithValue(context.Background(), rest.LanguageKey, lang)
			httpErr := rest.NewHTTPError(ctx, status, downstreamErrorCode(status))
			if httpErr == nil {
				t.Fatalf("status %d lang %v: error code is not registered", status, lang)
			}
			if httpErr.BaseError.Description == "" {
				t.Fatalf("status %d lang %v: description is empty", status, lang)
			}
			if httpErr.HTTPCode != status {
				t.Fatalf("status %d lang %v: status code was rewritten to %d", status, lang, httpErr.HTTPCode)
			}
		}
	}
}
