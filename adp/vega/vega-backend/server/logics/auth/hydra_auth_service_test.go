package auth

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-comm-go/hydra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHydraAuthServiceVerifyToken(t *testing.T) {
	t.Run("delegates to auth access", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		expected := hydra.Visitor{ID: "u1", Type: hydra.VisitorType_User}
		access := &fakeAuthAccess{visitor: expected}
		service := &hydraAuthService{aa: access}

		got, err := service.VerifyToken(context.Background(), c)

		require.NoError(t, err)
		assert.Equal(t, expected, got)
		assert.Same(t, c, access.gotCtx)
	})

	t.Run("returns auth access error", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		service := &hydraAuthService{aa: &fakeAuthAccess{err: errors.New("invalid token")}}

		got, err := service.VerifyToken(context.Background(), c)

		require.Error(t, err)
		assert.Empty(t, got)
		assert.Contains(t, err.Error(), "invalid token")
	})
}

type fakeAuthAccess struct {
	visitor hydra.Visitor
	err     error
	gotCtx  *gin.Context
}

func (f *fakeAuthAccess) VerifyToken(_ context.Context, c *gin.Context) (hydra.Visitor, error) {
	f.gotCtx = c
	return f.visitor, f.err
}
