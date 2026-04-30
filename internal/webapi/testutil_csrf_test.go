package webapi_test

import (
	"net/http"
	"testing"

	"github.com/gambtho/cronfoundry/internal/webapi"
)

// withCSRF attaches a matching cf_csrf cookie + X-CSRF-Token header to req
// so it survives the CSRF middleware in RegisterRoutes' adminOnly chain.
// All handler tests that exercise mutating endpoints through the mux must
// call this on the request before serving.
func withCSRF(_ *testing.T, req *http.Request) *http.Request {
	const tok = "test-csrf-token-value"
	req.AddCookie(&http.Cookie{Name: webapi.CSRFCookieName, Value: tok})
	req.Header.Set(webapi.CSRFHeaderName, tok)
	return req
}
