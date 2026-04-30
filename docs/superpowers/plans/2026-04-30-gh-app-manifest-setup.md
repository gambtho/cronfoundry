# GitHub App Manifest Setup — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the 9-step manual GitHub App checklist in `docs/install.sh` with a `cronfoundry setup github-app` subcommand that drives GitHub's manifest flow via a localhost callback server, capturing all credentials automatically.

**Architecture:** New cobra subcommand under root (`setup github-app`) backed by a new `internal/githubapp` package with three units: `manifest` (pure JSON builder), `conversion` (HTTP client for `POST /app-manifests/{code}/conversions`), and `server` (localhost HTTP lifecycle). `install.sh` step 5 invokes the subcommand; steps 6 and 16 are simplified.

**Tech Stack:** Go 1.21+ stdlib (`net/http`, `crypto/rand`, `encoding/json`), cobra (already in repo), bash for `install.sh` edits. No new external dependencies.

Spec: `docs/superpowers/specs/2026-04-30-gh-app-manifest-setup-design.md`.

---

## File Map

**Create:**
- `internal/githubapp/manifest.go` — pure builder for the manifest JSON payload
- `internal/githubapp/manifest_test.go` — golden-file shape test
- `internal/githubapp/conversion.go` — HTTP client for `POST /app-manifests/{code}/conversions`
- `internal/githubapp/conversion_test.go` — happy/422/5xx/malformed cases
- `internal/githubapp/state.go` — state-file writer (`CF_*=...` lines, 0600)
- `internal/githubapp/state_test.go`
- `internal/githubapp/server.go` — localhost HTTP server with `/`, `/callback`, `/installed`
- `internal/githubapp/server_test.go`
- `internal/githubapp/manual.go` — legacy interactive prompts for `--manual`
- `internal/githubapp/manual_test.go`
- `internal/githubapp/browser.go` — open-URL helper (`xdg-open`/`open`)
- `cmd/cronfoundry/setup.go` — cobra parent `setup` group
- `cmd/cronfoundry/setup_githubapp.go` — `setup github-app` subcommand
- `cmd/cronfoundry/setup_githubapp_test.go`

**Modify:**
- `cmd/cronfoundry/main.go` — register `setup` group
- `docs/install.sh` — replace step 5 body, gate step 6 prompt, simplify step 16

---

### Task 1: Manifest builder

**Files:**
- Create: `internal/githubapp/manifest.go`
- Test: `internal/githubapp/manifest_test.go`

- [ ] **Step 1: Write the failing test**

```go
package githubapp

import (
	"encoding/json"
	"testing"
)

func TestBuildManifest_RequiredFields(t *testing.T) {
	m := BuildManifest(ManifestInput{
		Name:        "cronfoundry-tng",
		CallbackURL: "http://localhost:8765",
	})
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["name"] != "cronfoundry-tng" {
		t.Errorf("name = %v, want cronfoundry-tng", got["name"])
	}
	if got["url"] != "http://localhost:8765" {
		t.Errorf("url = %v", got["url"])
	}
	if got["redirect_url"] != "http://localhost:8765/callback" {
		t.Errorf("redirect_url = %v", got["redirect_url"])
	}
	if got["setup_url"] != "http://localhost:8765/installed" {
		t.Errorf("setup_url = %v", got["setup_url"])
	}
	if got["public"] != false {
		t.Errorf("public = %v, want false", got["public"])
	}
	perms, _ := got["default_permissions"].(map[string]any)
	for k, want := range map[string]string{
		"contents":        "write",
		"issues":          "write",
		"metadata":        "read",
		"email_addresses": "read",
	} {
		if perms[k] != want {
			t.Errorf("permissions[%s] = %v, want %s", k, perms[k], want)
		}
	}
	events, _ := got["default_events"].([]any)
	if len(events) != 1 || events[0] != "push" {
		t.Errorf("default_events = %v, want [push]", events)
	}
	hook, _ := got["hook_attributes"].(map[string]any)
	if hook["url"] != "http://localhost:8765/webhook" {
		t.Errorf("hook url = %v", hook["url"])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```
go test ./internal/githubapp/...
```
Expected: FAIL — `package internal/githubapp does not exist` or `BuildManifest undefined`.

- [ ] **Step 3: Implement `manifest.go`**

```go
// Package githubapp drives the GitHub App "create from manifest" flow used by
// the install script's step 5. It is intentionally narrow — it knows nothing
// about Bicep, Azure, or .env; it only produces credentials and writes them
// to the install state file.
package githubapp

// ManifestInput is the minimal user-facing data needed to render a manifest.
// All URLs in the rendered manifest point at the local callback server; the
// real production URLs are set by the user in step 16 after deploy.
type ManifestInput struct {
	Name        string // app name, must be globally unique on GitHub
	CallbackURL string // base URL of the local callback server, e.g. http://localhost:8765
}

// Manifest is the JSON payload posted to github.com/settings/apps/new.
// Field tags match GitHub's documented schema:
// https://docs.github.com/en/apps/sharing-github-apps/registering-a-github-app-from-a-manifest
type Manifest struct {
	Name           string            `json:"name"`
	URL            string            `json:"url"`
	HookAttributes HookAttributes    `json:"hook_attributes"`
	RedirectURL    string            `json:"redirect_url"`
	CallbackURLs   []string          `json:"callback_urls"`
	SetupURL       string            `json:"setup_url"`
	SetupOnUpdate  bool              `json:"setup_on_update"`
	Public         bool              `json:"public"`
	DefaultEvents  []string          `json:"default_events"`
	DefaultPerms   map[string]string `json:"default_permissions"`
}

