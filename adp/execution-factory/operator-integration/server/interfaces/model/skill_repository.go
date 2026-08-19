package model

import (
	"context"
	"database/sql"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common/ormhelper"
)

//go:generate mockgen -source=skill_repository.go -destination=../../mocks/model_skill_repository.go -package=mocks

// SkillRepositoryDB Skill main table.
type SkillRepositoryDB struct {
	ID           int64  `json:"id" db:"f_id"`
	SkillID      string `json:"skill_id" db:"f_skill_id"`
	Name         string `json:"name" db:"f_name"`
	Description  string `json:"description" db:"f_description"`
	SkillContent string `json:"skill_content" db:"f_skill_content"`
	Version      string `json:"version" db:"f_version"`
	Category     string `json:"f_category" db:"f_category"`
	Status       string `json:"status" db:"f_status"`
	Source       string `json:"source" db:"f_source"`
	ExtendInfo   string `json:"extend_info" db:"f_extend_info"`
	Dependencies string `json:"dependencies" db:"f_dependencies"`
	FileManifest string `json:"file_manifest" db:"f_file_manifest"`
	IsDeleted    bool   `json:"is_deleted" db:"f_is_deleted"`
	CreateTime   int64  `json:"create_time" db:"f_create_time"`
	CreateUser   string `json:"create_user" db:"f_create_user"`
	UpdateTime   int64  `json:"update_time" db:"f_update_time"`
	UpdateUser   string `json:"update_user" db:"f_update_user"`
	DeleteTime   int64  `json:"delete_time" db:"f_delete_time"`
	DeleteUser   string `json:"delete_user" db:"f_delete_user"`
}

// GetBizID Get business ID.
func (s *SkillRepositoryDB) GetBizID() string {
	return s.SkillID
}

// ISkillRepository Skill main table interface.
type ISkillRepository interface {
	InsertSkill(ctx context.Context, tx *sql.Tx, skill *SkillRepositoryDB) (skillID string, err error)
	UpdateSkill(ctx context.Context, tx *sql.Tx, skill *SkillRepositoryDB) error
	// UpdateSkillStatus only updates the business status field f_status, and the status value semantics are consistent with interfaces.BizStatus.
	UpdateSkillStatus(ctx context.Context, tx *sql.Tx, skillID string, status string, updateUser string) error
	// UpdateSkillDeleted updates the deletion flag f_is_deleted, and no longer expresses the deletion process status through status.
	UpdateSkillDeleted(ctx context.Context, tx *sql.Tx, skillID string, isDeleted bool, updateUser string) error
	SelectSkillByID(ctx context.Context, tx *sql.Tx, skillID string) (skill *SkillRepositoryDB, err error)
	SelectSkillListPage(ctx context.Context, tx *sql.Tx, filter map[string]interface{},
		sort *ormhelper.SortParams, cursor *ormhelper.CursorParams) (skills []*SkillRepositoryDB, err error)
	SelectSkillBuildPage(ctx context.Context, tx *sql.Tx, cursorUpdateTime int64, cursorSkillID string, limit int) (skills []*SkillRepositoryDB, err error)
	CountByWhereClause(ctx context.Context, tx *sql.Tx, filter map[string]interface{}) (count int64, err error)
	DeleteSkillByID(ctx context.Context, tx *sql.Tx, skillID string) error
	SelectSkillByName(ctx context.Context, tx *sql.Tx, name string, status []string) (bool, *SkillRepositoryDB, error)
	// SelectSkillListByIDs batch query by skillID (not deleted), empty parameters return an empty list.
	SelectSkillListByIDs(ctx context.Context, skillIDs []string) (skills []*SkillRepositoryDB, err error)
}
