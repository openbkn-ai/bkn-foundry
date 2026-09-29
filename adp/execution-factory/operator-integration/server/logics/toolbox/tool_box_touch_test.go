package toolbox

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/metric"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
	"go.uber.org/mock/gomock"
)

// A toolbox's update time is what the toolbox list shows, so every write to one of its tools
// must move it too (#1217): creating, editing, enabling, disabling and deleting a tool.
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
				f.metadata.EXPECT().RegisterMetadata(gomock.Any(), f.tx, gomock.Any()).Return("source-a", nil)
				f.toolDB.EXPECT().InsertTool(gomock.Any(), f.tx, gomock.Any()).Return(touchToolID, nil)
				f.expectTouchInTx(nil)
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
				f.toolDB.EXPECT().UpdateToolStatus(gomock.Any(), f.tx, touchToolID,
					string(interfaces.ToolStatusTypeDisabled), touchUserID).Return(nil)
				f.expectTouchInTx(nil)
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
					Return([]*model.ToolDB{{ToolID: touchToolID, BoxID: touchBoxID, SourceType: model.SourceTypeOperator}}, nil)
				f.toolDB.EXPECT().DeleteBoxByIDAndTools(gomock.Any(), f.tx, touchBoxID, []string{touchToolID}).Return(nil)
				f.expectTouchInTx(nil)
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
// the tool change is rolled back instead of leaving the toolbox time stale.
func TestToolBoxTouchFailureRollsBackTheToolWrite(t *testing.T) {
	f := newTouchFixture(t)
	f.toolDB.EXPECT().SelectToolBoxByID(gomock.Any(), touchBoxID, []string{touchToolID}).
		Return([]*model.ToolDB{{ToolID: touchToolID, BoxID: touchBoxID, SourceType: model.SourceTypeOpenAPI}}, nil)
	f.toolDB.EXPECT().UpdateToolStatus(gomock.Any(), f.tx, touchToolID,
		string(interfaces.ToolStatusTypeEnabled), touchUserID).Return(nil)
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

// Deleting an operator disables the tools built from it; each toolbox that lost an enabled tool
// is touched once, and a toolbox whose tool was already disabled is left alone.
func TestOperatorDeleteEventTouchesToolBoxesOfDisabledTools(t *testing.T) {
	f := newTouchFixture(t)
	const operatorID = "operator-1"
	f.toolDB.EXPECT().SelectToolBySource(gomock.Any(), model.SourceTypeOperator, operatorID).Return([]*model.ToolDB{
		{ToolID: "t1", BoxID: "box-a", Status: string(interfaces.ToolStatusTypeEnabled)},
		{ToolID: "t2", BoxID: "box-a", Status: string(interfaces.ToolStatusTypeEnabled)},
		{ToolID: "t3", BoxID: "box-b", Status: string(interfaces.ToolStatusTypeEnabled)},
		{ToolID: "t4", BoxID: "box-c", Status: string(interfaces.ToolStatusTypeDisabled)},
	}, nil)
	f.toolDB.EXPECT().UpdateToolStatus(gomock.Any(), gomock.Nil(), gomock.Any(),
		interfaces.ToolStatusTypeDisabled.String(), touchUserID).Return(nil).Times(3)
	var touched []string
	f.toolBoxDB.EXPECT().TouchToolBox(gomock.Any(), gomock.Nil(), gomock.Any(), touchUserID).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, boxID, _ string) error {
			touched = append(touched, boxID)
			return nil
		}).AnyTimes()

	err := f.svc.HandleOperatorDeleteEvent(context.Background(),
		[]byte(`{"operator_id":"`+operatorID+`","update_user":"`+touchUserID+`"}`))
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	sort.Strings(touched)
	if len(touched) != 2 || touched[0] != "box-a" || touched[1] != "box-b" {
		t.Fatalf("touched toolboxes %v, want [box-a box-b]", touched)
	}
}
