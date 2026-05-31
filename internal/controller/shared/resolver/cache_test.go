/*
Copyright 2026 Dmitry Lebedev.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package resolver

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCacheTTLClamping(t *testing.T) {
	for _, tt := range []struct {
		in   time.Duration
		want time.Duration
	}{
		{0, DefaultTTL},
		{-1 * time.Second, DefaultTTL},
		{30 * time.Second, MinTTL},
		{30 * time.Minute, 30 * time.Minute},
		{2 * time.Hour, MaxTTL},
	} {
		if got := clampTTL(tt.in); got != tt.want {
			t.Errorf("clampTTL(%v) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestCacheFreshHit(t *testing.T) {
	clock := newClock()
	c := newCache(Options{TTL: 5 * time.Minute, Now: clock.Now})
	key := cacheKey{pc: PCRef{Name: "default"}, dim: Dimension{Name: "X"}}

	calls := int32(0)
	f := func(_ context.Context) (any, error) { atomic.AddInt32(&calls, 1); return "first", nil }
	if _, err := c.getOrFetch(context.Background(), key, f); err != nil {
		t.Fatal(err)
	}
	if _, err := c.getOrFetch(context.Background(), key, f); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("expected 1 fetch, got %d", calls)
	}
}

func TestCacheTTLExpiry(t *testing.T) {
	clock := newClock()
	c := newCache(Options{TTL: 5 * time.Minute, Now: clock.Now})
	key := cacheKey{pc: PCRef{Name: "default"}, dim: Dimension{Name: "X"}}

	calls := int32(0)
	f := func(_ context.Context) (any, error) {
		v := atomic.AddInt32(&calls, 1)
		return int(v), nil
	}

	v1, _ := c.getOrFetch(context.Background(), key, f)
	clock.advance(6 * time.Minute)
	v2, _ := c.getOrFetch(context.Background(), key, f)
	if v1.(int) != 1 || v2.(int) != 2 {
		t.Errorf("v1=%v v2=%v, want 1 then 2", v1, v2)
	}
}

func TestCacheConcurrentMissCoalesced(t *testing.T) {
	c := newCache(Options{TTL: 5 * time.Minute, Now: time.Now})
	key := cacheKey{pc: PCRef{Name: "default"}, dim: Dimension{Name: "X"}}

	calls := int32(0)
	gate := make(chan struct{})
	f := func(_ context.Context) (any, error) {
		<-gate
		atomic.AddInt32(&calls, 1)
		return "x", nil
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.getOrFetch(context.Background(), key, f)
		}()
	}
	// Let everyone arrive at the gate, then release.
	time.Sleep(10 * time.Millisecond)
	close(gate)
	wg.Wait()
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("expected 1 coalesced fetch, got %d", got)
	}
}

func TestCacheUnauthorizedSticky(t *testing.T) {
	clock := newClock()
	c := newCache(Options{TTL: 5 * time.Minute, Now: clock.Now})
	key := cacheKey{pc: PCRef{Name: "default"}, dim: Dimension{Name: "X"}}

	calls := int32(0)
	f := func(_ context.Context) (any, error) {
		atomic.AddInt32(&calls, 1)
		return nil, ErrCatalogUnauthorized
	}

	_, err := c.getOrFetch(context.Background(), key, f)
	if !errors.Is(err, ErrCatalogUnauthorized) {
		t.Fatalf("err = %v", err)
	}
	// Second access within TTL should return cached error without re-fetching.
	_, err = c.getOrFetch(context.Background(), key, f)
	if !errors.Is(err, ErrCatalogUnauthorized) {
		t.Fatalf("err = %v", err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("expected 1 fetch (sticky 401), got %d", calls)
	}
}

func TestCacheTransientNotCached(t *testing.T) {
	c := newCache(Options{TTL: 5 * time.Minute, Now: time.Now})
	key := cacheKey{pc: PCRef{Name: "default"}, dim: Dimension{Name: "X"}}

	calls := int32(0)
	f := func(_ context.Context) (any, error) {
		atomic.AddInt32(&calls, 1)
		return nil, ErrCatalogTransient
	}

	for i := 0; i < 3; i++ {
		_, err := c.getOrFetch(context.Background(), key, f)
		if !errors.Is(err, ErrCatalogTransient) {
			t.Fatalf("err = %v", err)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("expected 3 fetches (transient not cached), got %d", got)
	}
}

// TestCacheKey_PerPCRefIsolation locks the structural-isolation contract
// referenced by spec.md US3: two PCs of distinct Kind / Name / Namespace
// produce distinct cacheKey values, so a fetch under PC-A's identity can
// never leak its data to PC-B (and vice versa) — even when the dimension
// is identical. Closes the open spec task T053 (the isolation is already
// structural via the map key, this test pins the contract).
func TestCacheKey_PerPCRefIsolation(t *testing.T) {
	c := newCache(Options{TTL: 5 * time.Minute, Now: time.Now})

	dim := Dimension{Name: "X"}

	// Six PCRefs that should ALL produce distinct cache keys.
	refs := []PCRef{
		{Kind: "ProviderConfig", Name: "default", Namespace: "team-a"},
		{Kind: "ProviderConfig", Name: "default", Namespace: "team-b"}, // different namespace
		{Kind: "ProviderConfig", Name: "other", Namespace: "team-a"},   // different name
		{Kind: "ClusterProviderConfig", Name: "default"},               // different kind (and no ns)
		{Kind: "ClusterProviderConfig", Name: "shared"},                // different cluster PC
		{Kind: "", Name: "default"},                                    // runtime-default kind (empty)
	}

	type result struct {
		ref PCRef
		val string
	}

	// Pre-populate each key with a value derived from the ref so we can
	// assert "PC-X read PC-X's data".
	for i, ref := range refs {
		ref := ref
		want := result{ref: ref, val: ref.Kind + "/" + ref.Name + "/" + ref.Namespace}
		key := cacheKey{pc: ref, dim: dim}
		fetcher := func(_ context.Context) (any, error) { return want, nil }
		got, err := c.getOrFetch(context.Background(), key, fetcher)
		if err != nil {
			t.Fatalf("seed[%d]: %v", i, err)
		}
		if got.(result) != want {
			t.Fatalf("seed[%d]: got %v want %v", i, got, want)
		}
	}

	// Re-read each key; MUST come back as the matching ref's value. A leak
	// (shared cell across PCRefs) would surface as a value seeded by a
	// different ref since this fetcher always errors.
	leakyFetcher := func(_ context.Context) (any, error) {
		return nil, errors.New("fetcher MUST NOT be invoked — value should already be cached")
	}
	for i, ref := range refs {
		want := result{ref: ref, val: ref.Kind + "/" + ref.Name + "/" + ref.Namespace}
		key := cacheKey{pc: ref, dim: dim}
		got, err := c.getOrFetch(context.Background(), key, leakyFetcher)
		if err != nil {
			t.Fatalf("readback[%d]: cache miss for %+v: %v", i, ref, err)
		}
		if got.(result) != want {
			t.Errorf("readback[%d]: %+v leaked %v (wanted %v)", i, ref, got, want)
		}
	}

	// Defence-in-depth: the cache MUST hold exactly len(refs) entries.
	c.mu.Lock()
	n := len(c.entries)
	c.mu.Unlock()
	if n != len(refs) {
		t.Errorf("cache has %d entries, want %d (one per distinct PCRef)", n, len(refs))
	}
}

func TestCacheInvalidate(t *testing.T) {
	c := newCache(Options{TTL: 5 * time.Minute, Now: time.Now})
	key := cacheKey{pc: PCRef{Name: "default"}, dim: Dimension{Name: "X"}}

	calls := int32(0)
	f := func(_ context.Context) (any, error) { atomic.AddInt32(&calls, 1); return "ok", nil }

	_, _ = c.getOrFetch(context.Background(), key, f)
	c.invalidate(key)
	_, _ = c.getOrFetch(context.Background(), key, f)
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("expected 2 fetches after invalidate, got %d", calls)
	}
}

// clock is a manually-advanced time source for cache TTL tests.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: time.Unix(1_700_000_000, 0)} }

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}