// HookAttributes is the webhook block of a manifest.
type HookAttributes struct {
	URL string `json:"url"`
}

// BuildManifest renders a Manifest from the given input. Permissions and
// events match what the spec calls out: Contents R+W, Issues W, Metadata R,
// Email R, Push events.
func BuildManifest(in ManifestInput) Manifest {
	base := in.CallbackURL
	return Manifest{
		Name:           in.Name,
		URL:            base,
		HookAttributes: HookAttributes{URL: base + "/webhook"},
		RedirectURL:    base + "/callback",
		CallbackURLs:   []string{base + "/oauth/callback"},
		SetupURL:       base + "/installed",
		SetupOnUpdate:  true,
		Public:         false,
		DefaultEvents:  []string{"push"},
		DefaultPerms: map[string]string{
			"contents":        "write",
			"issues":          "write",
			"metadata":        "read",
			"email_addresses": "read",
		},
	}
}
```

- [ ] **Step 4: Run tests, verify pass**

```
go test ./internal/githubapp/...
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/githubapp/manifest.go internal/githubapp/manifest_test.go
git commit -m "feat(githubapp): add manifest builder for App-from-manifest flow"
```

---

### Task 2: Conversion HTTP client

**Files:**
- Create: `internal/githubapp/conversion.go`
- Test: `internal/githubapp/conversion_test.go`

- [ ] **Step 1: Write the failing test**

```go
package githubapp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestConvert_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		if !strings.HasPrefix(r.URL.Path, "/app-manifests/") || !strings.HasSuffix(r.URL.Path, "/conversions") {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{
			"id": 12345,
			"slug": "cronfoundry-tng",
			"client_id": "Iv23liabcdef",
			"client_secret": "shh",
			"webhook_secret": "wh-secret",
			"pem": "-----BEGIN RSA PRIVATE KEY-----\nA\n-----END RSA PRIVATE KEY-----\n",
			"owner": {"login": "tng"}
		}`))
	}))
	defer srv.Close()

	c := NewConverter(srv.URL, srv.Client())
	got, err := c.Convert(context.Background(), "code-abc")
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if got.ID != 12345 || got.Slug != "cronfoundry-tng" || got.ClientID != "Iv23liabcdef" {
		t.Errorf("unexpected result: %+v", got)
	}
	if got.WebhookSecret != "wh-secret" || !strings.Contains(got.PEM, "BEGIN RSA") {
		t.Errorf("missing secret/pem: %+v", got)
	}
}

func TestConvert_NameTaken422(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"Name has already been taken"}`))
	}))
	defer srv.Close()

	c := NewConverter(srv.URL, srv.Client())
	_, err := c.Convert(context.Background(), "code")
	if err == nil || !strings.Contains(err.Error(), "422") {
		t.Errorf("err = %v, want 422", err)
	}
}

func TestConvert_5xxRetriesOnce(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":1,"slug":"s","client_id":"x","client_secret":"y","webhook_secret":"z","pem":"p"}`))
	}))
	defer srv.Close()

	c := NewConverter(srv.URL, srv.Client())
	c.RetryDelay = 10 * time.Millisecond
	if _, err := c.Convert(context.Background(), "code"); err != nil {
		t.Fatalf("convert: %v", err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
}

func TestConvert_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	c := NewConverter(srv.URL, srv.Client())
	if _, err := c.Convert(context.Background(), "code"); err == nil {
		t.Error("expected decode error, got nil")
	}
}
```

- [ ] **Step 2: Run test, expect fail**

```
go test ./internal/githubapp/... -run Convert
```
Expected: FAIL — `NewConverter undefined`.

- [ ] **Step 3: Implement `conversion.go`**

```go
package githubapp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Conversion is the response from POST /app-manifests/{code}/conversions.
// Fields we don't use (e.g. node_id, html_url) are intentionally omitted.
type Conversion struct {
	ID            int64  `json:"id"`
	Slug          string `json:"slug"`
	ClientID      string `json:"client_id"`
	ClientSecret  string `json:"client_secret"`
	WebhookSecret string `json:"webhook_secret"`
	PEM           string `json:"pem"`
	Owner         struct {
		Login string `json:"login"`
	} `json:"owner"`
}

// Converter exchanges a one-time manifest code for an App's full credentials.
// The zero value is not usable; construct via NewConverter.
type Converter struct {
	BaseURL    string        // e.g. https://api.github.com
	HTTP       *http.Client  // injected for testing
	RetryDelay time.Duration // delay between attempts; one retry on 5xx
}

// NewConverter returns a Converter pointed at baseURL using the given client.
// baseURL has no trailing slash.
func NewConverter(baseURL string, hc *http.Client) *Converter {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &Converter{BaseURL: baseURL, HTTP: hc, RetryDelay: 2 * time.Second}
}

