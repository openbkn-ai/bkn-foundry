// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package query

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/factory"
	"github.com/stretchr/testify/require"
)

func TestConnectorInitializationErrorReturnsForbiddenForEntitlement(t *testing.T) {
	err := connectorInitializationError(context.Background(), factory.ErrConnectorEntitlementDenied)
	var httpErr *rest.HTTPError
	require.True(t, errors.As(err, &httpErr))
	require.Equal(t, http.StatusForbidden, httpErr.HTTPCode)

	err = connectorInitializationError(context.Background(), factory.ErrConnectorDisabled)
	require.True(t, errors.As(err, &httpErr))
	require.Equal(t, http.StatusServiceUnavailable, httpErr.HTTPCode)
}
