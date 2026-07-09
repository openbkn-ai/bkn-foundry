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

	"vega-backend/common"
)

type fakeHydra struct {
	visitor hydra.Visitor
	err     error

	ctx     context.Context
	request string
}

func (f *fakeHydra) Introspect(ctx context.Context, token string) (hydra.TokenIntrospectInfo, error) {
	return hydra.TokenIntrospectInfo{}, nil
}

func (f *fakeHydra) VerifyToken(ctx context.Context, c *gin.Context) (hydra.Visitor, error) {
	f.ctx = ctx
	f.request = c.Request.URL.Path
	return f.visitor, f.err
}

func TestNewHydraAuthAccess(t *testing.T) {
	t.Run("returns singleton access", func(t *testing.T) {
		access1 := NewHydraAuthAccess(&common.AppSetting{})
		access2 := NewHydraAuthAccess(&common.AppSetting{})

		require.NotNil(t, access1)
		assert.Same(t, access1, access2)
	})
}

func TestHydraAuthAccessVerifyToken(t *testing.T) {
	t.Run("delegates to hydra client", func(t *testing.T) {
		visitor := hydra.Visitor{ID: "user-1", Type: hydra.VisitorType_User}
		fake := &fakeHydra{visitor: visitor}
		access := &hydraAuthAccess{hydra: fake}
		ctx := context.Background()
		ginCtx := testGinContext("/api/resources")

		got, err := access.VerifyToken(ctx, ginCtx)

		require.NoError(t, err)
		assert.Equal(t, visitor, got)
		assert.Equal(t, ctx, fake.ctx)
		assert.Equal(t, "/api/resources", fake.request)
	})

	t.Run("returns hydra error", func(t *testing.T) {
		fake := &fakeHydra{err: errors.New("invalid token")}
		access := &hydraAuthAccess{hydra: fake}

		got, err := access.VerifyToken(context.Background(), testGinContext("/api/resources"))

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
