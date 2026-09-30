// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.

package model_factory

import (
	"context"
	"sync"

	"bkn-backend/interfaces"
)

type defaultModelCacheKey struct{}

type defaultModelCache struct {
	once  sync.Once
	model *interfaces.SmallModel
	err   error
}

// WithDefaultModelCache installs an operation-scoped cache. Nested callers
// share the same cache so a whole-network import resolves the default model at
// most once.
func WithDefaultModelCache(ctx context.Context) context.Context {
	if _, ok := ctx.Value(defaultModelCacheKey{}).(*defaultModelCache); ok {
		return ctx
	}
	return context.WithValue(ctx, defaultModelCacheKey{}, &defaultModelCache{})
}

// GetDefaultModel uses an operation-scoped cache when present and otherwise
// preserves the service's existing behavior.
func GetDefaultModel(ctx context.Context, service interfaces.ModelFactoryService) (*interfaces.SmallModel, error) {
	cache, ok := ctx.Value(defaultModelCacheKey{}).(*defaultModelCache)
	if !ok {
		return service.GetDefaultModel(ctx)
	}
	cache.once.Do(func() {
		cache.model, cache.err = service.GetDefaultModel(ctx)
	})
	return cache.model, cache.err
}
