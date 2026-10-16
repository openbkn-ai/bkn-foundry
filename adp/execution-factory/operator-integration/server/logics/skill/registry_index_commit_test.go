package skill

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
	"go.uber.org/mock/gomock"
)

func TestSkillIndexChangesRequireCommittedSource(t *testing.T) {
	for _, tc := range []struct {
		name                          string
		rollback, commitFails, remove bool
	}{
		{name: "publish commit"}, {name: "purge commit", remove: true}, {name: "write rollback", rollback: true, remove: true}, {name: "commit failure", commitFails: true, remove: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			mock.ExpectBegin()
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			commitCompleted := false
			if tc.rollback {
				mock.ExpectRollback()
			} else if tc.commitFails {
				mock.ExpectCommit().WillReturnError(errors.New("commit failed"))
			} else {
				mock.ExpectCommit()
			}
			ctrl := gomock.NewController(t)
			index := mocks.NewMockSkillIndexSyncService(ctrl)
			notified := false
			registry := &skillRegistry{indexSync: index, Logger: logger.DefaultLogger(), notifyIndexSync: func() {
				if err := mock.ExpectationsWereMet(); err != nil {
					t.Errorf("notified before commit: %v", err)
				}
				notified = true
			}}
			if tc.remove && !tc.rollback && !tc.commitFails {
				index.EXPECT().DeleteSkill(gomock.Any(), "skill").DoAndReturn(func(ctx context.Context, _ string) error {
					if err := mock.ExpectationsWereMet(); err != nil {
						t.Errorf("purged before commit: %v", err)
					}
					deadline, ok := ctx.Deadline()
					if !ok || time.Until(deadline) > skillIndexOperationTimeout {
						t.Error("purge lacks bounded context")
					}
					if ctx.Err() != nil {
						t.Error("purge inherited canceled request")
					}
					commitCompleted = true
					return nil
				})
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			var writeErr error
			if tc.rollback {
				writeErr = errors.New("write failed")
			}
			registry.finishIndexedSkillWrite(ctx, tx, &writeErr, "skill", tc.remove)
			wantNotify := !tc.rollback && !tc.commitFails
			if notified != wantNotify {
				t.Errorf("notified=%v want=%v", notified, wantNotify)
			}
			if tc.remove && wantNotify && !commitCompleted {
				t.Error("committed purge missing")
			}
			if tc.commitFails && writeErr == nil {
				t.Error("commit failure was swallowed")
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
