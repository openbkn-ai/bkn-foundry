// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"bkn-backend/common"
	"bkn-backend/interfaces"
	bmock "bkn-backend/interfaces/mock"
)

// exportedKN is what the service hands the handler for mode=export: the network plus its
// child types, each carrying the per-item detail summary is meant to leave behind.
func exportedKN() *interfaces.KN {
	return &interfaces.KN{
		ObjectTypes: []*interfaces.ObjectType{{
			ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
				OTID:   "ot_order",
				OTName: "order",
				DataProperties: []*interfaces.DataProperty{{
					Name:                "amount",
					DisplayName:         "amount of the order",
					Type:                "double",
					Comment:             "what the order came to",
					MappedField:         &interfaces.Field{},
					ConditionOperations: []string{"gt", "lt"},
					IndexFeatures:       []interfaces.ObjectTypeIndexFeature{{Type: "keyword", Configured: true}},
				}},
			},
		}},
		ActionTypes: []*interfaces.ActionType{{
			ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{ATID: "at_cancel", ATName: "cancel"},
		}},
	}
}

// The contract says detail_level chooses how much of each concept definition comes back, and
// concept definitions only come back with mode=export. Without that mode there is nothing for
// it to act on, and the two values have to agree byte for byte — which is what a caller
// reporting "detail_level does nothing" was seeing.
func Test_GetKN_DetailLevelOnlyActsOnTheExportedView(t *testing.T) {
	Convey("detail_level has no effect without mode=export", t, func() {
		reset := setGinMode()
		defer reset()

		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		as := bmock.NewMockAuthService(mockCtrl)
		kns := bmock.NewMockKNService(mockCtrl)
		as.EXPECT().VerifyToken(gomock.Any(), gomock.Any()).AnyTimes().Return(hydra.Visitor{}, nil)
		// No mode, so the service returns the network alone, whatever detail_level says.
		kns.EXPECT().GetKNByID(gomock.Any(), "kn1", gomock.Any(), "").Times(2).
			Return(&interfaces.KN{KNID: "kn1", KNName: "sales"}, nil)

		engine := gin.New()
		engine.Use(gin.Recovery())
		MockNewKnowledgeNetworkRestHandler(&common.AppSetting{}, as, kns).RegisterPublic(engine)

		body := func(query string) string {
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet,
				"/api/bkn-backend/v1/knowledge-networks/kn1"+query, nil))
			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			return w.Body.String()
		}

		So(body("?detail_level=summary"), ShouldEqual, body("?detail_level=full"))
	})
}

// With mode=export, summary keeps the skeleton and each property's name, display_name, type and
// comment, and drops the per-item detail a caller fetches from object-types/{ot_ids} when it
// needs it. full is the default and returns everything, so an existing caller sees no change.
func Test_GetKN_ExportedSummaryKeepsTheSkeletonAndDropsTheDetail(t *testing.T) {
	Convey("mode=export with detail_level=summary", t, func() {
		reset := setGinMode()
		defer reset()

		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		as := bmock.NewMockAuthService(mockCtrl)
		kns := bmock.NewMockKNService(mockCtrl)
		as.EXPECT().VerifyToken(gomock.Any(), gomock.Any()).AnyTimes().Return(hydra.Visitor{}, nil)
		// A fresh copy per call: summary trims in place, so a shared one would leak into the
		// second request and make full look slim.
		kns.EXPECT().GetKNByID(gomock.Any(), "kn1", gomock.Any(), interfaces.Mode_Export).Times(2).
			DoAndReturn(func(context.Context, string, string, string) (*interfaces.KN, error) {
				return exportedKN(), nil
			})

		engine := gin.New()
		engine.Use(gin.Recovery())
		MockNewKnowledgeNetworkRestHandler(&common.AppSetting{}, as, kns).RegisterPublic(engine)

		body := func(query string) string {
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet,
				"/api/bkn-backend/v1/knowledge-networks/kn1?mode=export"+query, nil))
			So(w.Result().StatusCode, ShouldEqual, http.StatusOK)
			return w.Body.String()
		}

		summary := body("&detail_level=summary")
		for _, kept := range []string{"ot_order", "amount", "amount of the order", "double", "what the order came to"} {
			So(strings.Contains(summary, kept), ShouldBeTrue)
		}
		So(strings.Contains(summary, "mapped_field"), ShouldBeFalse)
		So(strings.Contains(summary, "condition_operations"), ShouldBeFalse)
		// index_features survives: it says what the bound resource can search by, which is not
		// something summary drops, and the description has to say so rather than assume.
		So(strings.Contains(summary, "index_features"), ShouldBeTrue)
		// Action types are returned whole at either level.
		So(strings.Contains(summary, "at_cancel"), ShouldBeTrue)

		full := body("&detail_level=full")
		So(strings.Contains(full, "mapped_field"), ShouldBeTrue)
		So(strings.Contains(full, "condition_operations"), ShouldBeTrue)
	})
}
