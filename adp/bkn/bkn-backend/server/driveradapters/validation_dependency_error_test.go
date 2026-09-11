// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"

	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
	"bkn-backend/logics"
)

func TestReplyDependencyValidationErrorPreservesMachineReadableContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/validation", nil)
	err := logics.MapDependencyError(context.Background(),
		interfaces.NewDependencyError("vega", "get_resource_schema", interfaces.DependencyTimeout, 0),
		false,
		rest.NewHTTPError(context.Background(), http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter),
		berrors.BknBackend_ObjectType_InternalError)

	replied := replyDependencyValidationError(ctx, trace.SpanFromContext(context.Background()), err)

	require.True(t, replied)
	assert.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"dependency_kind":"timeout"`)
	assert.NotContains(t, recorder.Body.String(), "vega")
}

func TestReplyDependencyValidationErrorLeavesOrdinaryValidationFeedbackAlone(t *testing.T) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)

	assert.False(t, replyDependencyValidationError(ctx,
		trace.SpanFromContext(context.Background()), errors.New("invalid model")))
	assert.Equal(t, http.StatusOK, recorder.Code)
}