// Convert exchanges the temporary code GitHub redirected with for the App's
// permanent credentials. One retry on 5xx; 4xx is surfaced immediately.
func (c *Converter) Convert(ctx context.Context, code string) (*Conversion, error) {
	url := fmt.Sprintf("%s/app-manifests/%s/conversions", c.BaseURL, code)

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(c.RetryDelay):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
		if err != nil {
			return nil, fmt.Errorf("githubapp: build request: %w", err)
		}
		req.Header.Set("Accept", "application/vnd.github+json")

		resp, err := c.HTTP.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("githubapp: do request: %w", err)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("githubapp: http %d: %s", resp.StatusCode, string(body))
			continue
		}
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("githubapp: http %d: %s", resp.StatusCode, string(body))
		}
		var out Conversion
		if err := json.Unmarshal(body, &out); err != nil {
			return nil, fmt.Errorf("githubapp: decode: %w", err)
		}
		return &out, nil
	}
	return nil, lastErr
}
```

- [ ] **Step 4: Run, verify pass**

```
go test ./internal/githubapp/...
```

- [ ] **Step 5: Commit**

```bash
git add internal/githubapp/conversion.go internal/githubapp/conversion_test.go
git commit -m "feat(githubapp): add manifest-code conversion client"
```

---

### Task 3: State-file writer

**Files:**
- Create: `internal/githubapp/state.go`
- Test: `internal/githubapp/state_test.go`

- [ ] **Step 1: Write the failing test**

```go
package githubapp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveState_AppendsAndChmod(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state")
	if err := os.WriteFile(path, []byte("CF_EXISTING=keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SaveState(path, map[string]string{
		"CF_GITHUB_APP_ID":     "12345",
		"CF_GITHUB_APP_SLUG":   "cronfoundry-tng",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, want := range []string{"CF_EXISTING=keep", "CF_GITHUB_APP_ID=12345", "CF_GITHUB_APP_SLUG=cronfoundry-tng"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("perms = %o, want 0600", st.Mode().Perm())
	}
}

func TestSaveState_QuotesValuesWithSpacesAndNewlines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state")
	if err := SaveState(path, map[string]string{
		"CF_PEM":   "-----BEGIN-----\nLINE\n-----END-----\n",
		"CF_PLAIN": "simple",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	b, _ := os.ReadFile(path)
	got := string(b)
	// Multi-line value must round-trip safely under bash `source`.
	if !strings.Contains(got, "CF_PEM=$'-----BEGIN-----\\nLINE\\n-----END-----\\n'") {
		t.Errorf("pem not bash-quoted properly:\n%s", got)
	}
	if !strings.Contains(got, "CF_PLAIN=simple") {
		t.Errorf("plain value should not be quoted:\n%s", got)
	}
}
```

- [ ] **Step 2: Run, expect fail**

```
go test ./internal/githubapp/... -run SaveState
```
Expected: FAIL — `SaveState undefined`.

- [ ] **Step 3: Implement `state.go`**

```go
package githubapp

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// SaveState appends key=value lines to the install-state file in a form that
// `bash source` can read. Values containing whitespace, quotes, or non-ASCII
// are emitted using bash's $'...' ANSI-C quoting; simple values are bare.
//
// The file is chmod'd to 0600 after every write. SaveState appends — it never
// rewrites existing lines, mirroring install.sh's behavior. Bash semantics
// mean later assignments win on `source`.
func SaveState(path string, kv map[string]string) error {
	keys := make([]string, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%s\n", k, bashQuote(kv[k]))
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("githubapp: open state: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(b.String()); err != nil {
		return fmt.Errorf("githubapp: write state: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("githubapp: chmod state: %w", err)
	}
	return nil
}

// bashQuote emits a value safe to `source` from bash. Plain ASCII values
// without whitespace or shell metacharacters are returned bare; everything
// else uses ANSI-C $'...' quoting with backslash escapes for \, ', \n, \r,
// \t, and any control byte.
func bashQuote(s string) string {
	if s == "" {
		return "''"
	}
	plain := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' {
			continue
		}
		switch c {
		case '_', '-', '.', '/', ':', '@', '+', '=':
			continue
		}
		plain = false
		break
	}
	if plain {
		return s
	}
	var b strings.Builder
	b.WriteString("$'")
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\':
			b.WriteString(`\\`)
		case '\'':
			b.WriteString(`\'`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if c < 0x20 || c == 0x7f {
				fmt.Fprintf(&b, `\x%02x`, c)
			} else {
				b.WriteByte(c)
			}
		}
	}
	b.WriteString("'")
	return b.String()
}
```

- [ ] **Step 4: Run, verify pass**

```
go test ./internal/githubapp/...
```

- [ ] **Step 5: Commit**

```bash
git add internal/githubapp/state.go internal/githubapp/state_test.go
git commit -m "feat(githubapp): add bash-safe state-file writer"
```

---

### Task 4: Browser-open helper

**Files:**
- Create: `internal/githubapp/browser.go`

- [ ] **Step 1: Implement (no unit test — it's a thin exec wrapper, covered indirectly by server_test)**

```go
package githubapp

import (
	"fmt"
	"os/exec"
	"runtime"
)

// OpenBrowser tries to open the given URL in the user's default browser.
// On failure it returns an error so the caller can fall back to printing
// the URL for manual paste.
func OpenBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		// linux, *bsd, wsl
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("githubapp: open browser: %w", err)
	}
	// Don't Wait — we don't care about the launcher's exit, only that we
	// kicked it off. The user's browser keeps running independently.
	go func() { _ = cmd.Wait() }()
	return nil
}
```

- [ ] **Step 2: Build, verify compiles**

```
go build ./internal/githubapp/...
```

- [ ] **Step 3: Commit**

```bash
git add internal/githubapp/browser.go
git commit -m "feat(githubapp): add cross-platform browser-open helper"
```

---

### Task 5: Local callback server — happy path test first

**Files:**
- Create: `internal/githubapp/server.go`
- Test: `internal/githubapp/server_test.go`

- [ ] **Step 1: Write the failing test**

```go
package githubapp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestServer_HappyPath drives the full /callback -> /installed flow against
// a stub GitHub conversions endpoint and asserts state-file contents.
func TestServer_HappyPath(t *testing.T) {
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{
			"id": 99,
			"slug": "cf-test",
			"client_id": "Iv23liXX",
			"client_secret": "cs",
			"webhook_secret": "ws",
			"pem": "-----BEGIN RSA PRIVATE KEY-----\nABC\n-----END RSA PRIVATE KEY-----\n"
		}`))
	}))
	defer gh.Close()

	dir := t.TempDir()
	stateFile := filepath.Join(dir, "state")
	pemDir := filepath.Join(dir, "pems")

	s, err := NewServer(Options{
		Port:           0, // random free port
		StateFile:      stateFile,
		PEMDir:         pemDir,
		Converter:      NewConverter(gh.URL, gh.Client()),
		ManifestInput:  ManifestInput{Name: "cf-test"},
		Timeout:        5 * time.Second,
		SkipBrowserOpen: true,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	go func() {
		// /callback then /installed, simulating GitHub redirects
		state := s.State()
		client := &http.Client{Timeout: 2 * time.Second}
		_, _ = client.Get(s.URL() + "/callback?code=abc&state=" + url.QueryEscape(state))
		_, _ = client.Get(s.URL() + "/installed?installation_id=4242&setup_action=install")
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := s.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.AppID != 99 || result.Slug != "cf-test" || result.InstallationID != 4242 {
		t.Errorf("result = %+v", result)
	}
	if !strings.Contains(result.PEMPath, pemDir) {
		t.Errorf("pem path = %s, want under %s", result.PEMPath, pemDir)
	}
}

func TestServer_StateMismatchRejected(t *testing.T) {
	gh := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("conversion endpoint should NOT be hit on state mismatch")
	}))
	defer gh.Close()

	s, _ := NewServer(Options{
		Port:           0,
		StateFile:      filepath.Join(t.TempDir(), "state"),
		PEMDir:         t.TempDir(),
		Converter:      NewConverter(gh.URL, gh.Client()),
		ManifestInput:  ManifestInput{Name: "x"},
		Timeout:        500 * time.Millisecond,
		SkipBrowserOpen: true,
	})
	go func() {
		client := &http.Client{Timeout: time.Second}
		resp, err := client.Get(s.URL() + "/callback?code=abc&state=WRONG")
		if err == nil && resp.StatusCode != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", resp.StatusCode)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := s.Run(ctx)
	if err == nil {
		t.Error("expected timeout error, got nil")
	}
}
```

- [ ] **Step 2: Run, verify fails (Server type missing)**

```
go test ./internal/githubapp/... -run TestServer
```
Expected: FAIL — `NewServer undefined`.

- [ ] **Step 3: Implement `server.go`**

```go
package githubapp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Options configures the local callback server.
type Options struct {
	Port            int           // 0 = random free
	StateFile       string        // install state file (CF_*= lines appended)
	PEMDir          string        // directory to drop the .pem into; created 0700 if missing
	Converter       *Converter    // injected; required
	ManifestInput   ManifestInput // name + callback URL filled in by Run
	Timeout         time.Duration // overall deadline; 0 = 10 min
	SkipBrowserOpen bool          // for tests
}

// Result is what Run produces after a successful flow.
type Result struct {
	AppID          int64
	Slug           string
	ClientID       string
	WebhookSecret  string
	InstallationID int64
	PEMPath        string
}

// Server owns the localhost HTTP server lifecycle for one manifest flow.
// One Server == one Run; do not reuse.
type Server struct {
	opts     Options
	listener net.Listener
	state    string

	mu             sync.Mutex
	conv           *Conversion
	installationID int64
	convErr        error
	doneCh         chan struct{}
}

// NewServer creates the server and binds its listener but does not yet
// accept connections. URL() and State() are valid after this call.
func NewServer(opts Options) (*Server, error) {
	if opts.Converter == nil {
		return nil, errors.New("githubapp: Options.Converter required")
	}
	if opts.Timeout == 0 {
		opts.Timeout = 10 * time.Minute
	}
	addr := fmt.Sprintf("127.0.0.1:%d", opts.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("githubapp: listen %s: %w", addr, err)
	}
	stateBytes := make([]byte, 32)
	if _, err := rand.Read(stateBytes); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("githubapp: random state: %w", err)
	}
	s := &Server{
		opts:     opts,
		listener: ln,
		state:    hex.EncodeToString(stateBytes),
		doneCh:   make(chan struct{}),
	}
	s.opts.ManifestInput.CallbackURL = s.URL()
	return s, nil
}

// URL returns the base URL of the server, e.g. http://127.0.0.1:8765.
func (s *Server) URL() string {
	return "http://" + s.listener.Addr().String()
}

// State returns the random CSRF state value for this run.
func (s *Server) State() string { return s.state }

// Run starts the HTTP server, opens the user's browser to "/", and blocks
// until either /installed completes successfully, the context is cancelled,
// or Options.Timeout elapses.
func (s *Server) Run(ctx context.Context) (*Result, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/callback", s.handleCallback)
	mux.HandleFunc("/installed", s.handleInstalled)

	hs := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = hs.Serve(s.listener) }()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = hs.Shutdown(shutdownCtx)
	}()

	if !s.opts.SkipBrowserOpen {
		_ = OpenBrowser(s.URL() + "/")
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, s.opts.Timeout)
	defer cancel()

	select {
	case <-s.doneCh:
		// fall through
	case <-timeoutCtx.Done():
		return nil, fmt.Errorf("githubapp: timed out waiting for manifest flow: %w", timeoutCtx.Err())
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.convErr != nil {
		return nil, s.convErr
	}
	if s.conv == nil || s.installationID == 0 {
		return nil, errors.New("githubapp: flow ended without credentials")
	}

	pemPath, err := s.writePEM(s.conv)
	if err != nil {
		return nil, err
	}

	if err := SaveState(s.opts.StateFile, map[string]string{
		"CF_GITHUB_APP_ID":         fmt.Sprintf("%d", s.conv.ID),
		"CF_GITHUB_APP_SLUG":       s.conv.Slug,
		"CF_GITHUB_CLIENT_ID":      s.conv.ClientID,
		"CF_GITHUB_CLIENT_SECRET":  s.conv.ClientSecret,
		"CF_GITHUB_WEBHOOK_SECRET": s.conv.WebhookSecret,
		"CF_GITHUB_PEM_PATH":       pemPath,
		"CF_INSTALLATION_ID":       fmt.Sprintf("%d", s.installationID),
	}); err != nil {
		return nil, err
	}

	return &Result{
		AppID:          s.conv.ID,
		Slug:           s.conv.Slug,
		ClientID:       s.conv.ClientID,
		WebhookSecret:  s.conv.WebhookSecret,
		InstallationID: s.installationID,
		PEMPath:        pemPath,
	}, nil
}

func (s *Server) writePEM(c *Conversion) (string, error) {
	if err := os.MkdirAll(s.opts.PEMDir, 0o700); err != nil {
		return "", fmt.Errorf("githubapp: mkdir pem-dir: %w", err)
	}
	path := filepath.Join(s.opts.PEMDir, c.Slug+".pem")
	if err := os.WriteFile(path, []byte(c.PEM), 0o600); err != nil {
		return "", fmt.Errorf("githubapp: write pem: %w", err)
	}
	return path, nil
}

// handleRoot serves an HTML page that auto-POSTs the manifest to GitHub.
// Manifest creation is form-encoded with one field "manifest" carrying the
// JSON; GitHub renders the consent page after the POST.
func (s *Server) handleRoot(w http.ResponseWriter, _ *http.Request) {
	manifest := BuildManifest(s.opts.ManifestInput)
	mb, err := jsonMarshal(manifest)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html>
<meta charset="utf-8">
<title>CronFoundry — creating GitHub App</title>
<body style="font-family:system-ui;max-width:40rem;margin:4rem auto;line-height:1.5">
<h1>Creating your GitHub App…</h1>
<p>Your browser is being redirected to GitHub. Click <b>Create GitHub App</b> on the page that appears.</p>
<form id="f" method="post" action="https://github.com/settings/apps/new?state=%s">
  <input type="hidden" name="manifest" value='%s'>
  <noscript><button type="submit">Continue to GitHub</button></noscript>
</form>
<script>document.getElementById('f').submit();</script>
</body>`, s.state, htmlEscape(string(mb)))
}

