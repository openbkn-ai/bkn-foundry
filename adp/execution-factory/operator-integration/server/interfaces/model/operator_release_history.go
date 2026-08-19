package model

import (
	"context"
	"database/sql"
)

// OperatorReleaseHistoryDB operator release history table.
//
//go:generate mockgen -source=operator_release_history.go -destination=../../mocks/model_operator_release_history.go -package=mocks
type OperatorReleaseHistoryDB struct {
	ID              int64  `json:"id" db:"f_id"`                             // Primary key ID.
	OpID            string `json:"op_id" db:"f_op_id"`                       // Operator ID.
	MetadataVersion string `json:"metadata_version" db:"f_metadata_version"` // metadata version.
	MetadataType    string `json:"metadata_type" db:"f_metadata_type"`       // metadata type.
	OpRelease       string `json:"op_release" db:"f_op_release"`             // Operator releases information.
	Tag             int    `json:"tag" db:"f_tag"`                           // version.
	CreateTime      int64  `json:"create_time" db:"f_create_time"`           // creation time.
	UpdateTime      int64  `json:"update_time" db:"f_update_time"`           // Update time.
	CreateUser      string `json:"create_user" db:"f_create_user"`           // Create user.
	UpdateUser      string `json:"update_user" db:"f_update_user"`           // Update user.
}

// IOperatorReleaseHistoryDB operator release history table operation interface.
type IOperatorReleaseHistoryDB interface {
	Insert(ctx context.Context, tx *sql.Tx, historyDB *OperatorReleaseHistoryDB) (err error)
	DeleteByOpID(ctx context.Context, tx *sql.Tx, opID string) error
	SelectByOpID(ctx context.Context, opID string) (histories []*OperatorReleaseHistoryDB, err error)
	BatchDeleteByID(ctx context.Context, tx *sql.Tx, ids []int64) error
	SelectByOpIDAndMetdata(ctx context.Context, opID, metadataVersion string) (has bool, historyDB *OperatorReleaseHistoryDB, err error)
	SelectByOpIDAndTag(ctx context.Context, opID string, tag int) (has bool, historyDB *OperatorReleaseHistoryDB, err error)
	UpdateReleaseHistoryByID(ctx context.Context, tx *sql.Tx, historyDB *OperatorReleaseHistoryDB) error
}
