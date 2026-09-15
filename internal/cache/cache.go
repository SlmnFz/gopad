package cache

import (
	"sync"
	"time"
)

type entry[V any] struct {
	value     V
	expiresAt time.Time
}

// Cache is a bounded, generic in-memory TTL cache. Expired entries are lazy
// misses on the read path and are also removed by the background sweeper.
type Cache[K comparable, V any] struct {
	mu         sync.RWMutex
	values     map[K]entry[V]
	ttl        time.Duration
	maxEntries int

	stopOnce sync.Once
	stop     chan struct{}
	done     chan struct{}
}

// New creates a cache and starts its bounded expiry sweeper. Non-positive
// values are clamped to safe minimums so a caller cannot create an invalid
// ticker or an unbounded cache accidentally.
func New[K comparable, V any](ttl time.Duration, maxEntries int) *Cache[K, V] {
	if ttl <= 0 {
		ttl = time.Nanosecond
	}
	if maxEntries <= 0 {
		maxEntries = 1
	}
	cache := &Cache[K, V]{
		values:     make(map[K]entry[V]),
		ttl:        ttl,
		maxEntries: maxEntries,
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
	}
	go cache.sweepLoop()
	return cache
}

// Get returns a live value. An expired value is treated as a miss even if the
// sweeper has not visited it yet.
func (cache *Cache[K, V]) Get(key K) (V, bool) {
	var zero V
	if cache == nil {
		return zero, false
	}
	now := time.Now()
	cache.mu.RLock()
	value, ok := cache.values[key]
	cache.mu.RUnlock()
	if !ok || !now.Before(value.expiresAt) {
		return zero, false
	}
	return value.value, true
}

// Set stores a value and refreshes its TTL. On overflow, the entry with the
// oldest expiration is evicted; the scan is intentionally off the hot read
// path and only runs when the size limit is exceeded.
func (cache *Cache[K, V]) Set(key K, value V) {
	if cache == nil {
		return
	}
	now := time.Now()
	cache.mu.Lock()
	if _, exists := cache.values[key]; !exists && len(cache.values) >= cache.maxEntries {
		var oldestKey K
		var oldestExpiry time.Time
		first := true
		for candidateKey, candidate := range cache.values {
			if first || candidate.expiresAt.Before(oldestExpiry) {
				oldestKey = candidateKey
				oldestExpiry = candidate.expiresAt
				first = false
			}
		}
		if !first {
			delete(cache.values, oldestKey)
		}
	}
	cache.values[key] = entry[V]{value: value, expiresAt: now.Add(cache.ttl)}
	cache.mu.Unlock()
}

// Delete removes a key before its TTL expires.
func (cache *Cache[K, V]) Delete(key K) {
	if cache == nil {
		return
	}
	cache.mu.Lock()
	delete(cache.values, key)
	cache.mu.Unlock()
}

// Len returns the number of live entries. It also removes entries that have
// expired but have not yet been visited by the sweeper.
func (cache *Cache[K, V]) Len() int {
	if cache == nil {
		return 0
	}
	now := time.Now()
	cache.mu.Lock()
	cache.removeExpiredLocked(now)
	length := len(cache.values)
	cache.mu.Unlock()
	return length
}

// Close stops the sweeper and waits for it to exit. It is safe to call more
// than once and makes lifecycle ownership explicit for long-running servers.
func (cache *Cache[K, V]) Close() {
	if cache == nil {
		return
	}
	cache.stopOnce.Do(func() { close(cache.stop) })
	<-cache.done
}

func (cache *Cache[K, V]) sweepLoop() {
	defer close(cache.done)
	interval := cache.ttl / 2
	if interval <= 0 {
		interval = time.Nanosecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			cache.mu.Lock()
			cache.removeExpiredLocked(time.Now())
			cache.mu.Unlock()
		case <-cache.stop:
			return
		}
	}
}

func (cache *Cache[K, V]) removeExpiredLocked(now time.Time) {
	for key, value := range cache.values {
		if !now.Before(value.expiresAt) {
			delete(cache.values, key)
		}
	}
}

const (
	DefaultFoundSlugTTL   = 5 * time.Second
	DefaultMissingSlugTTL = time.Second
	DefaultSlugCacheSize  = 4096
)

// SlugCache keeps only document existence, never document content. Positive
// and negative results use separate caches so a newly created slug is not
// hidden by a stale not-found result.
type SlugCache struct {
	found     *Cache[string, struct{}]
	missing   *Cache[string, struct{}]
	closeOnce sync.Once
}

func NewSlugCache(foundTTL, missingTTL time.Duration, maxEntries int) *SlugCache {
	return &SlugCache{
		found:   New[string, struct{}](foundTTL, maxEntries),
		missing: New[string, struct{}](missingTTL, maxEntries),
	}
}

func DefaultSlugCache() *SlugCache {
	return NewSlugCache(DefaultFoundSlugTTL, DefaultMissingSlugTTL, DefaultSlugCacheSize)
}

// Lookup returns (exists, cached). A cached false is a known negative result.
func (cache *SlugCache) Lookup(slug string) (bool, bool) {
	if cache == nil {
		return false, false
	}
	if _, ok := cache.found.Get(slug); ok {
		return true, true
	}
	if _, ok := cache.missing.Get(slug); ok {
		return false, true
	}
	return false, false
}

func (cache *SlugCache) SetExists(slug string) {
	if cache == nil {
		return
	}
	cache.missing.Delete(slug)
	cache.found.Set(slug, struct{}{})
}

func (cache *SlugCache) SetMissing(slug string) {
	if cache == nil {
		return
	}
	cache.found.Delete(slug)
	cache.missing.Set(slug, struct{}{})
}

func (cache *SlugCache) Close() {
	if cache == nil {
		return
	}
	cache.closeOnce.Do(func() {
		cache.found.Close()
		cache.missing.Close()
	})
}