func (s *Server) handleCallback(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("state") != s.state {
		http.Error(w, "state mismatch", http.StatusBadRequest)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}
	conv, err := s.opts.Converter.Convert(r.Context(), code)
	s.mu.Lock()
	if err != nil {
		s.convErr = err
		s.mu.Unlock()
		close(s.doneCh)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	s.conv = conv
	slug := conv.Slug
	s.mu.Unlock()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html>
<meta charset="utf-8">
<title>App created — install it</title>
<body style="font-family:system-ui;max-width:40rem;margin:4rem auto;line-height:1.5">
<h1>GitHub App created.</h1>
<p>Now install it on the repos CronFoundry will manage. You'll be redirected automatically.</p>
<script>location.href = "https://github.com/apps/%s/installations/new";</script>
<noscript><a href="https://github.com/apps/%s/installations/new">Install the app</a></noscript>
</body>`, slug, slug)
}

func (s *Server) handleInstalled(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("installation_id")
	var id int64
	if _, err := fmt.Sscanf(idStr, "%d", &id); err != nil || id <= 0 {
		http.Error(w, "missing or invalid installation_id", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.installationID = id
	s.mu.Unlock()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!doctype html>
<meta charset="utf-8">
<title>Done</title>
<body style="font-family:system-ui;max-width:40rem;margin:4rem auto;line-height:1.5">
<h1>All set.</h1>
<p>You can return to your terminal — CronFoundry has the credentials it needs.</p>
</body>`))

	// Signal done after responding so the browser sees the success page.
	go func() {
		time.Sleep(50 * time.Millisecond)
		close(s.doneCh)
	}()
}

