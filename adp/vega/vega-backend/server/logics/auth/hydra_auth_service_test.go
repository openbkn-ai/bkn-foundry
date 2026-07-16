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
	"go.uber.org/mock/gomock"

	vmock "vega-backend/interfaces/mock"
)

func TestHydraAuthServiceVerifyToken(t *testing.T) {
	t.Run("delegates to auth access", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		gin.SetMode(gin.TestMode)
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		expected := hydra.Visitor{ID: "u1", Type: hydra.VisitorType_User}
		access := vmock.NewMockAuthAccess(ctrl)
		access.EXPECT().VerifyToken(gomock.Any(), c).Return(expected, nil)
		service := &hydraAuthService{aa: access}

		got, err := service.VerifyToken(context.Background(), c)

		require.NoError(t, err)
		assert.Equal(t, expected, got)
	})

	t.Run("returns auth access error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		gin.SetMode(gin.TestMode)
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		access := vmock.NewMockAuthAccess(ctrl)
		access.EXPECT().VerifyToken(gomock.Any(), c).Return(hydra.Visitor{}, errors.New("invalid token"))
		service := &hydraAuthService{aa: access}

		got, err := service.VerifyToken(context.Background(), c)

		require.Error(t, err)
		assert.Empty(t, got)
		assert.Contains(t, err.Error(), "invalid token")
	})
}
