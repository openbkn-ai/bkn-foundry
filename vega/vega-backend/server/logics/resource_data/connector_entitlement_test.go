// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package resource_data

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/stretchr/testify/require"

	verrors "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/factory"
)

func TestConnectorCreationError(t *testing.T) {
	t.Run("entitlement denied", func(t *testing.T) {
		err := connectorCreationError(context.Background(), factory.ErrConnectorEntitlementDenied)
		var httpErr *httperrors.HTTPError
		require.True(t, errors.As(err, &httpErr))
		require.Equal(t, http.StatusForbidden, httpErr.HTTPCode)
		require.Equal(t, verrors.VegaBackend_Connector_EntitlementDenied, httpErr.ErrorCode)
	})

	t.Run("connector disabled", func(t *testing.T) {
		err := connectorCreationError(context.Background(), factory.ErrConnectorDisabled)
		var httpErr *httperrors.HTTPError
		require.True(t, errors.As(err, &httpErr))
		require.Equal(t, http.StatusConflict, httpErr.HTTPCode)
		require.Equal(t, verrors.VegaBackend_Connector_Disabled, httpErr.ErrorCode)
	})
}
