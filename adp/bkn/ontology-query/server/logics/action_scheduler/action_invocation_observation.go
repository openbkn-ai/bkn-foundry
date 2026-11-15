// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_scheduler

import (
	"context"
	"errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"net/http"
)

type invocationObservationKey struct{}

func withInvocationObservation(ctx context.Context, observation *interfaces.ActionInvocationObservation) context.Context {
	return context.WithValue(ctx, invocationObservationKey{}, observation)
}
func observeInvocationAuthorization(ctx context.Context, err error) {
	observation, _ := ctx.Value(invocationObservationKey{}).(*interfaces.ActionInvocationObservation)
	if observation == nil {
		return
	}
	observation.Authorization = "accepted"
	if err != nil {
		observation.Authorization = "unavailable"
		var httpErr *httperrors.HTTPError
		if errors.As(err, &httpErr) {
			observation.ErrorCode = httpErr.ErrorCode
			if httpErr.HTTPCode == http.StatusForbidden {
				observation.Authorization = "rejected"
			}
		}
	}
}

// Source authorization is separate from the caller's action permission. Transport
// failures do not establish whether the execution source accepted its credentials.
func observeSourceAuthorization(ctx context.Context, err error) {
	observation, _ := ctx.Value(invocationObservationKey{}).(*interfaces.ActionInvocationObservation)
	if observation == nil {
		return
	}
	observation.SourceAuthorization = "accepted"
	if err != nil {
		observation.SourceAuthorization = "unavailable"
		var httpErr *httperrors.HTTPError
		if errors.As(err, &httpErr) && httpErr.HTTPCode == http.StatusForbidden {
			observation.SourceAuthorization = "rejected"
			observation.ErrorCode = httpErr.ErrorCode
		}
	}
}
