package parsers

import (
	"context"
	"fmt"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/validator"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

// Parser parser interface.
type Parser interface {
	// Type returns the metadata type processed by the parser.
	Type() interfaces.MetadataType
	// Parse parses raw input into metadata.
	Parse(ctx context.Context, input any) ([]interfaces.IMetadataDB, error)
	// GetAllContent Get all content.
	GetAllContent(ctx context.Context, input any) (any, error)
}

// Registry parser registry.
type Registry struct {
	mu      sync.RWMutex
	parsers map[interfaces.MetadataType]Parser
	Logger  interfaces.Logger
}

var (
	mrSync sync.Once
	mr     *Registry
)

// NewRegistry creates a parser registry.
func NewRegistry() *Registry {
	mrSync.Do(func() {
		conf := config.NewConfigLoader()
		mr = &Registry{
			Logger:  conf.GetLogger(),
			parsers: make(map[interfaces.MetadataType]Parser),
		}
		err := mr.Register(&openAPIParser{
			Logger: conf.GetLogger(),
		})
		if err != nil {
			panic(err)
		}
		err = mr.Register(&pythonFunctionParser{
			Logger:    conf.GetLogger(),
			Validator: validator.NewValidator(),
		})
		if err != nil {
			panic(err)
		}
	})
	return mr
}

// Register register parser.
func (r *Registry) Register(parser Parser) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	metaType := parser.Type()
	if _, exists := r.parsers[metaType]; exists {
		return fmt.Errorf("parser for type %s already registered", metaType)
	}

	r.parsers[metaType] = parser
	return nil
}

// Get Get the parser.
func (r *Registry) Get(metaType interfaces.MetadataType) (Parser, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	parser, exists := r.parsers[metaType]
	if !exists {
		return nil, fmt.Errorf("parser for type %s not found", metaType)
	}

	return parser, nil
}

// MustGet gets the parser (panics if it does not exist)
func (r *Registry) MustGet(metaType interfaces.MetadataType) Parser {
	parser, err := r.Get(metaType)
	if err != nil {
		panic(err)
	}
	return parser
}

// List lists all registered parser types.
func (r *Registry) List() []interfaces.MetadataType {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]interfaces.MetadataType, 0, len(r.parsers))
	for t := range r.parsers {
		result = append(result, t)
	}
	return result
}
