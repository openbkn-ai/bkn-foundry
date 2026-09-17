// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package permission

import (
	"context"
	"errors"
	"testing"

	mqclient "github.com/openbkn-ai/bkn-foundry/comm-go/mq"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"bkn-backend/common"
	"bkn-backend/interfaces"
	bmock "bkn-backend/interfaces/mock"
)

// mockMQClient is a minimal in-test stub for mqclient.OpenBKNMQClient.
type mockMQClient struct {
	pubErr error
}

func (m *mockMQClient) Pub(topic string, msg []byte) error { return m.pubErr }
func (m *mockMQClient) Sub(topic, channel string, handler mqclient.MessageHandler, pollIntervalMilliseconds int64, maxInFlight int, opts ...mqclient.SubOpt) error {
	return nil
}
func (m *mockMQClient) Close() {}

// withAccountInfo attaches an AccountInfo to a context.
func withAccountInfo(ctx context.Context, id, typ string) context.Context {
	return context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: id, Type: typ})
}

// ── PermissionServiceImpl ────────────────────────────────────────────────────

func newTestPermissionImpl(t *testing.T) (*PermissionServiceImpl, *gomock.Controller, *bmock.MockPermissionAccess, *mockMQClient) {
	t.Helper()
	mockCtrl := gomock.NewController(t)
	pa := bmock.NewMockPermissionAccess(mockCtrl)
	mq := &mockMQClient{}
	svc := &PermissionServiceImpl{
		appSetting: &common.AppSetting{},
		pa:         pa,
		mqClient:   mq,
	}
	return svc, mockCtrl, pa, mq
}

func Test_PermissionServiceImpl_CheckPermission(t *testing.T) {
	Convey("Test PermissionServiceImpl CheckPermission\n", t, func() {
		svc, mockCtrl, pa, _ := newTestPermissionImpl(t)
		defer mockCtrl.Finish()

		resource := interfaces.PermissionResource{Type: "kn", ID: "kn1"}
		ops := []string{"read"}

		Convey("Failed: missing account info in context uses the Chinese catalog", func() {
			err := svc.CheckPermission(rest.WithLanguage(context.Background(), rest.SimplifiedChinese), resource, ops)

			httpErr, ok := err.(*rest.HTTPError)
			So(ok, ShouldBeTrue)
			So(httpErr.BaseError.ErrorDetails, ShouldEqual, "\u5e10\u6237\u4fe1\u606f\u4e0d\u53ef\u7528\uff0c\u8bf7\u91cd\u65b0\u767b\u5f55\u3002")
		})

		Convey("Success: pa returns true\n", func() {
			ctx := withAccountInfo(context.Background(), "u1", "user")
			pa.EXPECT().CheckPermissions(gomock.Any(), gomock.Any()).Return(interfaces.PermissionChecksResponse{Allowed: true}, nil)

			err := svc.CheckPermission(ctx, resource, ops)
			So(err, ShouldBeNil)
		})

		Convey("Failed: pa returns false uses the English permission detail", func() {
			ctx := rest.WithLanguage(withAccountInfo(context.Background(), "u1", "user"), rest.AmericanEnglish)
			pa.EXPECT().CheckPermissions(gomock.Any(), gomock.Any()).Return(interfaces.PermissionChecksResponse{Allowed: false}, nil)

			err := svc.CheckPermission(ctx, resource, ops)

			httpErr, ok := err.(*rest.HTTPError)
			So(ok, ShouldBeTrue)
			So(httpErr.BaseError.ErrorDetails, ShouldEqual, "You do not have permission to perform this operation.")
		})

		Convey("Failed: pa returns error\n", func() {
			ctx := withAccountInfo(context.Background(), "u1", "user")
			pa.EXPECT().CheckPermissions(gomock.Any(), gomock.Any()).Return(interfaces.PermissionChecksResponse{}, errors.New("access error"))

			err := svc.CheckPermission(ctx, resource, ops)
			So(err, ShouldNotBeNil)
		})
	})
}

