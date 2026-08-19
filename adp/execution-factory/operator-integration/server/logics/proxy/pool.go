package proxy

import (
	"net/http"
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

const (
	// Cleanup interval.
	cleanupInterval = 1 * time.Minute
)

// Client key (used to distinguish different client instances)
type clientKey struct {
	ExecutionMode interfaces.ExecutionMode `json:"execution_mode,omitempty"`
	StreamingMode interfaces.StreamingMode `json:"streaming_mode,omitempty"`
	Timeout       time.Duration            `json:"timeout,omitempty"`
}

// GetClientKey Gets the client key.
func GetClientKey(executionMode interfaces.ExecutionMode, streamingMode interfaces.StreamingMode, timeout time.Duration) clientKey {
	return clientKey{
		ExecutionMode: executionMode,
		StreamingMode: streamingMode,
		Timeout:       timeout,
	}
}

// PoolConfig connection pool configuration.
type PoolConfig struct {
	MaxClients     int           // Maximum number of clients.
	MaxTimeout     time.Duration // Maximum timeout.
	DefaultTimeout time.Duration // Default timeout.
	ClientLifetime time.Duration // Client lifecycle.
}

// ProxyClient proxy client information.
type ProxyClient struct {
	*http.Client
	IsStreaming   bool
	StreamingMode interfaces.StreamingMode
	CreateAt      time.Time
}

// clientPool proxy client pool.
// Client pool structure.
type clientPool struct {
	logger      interfaces.Logger
	mu          sync.Mutex
	clients     map[clientKey]*ProxyClient
	config      PoolConfig
	stopCleanup chan struct{}
}

var (
	clientPoolInstance *clientPool
	clientPoolOnce     sync.Once
)

// NewClientPool creates a new client pool.
func NewClientPool() *clientPool {
	clientPoolOnce.Do(func() {
		conf := config.NewConfigLoader()
		poolConfig := PoolConfig{
			MaxClients:     conf.ProxyModuleConfig.MaxClients,
			MaxTimeout:     time.Duration(conf.ProxyModuleConfig.MaxTimeout) * time.Second,
			DefaultTimeout: time.Duration(conf.ProxyModuleConfig.DefaultTimeout) * time.Second,
			ClientLifetime: time.Duration(conf.ProxyModuleConfig.ClientLifetime) * time.Second,
		}
		clientPoolInstance = &clientPool{
			mu:          sync.Mutex{},
			logger:      conf.GetLogger(),
			clients:     make(map[clientKey]*ProxyClient),
			config:      poolConfig,
			stopCleanup: make(chan struct{}),
		}
		// Start regular cleanup goroutine.
		go clientPoolInstance.startCleanupTimer()
	})
	return clientPoolInstance
}

// GetClient Gets the synchronization type client.
func (p *clientPool) GetClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = p.config.DefaultTimeout
	}
	if timeout > p.config.MaxTimeout {
		timeout = p.config.MaxTimeout
	}
	key := GetClientKey(interfaces.ExecutionModeSync, "", timeout)
	p.mu.Lock()
	defer p.mu.Unlock()

	// Check if the client already exists.
	if client, exists := p.clients[key]; exists {
		client.CreateAt = time.Now() // Update access time.
		return client.Client
	}

	// If the maximum number of clients is reached, remove the oldest client.
	if len(p.clients) >= p.config.MaxClients {
		p.removeOldestClient()
	}

	// Create new client.
	client := &ProxyClient{
		Client: rest.NewRawHTTPClientWithOptions(rest.HTTPClientOptions{
			TimeOut: int(timeout.Seconds()),
		}),
		IsStreaming: false,
		CreateAt:    time.Now(),
	}

	p.clients[key] = client
	return client.Client
}

// getStreamClient universal streaming client creation method.
func (p *clientPool) GetStreamClient(streamingMode interfaces.StreamingMode, timeout time.Duration) *http.Client {
	p.logger.Debugf("get stream client, streamingMode: %v, timeout: %v", streamingMode, timeout)
	key := GetClientKey(interfaces.ExecutionModeStream, streamingMode, timeout)
	p.mu.Lock()
	defer p.mu.Unlock()

	// Check if the client already exists.
	if client, exists := p.clients[key]; exists {
		p.logger.Debugf("stream client exists, streamingMode: %v, timeout: %v", streamingMode, timeout)
		client.CreateAt = time.Now() // Update access time.
		return client.Client
	}
	p.logger.Debugf("stream client not exists, streamingMode: %v, timeout: %v", streamingMode, timeout)
	// If the maximum number of clients is reached, synchronization clients will be removed first.
	if len(p.clients) >= p.config.MaxClients {
		p.logger.Debugf("stream client not exists, remove oldest client")
		p.removeOldestClient()
	}

	responseHeaderTimeout := p.config.DefaultTimeout
	if timeout > 0 {
		responseHeaderTimeout = timeout
	}
	// Create new client.
	client := &ProxyClient{
		Client: rest.NewRawHTTPClientWithOptions(rest.HTTPClientOptions{
			TimeOut:               int(timeout.Seconds()),
			ResponseHeaderTimeout: int(responseHeaderTimeout.Seconds()),
		}),
		IsStreaming:   true,
		StreamingMode: streamingMode,
		CreateAt:      time.Now(),
	}
	p.logger.Debugf("create stream client, streamingMode: %v, timeout: %v", streamingMode, timeout)

	p.clients[key] = client
	return client.Client
}

// / Remove the oldest client.
func (p *clientPool) removeOldestClient() {
	var oldestKey *clientKey
	oldestTime := time.Time{}

	// Find the oldest client.
	first := true
	for key, client := range p.clients {
		if client.IsStreaming {
			continue
		}
		if first || oldestTime.IsZero() || client.CreateAt.Before(oldestTime) {
			oldestTime = client.CreateAt
			oldestKey = &key
			first = false
		}
	}
	// If they are all streaming clients, remove the oldest.
	if oldestKey == nil {
		for key, client := range p.clients {
			if first || client.CreateAt.Before(oldestTime) {
				oldestKey = &key
				oldestTime = client.CreateAt
				first = false
			}
		}
	}

	if !oldestTime.IsZero() && oldestKey != nil {
		p.clients[*oldestKey].CloseIdleConnections()
		delete(p.clients, *oldestKey)
	}
}

// Regularly clean up idle clients.
func (p *clientPool) startCleanupTimer() {
	cleanupTicker := time.NewTicker(cleanupInterval)
	defer cleanupTicker.Stop()

	for {
		select {
		case <-cleanupTicker.C:
			p.cleanupIdleClients()
		case <-p.stopCleanup:
			return
		}
	}
}

// Clean up idle clients.
func (p *clientPool) cleanupIdleClients() {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	for key, client := range p.clients {
		createdAt := client.CreateAt
		// Apply different timeout policies based on connection type.
		if key.ExecutionMode == interfaces.ExecutionModeSync &&
			now.Sub(createdAt) > p.config.ClientLifetime {
			p.logger.Infof("cleanup idle sync client, key: %v, created at: %v", key, createdAt)
			client.CloseIdleConnections() // Close idle connections for sync clients.
			delete(p.clients, key)
		}
	}
}

// Close closes the connection pool.
func (p *clientPool) Close() {
	close(p.stopCleanup)
}
