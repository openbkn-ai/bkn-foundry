// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package object_type_stats

import (
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// maxCachedCounts bounds the cache. Expired counts are swept when it fills; if every count is still
// live the cache starts over rather than grow without limit.
const maxCachedCounts = 10000

// cachedCount is one count and where it came from.
type cachedCount struct {
	count  int64
	exact  bool
	source string
	asOf   time.Time
}

// fill writes the count into a response row.
func (c cachedCount) fill(entry *interfaces.ObjectTypeStatsEntry) {
	count := c.count
	entry.Count = &count
	entry.Exact = c.exact
	entry.Source = c.source
	entry.AsOf = c.asOf.UnixMilli()
	entry.Error = nil
}

type cacheSlot struct {
	value   cachedCount
	expires time.Time
}

// countCache keeps counts in this process for a fixed time. Its key names the object type, its
// model version and the caller's effective row policy, so it is shared by every caller who would
// see the same rows and by no one else.
type countCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]cacheSlot
}

func newCountCache(ttl time.Duration) *countCache {
	return &countCache{ttl: ttl, entries: map[string]cacheSlot{}}
}

func (c *countCache) get(key string, now time.Time) (cachedCount, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	slot, ok := c.entries[key]
	if !ok {
		return cachedCount{}, false
	}
	if !now.Before(slot.expires) {
		delete(c.entries, key)
		return cachedCount{}, false
	}
	return slot.value, true
}

func (c *countCache) put(key string, value cachedCount, now time.Time) {
	if c.ttl <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.entries[key]; !ok && len(c.entries) >= maxCachedCounts {
		for k, slot := range c.entries {
			if !now.Before(slot.expires) {
				delete(c.entries, k)
			}
		}
		if len(c.entries) >= maxCachedCounts {
			c.entries = map[string]cacheSlot{}
		}
	}
	expires := now.Add(c.ttl)
	if value.exact {
		expires = value.asOf.Add(c.ttl)
	}
	c.entries[key] = cacheSlot{value: value, expires: expires}
}

// countFlight publishes a single count's result to all waiting requests.
// Closing done synchronizes reads of result and err.
type countFlight struct {
	done   chan struct{}
	result cachedCount
	err    error
}

type inflightCounts struct {
	mu   sync.Mutex
	keys map[string]*countFlight
}

func newInflightCounts() *inflightCounts {
	return &inflightCounts{keys: map[string]*countFlight{}}
}

func (i *inflightCounts) get(key string) *countFlight {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.keys[key]
}

func (i *inflightCounts) begin(key string) (*countFlight, bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if flight := i.keys[key]; flight != nil {
		return flight, false
	}
	flight := &countFlight{done: make(chan struct{})}
	i.keys[key] = flight
	return flight, true
}

func (i *inflightCounts) finish(key string, flight *countFlight, result cachedCount, err error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	flight.result, flight.err = result, err
	close(flight.done)
	delete(i.keys, key)
}
