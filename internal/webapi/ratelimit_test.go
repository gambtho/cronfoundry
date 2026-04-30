package webapi

import (
	"net/http/httptest"
	"testing"

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
