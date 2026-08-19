// Package interfaces define interfaces.
// @file interfaces.go
// @description: define interface.
package interfaces

//go:generate mockgen -source=interface.go -destination=../mocks/interface.go -package=mocks
import "context"

// App application interface.
type App interface {
	Start() error
	Stop(context.Context)
}

// LogModelOperator log model operator.
type LogModelOperator[T any] interface {
	Logger(context.Context, T)
}

const (
	DefaultBatchSize = 1000 // Default batch size is 1000.
	MaxQuerySize     = 5000 // The maximum number of queries is 5000.
)

// ResourceObjectType resource object type.
type ResourceObjectType string

const (
	ResourceObjectTool     ResourceObjectType = "tool"     // Tools.
	ResourceObjectOperator ResourceObjectType = "operator" // operator.
)

// ResourceDeployType resource deployment type.
type ResourceDeployType string

func (r ResourceDeployType) String() string {
	return string(r)
}

const (
	ResourceDeployTypeMCP ResourceDeployType = "mcp"
)
