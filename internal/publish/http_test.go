package publish

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/config"
	"github.com/gambtho/cronfoundry/internal/template"
)

func TestHTTP_Publish_DefaultEnvelope(t *testing.T) {
	var gotBody map[string]any
	var gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := NewHTTPPublisher()
	dest := config.Destination{HTTP: &config.HTTPDest{URL: srv.URL}}
	res := p.Publish(context.Background(), dest, "hello world", template.Context{}, mapSecrets{})

	require.True(t, res.OK, "err: %v", res.Err)
	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Equal(t, "hello world", gotBody["output"])
}

func TestHTTP_Publish_BodyTemplate(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := NewHTTPPublisher()
	dest := config.Destination{HTTP: &config.HTTPDest{
		URL:          srv.URL,
		BodyTemplate: `{"text":"{{ output }}","channel":"#ops"}`,
	}}
	res := p.Publish(context.Background(), dest, "report", template.Context{Output: "report"}, mapSecrets{})

	require.True(t, res.OK, "err: %v", res.Err)
	assert.Equal(t, "report", gotBody["text"])
	assert.Equal(t, "#ops", gotBody["channel"])
}

func TestHTTP_Publish_BearerToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := NewHTTPPublisher()
	dest := config.Destination{HTTP: &config.HTTPDest{
		URL:    srv.URL,
		Secret: "tok",
	}}
	res := p.Publish(context.Background(), dest, "x", template.Context{}, mapSecrets{"tok": "my-token"})

	require.True(t, res.OK, "err: %v", res.Err)
	assert.Equal(t, "Bearer my-token", gotAuth)
}

func TestHTTP_Publish_StaticHeaders(t *testing.T) {
	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Source")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := NewHTTPPublisher()
	dest := config.Destination{HTTP: &config.HTTPDest{
		URL:     srv.URL,
		Headers: map[string]string{"X-Source": "cronfoundry"},
	}}
	res := p.Publish(context.Background(), dest, "x", template.Context{}, mapSecrets{})

	require.True(t, res.OK)
	assert.Equal(t, "cronfoundry", gotHeader)
}

func TestHTTP_Publish_NonOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := NewHTTPPublisher()
	dest := config.Destination{HTTP: &config.HTTPDest{URL: srv.URL}}
	res := p.Publish(context.Background(), dest, "x", template.Context{}, mapSecrets{})

	assert.False(t, res.OK)
	assert.NotNil(t, res.Err)
	assert.Contains(t, res.Detail, "http 500")
}

func TestHTTP_Publish_SecretResolveFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := NewHTTPPublisher()
	dest := config.Destination{HTTP: &config.HTTPDest{URL: srv.URL, Secret: "missing"}}
	res := p.Publish(context.Background(), dest, "x", template.Context{}, mapSecrets{})

	assert.False(t, res.OK)
	require.NotNil(t, res.Err)
	assert.Contains(t, res.Err.Error(), "http: resolve secret")
}

func TestHTTP_Publish_CustomMethod(t *testing.T) {
	var gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := NewHTTPPublisher()
	dest := config.Destination{HTTP: &config.HTTPDest{URL: srv.URL, Method: "PUT"}}
	res := p.Publish(context.Background(), dest, "x", template.Context{}, mapSecrets{})

	require.True(t, res.OK)
	assert.Equal(t, "PUT", gotMethod)
}