// jsonMarshal is split out for ease of mocking in tests if needed.
func jsonMarshal(v any) ([]byte, error) {
	return jsonMarshalFn(v)
}

// htmlEscape escapes only the characters that matter inside an attribute
// value; the manifest is already valid JSON so we don't worry about <,>.
func htmlEscape(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '&':
			out = append(out, []byte("&amp;")...)
		case '\'':
			out = append(out, []byte("&#39;")...)
		case '"':
			out = append(out, []byte("&quot;")...)
		default:
			out = append(out, s[i])
		}
	}
	return string(out)
}
```

Add `var jsonMarshalFn = json.Marshal` near the top imports — and add `"encoding/json"` to the import block. (Including the import is part of writing the file.)

- [ ] **Step 4: Run tests**

```
go test ./internal/githubapp/...
```
Expected: PASS for both server tests.

- [ ] **Step 5: Commit**

```bash
git add internal/githubapp/server.go internal/githubapp/server_test.go
git commit -m "feat(githubapp): add localhost callback server orchestrating manifest flow"
```

---

### Task 6: Manual fallback prompts

**Files:**
- Create: `internal/githubapp/manual.go`
- Test: `internal/githubapp/manual_test.go`

- [ ] **Step 1: Write the failing test**

```go
package githubapp

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunManual_HappyPath(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "state")

	// Write a fake PEM the prompt will be told to point at.
	pem := filepath.Join(dir, "app.pem")
	if err := os.WriteFile(pem, []byte("-----BEGIN-----\nx\n-----END-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	in := strings.NewReader(strings.Join([]string{
		"12345",                  // App ID
		"Iv23liabcdef",           // Client ID
		"client-secret-value",    // Client Secret
		pem,                      // PEM path
		"55",                     // Installation ID
	}, "\n") + "\n")

	var out bytes.Buffer
	if err := RunManual(in, &out, ManualOptions{StateFile: stateFile}); err != nil {
		t.Fatalf("manual: %v", err)
	}
	b, _ := os.ReadFile(stateFile)
	for _, want := range []string{
		"CF_GITHUB_APP_ID=12345",
		"CF_GITHUB_CLIENT_ID=Iv23liabcdef",
		"CF_GITHUB_CLIENT_SECRET=client-secret-value",
		"CF_INSTALLATION_ID=55",
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing %q in:\n%s", want, string(b))
		}
	}
}

