package toolbox

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
)

func TestDeleteToolboxReportsSafeAndCommitFailures(t *testing.T) {
	for _, failure := range []string{"safe", "commit"} {
		t.Run(failure, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			db, sqlMock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			sqlMock.ExpectBegin()
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			injected := errors.New("injected " + failure + " failure")
			var safeErr error
			if failure == "safe" {
				safeErr = injected
				sqlMock.ExpectRollback()
			} else {
				sqlMock.ExpectCommit().WillReturnError(injected)
			}
			auth := mocks.NewMockIAuthorizationService(ctrl)
			boxes := mocks.NewMockIToolboxDB(ctrl)
			tools := mocks.NewMockIToolDB(ctrl)
			dbTx := mocks.NewMockDBTx(ctrl)
			configs := mocks.NewMockIIntCompConfigService(ctrl)
			svc := &ToolServiceImpl{AuthService: auth, ToolBoxDB: boxes, ToolDB: tools,
				DBTx: dbTx, IntCompConfigSvc: configs, Logger: logger.DefaultLogger()}
			auth.EXPECT().GetAccessor(gomock.Any(), "user").Return(&interfaces.AuthAccessor{ID: "user"}, nil)
			boxes.EXPECT().SelectToolBox(gomock.Any(), "box").Return(true, &model.ToolboxDB{
				BoxID: "box", MetadataType: string(interfaces.MetadataTypeFunc),
			}, nil).Times(2)
			auth.EXPECT().CheckDeletePermission(gomock.Any(), gomock.Any(), "box", interfaces.AuthResourceTypeFunction).Return(nil)
			dbTx.EXPECT().GetTx(gomock.Any()).Return(tx, nil)
			tools.EXPECT().SelectToolByBoxID(gomock.Any(), "box").Return(nil, nil)
			boxes.EXPECT().DeleteToolBox(gomock.Any(), tx, "box").Return(nil)
			configs.EXPECT().DeleteConfig(gomock.Any(), tx, interfaces.ComponentTypeToolBox.String(), "box").Return(nil)
			auth.EXPECT().DeletePolicy(gomock.Any(), []string{"box"}, interfaces.AuthResourceTypeFunction).Return(safeErr)
			if _, err := svc.DeleteBoxByID(t.Context(), &interfaces.DeleteBoxReq{BoxID: "box", UserID: "user"}); err == nil {
				t.Fatal("DeleteBoxByID() unexpectedly succeeded")
			}
			if err := sqlMock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
