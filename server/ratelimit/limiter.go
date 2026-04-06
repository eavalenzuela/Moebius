package ratelimit

import (
	"math"
	"sync"
	"time"
)

// BucketConfig defines the rate and burst capacity for a limiter tier.
type BucketConfig struct {
	Rate  float64 // tokens per second
	Burst int     // maximum tokens (burst capacity)
}

type bucket struct {
	tokens   float64
	lastTime time.Time
}

// KeyedLimiter manages per-key token buckets with lazy eviction.
type KeyedLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	rate    float64
	burst   float64
	ttl     time.Duration
	calls   int
}

// NewKeyedLimiter creates a limiter that tracks separate token buckets per key.
// cleanupTTL controls how long idle entries are kept before eviction.
func NewKeyedLimiter(cfg BucketConfig, cleanupTTL time.Duration) *KeyedLimiter {
	return &KeyedLimiter{
		buckets: make(map[string]*bucket),
		rate:    cfg.Rate,
		burst:   float64(cfg.Burst),
		ttl:     cleanupTTL,
	}
}

// Allow checks whether a request for the given key is permitted.
// It returns whether the request is allowed and, if not, the duration
// until a token becomes available.
func (kl *KeyedLimiter) Allow(key string) (bool, time.Duration) {
	now := time.Now()

	kl.mu.Lock()
	defer kl.mu.Unlock()

	b, ok := kl.buckets[key]
	if !ok {
		b = &bucket{tokens: kl.burst, lastTime: now}
		kl.buckets[key] = b
	}

	// Refill tokens based on elapsed time.
	elapsed := now.Sub(b.lastTime).Seconds()
	b.tokens = math.Min(kl.burst, b.tokens+elapsed*kl.rate)
	b.lastTime = now

	if b.tokens >= 1 {
		b.tokens--
		kl.maybeCleanup(now)
		return true, 0
	}

	// Calculate wait time until one token is available.
	wait := time.Duration((1 - b.tokens) / kl.rate * float64(time.Second))
	kl.maybeCleanup(now)
	return false, wait
}

// maybeCleanup runs lazy eviction every 1024 calls. Must be called with mu held.
func (kl *KeyedLimiter) maybeCleanup(now time.Time) {
	kl.calls++
	if kl.calls < 1024 {
		return
	}
	kl.calls = 0
	cutoff := now.Add(-kl.ttl)
	for k, b := range kl.buckets {
		if b.lastTime.Before(cutoff) {
			delete(kl.buckets, k)
		}
	}
}

// Len returns the number of tracked keys (for testing).
func (kl *KeyedLimiter) Len() int {
	kl.mu.Lock()
	defer kl.mu.Unlock()
	return len(kl.buckets)
}
