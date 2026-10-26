package module5_plugin

import (
	"testing"
	"time"
)

func TestMatchRule(t *testing.T) {
	rules := []RateRule{
		{
			Endpoint:          "/api",
			LimitRequests:     10,
			TimeWindowSeconds: 1,
		},
		{
			Endpoint:          "/static",
			LimitRequests:     100,
			TimeWindowSeconds: 1,
		},
	}

	tests := []struct {
		name     string
		path     string
		wantRule string
		wantOK   bool
	}{
		{
			name:     "exact API path",
			path:     "/api",
			wantRule: "/api",
			wantOK:   true,
		},
		{
			name:     "nested API path",
			path:     "/api/users",
			wantRule: "/api",
			wantOK:   true,
		},
		{
			name:     "exact static path",
			path:     "/static",
			wantRule: "/static",
			wantOK:   true,
		},
		{
			name:     "nested static path",
			path:     "/static/image.png",
			wantRule: "/static",
			wantOK:   true,
		},
		{
			name:   "unmatched path",
			path:   "/other",
			wantOK: false,
		},
		{
			name:   "similar but different prefix",
			path:   "/apix",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := matchRule(tt.path, rules)

			if ok != tt.wantOK {
				t.Fatalf("matchRule(%q) matched = %v, want %v", tt.path, ok, tt.wantOK)
			}

			if ok && got.Endpoint != tt.wantRule {
				t.Errorf("matchRule(%q) endpoint = %q, want %q", tt.path, got.Endpoint, tt.wantRule)
			}
		})
	}
}

func TestTokenBucketWithRedis(t *testing.T) {
	addr := "127.0.0.1:6379"

	if err := redisPing(addr); err != nil {
		t.Skipf("Redis không truy cập được tại %s: %v", addr, err)
	}

	key := "test:module5:token-bucket"
	_, _ = redisCommand(addr, "DEL", key)
	t.Cleanup(func() {
		_, _ = redisCommand(addr, "DEL", key)
	})

	// Bucket capacity 2, refill 1 token per second.
	allowed1, _, err := checkTokenBucket(addr, key, 2, 1)
	if err != nil {
		t.Fatalf("first request returned error: %v", err)
	}
	allowed2, _, err := checkTokenBucket(addr, key, 2, 1)
	if err != nil {
		t.Fatalf("second request returned error: %v", err)
	}
	allowed3, retryAfter, err := checkTokenBucket(addr, key, 2, 1)
	if err != nil {
		t.Fatalf("third request returned error: %v", err)
	}

	if !allowed1 || !allowed2 {
		t.Fatal("first two requests should be allowed by a full bucket")
	}
	if allowed3 {
		t.Fatal("third request should be rejected after the bucket is exhausted")
	}
	if retryAfter <= 0 {
		t.Fatalf("retryAfter should be positive, got %v", retryAfter)
	}

	// Wait for one token to refill.
	time.Sleep(1100 * time.Millisecond)

	allowed4, _, err := checkTokenBucket(addr, key, 2, 1)
	if err != nil {
		t.Fatalf("request after refill returned error: %v", err)
	}
	if !allowed4 {
		t.Fatal("request should be allowed after a token refills")
	}
}
