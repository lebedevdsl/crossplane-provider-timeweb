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
	"fmt"
	"reflect"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// cacheKey identifies one entry. PCRef has three string fields; dimension
// has Name + Kind. Kind is included so the same dimension Name registered
// under two kinds (defensive — should not happen in practice) stays separated.
type cacheKey struct {
	pc  PCRef
	dim Dimension
}

// cacheEntry holds one fetched payload + its fetch timestamp. The payload
// is the raw return value from the dimension's fetcher (e.g. a slice of
// PresetEntry, a slice of ConfiguratorEntry, a slice of strings for Enum).
// The resolution step in resolver.go does the kind-specific dispatch.
type cacheEntry struct {
	payload   any
	fetchedAt time.Time
	// err is the sticky error from the most recent fetch attempt.
	// ErrCatalogUnauthorized sticks until the next successful fetch
	// (see contract). Transient errors are NOT cached — they bubble up
	// without populating the entry.
	err error
}

// cache is the (pcRef, dimension)-keyed TTL store. Coalescing uses
// golang.org/x/sync/singleflight so concurrent reconciles for the same
// key share one in-flight upstream GET.
type cache struct {
	mu      sync.RWMutex
	entries map[cacheKey]*cacheEntry
	sf      singleflight.Group
	ttl     time.Duration
	now     func() time.Time
}

func newCache(opts Options) *cache {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &cache{
		entries: make(map[cacheKey]*cacheEntry),
		ttl:     clampTTL(opts.TTL),
		now:     now,
	}
}

// fetcher returns the upstream payload for a given key. The cache wraps
// every miss through singleflight + the configured fetcher.
type fetcher func(ctx context.Context) (any, error)

// getOrFetch returns the cached payload if it is still fresh; otherwise it
// invokes f under singleflight and stores the result. ErrCatalogUnauthorized
// results are cached (sticky until next successful call); ErrCatalogTransient
// results are NOT cached so the caller's next attempt re-issues the fetch.
func (c *cache) getOrFetch(ctx context.Context, key cacheKey, f fetcher) (any, error) {
	// Fast path: cached + fresh.
	c.mu.RLock()
	e, ok := c.entries[key]
	c.mu.RUnlock()
	if ok && !c.expired(e) {
		if e.err != nil {
			return nil, e.err
		}
		return e.payload, nil
	}

	// Slow path: coalesce concurrent misses on the same key.
	sfKey := key.pc.Kind + "|" + key.pc.Namespace + "|" + key.pc.Name + "|" + key.dim.Name
	v, err, _ := c.sf.Do(sfKey, func() (any, error) {
		// Re-check inside the singleflight callback so a second waiter
		// doesn't re-fetch what the first one just stored.
		c.mu.RLock()
		e, ok := c.entries[key]
		c.mu.RUnlock()
		if ok && !c.expired(e) {
			if e.err != nil {
				return nil, e.err
			}
			return e.payload, nil
		}

		payload, ferr := f(ctx)
		c.mu.Lock()
		defer c.mu.Unlock()
		if ferr != nil {
			// Only cache sticky 401/403 (ErrCatalogUnauthorized). Transient
			// 5xx errors and permanent other-4xx errors are NOT cached so
			// subsequent reconciles re-fetch: transient errors may resolve,
			// and permanent-4xx errors from a misconfigured catalog URL
			// should surface on every reconcile rather than silently pinning.
			if errors.Is(ferr, ErrCatalogUnauthorized) {
				c.entries[key] = &cacheEntry{err: ferr, fetchedAt: c.now()}
			}
			return nil, ferr
		}
		// An empty catalog (HTTP 200 but zero entries) is treated as transient:
		// the upstream may still be populating the catalog, or the request hit a
		// temporarily-empty shard. Return without caching so the next reconcile
		// re-fetches instead of pinning the empty list for a full TTL.
		if isEmptySlice(payload) {
			return nil, fmt.Errorf("%w: catalog returned 0 entries", ErrCatalogTransient)
		}
		c.entries[key] = &cacheEntry{payload: payload, fetchedAt: c.now()}
		return payload, nil
	})
	return v, err
}

// invalidate drops the entry for key. Idempotent.
func (c *cache) invalidate(key cacheKey) {
	c.mu.Lock()
	delete(c.entries, key)
	c.mu.Unlock()
}

// expired returns true when e is older than the configured TTL.
func (c *cache) expired(e *cacheEntry) bool {
	return c.now().Sub(e.fetchedAt) >= c.ttl
}

// isEmptySlice returns true when payload is a non-nil slice with zero
// elements. Used to detect a 200-but-empty catalog response that should be
// treated as transient rather than cached.
func isEmptySlice(payload any) bool {
	if payload == nil {
		return false
	}
	v := reflect.ValueOf(payload)
	return v.Kind() == reflect.Slice && v.Len() == 0
}
