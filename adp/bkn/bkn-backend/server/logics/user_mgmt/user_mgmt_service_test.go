// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package user_mgmt

import (
	"context"
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	bmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/mock"
)

func Test_UserMgmtServiceImpl_GetAccountNames(t *testing.T) {
	Convey("Test UserMgmtServiceImpl GetAccountNames\n", t, func() {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		uma := bmock.NewMockUserMgmtAccess(mockCtrl)
		svc := &UserMgmtServiceImpl{
			appSetting: &common.AppSetting{},
			uma:        uma,
		}
		ctx := context.Background()

		Convey("Success: delegates to UserMgmtAccess\n", func() {
			infos := []*interfaces.AccountInfo{{ID: "u1", Name: ""}}
			uma.EXPECT().GetAccountNames(ctx, infos).Return(nil)

			err := svc.GetAccountNames(ctx, infos)
			So(err, ShouldBeNil)
		})

		Convey("Failed: UserMgmtAccess returns error\n", func() {
			infos := []*interfaces.AccountInfo{{ID: "u1", Name: ""}}
			accessErr := errors.New("upstream error")
			uma.EXPECT().GetAccountNames(ctx, infos).Return(accessErr)

			err := svc.GetAccountNames(ctx, infos)
			So(err, ShouldEqual, accessErr)
		})
	})
}
