package traceconfig

import (
	"context"
	"sync"
)

// ConfigurationStore is the shared, transactional authority used by the API
// and the separately deployed release controller.
type ConfigurationStore interface {
	Current(context.Context) (Configuration, error)
	Update(context.Context, func(*Configuration) error) (Configuration, error)
}

type memoryConfigurationStore struct {
	mu      sync.Mutex
	current Configuration
}

func NewMemoryConfigurationStore() ConfigurationStore { return &memoryConfigurationStore{} }

func (store *memoryConfigurationStore) Current(_ context.Context) (Configuration, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return cloneConfiguration(store.current), nil
}

func (store *memoryConfigurationStore) Update(_ context.Context, fn func(*Configuration) error) (Configuration, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := fn(&store.current); err != nil {
		return Configuration{}, err
	}
	return cloneConfiguration(store.current), nil
}
