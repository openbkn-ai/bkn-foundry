package toolbox

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/agiledragon/gomonkey/v2"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/metric"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"go.uber.org/mock/gomock"
)

// A toolbox's update time is what the toolbox list shows, so every write to one of its tools
// must move it too (#1217): creating, editing, enabling, disabling and deleting a tool.
// Inside a transaction the toolbox row is touched before any tool row, the order the importer
// locks them in, so the two cannot deadlock on the same toolbox.
const (
	touchUserID = "user-1"
	touchBoxID  = "box-a"
	touchToolID = "tool-a"
)

type touchFixture struct {
	svc        *ToolServiceImpl
	toolBoxDB  *mocks.MockIToolboxDB
	toolDB     *mocks.MockIToolDB
	metadata   *mocks.MockIMetadataService
	dbTx       *mocks.MockDBTx
	tx         *sql.Tx
	rolledBack bool
	committed  bool
}

func newTouchFixture(t *testing.T) *touchFixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	auth := mocks.NewMockIAuthorizationService(ctrl)
	f := &touchFixture{
		toolBoxDB: mocks.NewMockIToolboxDB(ctrl),
		toolDB:    mocks.NewMockIToolDB(ctrl),
		metadata:  mocks.NewMockIMetadataService(ctrl),
		dbTx:      mocks.NewMockDBTx(ctrl),
		tx:        &sql.Tx{},
	}
	rollback := gomonkey.ApplyFunc((*sql.Tx).Rollback, func(*sql.Tx) error { f.rolledBack = true; return nil })
	t.Cleanup(rollback.Reset)
	commit := gomonkey.ApplyFunc((*sql.Tx).Commit, func(*sql.Tx) error { f.committed = true; return nil })
	t.Cleanup(commit.Reset)

	accessor := &interfaces.AuthAccessor{ID: touchUserID}
	auth.EXPECT().GetAccessor(gomock.Any(), touchUserID).Return(accessor, nil).AnyTimes()
	auth.EXPECT().CheckModifyPermission(gomock.Any(), accessor, touchBoxID, gomock.Any()).Return(nil).AnyTimes()
	f.toolBoxDB.EXPECT().SelectToolBox(gomock.Any(), touchBoxID).Return(true, &model.ToolboxDB{
		BoxID:        touchBoxID,
		Name:         "box a",
		MetadataType: string(interfaces.MetadataTypeAPI),
	}, nil).AnyTimes()
	f.dbTx.EXPECT().GetTx(gomock.Any()).Return(f.tx, nil).AnyTimes()

	f.svc = &ToolServiceImpl{
		Logger:          logger.DefaultLogger(),
		DBTx:            f.dbTx,
		AuthService:     auth,
		ToolBoxDB:       f.toolBoxDB,
		ToolDB:          f.toolDB,
		MetadataService: f.metadata,
		AuditLog:        mocks.NewMockLogModelOperator[*metric.AuditLogBuilderParams](ctrl),
	}
	return f
}

func TestDeleteToolBoxReportsCommitFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	db, dbMock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	dbMock.MatchExpectationsInOrder(false)
	t.Cleanup(func() { _ = db.Close() })
	dbMock.ExpectBegin()
	markTx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	dbMock.ExpectCommit()
	dbMock.ExpectBegin()
	deleteTx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	dbMock.ExpectCommit().WillReturnError(errors.New("commit failed"))

	auth := mocks.NewMockIAuthorizationService(ctrl)
	boxDB := mocks.NewMockIToolboxDB(ctrl)
	toolDB := mocks.NewMockIToolDB(ctrl)
	config := mocks.NewMockIIntCompConfigService(ctrl)
	dbTx := mocks.NewMockDBTx(ctrl)
	accessor := &interfaces.AuthAccessor{ID: touchUserID}
	box := &model.ToolboxDB{BoxID: touchBoxID, Name: "box a", MetadataType: string(interfaces.MetadataTypeAPI)}
	auth.EXPECT().GetAccessor(gomock.Any(), touchUserID).Return(accessor, nil)
	boxDB.EXPECT().SelectToolBoxForDelete(gomock.Any(), touchBoxID).Return(true, box, nil)
	boxDB.EXPECT().SelectToolBox(gomock.Any(), touchBoxID).Return(true, box, nil)
	auth.EXPECT().CheckDeletePermission(gomock.Any(), accessor, touchBoxID, gomock.Any()).Return(nil)
	dbTx.EXPECT().GetTx(gomock.Any()).Return(markTx, nil)
	boxDB.EXPECT().UpdateToolBoxStatus(gomock.Any(), markTx, touchBoxID, model.ToolboxStatusDeleting, touchUserID).Return(nil)
	dbTx.EXPECT().GetTx(gomock.Any()).Return(deleteTx, nil)
	toolDB.EXPECT().SelectToolByBoxID(gomock.Any(), touchBoxID).Return(nil, nil)
	boxDB.EXPECT().DeleteToolBox(gomock.Any(), deleteTx, touchBoxID).Return(nil)
	config.EXPECT().DeleteConfig(gomock.Any(), deleteTx, interfaces.ComponentTypeToolBox.String(), touchBoxID).Return(nil)
	auth.EXPECT().DeletePolicy(gomock.Any(), []string{touchBoxID}, gomock.Any()).Return(nil)

	svc := &ToolServiceImpl{
		Logger: logger.DefaultLogger(), DBTx: dbTx, AuthService: auth, ToolBoxDB: boxDB,
		ToolDB: toolDB, MetadataService: mocks.NewMockIMetadataService(ctrl), IntCompConfigSvc: config,
		AuditLog: mocks.NewMockLogModelOperator[*metric.AuditLogBuilderParams](ctrl),
	}
	resp, err := svc.DeleteBoxByID(t.Context(), &interfaces.DeleteBoxReq{UserID: touchUserID, BoxID: touchBoxID})
	if err == nil || resp != nil {
		t.Fatalf("DeleteBoxByID() = (%#v, %v), want commit error", resp, err)
	}
	if err := dbMock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteToolBoxResumesAfterPolicyWasRemoved(t *testing.T) {
	ctrl := gomock.NewController(t)
	db, dbMock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	dbMock.ExpectBegin()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	dbMock.ExpectCommit()

	auth := mocks.NewMockIAuthorizationService(ctrl)
	boxDB := mocks.NewMockIToolboxDB(ctrl)
	toolDB := mocks.NewMockIToolDB(ctrl)
	config := mocks.NewMockIIntCompConfigService(ctrl)
	dbTx := mocks.NewMockDBTx(ctrl)
	accessor := &interfaces.AuthAccessor{ID: touchUserID}
	box := &model.ToolboxDB{BoxID: touchBoxID, Name: "box a", MetadataType: string(interfaces.MetadataTypeAPI),
		Status: model.ToolboxStatusDeleting, UpdateUser: touchUserID}
	auth.EXPECT().GetAccessor(gomock.Any(), touchUserID).Return(accessor, nil)
	boxDB.EXPECT().SelectToolBoxForDelete(gomock.Any(), touchBoxID).Return(true, box, nil)
	auth.EXPECT().DeletePolicy(gomock.Any(), []string{touchBoxID}, gomock.Any()).Return(nil)
	dbTx.EXPECT().GetTx(gomock.Any()).Return(tx, nil)
	toolDB.EXPECT().SelectToolByBoxID(gomock.Any(), touchBoxID).Return(nil, nil)
	boxDB.EXPECT().DeleteToolBox(gomock.Any(), tx, touchBoxID).Return(nil)
	config.EXPECT().DeleteConfig(gomock.Any(), tx, interfaces.ComponentTypeToolBox.String(), touchBoxID).Return(nil)

	svc := &ToolServiceImpl{
		Logger: logger.DefaultLogger(), DBTx: dbTx, AuthService: auth, ToolBoxDB: boxDB,
		ToolDB: toolDB, MetadataService: mocks.NewMockIMetadataService(ctrl), IntCompConfigSvc: config,
		AuditLog: mocks.NewMockLogModelOperator[*metric.AuditLogBuilderParams](ctrl),
	}
	resp, err := svc.DeleteBoxByID(t.Context(), &interfaces.DeleteBoxReq{UserID: touchUserID, BoxID: touchBoxID})
	if err != nil || resp == nil {
		t.Fatalf("DeleteBoxByID() = (%#v, %v), want success", resp, err)
	}
	if err := dbMock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteToolBoxStopsBeforePhysicalDeleteWhenSafeRejects(t *testing.T) {
	ctrl := gomock.NewController(t)
	db, dbMock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	dbMock.MatchExpectationsInOrder(false)
	t.Cleanup(func() { _ = db.Close() })
	dbMock.ExpectBegin()
	markTx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	dbMock.ExpectCommit()
	dbMock.ExpectBegin()
	restoreTx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	dbMock.ExpectCommit()

	auth := mocks.NewMockIAuthorizationService(ctrl)
	boxDB := mocks.NewMockIToolboxDB(ctrl)
	dbTx := mocks.NewMockDBTx(ctrl)
	accessor := &interfaces.AuthAccessor{ID: touchUserID}
	box := &model.ToolboxDB{BoxID: touchBoxID, Name: "box a", Status: string(interfaces.BizStatusPublished),
		MetadataType: string(interfaces.MetadataTypeAPI), UpdateUser: "editor-1", UpdateTime: 123,
		ReleaseUser: "publisher-1", ReleaseTime: 456}
	auth.EXPECT().GetAccessor(gomock.Any(), touchUserID).Return(accessor, nil)
	boxDB.EXPECT().SelectToolBoxForDelete(gomock.Any(), touchBoxID).Return(true, box, nil)
	boxDB.EXPECT().SelectToolBox(gomock.Any(), touchBoxID).Return(true, box, nil)
	auth.EXPECT().CheckDeletePermission(gomock.Any(), accessor, touchBoxID, gomock.Any()).Return(nil)
	dbTx.EXPECT().GetTx(gomock.Any()).Return(markTx, nil)
	boxDB.EXPECT().UpdateToolBoxStatus(gomock.Any(), markTx, touchBoxID,
		model.ToolboxStatusDeleting, touchUserID).Return(nil)
	auth.EXPECT().DeletePolicy(gomock.Any(), []string{touchBoxID}, gomock.Any()).
		Return(httperrors.NewHTTPError(t.Context(), http.StatusConflict, "Public.Conflict"))
	dbTx.EXPECT().GetTx(gomock.Any()).Return(restoreTx, nil)
	boxDB.EXPECT().RestoreToolBoxAfterDeleteConflict(gomock.Any(), restoreTx, box, touchUserID).Return(nil)

	svc := &ToolServiceImpl{
		Logger: logger.DefaultLogger(), DBTx: dbTx, AuthService: auth, ToolBoxDB: boxDB,
		ToolDB: mocks.NewMockIToolDB(ctrl), MetadataService: mocks.NewMockIMetadataService(ctrl),
		IntCompConfigSvc: mocks.NewMockIIntCompConfigService(ctrl),
		AuditLog:         mocks.NewMockLogModelOperator[*metric.AuditLogBuilderParams](ctrl),
	}
	resp, err := svc.DeleteBoxByID(t.Context(), &interfaces.DeleteBoxReq{UserID: touchUserID, BoxID: touchBoxID})
	if err == nil || resp != nil {
		t.Fatalf("DeleteBoxByID() = (%#v, %v), want Safe conflict", resp, err)
	}
	if err := dbMock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func (f *touchFixture) expectTouchInTx(err error) *gomock.Call {
	return f.toolBoxDB.EXPECT().TouchToolBox(gomock.Any(), f.tx, touchBoxID, touchUserID).Return(err)
}

func TestToolWritesTouchTheirToolBox(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T, f *touchFixture) error
	}{
		{
			name: "create",
			run: func(t *testing.T, f *touchFixture) error {
				gomock.InOrder(
					f.expectTouchInTx(nil),
					f.metadata.EXPECT().RegisterMetadata(gomock.Any(), f.tx, gomock.Any()).Return("source-a", nil),
					f.toolDB.EXPECT().InsertTool(gomock.Any(), f.tx, gomock.Any()).Return(touchToolID, nil),
				)
				_, err := f.svc.saveToolToBox(context.Background(),
					&model.ToolDB{BoxID: touchBoxID, UpdateUser: touchUserID}, &model.APIMetadataDB{})
				return err
			},
		},
		{
			name: "enable or disable",
			run: func(t *testing.T, f *touchFixture) error {
				f.toolDB.EXPECT().SelectToolBoxByID(gomock.Any(), touchBoxID, []string{touchToolID}).
					Return([]*model.ToolDB{{ToolID: touchToolID, BoxID: touchBoxID, SourceType: model.SourceTypeOpenAPI}}, nil)
				gomock.InOrder(
					f.expectTouchInTx(nil),
					f.toolDB.EXPECT().UpdateToolStatus(gomock.Any(), f.tx, touchToolID,
						string(interfaces.ToolStatusTypeDisabled), touchUserID).Return(nil),
				)
				_, err := f.svc.UpdateToolStatus(context.Background(), &interfaces.UpdateToolStatusReq{
					UserID: touchUserID, BoxID: touchBoxID,
					ToolStatusList: []*interfaces.ToolStatus{{ToolID: touchToolID, Status: interfaces.ToolStatusTypeDisabled}},
				})
				return err
			},
		},
		{
			name: "delete",
			run: func(t *testing.T, f *touchFixture) error {
				f.toolDB.EXPECT().SelectToolBoxByID(gomock.Any(), touchBoxID, []string{touchToolID}).
					Return([]*model.ToolDB{{ToolID: touchToolID, BoxID: touchBoxID, SourceType: model.SourceTypeOpenAPI, SourceID: "source-a"}}, nil)
				gomock.InOrder(
					f.expectTouchInTx(nil),
					f.metadata.EXPECT().BatchDeleteMetadata(gomock.Any(), f.tx, interfaces.MetadataTypeAPI, []string{"source-a"}).Return(nil),
					f.toolDB.EXPECT().DeleteBoxByIDAndTools(gomock.Any(), f.tx, touchBoxID, []string{touchToolID}).Return(nil),
				)
				_, err := f.svc.DeleteBoxTool(context.Background(), &interfaces.BatchDeleteToolReq{
					UserID: touchUserID, BoxID: touchBoxID, ToolIDs: []string{touchToolID},
				})
				return err
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newTouchFixture(t)
			if err := tc.run(t, f); err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if !f.committed || f.rolledBack {
				t.Fatalf("committed=%v rolledBack=%v, want the tool write and the touch committed together",
					f.committed, f.rolledBack)
			}
		})
	}
}

