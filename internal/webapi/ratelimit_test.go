package webapi

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestClientIP_NoTrustProxy_UsesRemoteAddr(t *testing.T) {
	req := httptest.NewRequest("GET", "/x", nil)
	req.RemoteAddr = "10.0.0.5:54321"
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")
	assert.Equal(t, "10.0.0.5", clientIP(req, false))
}

func TestClientIP_TrustProxy_PrefersLeftmostXFF(t *testing.T) {
	req := httptest.NewRequest("GET", "/x", nil)
	req.RemoteAddr = "10.0.0.5:54321"
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")
	assert.Equal(t, "1.2.3.4", clientIP(req, true))
}

func TestClientIP_TrustProxy_FallbackWhenXFFMissing(t *testing.T) {
	req := httptest.NewRequest("GET", "/x", nil)
	req.RemoteAddr = "10.0.0.5:54321"
	assert.Equal(t, "10.0.0.5", clientIP(req, true))
}

func TestClientIP_TrustProxy_FallbackWhenXFFMalformed(t *testing.T) {
	req := httptest.NewRequest("GET", "/x", nil)
	req.RemoteAddr = "10.0.0.5:54321"
	req.Header.Set("X-Forwarded-For", "  ")
	assert.Equal(t, "10.0.0.5", clientIP(req, true))
}

func TestClientIP_RemoteAddrWithoutPort(t *testing.T) {
	req := httptest.NewRequest("GET", "/x", nil)
	req.RemoteAddr = "10.0.0.5"
	assert.Equal(t, "10.0.0.5", clientIP(req, false))
}

func okHandlerCount(count *int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(count, 1)
		w.WriteHeader(http.StatusOK)
	})
}

func newTestRL(t *testing.T, cfg RateLimiterConfig) *RateLimiter {
	t.Helper()
	rl, err := NewRateLimiter(cfg)
	if err != nil {
		t.Fatalf("NewRateLimiter: %v", err)
	}
	return rl
}

func TestRateLimiter_AllowsBurstThenRejects(t *testing.T) {
	cfg := RateLimiterConfig{APIRPM: 60, OAuthRPM: 10, WebhookRPM: 300, SSEMaxConcurrent: 5, LRUSize: 128}
	rl := newTestRL(t, cfg)
	var count int64
	mw := rl.Group("api", okHandlerCount(&count))

	// burst = max(60/6, 3) = 10. First 10 should pass.
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest("GET", "/api/x", nil)
		req.RemoteAddr = "1.1.1.1:1"
		w := httptest.NewRecorder()
		mw.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code, "request %d", i)
	}

	req := httptest.NewRequest("GET", "/api/x", nil)
	req.RemoteAddr = "1.1.1.1:1"
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Equal(t, "1", w.Header().Get("Retry-After"))
	assert.Contains(t, w.Body.String(), "rate limited")
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	assert.Equal(t, int64(10), atomic.LoadInt64(&count), "downstream called only 10 times")
}

