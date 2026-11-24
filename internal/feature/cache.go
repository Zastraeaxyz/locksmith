package feature

import (
	"sync"
	"time"
)

// autocompleteCacheTTL is how long autocomplete reuses API results. It is long
// enough to keep every keystroke off the ChastiSafe API, and short enough that
// a new template or lock appears before the member finishes typing.
const autocompleteCacheTTL = 30 * time.Second

// ttlCache is a small per-member cache for autocomplete API results.
type ttlCache[T any] struct {
	mu      sync.Mutex
	entries map[string]ttlEntry[T]
	ttl     time.Duration
	now     func() time.Time
}

type ttlEntry[T any] struct {
	value   T
	expires time.Time
}

func newTTLCache[T any](ttl time.Duration, now func() time.Time) *ttlCache[T] {
	if now == nil {
		now = time.Now
	}
	return &ttlCache[T]{
		entries: make(map[string]ttlEntry[T]),
		ttl:     ttl,
		now:     now,
	}
}

// get returns a cached value that has not expired.
func (c *ttlCache[T]) get(key string) (T, bool) {
	var zero T
	if c == nil {
		return zero, false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[key]
	if !ok {
		return zero, false
	}
	if !c.now().Before(entry.expires) {
		delete(c.entries, key)
		return zero, false
	}
	return entry.value, true
}

// put stores a value and prunes expired entries.
func (c *ttlCache[T]) put(key string, value T) {
	if c == nil {
		return
	}

	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()

	for existing, entry := range c.entries {
		if !now.Before(entry.expires) {
			delete(c.entries, existing)
		}
	}
	c.entries[key] = ttlEntry[T]{value: value, expires: now.Add(c.ttl)}
}

// forget drops one member's cached value.
func (c *ttlCache[T]) forget(key string) {
	if c == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
}