func Test_PermissionServiceImpl_RequireFullPropertyAccess(t *testing.T) {
	ctx := withAccountInfo(context.Background(), "u1", "user")
	tests := []struct {
		name       string
		level      string
		responseID string
		wantStatus int
	}{
		{name: "full access", level: "full", responseID: "kn1/orders"},
		{name: "masked access is denied", level: "masked", responseID: "kn1/orders", wantStatus: 403},
		{name: "unknown level fails closed", level: "unknown", responseID: "kn1/orders", wantStatus: 500},
		{name: "mismatched object fails closed", level: "full", responseID: "kn1/other", wantStatus: 500},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			svc, _, pa, _ := newTestPermissionImpl(t)
			pa.EXPECT().ResolvePropertyLevels(gomock.Any(), interfaces.PropertyLevelsRequest{
				AccessorID: "u1",
				Items: []interfaces.PropertyLevelsRequestItem{{
					ObjectTypeRef: "kn1/orders",
					Properties:    []string{"amount", "region"},
				}},
			}).Return(interfaces.PropertyLevelsResponse{Entries: []interfaces.PropertyLevelsDecisionEntry{{
				ObjectTypeRef: test.responseID,
				Properties: []interfaces.PropertyAccessDecision{
					{Name: "amount", Level: test.level},
					{Name: "region", Level: "full"},
				},
			}}}, nil)

			err := svc.RequireFullPropertyAccess(ctx, "kn1/orders", []string{"region", "amount", "amount"})
			if test.wantStatus == 0 {
				if err != nil {
					t.Fatalf("RequireFullPropertyAccess() error = %v", err)
				}
				return
			}
			httpErr, ok := err.(*rest.HTTPError)
			if !ok || httpErr.HTTPCode != test.wantStatus {
				t.Fatalf("RequireFullPropertyAccess() error = %#v, want HTTP %d", err, test.wantStatus)
			}
		})
	}
}

func Test_PermissionServiceImpl_FilterFullPropertyAccess(t *testing.T) {
	svc, _, pa, _ := newTestPermissionImpl(t)
	ctx := withAccountInfo(context.Background(), "u1", "user")
	pa.EXPECT().ResolvePropertyLevels(gomock.Any(), gomock.Any()).Return(
		interfaces.PropertyLevelsResponse{Entries: []interfaces.PropertyLevelsDecisionEntry{{
			ObjectTypeRef: "kn1/orders",
			Properties: []interfaces.PropertyAccessDecision{
				{Name: "amount", Level: "masked"},
				{Name: "region", Level: "full"},
			},
		}}}, nil)

	full, err := svc.FilterFullPropertyAccess(ctx, "kn1/orders", []string{"region", "amount"})
	if err != nil {
		t.Fatalf("FilterFullPropertyAccess() error = %v", err)
	}
	if len(full) != 1 || full[0] != "region" {
		t.Fatalf("FilterFullPropertyAccess() = %v", full)
	}
}

func Test_PermissionServiceImpl_FilterVisiblePropertyAccess(t *testing.T) {
	svc, _, pa, _ := newTestPermissionImpl(t)
	ctx := withAccountInfo(context.Background(), "u1", "user")
	pa.EXPECT().ResolvePropertyLevels(gomock.Any(), gomock.Any()).Return(
		interfaces.PropertyLevelsResponse{Entries: []interfaces.PropertyLevelsDecisionEntry{{
			ObjectTypeRef: "kn1/orders",
			Properties: []interfaces.PropertyAccessDecision{
				{Name: "amount", Level: "masked"},
				{Name: "region", Level: "schema"},
				{Name: "secret", Level: "none"},
				{Name: "total", Level: "full"},
			},
		}}}, nil)

	visible, err := svc.FilterVisiblePropertyAccess(ctx, "kn1/orders", []string{"region", "secret", "total", "amount"})
	if err != nil {
		t.Fatalf("FilterVisiblePropertyAccess() error = %v", err)
	}
	if len(visible) != 3 || visible[0] != "amount" || visible[1] != "region" || visible[2] != "total" {
		t.Fatalf("FilterVisiblePropertyAccess() = %v", visible)
	}
}

