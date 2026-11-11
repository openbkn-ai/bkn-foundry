// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package worker

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	bmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/mock"
)

// fakeVersionAccess answers which networks are versioned. Methods it does not override panic
// through the nil embedded interface, so a test fails on any call it did not plan for.
type fakeVersionAccess struct {
	interfaces.KNVersionAccess
	listed    []string
	listErr   error
	versioned map[string]bool // version state read under the lock
	locks     []string        // "<kn id>:exclusive" or "<kn id>:share"
}

func (f *fakeVersionAccess) ListVersionedKNIDs(context.Context) ([]string, error) {
	return f.listed, f.listErr
}

func (f *fakeVersionAccess) LockNetwork(_ context.Context, tx *sql.Tx, knID string, exclusive bool) (bool, error) {
	if tx == nil {
		return false, errors.New("lock without a transaction")
	}
	mode := "share"
	if exclusive {
		mode = "exclusive"
	}
	f.locks = append(f.locks, knID+":"+mode)
	return true, nil
}

func (f *fakeVersionAccess) GetState(_ context.Context, _ *sql.Tx, knID string, _ bool) (*interfaces.KNVersionState, error) {
	if f.versioned[knID] {
		return &interfaces.KNVersionState{KNID: knID}, nil
	}
	return nil, nil
}

func emptyDataset() *interfaces.DatasetQueryResponse {
	return &interfaces.DatasetQueryResponse{Entries: []map[string]any{}}
}

