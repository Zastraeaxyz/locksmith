package feature

import (
	"testing"
	"time"
)

func TestTTLCache(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	cache := newTTLCache[string](30*time.Second, func() time.Time { return now })

	if _, ok := cache.get("me"); ok {
		t.Fatal("empty cache returned a value")
	}

	cache.put("me", "value")
	if got, ok := cache.get("me"); !ok || got != "value" {
		t.Fatalf("get = %q, %v", got, ok)
	}

	now = now.Add(29 * time.Second)
	if got, ok := cache.get("me"); !ok || got != "value" {
		t.Fatalf("just before expiry = %q, %v", got, ok)
	}

	now = now.Add(2 * time.Second)
	if _, ok := cache.get("me"); ok {
		t.Fatal("expired value was returned")
	}

	cache.put("you", "other")
	cache.forget("you")
	if _, ok := cache.get("you"); ok {
		t.Fatal("forgotten value was returned")
	}
}

func TestTTLCacheNilSafe(t *testing.T) {
	var cache *ttlCache[string]
	if _, ok := cache.get("me"); ok {
		t.Fatal("nil cache returned a value")
	}
	cache.put("me", "value")
	cache.forget("me")
}
