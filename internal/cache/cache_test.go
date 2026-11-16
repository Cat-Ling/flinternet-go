package cache

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestCacheSetGetAndLRUEviction(t *testing.T) {
	c := New[string, int](2, time.Minute)
	c.Set("a", 1)
	c.Set("b", 2)

	if got, ok := c.Get("a"); !ok || got != 1 {
		t.Fatalf("expected a=1, got %d (ok=%v)", got, ok)
	}

	// "a" was accessed most recently, so "b" must be evicted when "c" is added.
	c.Set("c", 3)
	if _, ok := c.Get("b"); ok {
		t.Fatalf("expected least-recently-used key b to be evicted")
	}
	if got, ok := c.Get("a"); !ok || got != 1 {
		t.Fatalf("expected a=1 after eviction of b, got %d (ok=%v)", got, ok)
	}
	if got, ok := c.Get("c"); !ok || got != 3 {
		t.Fatalf("expected c=3, got %d (ok=%v)", got, ok)
	}
}

func TestCacheTTLExpiration(t *testing.T) {
	current := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	c := newWithClock[string, string](4, 30*time.Second, func() time.Time { return current })

	c.Set("k1", "v1")
	if got, ok := c.Get("k1"); !ok || got != "v1" {
		t.Fatalf("expected k1=v1 before expiry, got %q (ok=%v)", got, ok)
	}

	current = current.Add(30 * time.Second)
	if _, ok := c.Get("k1"); ok {
		t.Fatalf("expected k1 to expire at ttl boundary")
	}
	if c.Len() != 0 {
		t.Fatalf("expected Len()=0 after expiration, got %d", c.Len())
	}
}

func TestCacheUpdateAndPurge(t *testing.T) {
	c := New[string, int](3, time.Minute)
	c.Set("x", 10)
	c.Set("x", 42)

	if got, ok := c.Get("x"); !ok || got != 42 {
		t.Fatalf("expected updated x=42, got %d (ok=%v)", got, ok)
	}
	if c.Len() != 1 {
		t.Fatalf("expected Len()=1, got %d", c.Len())
	}

	c.Purge()
	if c.Len() != 0 {
		t.Fatalf("expected Len()=0 after Purge, got %d", c.Len())
	}
}

func TestCacheConcurrentAccess(t *testing.T) {
	c := New[string, int](32, 5*time.Second)
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := range 100 {
				key := fmt.Sprintf("k-%d", (worker*100+i)%48)
				c.Set(key, i)
				c.Get(key)
			}
		}(g)
	}
	wg.Wait()
	if c.Len() > 32 {
		t.Fatalf("cache exceeded maxEntries=32: got %d", c.Len())
	}
}
