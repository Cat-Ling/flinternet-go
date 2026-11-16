// Package cache provides a thread-safe generic LRU cache with per-entry TTL expiration.
package cache

import (
	"container/list"
	"sync"
	"time"
)

type cacheEntry[K comparable, V any] struct {
	key       K
	value     V
	expiresAt time.Time
}

// Cache stores up to maxEntries items with a fixed time-to-live.
type Cache[K comparable, V any] struct {
	mu         sync.Mutex
	maxEntries int
	ttl        time.Duration
	now        func() time.Time
	items      map[K]*list.Element
	order      *list.List
}

// New constructs a Cache bounded by maxEntries and entry TTL.
// Non-positive maxEntries disables capacity limits; non-positive ttl disables expiration.
func New[K comparable, V any](maxEntries int, ttl time.Duration) *Cache[K, V] {
	return newWithClock[K, V](maxEntries, ttl, time.Now)
}

func newWithClock[K comparable, V any](maxEntries int, ttl time.Duration, now func() time.Time) *Cache[K, V] {
	return &Cache[K, V]{
		maxEntries: maxEntries,
		ttl:        ttl,
		now:        now,
		items:      make(map[K]*list.Element),
		order:      list.New(),
	}
}

// Get retrieves an unexpired entry and promotes it to the front of the LRU list.
func (c *Cache[K, V]) Get(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.items[key]
	if !ok {
		var zero V
		return zero, false
	}
	entry := elem.Value.(*cacheEntry[K, V])
	if !entry.expiresAt.IsZero() && !c.now().Before(entry.expiresAt) {
		c.removeElement(elem)
		var zero V
		return zero, false
	}
	c.order.MoveToFront(elem)
	return entry.value, true
}

// Set inserts or updates an entry, evicting expired or least-recently-used entries if full.
func (c *Cache[K, V]) Set(key K, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	var expiresAt time.Time
	if c.ttl > 0 {
		expiresAt = now.Add(c.ttl)
	}

	if elem, ok := c.items[key]; ok {
		entry := elem.Value.(*cacheEntry[K, V])
		entry.value = value
		entry.expiresAt = expiresAt
		c.order.MoveToFront(elem)
		return
	}

	c.evictExpiredLocked(now)
	if c.maxEntries > 0 && c.order.Len() >= c.maxEntries {
		if tail := c.order.Back(); tail != nil {
			c.removeElement(tail)
		}
	}

	entry := &cacheEntry[K, V]{
		key:       key,
		value:     value,
		expiresAt: expiresAt,
	}
	elem := c.order.PushFront(entry)
	c.items[key] = elem
}

// Len returns the number of entries currently stored, purging expired items first.
func (c *Cache[K, V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.evictExpiredLocked(c.now())
	return c.order.Len()
}

// Purge clears all entries from the cache.
func (c *Cache[K, V]) Purge() {
	c.mu.Lock()
	defer c.mu.Unlock()

	clear(c.items)
	c.order.Init()
}

func (c *Cache[K, V]) evictExpiredLocked(now time.Time) {
	if c.ttl <= 0 {
		return
	}
	for elem := c.order.Back(); elem != nil; {
		prev := elem.Prev()
		entry := elem.Value.(*cacheEntry[K, V])
		if !entry.expiresAt.IsZero() && !now.Before(entry.expiresAt) {
			c.removeElement(elem)
		}
		elem = prev
	}
}

func (c *Cache[K, V]) removeElement(elem *list.Element) {
	entry := elem.Value.(*cacheEntry[K, V])
	delete(c.items, entry.key)
	c.order.Remove(elem)
}
