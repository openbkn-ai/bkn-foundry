// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package user_mgmt

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	rmock "github.com/openbkn-ai/bkn-foundry/comm-go/rest/mock"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"bkn-backend/interfaces"
)

func newTestUserMgmtAccess(baseURL string, httpClient rest.HTTPClient) *userMgmtAccess {
	return &userMgmtAccess{
		httpClient: httpClient,
		bknSafeURL: baseURL,
	}
}

func TestNewUserMgmtAccess(t *testing.T) {
	Convey("Test NewUserMgmtAccess", t, func() {
		access := NewUserMgmtAccess("  http://bkn-safe:3000/  ")

		Convey("Should create a normalized bkn-safe directory adapter", func() {
			impl, ok := access.(*userMgmtAccess)
			So(ok, ShouldBeTrue)
			So(impl.bknSafeURL, ShouldEqual, "http://bkn-safe:3000")
		})
	})
}

func Test_userMgmtAccess_GetAccountNames(t *testing.T) {
	Convey("Test GetAccountNames", t, func() {
		ctx := context.Background()
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		mockHTTPClient := rmock.NewMockHTTPClient(mockCtrl)
		uma := newTestUserMgmtAccess("http://bkn-safe", mockHTTPClient)

		httpUrl := "http://bkn-safe/api/safe/v1/directory/names"

		Convey("Success getting account names", func() {
			accountInfos := []*interfaces.AccountInfo{
				{ID: "user1", Type: interfaces.ACCESSOR_TYPE_USER},
				{ID: "app1", Type: interfaces.ACCESSOR_TYPE_APP},
			}

			response := map[string]any{
				"user_names": []map[string]string{
					{"id": "user1", "name": "User One"},
				},
				"app_names": []map[string]string{
					{"id": "app1", "name": "App One"},
				},
			}
			respData, _ := sonic.Marshal(response)

			mockHTTPClient.EXPECT().
				PostNoUnmarshal(gomock.Any(), httpUrl, gomock.Any(), gomock.Any()).
				Return(http.StatusOK, respData, nil)

			err := uma.GetAccountNames(ctx, accountInfos)
			So(err, ShouldBeNil)
			So(accountInfos[0].Name, ShouldEqual, "User One")
			So(accountInfos[1].Name, ShouldEqual, "App One")
		})

		Convey("Empty account infos", func() {
			err := uma.GetAccountNames(ctx, []*interfaces.AccountInfo{})
			So(err, ShouldBeNil)
		})

		Convey("HTTP request error", func() {
			accountInfos := []*interfaces.AccountInfo{
				{ID: "user1", Type: interfaces.ACCESSOR_TYPE_USER},
			}

			mockHTTPClient.EXPECT().
				PostNoUnmarshal(gomock.Any(), httpUrl, gomock.Any(), gomock.Any()).
				Return(0, []byte(""), errors.New("network error"))

			err := uma.GetAccountNames(ctx, accountInfos)
			So(err, ShouldNotBeNil)
		})

		Convey("Non-200 status code", func() {
			accountInfos := []*interfaces.AccountInfo{
				{ID: "user1", Type: interfaces.ACCESSOR_TYPE_USER},
			}

			mockHTTPClient.EXPECT().
				PostNoUnmarshal(gomock.Any(), httpUrl, gomock.Any(), gomock.Any()).
				Return(http.StatusInternalServerError, []byte("error"), nil)

			err := uma.GetAccountNames(ctx, accountInfos)
			So(err, ShouldNotBeNil)
		})

		Convey("Invalid response format", func() {
			accountInfos := []*interfaces.AccountInfo{
				{ID: "user1", Type: interfaces.ACCESSOR_TYPE_USER},
			}

			mockHTTPClient.EXPECT().
				PostNoUnmarshal(gomock.Any(), httpUrl, gomock.Any(), gomock.Any()).
				Return(http.StatusOK, []byte("invalid json"), nil)

			err := uma.GetAccountNames(ctx, accountInfos)
			So(err, ShouldNotBeNil)
		})
	})
}