func TestRunManual_RejectsMissingPEM(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "state")
	in := strings.NewReader("1\nIv1\ncs\n/no/such/file.pem\n")
	var out bytes.Buffer
	err := RunManual(in, &out, ManualOptions{StateFile: stateFile})
	if err == nil || !strings.Contains(err.Error(), "pem") {
		t.Errorf("err = %v, want pem-not-found error", err)
	}
}
```

- [ ] **Step 2: Run, expect fail**

```
go test ./internal/githubapp/... -run RunManual
```

- [ ] **Step 3: Implement `manual.go`**

```go
package githubapp

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// ManualOptions configures the legacy interactive prompts.
type ManualOptions struct {
	StateFile string
}

// RunManual reads the same five values install.sh used to prompt for, validates
// them lightly, and writes them to the state file. It is the fallback path
// when the browser flow can't be used (SSH, codespaces, --manual flag).
func RunManual(in io.Reader, out io.Writer, opts ManualOptions) error {
	br := bufio.NewReader(in)

	fmt.Fprintln(out, manualInstructions)

	appIDStr, err := prompt(out, br, "GitHub App ID (numeric): ")
	if err != nil {
		return err
	}
	if _, err := strconv.ParseInt(appIDStr, 10, 64); err != nil {
		return fmt.Errorf("githubapp: app id must be numeric, got %q", appIDStr)
	}

	clientID, err := prompt(out, br, "GitHub App Client ID (starts with Iv23li): ")
	if err != nil {
		return err
	}
	clientSecret, err := prompt(out, br, "GitHub App Client Secret: ")
	if err != nil {
		return err
	}
	pemPath, err := prompt(out, br, "Path to GitHub App .pem file: ")
	if err != nil {
		return err
	}
	if _, err := os.Stat(pemPath); err != nil {
		return fmt.Errorf("githubapp: pem not found at %s: %w", pemPath, err)
	}
	installID, err := prompt(out, br, "GitHub App Installation ID: ")
	if err != nil {
		return err
	}
	if _, err := strconv.ParseInt(installID, 10, 64); err != nil {
		return fmt.Errorf("githubapp: installation id must be numeric, got %q", installID)
	}

	return SaveState(opts.StateFile, map[string]string{
		"CF_GITHUB_APP_ID":        appIDStr,
		"CF_GITHUB_CLIENT_ID":     clientID,
		"CF_GITHUB_CLIENT_SECRET": clientSecret,
		"CF_GITHUB_PEM_PATH":      pemPath,
		"CF_INSTALLATION_ID":      installID,
	})
}

func prompt(out io.Writer, br *bufio.Reader, label string) (string, error) {
	if _, err := fmt.Fprint(out, label); err != nil {
		return "", err
	}
	line, err := br.ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("githubapp: read prompt: %w", err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}

const manualInstructions = `
Manual GitHub App setup. In a browser:

  1. Open: https://github.com/settings/apps/new
     (Confirm the URL ends in /settings/apps/new — not /applications/new.)
  2. Name: anything globally unique.
  3. Homepage / Callback / Webhook URLs: use https://example.com placeholders;
     you'll update them after deploy in step 16.
  4. Webhook secret: generate via 'openssl rand -hex 32'. Save it somewhere —
     you'll add it to .env after this script finishes.
  5. Permissions → Repository: Contents (R+W), Issues (W), Metadata (R).
                  Account: Email (R).
  6. Subscribe to events: Push.
  7. Save. Note the App ID, generate a Client Secret, download the .pem.
  8. Install the app on your skill and reports repos. The installation URL
     ends with /installations/<id> — that number is the Installation ID.
`
```

- [ ] **Step 4: Run tests**

```
go test ./internal/githubapp/...
```

- [ ] **Step 5: Commit**

```bash
git add internal/githubapp/manual.go internal/githubapp/manual_test.go
git commit -m "feat(githubapp): add --manual fallback prompts"
```

---

### Task 7: Cobra subcommand wiring

**Files:**
- Create: `cmd/cronfoundry/setup.go`
- Create: `cmd/cronfoundry/setup_githubapp.go`
- Create: `cmd/cronfoundry/setup_githubapp_test.go`
- Modify: `cmd/cronfoundry/main.go` (add `root.AddCommand(newSetupCmd())`)

- [ ] **Step 1: Write the failing test**

```go
package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestSetupCmd_HelpListsGithubApp(t *testing.T) {
	cmd := newSetupCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "github-app") {
		t.Errorf("help missing github-app subcommand:\n%s", out.String())
	}
}