// Every level comes back as bkn-safe decided it, none included, from one
// request for the object type.
func Test_PermissionServiceImpl_ResolvePropertyAccessLevels(t *testing.T) {
	svc, _, pa, _ := newTestPermissionImpl(t)
	ctx := withAccountInfo(context.Background(), "u1", "user")
	pa.EXPECT().ResolvePropertyLevels(gomock.Any(), interfaces.PropertyLevelsRequest{
		AccessorID: "u1",
		Items: []interfaces.PropertyLevelsRequestItem{{
			ObjectTypeRef: "kn1/orders",
			Properties:    []string{"amount", "email", "id", "region"},
		}},
	}).Times(1).Return(interfaces.PropertyLevelsResponse{Entries: []interfaces.PropertyLevelsDecisionEntry{{
		ObjectTypeRef: "kn1/orders",
		Properties: []interfaces.PropertyAccessDecision{
			{Name: "amount", Level: "masked"},
			{Name: "email", Level: "none"},
			{Name: "id", Level: "full"},
			{Name: "region", Level: "schema"},
		},
	}}}, nil)

	levels, err := svc.ResolvePropertyAccessLevels(ctx, "kn1/orders", []string{"region", "id", "email", "amount", "id"})
	if err != nil {
		t.Fatalf("ResolvePropertyAccessLevels() error = %v", err)
	}
	want := map[string]string{"amount": "masked", "email": "none", "id": "full", "region": "schema"}
	if len(levels) != len(want) {
		t.Fatalf("ResolvePropertyAccessLevels() = %v, want %v", levels, want)
	}
	for property, level := range want {
		if levels[property] != level {
			t.Fatalf("ResolvePropertyAccessLevels() = %v, want %v", levels, want)
		}
	}
}

func Test_PermissionServiceImpl_CreateResources(t *testing.T) {
	Convey("Test PermissionServiceImpl CreateResources\n", t, func() {
		svc, mockCtrl, pa, _ := newTestPermissionImpl(t)
		defer mockCtrl.Finish()

		resources := []interfaces.PermissionResource{{Type: "kn", ID: "kn1"}}
		ops := []string{"read"}

		Convey("Failed: missing account info\n", func() {
			err := svc.CreateResources(context.Background(), resources, ops)
			So(err, ShouldNotBeNil)
		})

		Convey("Success: pa.CreateResources returns nil\n", func() {
			ctx := withAccountInfo(context.Background(), "u1", "user")
			pa.EXPECT().CreateResources(gomock.Any(), gomock.Any()).Return(nil)

			err := svc.CreateResources(ctx, resources, ops)
			So(err, ShouldBeNil)
		})

		Convey("Failed: pa.CreateResources returns error\n", func() {
			ctx := withAccountInfo(context.Background(), "u1", "user")
			pa.EXPECT().CreateResources(gomock.Any(), gomock.Any()).Return(errors.New("create failed"))

			err := svc.CreateResources(ctx, resources, ops)
			So(err, ShouldNotBeNil)
		})
	})
}

func Test_PermissionServiceImpl_DeleteResources(t *testing.T) {
	Convey("Test PermissionServiceImpl DeleteResources\n", t, func() {
		svc, mockCtrl, pa, _ := newTestPermissionImpl(t)
		defer mockCtrl.Finish()

		Convey("Empty IDs: returns nil without calling pa\n", func() {
			err := svc.DeleteResources(context.Background(), "kn", []string{})
			So(err, ShouldBeNil)
		})

		Convey("Success: pa.DeleteResources returns nil\n", func() {
			ctx := context.Background()
			pa.EXPECT().DeleteResources(gomock.Any(), gomock.Any()).Return(nil)

			err := svc.DeleteResources(ctx, "kn", []string{"kn1"})
			So(err, ShouldBeNil)
		})

		Convey("Failed: pa.DeleteResources returns error\n", func() {
			ctx := context.Background()
			pa.EXPECT().DeleteResources(gomock.Any(), gomock.Any()).Return(errors.New("delete failed"))

			err := svc.DeleteResources(ctx, "kn", []string{"kn1"})
			So(err, ShouldNotBeNil)
		})
	})
}

