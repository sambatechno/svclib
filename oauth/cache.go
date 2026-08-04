package oauth

import (
	"sync"
	"time"
)

// Cache stores grant-status results so a live grant costs at most one DB read per TTL rather than
// one per token validation. It returns the value together with its age, so the GrantChecker can
// distinguish a fresh hit (within TTL) from a stale-but-usable value (served only when the DB is
// unreachable). Implementations must be safe for concurrent use. The default is process-local
// (memCache) — deliberately not a shared/remote cache, which would reintroduce the network
// dependency the offline design avoids.
type Cache interface {
	// Get returns the cached value and how long ago it was stored. ok is false if absent.
	Get(key string) (value string, age time.Duration, ok bool)
	// Set stores value under key, stamped with the current time.
	Set(key, value string)
}

// memCache is the default in-process Cache: a mutex-guarded map of value+timestamp. Cardinality is
// tiny (one entry per active grant per tenant), so TTL-based lazy eviction is enough — entries
// older than maxAge are dropped when next seen, bounding growth for keys that keep being queried.
type memCache struct {
	mu     sync.RWMutex
	data   map[string]memEntry
	maxAge time.Duration
	now    func() time.Time
}

type memEntry struct {
	value string
	at    time.Time
}

// newMemCache builds the default cache. maxAge (ttl+grace) is the point past which an entry is
// useless even as a stale fallback, so it is dropped.
func newMemCache(maxAge time.Duration, now func() time.Time) *memCache {
	return &memCache{data: make(map[string]memEntry), maxAge: maxAge, now: now}
}

func (c *memCache) Get(key string) (string, time.Duration, bool) {
	c.mu.RLock()
	e, ok := c.data[key]
	c.mu.RUnlock()
	if !ok {
		return "", 0, false
	}
	age := c.now().Sub(e.at)
	if age > c.maxAge {
		c.mu.Lock()
		// Re-check under the write lock: another goroutine may have refreshed it.
		if cur, ok := c.data[key]; ok && c.now().Sub(cur.at) > c.maxAge {
			delete(c.data, key)
		}
		c.mu.Unlock()
		return "", 0, false
	}
	return e.value, age, true
}

func (c *memCache) Set(key, value string) {
	c.mu.Lock()
	c.data[key] = memEntry{value: value, at: c.now()}
	c.mu.Unlock()
}