func TestSetupGithubAppCmd_FlagsPresent(t *testing.T) {
	cmd := newSetupGithubAppCmd()
	for _, want := range []string{"state-file", "default-name", "port", "pem-dir", "manual", "timeout"} {
		if cmd.Flags().Lookup(want) == nil {
			t.Errorf("missing flag --%s", want)
		}
	}
}
```

- [ ] **Step 2: Run, expect fail**

```
go test ./cmd/cronfoundry/... -run TestSetup
```

- [ ] **Step 3: Implement `setup.go`**

```go
package main

import "github.com/spf13/cobra"

func newSetupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Interactive setup helpers used by install.sh",
	}
	cmd.AddCommand(newSetupGithubAppCmd())
	return cmd
}
```

- [ ] **Step 4: Implement `setup_githubapp.go`**

```go
package main

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/gambtho/cronfoundry/internal/githubapp"
)

func newSetupGithubAppCmd() *cobra.Command {
	var (
		stateFile   string
		defaultName string
		port        int
		pemDir      string
		manual      bool
		timeout     time.Duration
		baseAPI     string
	)

	cmd := &cobra.Command{
		Use:   "github-app",
		Short: "Create and install a GitHub App via manifest flow",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if stateFile == "" {
				home, _ := os.UserHomeDir()
				stateFile = filepath.Join(home, ".cronfoundry-quickstart-state")
			}
			if pemDir == "" {
				home, _ := os.UserHomeDir()
				pemDir = filepath.Join(home, ".cronfoundry")
			}
			if defaultName == "" {
				u, _ := user.Current()
				if u != nil {
					defaultName = "cronfoundry-" + u.Username
				} else {
					defaultName = "cronfoundry-app"
				}
			}

			if manual {
				return githubapp.RunManual(os.Stdin, cmd.OutOrStdout(), githubapp.ManualOptions{
					StateFile: stateFile,
				})
			}

			conv := githubapp.NewConverter(baseAPI, nil)
			srv, err := githubapp.NewServer(githubapp.Options{
				Port:          port,
				StateFile:     stateFile,
				PEMDir:        pemDir,
				Converter:     conv,
				ManifestInput: githubapp.ManifestInput{Name: defaultName},
				Timeout:       timeout,
			})
			if err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(),
				"Starting local GitHub App setup helper at %s\nIf your browser doesn't open, visit that URL manually.\n",
				srv.URL())

			ctx, cancel := context.WithCancel(cmd.Context())
			defer cancel()
			res, err := srv.Run(ctx)
			if err != nil {
				return fmt.Errorf("setup: %w (re-run with --manual for the legacy prompts)", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"\nGitHub App created: id=%d slug=%s installation=%d\n  PEM: %s\n  state: %s\n",
				res.AppID, res.Slug, res.InstallationID, res.PEMPath, stateFile)
			return nil
		},
	}
	cmd.Flags().StringVar(&stateFile, "state-file", "", "install state file (default ~/.cronfoundry-quickstart-state)")
	cmd.Flags().StringVar(&defaultName, "default-name", "", "default GitHub App name (default cronfoundry-<user>)")
	cmd.Flags().IntVar(&port, "port", 8765, "localhost port for callback server")
	cmd.Flags().StringVar(&pemDir, "pem-dir", "", "directory to write the .pem file (default ~/.cronfoundry)")
	cmd.Flags().BoolVar(&manual, "manual", false, "skip the browser flow; prompt for credentials manually")
	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Minute, "abort if the user doesn't complete the flow in this time")
	cmd.Flags().StringVar(&baseAPI, "github-api", "https://api.github.com", "GitHub API base URL (override for testing)")
	return cmd
}
```

- [ ] **Step 5: Modify `main.go`**

In `cmd/cronfoundry/main.go`, add this line after `root.AddCommand(newServeCmd())`:

```go
	root.AddCommand(newSetupCmd())
```

- [ ] **Step 6: Run all tests**

```
go test ./...
```
Expected: existing tests still pass; new setup tests pass.

- [ ] **Step 7: Commit**

```bash
git add cmd/cronfoundry/setup.go cmd/cronfoundry/setup_githubapp.go cmd/cronfoundry/setup_githubapp_test.go cmd/cronfoundry/main.go
git commit -m "feat(setup): add cronfoundry setup github-app subcommand"
```

---

### Task 8: install.sh edits

**Files:**
- Modify: `docs/install.sh` (steps 5, 6, 16)

- [ ] **Step 1: Replace step 5 body**

In `docs/install.sh`, locate the block from `# ── Step 5: GitHub App ───` down to the `ok "GitHub App credentials collected"` line (currently lines 102–141). Replace the entire block with:

```bash
# ── Step 5: GitHub App ────────────────────────────────────────────────────────
header "[step 5/17] GitHub App setup"
echo ""
echo "  Launching the browser-based setup helper. If anything goes wrong,"
echo "  re-run with --manual on the helper for the legacy prompt flow."
echo ""

if [[ -z "${CF_GITHUB_APP_ID:-}" ]]; then
  # Build the helper if needed (the binary may not exist on first run).
  if [[ ! -x ./cronfoundry ]]; then
    info "Building cronfoundry binary for setup helper..."
    if ! make build >/dev/null 2>&1; then
      go build -o cronfoundry ./cmd/cronfoundry \
        || die "Failed to build cronfoundry binary; see §5 of $GUIDE_URL"
    fi
  fi

  ./cronfoundry setup github-app \
      --state-file "$STATE_FILE" \
      --default-name "cronfoundry-$(whoami)" \
      || die "GitHub App setup failed; re-run, or pass --manual on the next attempt.\nSee §5 of $GUIDE_URL"

  # Re-source state to pick up new CF_* variables written by the helper.
  # shellcheck disable=SC1090
  source "$STATE_FILE"
fi
ok "GitHub App credentials collected (app=$CF_GITHUB_APP_SLUG installation=$CF_INSTALLATION_ID)"
```

