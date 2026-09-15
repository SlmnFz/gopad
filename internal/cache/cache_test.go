package cache

import (
	"sync"
	"testing"
	"time"
)

func TestCacheMissingKeyReturnsZeroValue(t *testing.T) {
	entries := New[string, int](time.Second, 4)
	defer entries.Close()

	value, ok := entries.Get("missing")
	if ok || value != 0 {
		t.Fatalf("Get(missing) = (%d, %t), want (0, false)", value, ok)
	}
}

func TestCacheExpiredEntryIsMissBeforeSweep(t *testing.T) {
	entries := New[string, string](40*time.Millisecond, 4)
	defer entries.Close()
	entries.Set("key", "value")
	time.Sleep(60 * time.Millisecond)

	if value, ok := entries.Get("key"); ok || value != "" {
		t.Fatalf("expired Get = (%q, %t), want (empty, false)", value, ok)
	}
}

func TestCacheEvictsOldestExpiryAtCapacity(t *testing.T) {
	entries := New[string, int](time.Second, 2)
	defer entries.Close()
	entries.Set("first", 1)
	time.Sleep(2 * time.Millisecond)
	entries.Set("second", 2)
	entries.Set("third", 3)

	if _, ok := entries.Get("first"); ok {
		t.Fatal("oldest entry was not evicted")
	}
	if entries.Len() != 2 {
		t.Fatalf("cache length = %d, want 2", entries.Len())
	}
}

func TestCacheConcurrentGetSet(t *testing.T) {
	entries := New[int, int](time.Second, 128)
	defer entries.Close()
	var waitGroup sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		waitGroup.Add(1)
		go func(worker int) {
			defer waitGroup.Done()
			for iteration := 0; iteration < 500; iteration++ {
				key := (worker + iteration) % 32
				entries.Set(key, iteration)
				_, _ = entries.Get(key)
			}
		}(worker)
	}
	waitGroup.Wait()
	if entries.Len() == 0 {
		t.Fatal("concurrent writes left the cache empty")
	}
}

func TestCacheCloseStopsSweeper(t *testing.T) {
	entries := New[string, string](time.Second, 4)
	entries.Close()
	select {
	case <-entries.done:
	default:
		t.Fatal("sweeper did not stop before Close returned")
	}
	entries.Close()
}

func TestSlugCacheSeparatesPositiveAndNegativeResults(t *testing.T) {
	entries := NewSlugCache(time.Second, time.Second, 4)
	defer entries.Close()

	if _, cached := entries.Lookup("doc"); cached {
		t.Fatal("uncached slug reported as cached")
	}
	entries.SetMissing("doc")
	if exists, cached := entries.Lookup("doc"); exists || !cached {
		t.Fatalf("missing slug lookup = (%t, %t), want (false, true)", exists, cached)
	}
	entries.SetExists("doc")
	if exists, cached := entries.Lookup("doc"); !exists || !cached {
		t.Fatalf("existing slug lookup = (%t, %t), want (true, true)", exists, cached)
	}
}
