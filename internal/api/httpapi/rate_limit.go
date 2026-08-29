package httpapi

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const maximumRateLimitKeys = 4096

type attemptWindow struct {
	started  time.Time
	lastSeen time.Time
	count    int
}

// attemptLimiter is deliberately small and in-memory. Credential truth stays
// in the durable auth store; this only bounds online guessing during one
// daemon lifetime and uses a capped key map so spoofed addresses cannot grow
// memory without bound.
type attemptLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	now     func() time.Time
	buckets map[string]attemptWindow
}

func newAttemptLimiter(limit int, window time.Duration) *attemptLimiter {
	return &attemptLimiter{
		limit:   limit,
		window:  window,
		now:     time.Now,
		buckets: make(map[string]attemptWindow),
	}
}

func (limiter *attemptLimiter) allow(key string) (bool, time.Duration) {
	if limiter == nil || limiter.limit < 1 || limiter.window <= 0 {
		return false, 0
	}
	now := limiter.now()
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	bucket, exists := limiter.buckets[key]
	if !exists || !now.Before(bucket.started.Add(limiter.window)) {
		if !exists && len(limiter.buckets) >= maximumRateLimitKeys {
			limiter.evictOldest()
		}
		limiter.buckets[key] = attemptWindow{started: now, lastSeen: now, count: 1}
		return true, 0
	}
	bucket.lastSeen = now
	if bucket.count >= limiter.limit {
		limiter.buckets[key] = bucket
		return false, bucket.started.Add(limiter.window).Sub(now)
	}
	bucket.count++
	limiter.buckets[key] = bucket
	return true, 0
}

func (limiter *attemptLimiter) reset(key string) {
	if limiter == nil {
		return
	}
	limiter.mu.Lock()
	delete(limiter.buckets, key)
	limiter.mu.Unlock()
}

func (limiter *attemptLimiter) evictOldest() {
	var oldestKey string
	var oldest time.Time
	for key, bucket := range limiter.buckets {
		if oldestKey == "" || bucket.lastSeen.Before(oldest) {
			oldestKey = key
			oldest = bucket.lastSeen
		}
	}
	delete(limiter.buckets, oldestKey)
}

func directClientKey(request *http.Request) string {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		return "unknown"
	}
	if address, _, found := strings.Cut(host, "%"); found {
		host = address
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return "unknown"
	}
	return ip.String()
}
