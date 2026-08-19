package interfaces

import (
	"context"
	"database/sql"
)

//go:generate mockgen -source=logics_intcomp_config.go -destination=../mocks/intcomp_config.go -package=mocks

// ComponentType component type.
type ComponentType string

const (
	// ComponentTypeToolBox toolbox component.
	ComponentTypeToolBox ComponentType = "toolbox"
	// ComponentTypeMCP MCP component.
	ComponentTypeMCP ComponentType = "mcp"
	// ComponentTypeOperator operator component.
	ComponentTypeOperator ComponentType = "operator"
)

func (c ComponentType) String() string {
	return string(c)
}

// IIntCompConfigService built-in component configuration service.
type IIntCompConfigService interface {
	// DeleteConfig deletes the configuration record of the built-in component (the end when the component itself is deleted)
	DeleteConfig(ctx context.Context, tx *sql.Tx, configType, configID string) error
}
