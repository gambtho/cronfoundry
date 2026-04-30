package webapi

import (
	"net"
	"net/http"
	"strings"
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
