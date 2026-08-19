package mcpinstance

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/dbaccess"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/utils"
)

// InstancePool is responsible for the running state management of MCP instances:
// - Dynamic on-demand creation: when the instance is missing, return to the source DB to parse the configuration and create it.
// - Concurrent solo flight: the same (mcpID, version) is created only once concurrently.
// - Bounded memory: supports LRU eviction for the maximum number of instances (MaxInstances)
// - Expired cleaning: supports TTL cleaning based on the latest access time, and provides a scheduled cleaning cycle.
// - Active protection: Instances with active connections (SSE/Stream) do not participate in elimination/cleanup.
//
// This pool "does not cache configuration" and only manages running instances. The resolver is responsible for configuration resolution.
var ErrMCPInstanceConfigNotFound = errors.New("mcp instance runtime config not found")

type instanceBuilder interface {
	Build(ctx context.Context, cfg *interfaces.MCPRuntimeConfig) (*interfaces.MCPServerInstance, error)
	Shutdown(ctx context.Context, instance *interfaces.MCPServerInstance) error
}

type createCall struct {
	done     chan struct{}
	instance *interfaces.MCPServerInstance
	err      error
}

// InstancePoolOptions pool behavior configuration.
type InstancePoolOptions struct {
	MaxInstances    int           // Maximum number of reserved instances (<=0 means no limit)
	InstanceTTL     time.Duration // Recent access timeout threshold (<=0 means no TTL scrubbing is enabled)
	CleanupInterval time.Duration // Scheduled cleaning cycle (<=0 means disabling scheduled cleaning)
}

// instanceEntry LRU node, records the instance and recent access time.
type instanceEntry struct {
	key        string
	instance   *interfaces.MCPServerInstance
	lastAccess time.Time
	element    *list.Element
}

// InstancePool instance pool.
type InstancePool struct {
	logger           interfaces.Logger
	dbResourceDeploy model.DBResourceDeploy
	builder          instanceBuilder
	opts             InstancePoolOptions
	now              func() time.Time
	mu               sync.Mutex
	entries          map[string]*instanceEntry
	lru              *list.List
	inflight         map[string]*createCall
	stopCleanup      chan struct{}
}

var (
	pOnce sync.Once
	pool  *InstancePool
)

// initInstancePool initializes the instance pool.
func initInstancePool(executor interfaces.IMCPToolExecutor) *InstancePool {
	pOnce.Do(func() {
		conf := config.NewConfigLoader()
		opts := InstancePoolOptions{
			MaxInstances:    conf.MCPConfig.MaxInstances,
			InstanceTTL:     time.Duration(conf.MCPConfig.InstanceTTL) * time.Second,
			CleanupInterval: time.Duration(conf.MCPConfig.CleanupInterval) * time.Second,
		}
		// Normalized illegal configuration value.
		if opts.MaxInstances < 0 {
			opts.MaxInstances = 0
		}
		if opts.InstanceTTL < 0 {
			opts.InstanceTTL = 0
		}
		if opts.CleanupInterval < 0 {
			opts.CleanupInterval = 0
		}

		pool = &InstancePool{
			logger:           conf.GetLogger(),
			builder:          newInstanceManager(executor, conf.GetLogger()),
			dbResourceDeploy: dbaccess.NewResourceDeployDBSingleton(),
			opts:             opts,
			now:              time.Now,
			entries:          make(map[string]*instanceEntry),
			lru:              list.New(),
			inflight:         make(map[string]*createCall),
			stopCleanup:      make(chan struct{}),
		}
		if pool.opts.CleanupInterval > 0 && pool.opts.InstanceTTL > 0 {
			go pool.startCleanupLoop()
		}
	})
	return pool
}

// GetOrCreate If there is no instance in the memory, resolve the configuration through resolver and create it.
func (p *InstancePool) GetOrCreate(ctx context.Context, mcpID string, version int) (*interfaces.MCPServerInstance, error) {
	key := p.key(mcpID, version)

	p.mu.Lock()
	if e, ok := p.entries[key]; ok && e != nil && e.instance != nil {
		p.touchLocked(e)
		p.mu.Unlock()
		return e.instance, nil
	}
	if call, ok := p.inflight[key]; ok {
		p.mu.Unlock()
		<-call.done
		if call.err == nil && call.instance != nil {
			p.mu.Lock()
			if e, ok := p.entries[key]; ok && e != nil {
				p.touchLocked(e)
			}
			p.mu.Unlock()
		}
		return call.instance, call.err
	}
	p.mu.Unlock()

	loaded, err := p.resolve(ctx, mcpID, version)
	if err != nil {
		return nil, err
	}
	if loaded == nil {
		return nil, ErrMCPInstanceConfigNotFound
	}
	return p.getOrCreateFromConfig(ctx, loaded)
}

// GetOrCreateWithConfig creates an instance using externally provided configuration (avoids extra DB queries)
func (p *InstancePool) GetOrCreateWithConfig(ctx context.Context, cfg *interfaces.MCPRuntimeConfig) (*interfaces.MCPServerInstance, error) {
	if cfg == nil {
		return nil, ErrMCPInstanceConfigNotFound
	}
	return p.getOrCreateFromConfig(ctx, cfg)
}