// Inside a transaction the touch belongs to the tool write: if the toolbox cannot be touched,
// no tool row is written and the transaction is rolled back instead of leaving the time stale.
func TestToolBoxTouchFailureAbortsTheToolWrite(t *testing.T) {
	f := newTouchFixture(t)
	f.toolDB.EXPECT().SelectToolBoxByID(gomock.Any(), touchBoxID, []string{touchToolID}).
		Return([]*model.ToolDB{{ToolID: touchToolID, BoxID: touchBoxID, SourceType: model.SourceTypeOpenAPI}}, nil)
	// UpdateToolStatus is not expected: once called, gomock fails the test.
	f.expectTouchInTx(errors.New("db down"))

	_, err := f.svc.UpdateToolStatus(context.Background(), &interfaces.UpdateToolStatusReq{
		UserID: touchUserID, BoxID: touchBoxID,
		ToolStatusList: []*interfaces.ToolStatus{{ToolID: touchToolID, Status: interfaces.ToolStatusTypeEnabled}},
	})
	if err == nil {
		t.Fatal("err = nil, want the touch failure")
	}
	if f.committed || !f.rolledBack {
		t.Fatalf("committed=%v rolledBack=%v, want the status change rolled back", f.committed, f.rolledBack)
	}
}

// Without a transaction the tool write has already committed, so a failed touch must not turn
// a successful write into a reported failure.
func TestToolBoxTouchFailureOutsideTransactionIsNotReported(t *testing.T) {
	f := newTouchFixture(t)
	f.toolBoxDB.EXPECT().TouchToolBox(gomock.Any(), gomock.Nil(), touchBoxID, touchUserID).Return(errors.New("db down"))
	if err := f.svc.touchToolBox(context.Background(), nil, touchBoxID, touchUserID); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
}

