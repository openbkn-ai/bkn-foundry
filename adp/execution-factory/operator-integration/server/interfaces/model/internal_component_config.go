package model

import (
	"context"
	"database/sql"
)

// InternalComponentConfigDB built-in component configuration table.
//
//go:generate mockgen -source=internal_component_config.go -destination=../../mocks/model_internal_component_config.go -package=mocks
type InternalComponentConfigDB struct {
	ID            string `json:"f_id" db:"f_id"`                         // primary key.
	ComponentType string `json:"f_component_type" db:"f_component_type"` // Component type.
	ComponentID   string `json:"f_component_id" db:"f_component_id"`     // Component ID.
	ConfigVersion string `json:"f_config_version" db:"f_config_version"` // Configuration version.
	ConfigSource  string `json:"f_config_source" db:"f_config_source"`   // Configure source (automatic/manual)
	ProtectedFlag bool   `json:"f_protected_flag" db:"f_protected_flag"` // Manual configuration of protection lock (internal)
}

// IInternalComponentConfigDB built-in component configuration interface.
type IInternalComponentConfigDB interface {
	DeleteConfig(ctx context.Context, tx *sql.Tx, configType, configID string) error                         // Delete configuration.
	SelectConfig(ctx context.Context, configType, configID string) (bool, *InternalComponentConfigDB, error) // Query configuration.
}
