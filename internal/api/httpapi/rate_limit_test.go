package httpapi

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestAttemptLimiterBoundsAndExpiresAttempts(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	limiter := newAttemptLimiter(2, time.Minute)
	limiter.now = func() time.Time { return now }
	if allowed, _ := limiter.allow("client"); !allowed {
		t.Fatal("first attempt denied")
	}
	if allowed, _ := limiter.allow("client"); !allowed {
		t.Fatal("second attempt denied")
	}
	if allowed, retry := limiter.allow("client"); allowed || retry != time.Minute {
		t.Fatalf("third attempt = allowed %v, retry %s", allowed, retry)
	}
	now = now.Add(time.Minute)
	if allowed, _ := limiter.allow("client"); !allowed {
		t.Fatal("attempt after window expiry denied")
	}
	limiter.reset("client")
	if allowed, _ := limiter.allow("client"); !allowed {
		t.Fatal("attempt after reset denied")
	}
}

func TestDirectClientKeyIgnoresForwardingHeaders(t *testing.T) {
	request := httptest.NewRequest("POST", "/api/v1/auth/login", nil)
	request.RemoteAddr = "192.0.2.20:54321"
	request.Header.Set("X-Forwarded-For", "198.51.100.8")
	if got := directClientKey(request); got != "192.0.2.20" {
		t.Fatalf("client key = %q", got)
	}
}

func TestDirectClientKeyAcceptsScopedIPv6PeerAddress(t *testing.T) {
	request := httptest.NewRequest("POST", "/api/v1/auth/login", nil)
	request.RemoteAddr = "[fe80::1%en0]:54321"
	if got := directClientKey(request); got != "fe80::1" {
		t.Fatalf("client key = %q", got)
	}
}

func TestAttemptLimiterCapsAddressMap(t *testing.T) {
	limiter := newAttemptLimiter(1, time.Hour)
	for index := range maximumRateLimitKeys + 100 {
		limiter.allow(string(rune(index + 1)))
	}
	if len(limiter.buckets) != maximumRateLimitKeys {
		t.Fatalf("bucket count = %d", len(limiter.buckets))
	}
}
