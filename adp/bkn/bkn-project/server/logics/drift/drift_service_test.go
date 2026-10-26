// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drift

import (
	"context"
	"errors"
	"net/http"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

const token = "Bearer test-token"

func Test_DriftService_DriftCheck(t *testing.T) {
	Convey("Test driftService DriftCheck\n", t, func() {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		gs := pmock.NewMockGitStore(mockCtrl)
		bba := pmock.NewMockBknBackendAccess(mockCtrl)
		svc := &driftService{gs: gs, bba: bba}
		ctx := context.Background()
		main := map[string][]byte{
			"network.bkn":              []byte("network"),
			"object_types/order.bkn":   []byte("order v1"),
			"object_types/product.bkn": []byte("product"),
		}

		Convey("Live edits show up as added, changed and removed paths\n", func() {
			live := map[string][]byte{
				"network.bkn":             []byte("network"),
				"object_types/order.bkn":  []byte("order edited in Studio"),
				"relation_types/buys.bkn": []byte("buys"),
			}
			bba.EXPECT().ExportBKN(gomock.Any(), token, "orders").Return(live, nil)
			gs.EXPECT().ReadTree(gomock.Any(), "orders", "main").Return(main, nil)

			report, err := svc.DriftCheck(ctx, token, "orders", "orders")

			So(err, ShouldBeNil)
			So(report.LineageID, ShouldEqual, "orders")
			So(report.KNID, ShouldEqual, "orders")
			So(report.PathDiff, ShouldResemble, interfaces.PathDiff{
				Added:   []string{"relation_types/buys.bkn"},
				Changed: []string{"object_types/order.bkn"},
				Removed: []string{"object_types/product.bkn"},
			})
			So(report.Empty(), ShouldBeFalse)
			So(report.Live, ShouldResemble, live)
		})

		Convey("Identical definitions report no drift\n", func() {
			bba.EXPECT().ExportBKN(gomock.Any(), token, "orders_1-0-0").Return(main, nil)
			gs.EXPECT().ReadTree(gomock.Any(), "orders", "main").Return(main, nil)

			report, err := svc.DriftCheck(ctx, token, "orders", "orders_1-0-0")

			So(err, ShouldBeNil)
			So(report.Empty(), ShouldBeTrue)
		})

		Convey("An export refused by bkn-backend travels back unchanged\n", func() {
			refused := &interfaces.BknBackendError{HTTPCode: http.StatusForbidden}
			bba.EXPECT().ExportBKN(gomock.Any(), token, "orders").Return(nil, refused)

			_, err := svc.DriftCheck(ctx, token, "orders", "orders")

			var backendErr *interfaces.BknBackendError
			So(errors.As(err, &backendErr), ShouldBeTrue)
			So(backendErr.HTTPCode, ShouldEqual, http.StatusForbidden)
		})

		Convey("A failed git read fails the check\n", func() {
			bba.EXPECT().ExportBKN(gomock.Any(), token, "orders").Return(main, nil)
			gs.EXPECT().ReadTree(gomock.Any(), "orders", "main").Return(nil, errors.New("no repository"))

			_, err := svc.DriftCheck(ctx, token, "orders", "orders")

			So(err, ShouldNotBeNil)
		})
	})
}

func Test_DriftService_CommitLiveToMain(t *testing.T) {
	Convey("Test driftService CommitLiveToMain\n", t, func() {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		gs := pmock.NewMockGitStore(mockCtrl)
		svc := &driftService{gs: gs}
		ctx := context.Background()
		author := interfaces.GitAuthor{Name: "u1"}
		report := &interfaces.DriftReport{
			LineageID: "orders",
			KNID:      "orders_1-0-0",
			Live:      map[string][]byte{"network.bkn": []byte("network")},
		}

		Convey("The live set is committed to main with a sync message\n", func() {
			gs.EXPECT().CommitFiles(gomock.Any(), "orders", "main", report.Live,
				"sync live definitions from orders_1-0-0", author).Return("abc123", nil)

			head, err := svc.CommitLiveToMain(ctx, report, author)

			So(err, ShouldBeNil)
			So(head, ShouldEqual, "abc123")
		})

		Convey("A failed commit is reported\n", func() {
			gs.EXPECT().CommitFiles(gomock.Any(), "orders", "main", gomock.Any(), gomock.Any(), author).
				Return("", errors.New("disk full"))

			_, err := svc.CommitLiveToMain(ctx, report, author)

			So(err, ShouldNotBeNil)
		})
	})
}
