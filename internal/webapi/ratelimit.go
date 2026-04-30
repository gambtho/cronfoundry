package webapi

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"

	lru "github.com/hashicorp/golang-lru/v2"
	"golang.org/x/time/rate"
)

// clientIP returns the request's client IP. When trustProxy is true, it
// honors the leftmost entry of X-Forwarded-For; otherwise it always uses
// r.RemoteAddr. Falls back to RemoteAddr if XFF is missing or whitespace.
// Strips ports; never returns "".
func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			leftmost := strings.TrimSpace(strings.SplitN(xff, ",", 2)[0])
			if leftmost != "" {
				return leftmost
			}
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// RateLimiterConfig holds the operator-tunable rate-limit knobs. Zero RPM
// for a group disables that group; Disabled=true disables the entire
// middleware (kill switch).
type RateLimiterConfig struct {
	TrustProxy       bool
	Disabled         bool
	APIRPM           int
	OAuthRPM         int
	WebhookRPM       int
	SSEMaxConcurrent int
	LRUSize          int
}

// RateLimiter holds per-group token-bucket state plus an SSE concurrency
// counter. Construct with NewRateLimiter.
type RateLimiter struct {
	cfg           RateLimiterConfig
	groups        map[string]*groupBucket
	sseConcurrent sync.Map // ip → *int64
}

type groupBucket struct {
	limit rate.Limit
	burst int
	mu    sync.Mutex
	lru   *lru.Cache[string, *rate.Limiter]
}

// NewRateLimiter returns a RateLimiter. LRUSize defaults to 4096 if < 1.
func NewRateLimiter(cfg RateLimiterConfig) (*RateLimiter, error) {
	if cfg.LRUSize < 1 {
		cfg.LRUSize = 4096
	}
	rl := &RateLimiter{cfg: cfg, groups: make(map[string]*groupBucket, 3)}
	for _, g := range []struct {
		name string
		rpm  int
	}{
		{"api", cfg.APIRPM},
		{"oauth", cfg.OAuthRPM},
		{"webhook", cfg.WebhookRPM},
	} {
		if g.rpm <= 0 {
			rl.groups[g.name] = nil // sentinel: group disabled
			continue
		}
		burst := g.rpm / 6
		if burst < 3 {
			burst = 3
		}
		c, err := lru.New[string, *rate.Limiter](cfg.LRUSize)
		if err != nil {
			return nil, fmt.Errorf("ratelimit: lru for %s: %w", g.name, err)
		}
		rl.groups[g.name] = &groupBucket{
			limit: rate.Limit(float64(g.rpm) / 60.0),
			burst: burst,
			lru:   c,
		}
	}
	return rl, nil
}

// Group returns a middleware applying the named group's per-IP token bucket.
// Unknown group name panics — caller bug.
func (rl *RateLimiter) Group(name string, next http.Handler) http.Handler {
	if rl.cfg.Disabled {
		return next
	}
	b, ok := rl.groups[name]
	if !ok {
		panic(fmt.Sprintf("ratelimit: unknown group %q", name))
	}
	if b == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r, rl.cfg.TrustProxy)
		lim := b.limiterFor(ip)
		if !lim.Allow() {
			rejectRateLimit(w, r, name, ip, "1")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (b *groupBucket) limiterFor(ip string) *rate.Limiter {
	b.mu.Lock()
	defer b.mu.Unlock()
	if lim, ok := b.lru.Get(ip); ok {
		return lim
	}
	lim := rate.NewLimiter(b.limit, b.burst)
	b.lru.Add(ip, lim)
	return lim
}

func rejectRateLimit(w http.ResponseWriter, r *http.Request, group, ip, retryAfter string) {
	slog.Info("ratelimit reject", "group", group, "ip", ip, "method", r.Method, "path", r.URL.Path)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", retryAfter)
	w.WriteHeader(http.StatusTooManyRequests)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "rate limited"})
}
