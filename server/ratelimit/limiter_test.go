package ratelimit

import (
	"sync"
	"testing"
	"time"
)

func TestAllow_UnderLimit(t *testing.T) {
	kl := NewKeyedLimiter(BucketConfig{Rate: 10, Burst: 5}, time.Minute)
	for i := 0; i < 5; i++ {
		allowed, _ := kl.Allow("key1")
		if !allowed {
			t.Fatalf("request %d should be allowed (within burst)", i)
		}
	}
}

func TestAllow_ExceedsBurst(t *testing.T) {
	kl := NewKeyedLimiter(BucketConfig{Rate: 10, Burst: 3}, time.Minute)

	for i := 0; i < 3; i++ {
		allowed, _ := kl.Allow("key1")
		if !allowed {
			t.Fatalf("request %d should be allowed", i)
		}
	}

	allowed, retryAfter := kl.Allow("key1")
	if allowed {
		t.Fatal("request exceeding burst should be rejected")
	}
	if retryAfter <= 0 {
		t.Fatal("retryAfter should be positive")
	}
}

func TestAllow_RefillsOverTime(t *testing.T) {
	kl := NewKeyedLimiter(BucketConfig{Rate: 10, Burst: 2}, time.Minute)

	// Exhaust burst
	kl.Allow("key1")
	kl.Allow("key1")

	allowed, _ := kl.Allow("key1")
	if allowed {
		t.Fatal("should be rejected after burst exhausted")
	}

	// Simulate time passing by manipulating the bucket directly
	kl.mu.Lock()
	kl.buckets["key1"].lastTime = time.Now().Add(-200 * time.Millisecond) // enough for 2 tokens at rate 10/s
	kl.mu.Unlock()

	allowed, _ = kl.Allow("key1")
	if !allowed {
		t.Fatal("should be allowed after tokens refill")
	}
}

func TestAllow_RetryAfterDuration(t *testing.T) {
	kl := NewKeyedLimiter(BucketConfig{Rate: 1, Burst: 1}, time.Minute) // 1 token/sec

	kl.Allow("key1") // consume the one token

	_, retryAfter := kl.Allow("key1")
	// Should be approximately 1 second
	if retryAfter < 500*time.Millisecond || retryAfter > 2*time.Second {
		t.Fatalf("retryAfter should be ~1s, got %v", retryAfter)
	}
}

func TestAllow_IndependentKeys(t *testing.T) {
	kl := NewKeyedLimiter(BucketConfig{Rate: 10, Burst: 1}, time.Minute)

	allowed, _ := kl.Allow("key1")
	if !allowed {
		t.Fatal("key1 first request should be allowed")
	}
	allowed, _ = kl.Allow("key1")
	if allowed {
		t.Fatal("key1 second request should be rejected")
	}

	// key2 is independent
	allowed, _ = kl.Allow("key2")
	if !allowed {
		t.Fatal("key2 should be allowed (independent bucket)")
	}
}

func TestAllow_Cleanup(t *testing.T) {
	kl := NewKeyedLimiter(BucketConfig{Rate: 10, Burst: 5}, 1*time.Millisecond)

	kl.Allow("old-key")
	time.Sleep(5 * time.Millisecond) // exceed TTL

	// Force cleanup by making enough calls
	kl.mu.Lock()
	kl.calls = 1023
	kl.mu.Unlock()

	kl.Allow("trigger") // triggers cleanup on call 1024

	if kl.Len() != 1 {
		t.Fatalf("expected 1 bucket (trigger), got %d", kl.Len())
	}
}

func TestAllow_ConcurrentAccess(t *testing.T) {
	kl := NewKeyedLimiter(BucketConfig{Rate: 1000, Burst: 100}, time.Minute)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				kl.Allow("concurrent-key")
			}
		}()
	}
	wg.Wait()
	// No panic or race = pass (run with -race flag)
}

func TestAllow_TokensCappedAtBurst(t *testing.T) {
	kl := NewKeyedLimiter(BucketConfig{Rate: 100, Burst: 3}, time.Minute)

	// Let a long time pass for one key
	kl.Allow("key1")
	kl.mu.Lock()
	kl.buckets["key1"].lastTime = time.Now().Add(-1 * time.Hour)
	kl.mu.Unlock()

	// Even after huge elapsed time, only burst-many requests should succeed
	for i := 0; i < 3; i++ {
		allowed, _ := kl.Allow("key1")
		if !allowed {
			t.Fatalf("request %d should be allowed (within burst cap)", i)
		}
	}
	allowed, _ := kl.Allow("key1")
	if allowed {
		t.Fatal("request beyond burst should be rejected even with long refill time")
	}
}
