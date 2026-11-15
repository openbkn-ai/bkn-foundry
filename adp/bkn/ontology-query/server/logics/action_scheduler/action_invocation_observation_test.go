// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
package action_scheduler

import (
	"context"
	"errors"
	"fmt"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"net/http"
	"testing"
)

func TestInvocationObservationsDoNotInferSourceAuthorizationFromCallerPermission(t *testing.T) {
	for _, scenario := range []string{"accepted", "denied", "unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			observation := &interfaces.ActionInvocationObservation{}
			ctx := withInvocationObservation(context.Background(), observation)
			observeInvocationAuthorization(ctx, nil)
			if observation.Authorization != "accepted" || observation.SourceAuthorization != "" {
				t.Fatal("caller permission inferred source acceptance")
			}
			var err error
			switch scenario {
			case "denied":
				err = fmt.Errorf("source rejected: %w", httperrors.NewHTTPError(ctx, http.StatusForbidden, httperrors.PublicError_Forbidden))
			case "unavailable":
				err = errors.New("source transport failed")
			}
			observeSourceAuthorization(ctx, err)
			expected := map[string]string{"accepted": "accepted", "denied": "rejected", "unavailable": "unavailable"}[scenario]
			if observation.SourceAuthorization != expected || observation.Authorization != "accepted" {
				t.Fatalf("incorrect source decision: %+v", observation)
			}
		})
	}
}