func (p *InstancePool) getOrCreateFromConfig(ctx context.Context, cfg *interfaces.MCPRuntimeConfig) (*interfaces.MCPServerInstance, error) {
	key := p.key(cfg.MCPID, cfg.Version)

	p.mu.Lock()
	if e, ok := p.entries[key]; ok && e != nil && e.instance != nil {
		p.touchLocked(e)
		p.mu.Unlock()
		return e.instance, nil
	}
	if call, ok := p.inflight[key]; ok {
		p.mu.Unlock()
		<-call.done
		if call.err == nil && call.instance != nil {
			p.mu.Lock()
			if e, ok := p.entries[key]; ok && e != nil {
				p.touchLocked(e)
			}
			p.mu.Unlock()
		}
		return call.instance, call.err
	}
	call := &createCall{done: make(chan struct{})}
	p.inflight[key] = call
	p.mu.Unlock()

	ins, err := p.builder.Build(ctx, cfg)

	var evicted []*interfaces.MCPServerInstance
	p.mu.Lock()
	call.instance = ins
	call.err = err
	if err == nil && ins != nil {
		e := &instanceEntry{
			key:        key,
			instance:   ins,
			lastAccess: p.now(),
		}
		e.element = p.lru.PushFront(e)
		p.entries[key] = e
		evicted = p.evictLocked()
	}
	delete(p.inflight, key)
	close(call.done)
	p.mu.Unlock()

	for _, victim := range evicted {
		_ = p.builder.Shutdown(ctx, victim)
	}
	return ins, err
}

// DeleteInstance actively deletes the specified instance and calls lifecycle uninstallation.
func (p *InstancePool) DeleteInstance(ctx context.Context, mcpID string, version int) error {
	key := p.key(mcpID, version)
	p.mu.Lock()
	e, ok := p.entries[key]
	if !ok || e == nil || e.instance == nil {
		p.mu.Unlock()
		return nil
	}
	delete(p.entries, key)
	if e.element != nil {
		p.lru.Remove(e.element)
	}
	ins := e.instance
	p.mu.Unlock()
	return p.builder.Shutdown(ctx, ins)
}

// Close Close the scheduled cleaning cycle.
func Close() {
	if pool == nil {
		return
	}
	pool.mu.Lock()
	ch := pool.stopCleanup
	pool.stopCleanup = nil
	pool.mu.Unlock()
	if ch != nil {
		close(ch)
	}
}

// Cleanup performs TTL cleanup; skips instances with active connections.
func (p *InstancePool) cleanup(ctx context.Context) {
	var victims []*interfaces.MCPServerInstance
	now := p.now()

	p.mu.Lock()
	if p.opts.InstanceTTL <= 0 {
		p.mu.Unlock()
		return
	}
	for el := p.lru.Back(); el != nil; {
		prev := el.Prev()
		e, _ := el.Value.(*instanceEntry)
		if e == nil || e.instance == nil {
			p.lru.Remove(el)
			el = prev
			continue
		}
		if atomic.LoadInt64(&e.instance.ActiveStreamConn) > 0 || atomic.LoadInt64(&e.instance.ActiveSSEConn) > 0 {
			el = prev
			continue
		}
		if now.Sub(e.lastAccess) <= p.opts.InstanceTTL {
			break
		}
		delete(p.entries, e.key)
		p.lru.Remove(el)
		victims = append(victims, e.instance)
		el = prev
	}
	p.mu.Unlock()

	for _, ins := range victims {
		_ = p.builder.Shutdown(ctx, ins)
	}
}

// startCleanupLoop triggers Cleanup regularly.
func (p *InstancePool) startCleanupLoop() {
	ticker := time.NewTicker(p.opts.CleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			p.cleanup(context.Background())
		case <-p.stopCleanup:
			return
		}
	}
}

// evictLocked performs LRU eviction; skips instances with active connections.
func (p *InstancePool) evictLocked() []*interfaces.MCPServerInstance {
	if p.opts.MaxInstances <= 0 {
		return nil
	}
	var victims []*interfaces.MCPServerInstance
	for len(p.entries) > p.opts.MaxInstances {
		var removed bool
		for el := p.lru.Back(); el != nil; el = el.Prev() {
			e, _ := el.Value.(*instanceEntry)
			if e == nil || e.instance == nil {
				p.lru.Remove(el)
				continue
			}
			if atomic.LoadInt64(&e.instance.ActiveStreamConn) > 0 || atomic.LoadInt64(&e.instance.ActiveSSEConn) > 0 {
				continue
			}
			p.lru.Remove(el)
			delete(p.entries, e.key)
			victims = append(victims, e.instance)
			removed = true
			break
		}
		if !removed {
			break
		}
	}
	return victims
}

// touchLocked updates the last access time and moves to the LRU queue head.
func (p *InstancePool) touchLocked(e *instanceEntry) {
	if e == nil {
		return
	}
	e.lastAccess = p.now()
	if e.element != nil {
		p.lru.MoveToFront(e.element)
	}
}

func (p *InstancePool) key(mcpID string, version int) string {
	return fmt.Sprintf("%s-%d", mcpID, version)
}

func (p *InstancePool) resolve(ctx context.Context, mcpID string, version int) (*interfaces.MCPRuntimeConfig, error) {
	list, err := p.dbResourceDeploy.SelectList(ctx, nil, &model.ResourceDeployDB{
		ResourceID: mcpID,
		Type:       interfaces.ResourceDeployTypeMCP.String(),
		Version:    version,
	})
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, nil
	}
	return utils.JSONToObjectWithError[*interfaces.MCPRuntimeConfig](list[0].Config)
}