func Test_PermissionServiceImpl_FilterResources(t *testing.T) {
	Convey("Test PermissionServiceImpl FilterResources\n", t, func() {
		svc, mockCtrl, pa, _ := newTestPermissionImpl(t)
		defer mockCtrl.Finish()

		Convey("Failed: missing account info\n", func() {
			result, err := svc.FilterVisibleResourcesWithOperations(context.Background(), "kn", []string{"kn1"}, []string{"read"})
			So(err, ShouldNotBeNil)
			So(result, ShouldBeNil)
		})

		Convey("Success: returns resource ops map\n", func() {
			ctx := withAccountInfo(context.Background(), "u1", "user")
			paResult := map[string]interfaces.PermissionResourceOps{
				"kn1": {
					ResourceID: "kn1", Operations: []string{"read"},
				},
			}
			pa.EXPECT().FilterResources(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, filter interfaces.PermissionResourcesFilter) (map[string]interfaces.PermissionResourceOps, error) {
					So(filter.Operations, ShouldResemble, []string{"read"})
					So(filter.IncludeOperations, ShouldBeTrue)
					return paResult, nil
				})

			result, err := svc.FilterVisibleResourcesWithOperations(ctx, "kn", []string{"kn1"}, []string{"read"})
			So(err, ShouldBeNil)
			So(result["kn1"].ResourceID, ShouldEqual, "kn1")
		})

		Convey("Success: pure filtering disables operation projection\n", func() {
			ctx := withAccountInfo(context.Background(), "u1", "user")
			pa.EXPECT().FilterResources(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, filter interfaces.PermissionResourcesFilter) (map[string]interfaces.PermissionResourceOps, error) {
					So(filter.Operations, ShouldResemble, []string{"read"})
					So(filter.IncludeOperations, ShouldBeFalse)
					return map[string]interfaces.PermissionResourceOps{
						"kn1": {ResourceID: "kn1"},
					}, nil
				})

			result, err := svc.FilterVisibleResources(ctx, "kn", []string{"kn1"}, []string{"read"})
			So(err, ShouldBeNil)
			So(result["kn1"].Operations, ShouldBeEmpty)
		})

		Convey("Failed: pa.FilterResources returns error\n", func() {
			ctx := withAccountInfo(context.Background(), "u1", "user")
			pa.EXPECT().FilterResources(gomock.Any(), gomock.Any()).Return(nil, errors.New("filter error"))

			result, err := svc.FilterVisibleResourcesWithOperations(ctx, "kn", []string{"kn1"}, []string{"read"})
			So(err, ShouldNotBeNil)
			So(result, ShouldBeNil)
		})

		Convey("Failed: response contains an unrequested resource\n", func() {
			ctx := withAccountInfo(context.Background(), "u1", "user")
			pa.EXPECT().FilterResources(gomock.Any(), gomock.Any()).Return(map[string]interfaces.PermissionResourceOps{
				"kn2": {ResourceID: "kn2", Operations: []string{"read"}},
			}, nil)

			result, err := svc.FilterVisibleResourcesWithOperations(ctx, "kn", []string{"kn1"}, []string{"read"})
			So(err, ShouldNotBeNil)
			So(result, ShouldBeNil)
		})
	})
}

func Test_PermissionServiceImpl_UpdateResource(t *testing.T) {
	Convey("Test PermissionServiceImpl UpdateResource\n", t, func() {
		svc, mockCtrl, _, mq := newTestPermissionImpl(t)
		defer mockCtrl.Finish()

		Convey("Success: mqClient.Pub returns nil\n", func() {
			err := svc.UpdateResource(context.Background(), interfaces.PermissionResource{Type: "kn", ID: "kn1", Name: "Test"})
			So(err, ShouldBeNil)
		})

		Convey("Failed: mqClient.Pub returns error\n", func() {
			mq.pubErr = errors.New("mq unavailable")
			err := svc.UpdateResource(context.Background(), interfaces.PermissionResource{Type: "kn", ID: "kn1", Name: "Test"})
			So(err, ShouldNotBeNil)
		})
	})
}
