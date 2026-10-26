// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package auth

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

func Test_HydraAuthService_VerifyToken(t *testing.T) {
	Convey("Test hydraAuthService VerifyToken\n", t, func() {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		aa := pmock.NewMockAuthAccess(mockCtrl)
		svc := &hydraAuthService{aa: aa}
		ctx := context.Background()
		gin.SetMode(gin.TestMode)
		c, _ := gin.CreateTestContext(httptest.NewRecorder())

		Convey("Success: delegates to AuthAccess and returns visitor\n", func() {
			aa.EXPECT().VerifyToken(ctx, c).Return(hydra.Visitor{ID: "user-1", Type: hydra.VisitorType_User}, nil)

			visitor, err := svc.VerifyToken(ctx, c)

			So(err, ShouldBeNil)
			So(visitor.ID, ShouldEqual, "user-1")
		})

		Convey("Failed: AuthAccess returns error\n", func() {
			authErr := errors.New("invalid token")
			aa.EXPECT().VerifyToken(ctx, c).Return(hydra.Visitor{}, authErr)

			_, err := svc.VerifyToken(ctx, c)

			So(err, ShouldEqual, authErr)
		})
	})
}
