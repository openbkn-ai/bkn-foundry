// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package business_system

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	rmock "github.com/openbkn-ai/bkn-foundry/comm-go/rest/mock"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"bkn-backend/common"
)

func newTestBusinessSystemAccess(appSetting *common.AppSetting, httpClient rest.HTTPClient) *businessSystemAccess {
	return &businessSystemAccess{
		appSetting: appSetting,
		httpClient: httpClient,
		bsUrl:      appSetting.BusinessSystemUrl,
	}
}

func TestNewBusinessSystemAccess(t *testing.T) {
	Convey("Test NewBusinessSystemAccess", t, func() {
		appSetting := &common.AppSetting{
			BusinessSystemUrl: "http://test-bs",
		}

		access1 := NewBusinessSystemAccess(appSetting)
		access2 := NewBusinessSystemAccess(appSetting)

		Convey("Should return singleton instance", func() {
			So(access1, ShouldNotBeNil)
			So(access2, ShouldEqual, access1)
		})
	})
}

func TestBusinessSystemAccessUsesEffectiveLocale(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(rest.AcceptLanguageHeader); got != rest.AmericanEnglish {
			t.Errorf("Accept-Language = %q, want %q", got, rest.AmericanEnglish)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	bsa := newTestBusinessSystemAccess(&common.AppSetting{BusinessSystemUrl: server.URL},
		rest.NewHTTPClientWithRawClient(server.Client()))
	ctx := rest.WithLanguage(context.Background(), rest.AmericanEnglish)
	if err := bsa.BindResource(ctx, "business-system-1", "resource-1", "asset"); err != nil {
		t.Fatalf("BindResource() error = %v", err)
	}
}

func Test_businessSystemAccess_BindResource(t *testing.T) {
	Convey("Test BindResource", t, func() {
		ctx := context.Background()
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		appSetting := &common.AppSetting{
			BusinessSystemUrl: "http://test-bs",
		}
		mockHTTPClient := rmock.NewMockHTTPClient(mockCtrl)
		bsa := newTestBusinessSystemAccess(appSetting, mockHTTPClient)

		bdID := "bd1"
		rid := "r1"
		rtype := "type1"

		Convey("Success binding resource", func() {
			mockHTTPClient.EXPECT().
				PostNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(http.StatusOK, []byte(""), nil)

			err := bsa.BindResource(ctx, bdID, rid, rtype)
			So(err, ShouldBeNil)
		})

		Convey("HTTP request error", func() {
			mockHTTPClient.EXPECT().
				PostNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(0, []byte(""), errors.New("network error"))

			err := bsa.BindResource(ctx, bdID, rid, rtype)
			So(err, ShouldNotBeNil)
		})

		Convey("Non-200 status code", func() {
			mockHTTPClient.EXPECT().
				PostNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(http.StatusInternalServerError, []byte("error"), nil)

			err := bsa.BindResource(ctx, bdID, rid, rtype)
			So(err, ShouldNotBeNil)
		})
	})
}

func Test_businessSystemAccess_UnbindResource(t *testing.T) {
	Convey("Test UnbindResource", t, func() {
		ctx := context.Background()
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		appSetting := &common.AppSetting{
			BusinessSystemUrl: "http://test-bs",
		}
		mockHTTPClient := rmock.NewMockHTTPClient(mockCtrl)
		bsa := newTestBusinessSystemAccess(appSetting, mockHTTPClient)

		bdID := "bd1"
		rid := "r1"
		rtype := "type1"
		// httpUrl := "http://test-bs/resource?bd_id=bd1&id=r1&type=type1"

		Convey("Success unbinding resource", func() {
			mockHTTPClient.EXPECT().
				DeleteNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(http.StatusOK, []byte(""), nil)

			err := bsa.UnbindResource(ctx, bdID, rid, rtype)
			So(err, ShouldBeNil)
		})

		Convey("HTTP request error", func() {
			mockHTTPClient.EXPECT().
				DeleteNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(0, []byte(""), errors.New("network error"))

			err := bsa.UnbindResource(ctx, bdID, rid, rtype)
			So(err, ShouldNotBeNil)
		})

		Convey("Non-200 status code", func() {
			mockHTTPClient.EXPECT().
				DeleteNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(http.StatusInternalServerError, []byte("error"), nil)

			err := bsa.UnbindResource(ctx, bdID, rid, rtype)
			So(err, ShouldNotBeNil)
		})
	})
}
