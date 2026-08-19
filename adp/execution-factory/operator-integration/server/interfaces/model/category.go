package model

import (
	"context"
	"database/sql"
)

//go:generate mockgen -source=category.go -destination=../../mocks/model_category.go -package=mocks

// Structure corresponding to CategoryDB classification table.
type CategoryDB struct {
	ID           int64  `json:"f_id" db:"f_id"`                       // id
	CategoryID   string `json:"f_category_id" db:"f_category_id"`     // Category ID.
	CategoryName string `json:"f_category_name" db:"f_category_name"` // Category name.
	CreateUser   string `json:"f_create_user" db:"f_create_user"`     // Creator.
	CreateTime   int64  `json:"f_create_time" db:"f_create_time"`     // creation time.
	UpdateUser   string `json:"f_update_user" db:"f_update_user"`     // Editor.
	UpdateTime   int64  `json:"f_update_time" db:"f_update_time"`     // Edit time.
}

// DBCategory classification table database operations.
type DBCategory interface {
	// Insert insert category.
	Insert(ctx context.Context, tx *sql.Tx, category *CategoryDB) (categoryID string, err error)
	// UpdateByID update classification.
	UpdateByID(ctx context.Context, tx *sql.Tx, category *CategoryDB) error
	// SelectList Query category list.
	SelectList(ctx context.Context, tx *sql.Tx) (categoryList []*CategoryDB, err error)
	// SelectListByCategoryIDOrName Query the category list based on category ID or name.
	SelectListByCategoryIDOrName(ctx context.Context, tx *sql.Tx, categoryID string, categoryName string) (categoryList []*CategoryDB, err error)
	// SelectListByCategoryID Query the category list based on the category ID.
	SelectListByCategoryID(ctx context.Context, tx *sql.Tx, categoryID string) (category *CategoryDB, err error)
	// DeleteByCategoryID Delete a category based on category ID.
	DeleteByCategoryID(ctx context.Context, tx *sql.Tx, categoryID string) error
}
