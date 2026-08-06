package interfaces

import (
	"context"
	"database/sql"
)

//go:generate mockgen -source=logics_intcomp_config.go -destination=../mocks/intcomp_config.go -package=mocks

// ComponentType 组件类型
type ComponentType string

const (
	// ComponentTypeToolBox 工具箱组件
	ComponentTypeToolBox ComponentType = "toolbox"
	// ComponentTypeMCP MCP组件
	ComponentTypeMCP ComponentType = "mcp"
	// ComponentTypeOperator 算子组件
	ComponentTypeOperator ComponentType = "operator"
)

func (c ComponentType) String() string {
	return string(c)
}

// IIntCompConfigService 内置组件配置服务
type IIntCompConfigService interface {
	// DeleteConfig 删除内置组件的配置记录（组件本身被删时的收尾）
	DeleteConfig(ctx context.Context, tx *sql.Tx, configType, configID string) error
}