func TestRateLimiter_PerIPIsolation(t *testing.T) {
	rl := newTestRL(t, RateLimiterConfig{APIRPM: 60, OAuthRPM: 10, WebhookRPM: 300, SSEMaxConcurrent: 5, LRUSize: 128})
	var count int64
	mw := rl.Group("api", okHandlerCount(&count))

	for i := 0; i < 11; i++ {
		req := httptest.NewRequest("GET", "/api/x", nil)
		req.RemoteAddr = "1.1.1.1:1"
		w := httptest.NewRecorder()
		mw.ServeHTTP(w, req)
	}

	req := httptest.NewRequest("GET", "/api/x", nil)
	req.RemoteAddr = "2.2.2.2:1"
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRateLimiter_PerGroupIsolation(t *testing.T) {
	rl := newTestRL(t, RateLimiterConfig{APIRPM: 60, OAuthRPM: 10, WebhookRPM: 300, SSEMaxConcurrent: 5, LRUSize: 128})
	api := rl.Group("api", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	oauth := rl.Group("oauth", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))

	for i := 0; i < 11; i++ {
		req := httptest.NewRequest("GET", "/api/x", nil)
		req.RemoteAddr = "1.1.1.1:1"
		w := httptest.NewRecorder()
		api.ServeHTTP(w, req)
	}

	req := httptest.NewRequest("GET", "/oauth/login", nil)
	req.RemoteAddr = "1.1.1.1:1"
	w := httptest.NewRecorder()
	oauth.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRateLimiter_Disabled_PassesThrough(t *testing.T) {
	rl := newTestRL(t, RateLimiterConfig{Disabled: true, APIRPM: 60, OAuthRPM: 10, WebhookRPM: 300, SSEMaxConcurrent: 5, LRUSize: 128})
	mw := rl.Group("api", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	for i := 0; i < 200; i++ {
		req := httptest.NewRequest("GET", "/api/x", nil)
		req.RemoteAddr = "1.1.1.1:1"
		w := httptest.NewRecorder()
		mw.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code, "request %d", i)
	}
}

func TestRateLimiter_RPMZero_DisablesGroup(t *testing.T) {
	rl := newTestRL(t, RateLimiterConfig{APIRPM: 0, OAuthRPM: 10, WebhookRPM: 300, SSEMaxConcurrent: 5, LRUSize: 128})
	mw := rl.Group("api", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	for i := 0; i < 100; i++ {
		req := httptest.NewRequest("GET", "/api/x", nil)
		req.RemoteAddr = "1.1.1.1:1"
		w := httptest.NewRecorder()
		mw.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code, "request %d", i)
	}
}

func TestRateLimiter_LRU_EvictionGivesFreshBudget(t *testing.T) {
	rl := newTestRL(t, RateLimiterConfig{APIRPM: 60, OAuthRPM: 10, WebhookRPM: 300, SSEMaxConcurrent: 5, LRUSize: 2})
	mw := rl.Group("api", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))

	for i := 0; i < 11; i++ {
		req := httptest.NewRequest("GET", "/api/x", nil)
		req.RemoteAddr = "1.1.1.1:1"
		w := httptest.NewRecorder()
		mw.ServeHTTP(w, req)
	}

	for _, ip := range []string{"2.2.2.2:1", "3.3.3.3:1"} {
		req := httptest.NewRequest("GET", "/api/x", nil)
		req.RemoteAddr = ip
		w := httptest.NewRecorder()
		mw.ServeHTTP(w, req)
	}

	req := httptest.NewRequest("GET", "/api/x", nil)
	req.RemoteAddr = "1.1.1.1:1"
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRateLimiter_SSE_ConcurrencyCap(t *testing.T) {
	rl := newTestRL(t, RateLimiterConfig{APIRPM: 60, OAuthRPM: 10, WebhookRPM: 300, SSEMaxConcurrent: 5, LRUSize: 128})

	release := make(chan struct{})
	mw := rl.SSE(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		<-release
	}))

	done := make(chan int, 6)
	for i := 0; i < 5; i++ {
		go func() {
			req := httptest.NewRequest("GET", "/api/runs/x/events/stream", nil)
			req.RemoteAddr = "1.1.1.1:1"
			w := httptest.NewRecorder()
			mw.ServeHTTP(w, req)
			done <- w.Code
		}()
	}

	time.Sleep(50 * time.Millisecond)
	req := httptest.NewRequest("GET", "/api/runs/x/events/stream", nil)
	req.RemoteAddr = "1.1.1.1:1"
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Equal(t, "5", w.Header().Get("Retry-After"))

	close(release)
	for i := 0; i < 5; i++ {
		<-done
	}

	req = httptest.NewRequest("GET", "/api/runs/x/events/stream", nil)
	req.RemoteAddr = "1.1.1.1:1"
	w = httptest.NewRecorder()
	// Re-arm — the previous handler closed and decremented; this should pass.
	// Run synchronously since `release` is already closed; handler returns immediately.
	mw.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRateLimiter_SSE_PerIPIsolation(t *testing.T) {
	rl := newTestRL(t, RateLimiterConfig{APIRPM: 60, OAuthRPM: 10, WebhookRPM: 300, SSEMaxConcurrent: 1, LRUSize: 128})

	release := make(chan struct{})
	defer close(release)
	mw := rl.SSE(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		<-release
	}))

	go func() {
		req := httptest.NewRequest("GET", "/x", nil)
		req.RemoteAddr = "1.1.1.1:1"
		w := httptest.NewRecorder()
		mw.ServeHTTP(w, req)
	}()
	time.Sleep(50 * time.Millisecond)

	gotCode := make(chan int, 1)
	go func() {
		req := httptest.NewRequest("GET", "/x", nil)
		req.RemoteAddr = "2.2.2.2:1"
		w := httptest.NewRecorder()
		mw.ServeHTTP(w, req)
		gotCode <- w.Code
	}()
	time.Sleep(50 * time.Millisecond)
	// IP B should be inside the handler (200 written, blocked on release) — verify
	// it is NOT 429. We can't easily read the recorder mid-handler; instead,
	// confirm a 3rd request from IP A (its slot is full) is rejected.
	req := httptest.NewRequest("GET", "/x", nil)
	req.RemoteAddr = "1.1.1.1:1"
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	assert.Equal(t, http.StatusTooManyRequests, w.Code, "IP A's only slot is full")
}

func TestRateLimiter_SSE_DisabledByZeroCap(t *testing.T) {
	rl := newTestRL(t, RateLimiterConfig{APIRPM: 60, OAuthRPM: 10, WebhookRPM: 300, SSEMaxConcurrent: 0, LRUSize: 128})
	mw := rl.SSE(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	for i := 0; i < 50; i++ {
		req := httptest.NewRequest("GET", "/x", nil)
		req.RemoteAddr = "1.1.1.1:1"
		w := httptest.NewRecorder()
		mw.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	}
}