// Deleting a toolbox takes its row before its tool rows, the same order every tool write takes
// them in, so the two cannot deadlock on the same toolbox.
func TestDeleteToolBoxRemovesTheToolBoxRowBeforeItsTools(t *testing.T) {
	f := newTouchFixture(t)
	config := mocks.NewMockIIntCompConfigService(gomock.NewController(t))
	f.svc.IntCompConfigSvc = config
	f.toolDB.EXPECT().SelectToolByBoxID(gomock.Any(), touchBoxID).
		Return([]*model.ToolDB{{ToolID: touchToolID, BoxID: touchBoxID, SourceType: model.SourceTypeOpenAPI, SourceID: "source-a"}}, nil)
	gomock.InOrder(
		f.toolBoxDB.EXPECT().DeleteToolBox(gomock.Any(), f.tx, touchBoxID).Return(nil),
		f.metadata.EXPECT().BatchDeleteMetadata(gomock.Any(), f.tx, interfaces.MetadataTypeAPI, []string{"source-a"}).Return(nil),
		f.toolDB.EXPECT().DeleteBoxByIDAndTools(gomock.Any(), f.tx, touchBoxID, []string{touchToolID}).Return(nil),
		config.EXPECT().DeleteConfig(gomock.Any(), f.tx, interfaces.ComponentTypeToolBox.String(), touchBoxID).Return(nil),
	)
	if err := f.svc.deleteToolBox(context.Background(), f.tx, touchBoxID); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
}