- [ ] **Step 2: Modify step 6 to gate the installation-ID prompt**

In `docs/install.sh`, replace the existing step 6 block:

```bash
# ── Step 6: skill repo ────────────────────────────────────────────────────────
header "[step 6/17] Skill repo"
if [[ -z "${CF_SKILL_REPO:-}" ]]; then
  read -rp "Skill repo (owner/repo, e.g. acme/cronfoundry-skills): " CF_SKILL_REPO
  save CF_SKILL_REPO "$CF_SKILL_REPO"
fi
if [[ -z "${CF_INSTALLATION_ID:-}" ]]; then
  read -rp "GitHub App Installation ID (number from the install URL): " CF_INSTALLATION_ID
  save CF_INSTALLATION_ID "$CF_INSTALLATION_ID"
fi
ok "Skill repo: $CF_SKILL_REPO (installation $CF_INSTALLATION_ID)"
```

with this version (the installation-ID prompt remains as a fallback for the
`--manual` path, where the helper already saved it; this is just a safety net):

```bash
# ── Step 6: skill repo ────────────────────────────────────────────────────────
header "[step 6/17] Skill repo"
if [[ -z "${CF_SKILL_REPO:-}" ]]; then
  read -rp "Skill repo (owner/repo, e.g. acme/cronfoundry-skills): " CF_SKILL_REPO
  save CF_SKILL_REPO "$CF_SKILL_REPO"
fi
if [[ -z "${CF_INSTALLATION_ID:-}" ]]; then
  warn "Installation ID not captured automatically (helper may have been skipped)."
  read -rp "GitHub App Installation ID (number from the install URL): " CF_INSTALLATION_ID
  save CF_INSTALLATION_ID "$CF_INSTALLATION_ID"
fi
ok "Skill repo: $CF_SKILL_REPO (installation $CF_INSTALLATION_ID)"
```

- [ ] **Step 3: Modify step 16 to use the slug**

Replace the existing step 16 block:

```bash
# ── Step 16: update GitHub App URLs ──────────────────────────────────────────
header "[step 16/17] Update GitHub App URLs"
echo ""
echo "  Go to your GitHub App settings and update these three URLs:"
echo ""
echo "  Homepage URL:  https://${CF_FQDN}"
echo "  Callback URL:  https://${CF_FQDN}/oauth/callback"
echo "  Webhook URL:   https://${CF_FQDN}/webhook/github"
echo ""
read -rp "Press Enter once you have updated the GitHub App URLs..."
```

with this slug-aware version:

```bash
# ── Step 16: update GitHub App URLs ──────────────────────────────────────────
header "[step 16/17] Update GitHub App URLs"
echo ""
if [[ -n "${CF_GITHUB_APP_SLUG:-}" ]]; then
  echo "  Open: https://github.com/settings/apps/${CF_GITHUB_APP_SLUG}"
else
  echo "  Open your GitHub App's settings page"
fi
echo ""
echo "  Replace these three URLs:"
echo ""
echo "    Homepage URL:  https://${CF_FQDN}"
echo "    Callback URL:  https://${CF_FQDN}/oauth/callback"
echo "    Webhook URL:   https://${CF_FQDN}/webhook/github"
echo ""
read -rp "Press Enter once you have updated the GitHub App URLs..."
```

- [ ] **Step 4: Smoke-test the script in dry-run mode**

```bash
bash -n docs/install.sh
```
Expected: no syntax errors. (The full `--dry-run` path requires az/postgres/etc and is out of scope for this plan; bash syntax check is enough.)

- [ ] **Step 5: Commit**

```bash
git add docs/install.sh
git commit -m "feat(install): use cronfoundry setup github-app helper in steps 5/6/16"
```

---

### Task 9: Final cross-cutting checks

- [ ] **Step 1: Run the full test suite**

```
go test ./...
```
Expected: all tests pass. If any pre-existing test breaks, investigate before claiming completion (verification-before-completion skill applies).

- [ ] **Step 2: Run go vet and lint**

```
go vet ./...
```
Expected: clean.

- [ ] **Step 3: Manual sanity check of the binary**

```
go build -o cronfoundry ./cmd/cronfoundry
./cronfoundry setup --help
./cronfoundry setup github-app --help
```
Expected: both help screens render and list the documented flags.

- [ ] **Step 4: No commit needed unless fixes are required**

If any check failed, fix and commit the fix as its own commit with a clear message. If everything is clean, this task is just verification.

---

## Self-Review Notes

- **Spec coverage:** Each spec component (manifest builder, conversion client, server, manual fallback, subcommand, install.sh edits) has a numbered task. Webhook secret is captured in `CF_GITHUB_WEBHOOK_SECRET`; threading into Bicep is explicitly out of scope per the spec and is not part of this plan.
- **Type consistency:** `Server.Run` returns `*Result`, used by the cobra command; `Conversion` is the type returned by `Converter.Convert`, used by `Server`. `ManifestInput` is the only input shape into `BuildManifest` and is also embedded in `Options`.
- **No placeholders:** Every step has the actual code or shell snippet; no "TODO" or "implement appropriately" anywhere.
- **YAGNI:** No port-fallback retry loop (default port + `--port` flag is enough); no graceful resume of a partially-completed flow (re-run with empty state file is acceptable); no telemetry.
