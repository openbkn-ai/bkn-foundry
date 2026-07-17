package auth

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-comm-go/hydra"
	hmock "github.com/openbkn-ai/bkn-comm-go/hydra/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestHydraAuthAccessVerifyToken(t *testing.T) {
	t.Run("delegates to hydra client", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		visitor := hydra.Visitor{ID: "user-1", Type: hydra.VisitorType_User}
		hydraClient := hmock.NewMockHydra(ctrl)
		access := &hydraAuthAccess{hydra: hydraClient}
		ctx := context.Background()
		ginCtx := testGinContext("/api/resources")
		hydraClient.EXPECT().
			VerifyToken(ctx, ginCtx).
			DoAndReturn(func(gotCtx context.Context, gotGinCtx *gin.Context) (hydra.Visitor, error) {
				assert.Equal(t, ctx, gotCtx)
				assert.Equal(t, "/api/resources", gotGinCtx.Request.URL.Path)
				return visitor, nil
			})

		got, err := access.VerifyToken(ctx, ginCtx)

		require.NoError(t, err)
		assert.Equal(t, visitor, got)
	})

	t.Run("returns hydra error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		hydraClient := hmock.NewMockHydra(ctrl)
		access := &hydraAuthAccess{hydra: hydraClient}
		ginCtx := testGinContext("/api/resources")
		hydraClient.EXPECT().
			VerifyToken(gomock.Any(), ginCtx).
			Return(hydra.Visitor{}, errors.New("invalid token"))

		got, err := access.VerifyToken(context.Background(), ginCtx)

		require.Error(t, err)
		assert.ErrorContains(t, err, "invalid token")
		assert.Empty(t, got)
	})
}

func testGinContext(path string) *gin.Context {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", path, nil)
	return c
}
