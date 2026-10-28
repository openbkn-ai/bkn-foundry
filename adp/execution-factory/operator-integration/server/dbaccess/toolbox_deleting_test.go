// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package dbaccess

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common/ormhelper"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
)

func TestToolboxWritesDoNotOverwriteDeletingMarker(t *testing.T) {
	tests := []struct {
		name string
		run  func(context.Context, *toolboxDB) error
	}{
		{
			name: "update toolbox",
			run: func(ctx context.Context, db *toolboxDB) error {
				return db.UpdateToolBox(ctx, nil, &model.ToolboxDB{BoxID: "box-1"})
			},
		},
		{
			name: "update status",
			run: func(ctx context.Context, db *toolboxDB) error {
				return db.UpdateToolBoxStatus(ctx, nil, "box-1", "enabled", "user-1")
			},
		},
		{
			name: "touch toolbox",
			run: func(ctx context.Context, db *toolboxDB) error {
				return db.TouchToolBox(ctx, nil, "box-1", "user-1")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sqlDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = sqlDB.Close() })

			query := regexp.QuoteMeta("WHERE f_box_id = ? AND f_status != ?")
			mock.ExpectExec("UPDATE .*" + query).WillReturnResult(sqlmock.NewResult(0, 1))
			db := &toolboxDB{orm: ormhelper.New(sqlDB, "agent_operator_integration")}
			if err := tt.run(t.Context(), db); err != nil {
				t.Fatalf("toolbox write failed: %v", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSkillWritesDoNotOverwriteDeletingMarker(t *testing.T) {
	tests := []struct {
		name string
		run  func(context.Context, *skillRepositoryDB) error
	}{
		{
			name: "update skill",
			run: func(ctx context.Context, db *skillRepositoryDB) error {
				return db.UpdateSkill(ctx, nil, &model.SkillRepositoryDB{SkillID: "skill-1"})
			},
		},
		{
			name: "update status",
			run: func(ctx context.Context, db *skillRepositoryDB) error {
				return db.UpdateSkillStatus(ctx, nil, "skill-1", "offline", "user-1")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sqlDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = sqlDB.Close() })

			query := regexp.QuoteMeta("WHERE f_skill_id = ? AND f_is_deleted = ?")
			mock.ExpectExec("UPDATE .*" + query).WillReturnResult(sqlmock.NewResult(0, 0))
			db := &skillRepositoryDB{orm: ormhelper.New(sqlDB, "agent_operator_integration")}
			if err := tt.run(t.Context(), db); err == nil {
				t.Fatal("skill write succeeded after the deletion marker was set")
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
