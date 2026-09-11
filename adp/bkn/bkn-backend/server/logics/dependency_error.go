// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logics

import (
	"context"
	"errors"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"bkn-backend/interfaces"
)

type DependencyPublicErrorDetails struct {
	Kind   interfaces.DependencyErrorKind `json:"dependency_kind"`
	Detail any                            `json:"detail,omitempty"`
}

// MapDependencyError converts a transport-neutral dependency error into a
// stable public contract. forbiddenIsUserScoped is false for controlled
// lookups whose identity was resolved by proxy preflight.
func MapDependencyError(ctx context.Context, err error, forbiddenIsUserScoped bool,
	invalidError *rest.HTTPError, internalCode string) error {
	if err == nil {
		return nil
	}
	var httpErr *rest.HTTPError
	if errors.As(err, &httpErr) {
		return httpErr
	}
	var dependencyErr *interfaces.DependencyError
	if !errors.As(err, &dependencyErr) {
		return rest.NewHTTPError(ctx, http.StatusBadGateway, internalCode)
	}
	switch dependencyErr.Kind {
	case interfaces.DependencyInvalidBinding, interfaces.DependencyNotFound:
		return markDependencyHTTPError(invalidError, dependencyErr.Kind)
	case interfaces.DependencyForbidden:
		if forbiddenIsUserScoped {
			return markDependencyHTTPError(
				rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden), dependencyErr.Kind)
		}
		return markDependencyHTTPError(
			rest.NewHTTPError(ctx, http.StatusBadGateway, internalCode), dependencyErr.Kind)
	case interfaces.DependencyTimeout, interfaces.DependencyUnavailable:
		return markDependencyHTTPError(
			rest.NewHTTPError(ctx, http.StatusServiceUnavailable, internalCode), dependencyErr.Kind)
	default:
		return markDependencyHTTPError(
			rest.NewHTTPError(ctx, http.StatusBadGateway, internalCode), dependencyErr.Kind)
	}
}

func markDependencyHTTPError(httpErr *rest.HTTPError, kind interfaces.DependencyErrorKind) *rest.HTTPError {
	httpErr.BaseError.ErrorDetails = DependencyPublicErrorDetails{
		Kind: kind, Detail: httpErr.BaseError.ErrorDetails,
	}
	return httpErr
}

func IsDependencyHTTPError(err error) bool {
	var httpErr *rest.HTTPError
	if !errors.As(err, &httpErr) {
		return false
	}
	_, ok := httpErr.BaseError.ErrorDetails.(DependencyPublicErrorDetails)
	return ok
}

func DependencyErrorKind(err error) (interfaces.DependencyErrorKind, bool) {
	var dependencyErr *interfaces.DependencyError
	if !errors.As(err, &dependencyErr) {
		return "", false
	}
	return dependencyErr.Kind, true
}

// PreserveHTTPError keeps a child resource's public contract at aggregate
// entry points while converting unexpected generic errors to a safe fallback.
func PreserveHTTPError(ctx context.Context, err error, fallbackCode string) error {
	var httpErr *rest.HTTPError
	if errors.As(err, &httpErr) {
		return httpErr
	}
	return rest.NewHTTPError(ctx, http.StatusInternalServerError, fallbackCode)
}
