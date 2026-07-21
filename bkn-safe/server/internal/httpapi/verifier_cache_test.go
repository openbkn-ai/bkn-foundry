// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// countingVerifier records how many times the underlying introspection ran and
// maps token->subject the same way the production stub does (token == subject).
type countingVerifier struct {
	calls atomic.Int64
	fail  bool
	// block, when non-nil, holds each VerifyToken call until the channel is
	// closed — used to force concurrent calls to overlap for the singleflight
	// test.
	block chan struct{}
}

func (v *countingVerifier) VerifyToken(_ context.Context, token string) (string, error) {
	v.calls.Add(1)
	if v.block != nil {
		<-v.block
	}
	if v.fail || token == "" {
		return "", errors.New("inactive")
	}
	return token, nil
}

func TestCachingVerifier_HitWithinTTL(t *testing.T) {
	inner := &countingVerifier{}
	c := newCachingVerifier(inner, verifierCacheTTL)

	for i := 0; i < 5; i++ {
		sub, err := c.VerifyToken(context.Background(), "tok-a")
		if err != nil || sub != "tok-a" {
			t.Fatalf("call %d: sub=%q err=%v", i, sub, err)
		}
	}
	if got := inner.calls.Load(); got != 1 {
		t.Fatalf("want 1 upstream introspect, got %d", got)
	}
}

func TestCachingVerifier_DistinctTokens(t *testing.T) {
	inner := &countingVerifier{}
	c := newCachingVerifier(inner, verifierCacheTTL)

	for _, tok := range []string{"a", "b", "c", "a", "b"} {
		if sub, err := c.VerifyToken(context.Background(), tok); err != nil || sub != tok {
			t.Fatalf("tok %q: sub=%q err=%v", tok, sub, err)
		}
	}
	// a, b, c each introspected once; the repeat a, b are cache hits.
	if got := inner.calls.Load(); got != 3 {
		t.Fatalf("want 3 upstream introspects, got %d", got)
	}
}

func TestCachingVerifier_ExpiryReVerifies(t *testing.T) {
	inner := &countingVerifier{}
	c := newCachingVerifier(inner, 10*time.Second)
	base := time.Unix(1_000_000, 0)
	nowVal := base
	c.now = func() time.Time { return nowVal }

	if _, err := c.VerifyToken(context.Background(), "tok"); err != nil {
		t.Fatal(err)
	}
	// Still within TTL: cache hit.
	nowVal = base.Add(9 * time.Second)
	if _, err := c.VerifyToken(context.Background(), "tok"); err != nil {
		t.Fatal(err)
	}
	if got := inner.calls.Load(); got != 1 {
		t.Fatalf("within TTL: want 1 introspect, got %d", got)
	}
	// Past TTL: re-introspect.
	nowVal = base.Add(11 * time.Second)
	if _, err := c.VerifyToken(context.Background(), "tok"); err != nil {
		t.Fatal(err)
	}
	if got := inner.calls.Load(); got != 2 {
		t.Fatalf("past TTL: want 2 introspects, got %d", got)
	}
}

func TestCachingVerifier_ErrorsNotCached(t *testing.T) {
	inner := &countingVerifier{fail: true}
	c := newCachingVerifier(inner, verifierCacheTTL)

	// Two failing calls must both hit upstream — a rejected token is never cached
	// (fail-closed: re-verified every request).
	for i := 0; i < 2; i++ {
		if _, err := c.VerifyToken(context.Background(), "tok"); err == nil {
			t.Fatalf("call %d: want error", i)
		}
	}
	if got := inner.calls.Load(); got != 2 {
		t.Fatalf("want 2 introspects (no caching of failures), got %d", got)
	}

	// Once upstream starts accepting, the success is cached as usual.
	inner.fail = false
	for i := 0; i < 3; i++ {
		if _, err := c.VerifyToken(context.Background(), "tok"); err != nil {
			t.Fatalf("recovered call %d: %v", i, err)
		}
	}
	if got := inner.calls.Load(); got != 3 {
		t.Fatalf("want 3 total introspects (2 fail + 1 cached success), got %d", got)
	}
}

func TestCachingVerifier_SingleflightConcurrent(t *testing.T) {
	inner := &countingVerifier{block: make(chan struct{})}
	c := newCachingVerifier(inner, verifierCacheTTL)

	const n = 20
	var wg sync.WaitGroup
	subs := make([]string, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			subs[i], errs[i] = c.VerifyToken(context.Background(), "same-tok")
		}(i)
	}

	// Give the goroutines time to funnel into the in-flight introspection, then
	// release the single blocked upstream call.
	time.Sleep(50 * time.Millisecond)
	close(inner.block)
	wg.Wait()

	if got := inner.calls.Load(); got != 1 {
		t.Fatalf("singleflight: want 1 upstream introspect for %d concurrent callers, got %d", n, got)
	}
	for i := 0; i < n; i++ {
		if errs[i] != nil || subs[i] != "same-tok" {
			t.Fatalf("caller %d: sub=%q err=%v", i, subs[i], errs[i])
		}
	}
}

func TestCachingVerifier_TTLDisabledStillDedups(t *testing.T) {
	inner := &countingVerifier{block: make(chan struct{})}
	c := newCachingVerifier(inner, 0) // caching off

	const n = 10
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.VerifyToken(context.Background(), "tok")
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(inner.block)
	wg.Wait()

	// Concurrent calls still collapse via singleflight even with caching off.
	if got := inner.calls.Load(); got != 1 {
		t.Fatalf("ttl=0 concurrent: want 1 introspect, got %d", got)
	}
	// But a later, non-overlapping call re-introspects (nothing cached).
	if _, err := c.VerifyToken(context.Background(), "tok"); err != nil {
		t.Fatal(err)
	}
	if got := inner.calls.Load(); got != 2 {
		t.Fatalf("ttl=0 sequential: want 2 introspects, got %d", got)
	}
}
