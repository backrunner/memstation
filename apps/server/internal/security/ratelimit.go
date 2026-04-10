package security

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"
)

type rateBucket struct {
	tokens     float64
	lastRefill time.Time
}

type RateLimiter struct {
	mu      sync.Mutex
	rate    float64
	burst   float64
	buckets map[string]rateBucket
}

func NewRateLimiter(ratePerSecond float64, burst int) *RateLimiter {
	if ratePerSecond <= 0 || burst <= 0 {
		return nil
	}
	return &RateLimiter{
		rate:    ratePerSecond,
		burst:   float64(burst),
		buckets: map[string]rateBucket{},
	}
}

func (l *RateLimiter) Allow(key string) bool {
	if l == nil || key == "" {
		return true
	}
	now := time.Now()

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.rate <= 0 || l.burst <= 0 {
		return true
	}

	bucket, ok := l.buckets[key]
	if !ok {
		l.buckets[key] = rateBucket{
			tokens:     l.burst - 1,
			lastRefill: now,
		}
		return true
	}

	elapsed := now.Sub(bucket.lastRefill).Seconds()
	bucket.tokens += elapsed * l.rate
	if bucket.tokens > l.burst {
		bucket.tokens = l.burst
	}
	bucket.lastRefill = now
	if bucket.tokens < 1 {
		l.buckets[key] = bucket
		return false
	}
	bucket.tokens--
	l.buckets[key] = bucket
	return true
}

func (l *RateLimiter) Update(ratePerSecond float64, burst int) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.rate = ratePerSecond
	l.burst = float64(burst)
	l.buckets = map[string]rateBucket{}
	l.mu.Unlock()
}

func RateLimitKey(userID string, r *http.Request) string {
	return RateLimitKeyFromToken(userID, bearerToken(r.Header.Get("Authorization")), "")
}

func RateLimitKeyFromToken(userID, token, scope string) string {
	if userID == "" {
		return ""
	}
	token = strings.TrimSpace(token)
	if token == "" {
		if scope == "" {
			return userID
		}
		return userID + ":" + scope
	}
	sum := sha256.Sum256([]byte(token))
	key := userID + ":" + hex.EncodeToString(sum[:8])
	if scope != "" {
		key += ":" + scope
	}
	return key
}

func RateLimitKeyForRequest(userID, scope string, r *http.Request) string {
	if userID == "" {
		return userID
	}
	return RateLimitKeyFromToken(userID, bearerToken(r.Header.Get("Authorization")), scope)
}
