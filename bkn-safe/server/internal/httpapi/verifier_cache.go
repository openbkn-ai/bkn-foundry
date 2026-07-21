// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// verifierCacheTTL bounds how long a successful token->subject introspection is
// reused before hydra is asked again. It is deliberately short: a token revoked
// (or expired) upstream still verifies for up to this long (revocation lag). The
// window only has to be wide enough to absorb the login burst — the frontend
// fires /me and /me/permissions in parallel right after login and re-pulls them
// on refresh / re-nav — so a few seconds buys the whole win.
const verifierCacheTTL = 10 * time.Second

// cachingVerifier wraps a TokenVerifier with two independent latency cuts:
//
//   - singleflight de-duplication: /me and /me/permissions load in parallel with
//     the SAME bearer token at startup. A plain cache does not help that pair —
//     both miss and both introspect. singleflight collapses concurrent calls for
//     one token into a SINGLE upstream introspection; the others wait on it.
//   - a short-TTL success cache: repeat calls within verifierCacheTTL (refresh /
//     re-nav) return the cached subject without touching hydra.
//
// Only successful, active verifications are cached. Errors and inactive tokens
// are never stored, so the underlying verifier's fail-closed contract holds:
// a revoked token is re-checked on the very next request after the cache lapses.
// The cache keys on a sha256 of the token, never the raw bearer string.
//
// Caching only the SUBJECT (identity) is safe: authorization is re-evaluated
// live by the casbin enforcer on every request, so a permission change takes
// effect immediately regardless of this cache.
type cachingVerifier struct {
	inner TokenVerifier
	ttl   time.Duration
	now   func() time.Time // injectable clock; defaults to time.Now

	mu    sync.Mutex
	cache map[string]cacheEntry
	group singleflight.Group
}

type cacheEntry struct {
	subject   string
	expiresAt time.Time
}

// newCachingVerifier wraps inner with a ttl-bounded success cache. A ttl <= 0
// disables caching but keeps singleflight de-duplication of concurrent calls.
func newCachingVerifier(inner TokenVerifier, ttl time.Duration) *cachingVerifier {
	return &cachingVerifier{
		inner: inner,
		ttl:   ttl,
		now:   time.Now,
		cache: map[string]cacheEntry{},
	}
}

// VerifyToken returns the token subject from cache when fresh, otherwise from a
// singleflight-shared introspection of the underlying verifier.
func (c *cachingVerifier) VerifyToken(ctx context.Context, token string) (string, error) {
	key := tokenCacheKey(token)

	if sub, ok := c.lookup(key); ok {
		return sub, nil
	}

	// Concurrent callers with the same token share one upstream introspection.
	sub, err, _ := c.group.Do(key, func() (any, error) {
		// A sibling flight may have populated the cache between our outer miss
		// and acquiring this call — re-check before hitting the network.
		if s, ok := c.lookup(key); ok {
			return s, nil
		}
		s, err := c.inner.VerifyToken(ctx, token)
		if err != nil {
			return "", err // not cached: fail-closed, re-verified next request
		}
		c.store(key, s)
		return s, nil
	})
	if err != nil {
		return "", err
	}
	return sub.(string), nil
}

func (c *cachingVerifier) lookup(key string) (string, bool) {
	if c.ttl <= 0 {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.cache[key]
	if !ok {
		return "", false
	}
	if !c.now().Before(e.expiresAt) {
		delete(c.cache, key)
		return "", false
	}
	return e.subject, true
}

func (c *cachingVerifier) store(key, subject string) {
	if c.ttl <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	// Opportunistic sweep on write: entries live only ttl (seconds), so dropping
	// the expired ones here keeps the map bounded to roughly the distinct tokens
	// seen in the last ttl window — no background janitor goroutine needed.
	for k, e := range c.cache {
		if !now.Before(e.expiresAt) {
			delete(c.cache, k)
		}
	}
	c.cache[key] = cacheEntry{subject: subject, expiresAt: now.Add(c.ttl)}
}

// tokenCacheKey derives a stable, non-reversible cache key so raw bearer tokens
// are never held as map keys.
func tokenCacheKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
