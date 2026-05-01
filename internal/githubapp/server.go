package githubapp

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Options configures the local callback server.
type Options struct {
	Port            int
	StateFile       string
	PEMDir          string
	Converter       *Converter
	ManifestInput   ManifestInput
	Timeout         time.Duration
	SkipBrowserOpen bool
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
	installNonce   string // one-shot token mounted on the install URL; cleared on first match
	installationID int64
	convErr        error
	doneCh         chan struct{}
	doneOnce       sync.Once
}

// NewServer creates the server and binds its listener but does not yet
// accept connections. URL() and State() are valid after this call.
func NewServer(opts Options) (*Server, error) {
	if opts.Converter == nil {
		return nil, errors.New("githubapp: Options.Converter required")
	}
	if opts.Timeout == 0 {
		opts.Timeout = 30 * time.Minute
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

func (s *Server) URL() string   { return "http://" + s.listener.Addr().String() }
func (s *Server) State() string { return s.state }

func (s *Server) signalDone() { s.doneOnce.Do(func() { close(s.doneCh) }) }

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
	case <-timeoutCtx.Done():
		// Distinguish "user took too long" from "caller cancelled us" — both
		// surface here as timeoutCtx.Done(), but the parent ctx error wins
		// when present.
		if ctx.Err() != nil {
			return nil, fmt.Errorf("githubapp: cancelled while waiting for manifest flow: %w", ctx.Err())
		}
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
	if !isSafeSlug(c.Slug) {
		return "", fmt.Errorf("githubapp: refusing to write pem with unsafe slug %q", c.Slug)
	}
	path := filepath.Join(s.opts.PEMDir, c.Slug+".pem")
	if err := os.WriteFile(path, []byte(c.PEM), 0o600); err != nil {
		return "", fmt.Errorf("githubapp: write pem: %w", err)
	}
	return path, nil
}

// isSafeSlug constrains a GitHub App slug to characters safe for both filesystem
// paths and embedding into the install-redirect URL. GitHub slugs are normally
// [a-z0-9-]+; we accept that plus uppercase to be a little forgiving without
// opening the door to path or HTML/JS escapes.
func isSafeSlug(slug string) bool {
	if slug == "" || len(slug) > 64 {
		return false
	}
	if strings.HasPrefix(slug, "-") || strings.HasSuffix(slug, "-") {
		return false
	}
	for i := 0; i < len(slug); i++ {
		c := slug[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		case c == '-':
		default:
			return false
		}
	}
	return true
}

func (s *Server) handleRoot(w http.ResponseWriter, _ *http.Request) {
	manifest := BuildManifest(s.opts.ManifestInput)
	mb, err := json.Marshal(manifest)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprintf(w, `<!doctype html>
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
	if err != nil {
		s.mu.Lock()
		s.convErr = err
		s.mu.Unlock()
		s.signalDone()
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if !isSafeSlug(conv.Slug) {
		err := fmt.Errorf("githubapp: github returned unsafe slug %q", conv.Slug)
		s.mu.Lock()
		s.convErr = err
		s.mu.Unlock()
		s.signalDone()
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	// Mint a one-shot install nonce so /installed can verify that the next
	// hit is the redirect we triggered, not another localhost client racing
	// to inject an installation_id. GitHub preserves the `state` query param
	// from /installations/new through to the App's configured setup_url.
	nonceBytes := make([]byte, 32)
	if _, err := rand.Read(nonceBytes); err != nil {
		s.mu.Lock()
		s.convErr = fmt.Errorf("githubapp: random install nonce: %w", err)
		s.mu.Unlock()
		s.signalDone()
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	nonce := hex.EncodeToString(nonceBytes)

	s.mu.Lock()
	s.conv = conv
	s.installNonce = nonce
	slug := conv.Slug
	s.mu.Unlock()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprintf(w, `<!doctype html>
<meta charset="utf-8">
<title>App created — install it</title>
<body style="font-family:system-ui;max-width:40rem;margin:4rem auto;line-height:1.5">
<h1>GitHub App created.</h1>
<p>Now install it on the repos CronFoundry will manage. You'll be redirected automatically.</p>
<script>location.href = "https://github.com/apps/%s/installations/new?state=%s";</script>
<noscript><a href="https://github.com/apps/%s/installations/new?state=%s">Install the app</a></noscript>
</body>`, slug, nonce, slug, nonce)
}

func (s *Server) handleInstalled(w http.ResponseWriter, r *http.Request) {
	// /installed must run after /callback has produced a Conversion AND the
	// caller must present the one-shot nonce we minted in /callback. The
	// nonce is consumed atomically under s.mu so a race can't replay it.
	gotNonce := r.URL.Query().Get("state")
	s.mu.Lock()
	convReady := s.conv != nil
	nonceOK := convReady && s.installNonce != "" && subtle.ConstantTimeCompare([]byte(gotNonce), []byte(s.installNonce)) == 1
	if nonceOK {
		s.installNonce = "" // one-shot
	}
	s.mu.Unlock()
	if !convReady {
		http.Error(w, "manifest exchange not yet complete", http.StatusBadRequest)
		return
	}
	if !nonceOK {
		http.Error(w, "install nonce mismatch", http.StatusBadRequest)
		return
	}

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

	s.signalDone()
}

// htmlEscape escapes characters that matter inside an HTML attribute value.
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