func TestConceptSyncer_VersionedNetworks(t *testing.T) {
	Convey("Concept sync and versioned networks", t, func() {
		ctx := context.Background()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		db, smock, err := sqlmock.New()
		So(err, ShouldBeNil)
		defer func() { _ = db.Close() }()

		kna := bmock.NewMockKNAccess(ctrl)
		vbs := bmock.NewMockVegaBackendService(ctrl)
		ota := bmock.NewMockObjectTypeAccess(ctrl)
		rta := bmock.NewMockRelationTypeAccess(ctrl)
		ata := bmock.NewMockActionTypeAccess(ctrl)
		cga := bmock.NewMockConceptGroupAccess(ctrl)
		knva := &fakeVersionAccess{}
		cs := &ConceptSyncer{appSetting: &common.AppSetting{}, kna: kna, vbs: vbs,
			ota: ota, rta: rta, ata: ata, cga: cga, knva: knva, db: db}

		expectDefinitionsRead := func(knID, branch string) {
			ota.EXPECT().GetAllObjectTypesByKnID(ctx, knID, branch).Return(map[string]*interfaces.ObjectType{}, nil)
			rta.EXPECT().GetAllRelationTypesByKnID(ctx, knID, branch).Return(map[string]*interfaces.RelationType{}, nil)
			ata.EXPECT().GetAllActionTypesByKnID(ctx, knID, branch).Return(map[string]*interfaces.ActionType{}, nil)
			cga.EXPECT().GetAllConceptGroupsByKnID(ctx, knID, branch).Return(map[string]*interfaces.ConceptGroup{}, nil)
		}

		// The draft, main and v<N> concept indexes of a versioned network are written by
		// definition writes and version operations under the network lock. The syncer
		// rebuilding them from rows it read earlier brought back documents a publish had
		// removed, and its row write could land on a main a publish had just replaced.
		Convey("Leaves every branch of a versioned network alone", func() {
			knva.listed = []string{"kn_v"}
			kna.EXPECT().GetAllKNs(ctx).Return([]*interfaces.KN{
				{KNID: "kn_v", Branch: "main", UpdateTime: 1},
				{KNID: "kn_v", Branch: "draft", UpdateTime: 1},
				{KNID: "kn_v", Branch: "v2", UpdateTime: 1},
				{KNID: "kn_plain", Branch: "main", UpdateTime: 1},
			}, nil)
			// Dataset is empty, so every branch looks out of date; only kn_plain is synced.
			vbs.EXPECT().QueryResourceData(ctx, interfaces.BKN_DATASET_ID, gomock.Any()).Return(emptyDataset(), nil).AnyTimes()
			expectDefinitionsRead("kn_plain", "main")
			smock.ExpectBegin()
			smock.ExpectCommit()
			kna.EXPECT().UpdateKNDetail(ctx, gomock.Not(gomock.Nil()), "kn_plain", "main", gomock.Any()).Return(nil)
			vbs.EXPECT().WriteDatasetDocument(ctx, interfaces.BKN_DATASET_ID, gomock.Any(), gomock.Any()).Return(nil).Times(1)

			So(cs.handleKNs(), ShouldBeNil)
			So(knva.locks, ShouldResemble, []string{"kn_plain:exclusive"})
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		// Without the list the syncer cannot tell which networks to leave alone, so it writes none.
		Convey("Skips the round when the versioned networks cannot be listed", func() {
			knva.listErr = errors.New("db down")
			kna.EXPECT().GetAllKNs(ctx).Return([]*interfaces.KN{{KNID: "kn_plain", Branch: "main"}}, nil)
			vbs.EXPECT().QueryResourceData(ctx, interfaces.BKN_DATASET_ID, gomock.Any()).Return(emptyDataset(), nil).AnyTimes()

			So(cs.handleKNs(), ShouldNotBeNil)
		})

		// Versioning enabled after the round listed the versioned networks: the state read
		// under the lock catches it, and neither the row nor the network document is written.
		Convey("Does not write a network that became versioned during the round", func() {
			knva.versioned = map[string]bool{"kn1": true}
			vbs.EXPECT().QueryResourceData(ctx, interfaces.BKN_DATASET_ID, gomock.Any()).Return(emptyDataset(), nil).AnyTimes()
			expectDefinitionsRead("kn1", "main")
			smock.ExpectBegin()
			smock.ExpectRollback()

			err := cs.handleKnowledgeNetwork(ctx, &interfaces.KN{KNID: "kn1", Branch: "main"}, true)
			So(err, ShouldBeNil)
			So(knva.locks, ShouldResemble, []string{"kn1:exclusive"})
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		// Writing main updates the row the lock is on. A share lock upgraded there can deadlock
		// with a version operation waiting for it with FOR UPDATE, so main is locked exclusively,
		// as version operations and network-row writes do. Other branches lock main in share mode.
		Convey("Locks main exclusively when writing main and in share mode otherwise", func() {
			vbs.EXPECT().QueryResourceData(ctx, interfaces.BKN_DATASET_ID, gomock.Any()).Return(emptyDataset(), nil).AnyTimes()
			vbs.EXPECT().WriteDatasetDocument(ctx, interfaces.BKN_DATASET_ID, gomock.Any(), gomock.Any()).Return(nil).Times(2)
			for _, branch := range []string{"main", "dev"} {
				expectDefinitionsRead("kn1", branch)
				smock.ExpectBegin()
				smock.ExpectCommit()
				kna.EXPECT().UpdateKNDetail(ctx, gomock.Not(gomock.Nil()), "kn1", branch, gomock.Any()).Return(nil)
				So(cs.handleKnowledgeNetwork(ctx, &interfaces.KN{KNID: "kn1", Branch: branch}, true), ShouldBeNil)
			}
			So(knva.locks, ShouldResemble, []string{"kn1:exclusive", "kn1:share"})
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		// A branch deleted after it was listed has nothing to write and no document to add.
		Convey("Skips a branch row that is gone", func() {
			vbs.EXPECT().QueryResourceData(ctx, interfaces.BKN_DATASET_ID, gomock.Any()).Return(emptyDataset(), nil).AnyTimes()
			expectDefinitionsRead("kn1", "dev")
			smock.ExpectBegin()
			smock.ExpectRollback()
			kna.EXPECT().UpdateKNDetail(ctx, gomock.Not(gomock.Nil()), "kn1", "dev", gomock.Any()).Return(common.ErrRowNotFound)

			err := cs.handleKnowledgeNetwork(ctx, &interfaces.KN{KNID: "kn1", Branch: "dev"}, true)
			So(err, ShouldBeNil)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("Returns a row write failure without writing the document", func() {
			vbs.EXPECT().QueryResourceData(ctx, interfaces.BKN_DATASET_ID, gomock.Any()).Return(emptyDataset(), nil).AnyTimes()
			expectDefinitionsRead("kn1", "main")
			smock.ExpectBegin()
			smock.ExpectRollback()
			kna.EXPECT().UpdateKNDetail(ctx, gomock.Not(gomock.Nil()), "kn1", "main", gomock.Any()).Return(errors.New("db error"))

			err := cs.handleKnowledgeNetwork(ctx, &interfaces.KN{KNID: "kn1", Branch: "main"}, true)
			So(err, ShouldNotBeNil)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})
	})
}
