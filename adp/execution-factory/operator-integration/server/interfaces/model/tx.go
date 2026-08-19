// Package model defines database operation interface.
// @file tx.go
// @description: Define database transaction operation interface.
package model

//go:generate mockgen -source=tx.go -destination=../../mocks/model_tx.go -package=mocks
import (
	"context"
	"database/sql"
)

type DBTx interface {
	GetTx(ctx context.Context) (*sql.Tx, error)
}
