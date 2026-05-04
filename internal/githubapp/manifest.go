// Package githubapp drives the GitHub App "create from manifest" flow used by
// the install script's step 5. It is intentionally narrow — it knows nothing
// about Bicep, Azure, or .env; it only produces credentials and writes them
// to the install state file.
package githubapp

// ManifestInput is the minimal user-facing data needed to render a manifest.
//
// CallbackURL is always the *local* callback server base URL (set by
// server.go from s.URL()). It is required so the manifest-create handshake
// (RedirectURL) reaches the local helper. The other three fields are
// optional production URLs pointing at the deployed FQDN; when blank we fall
// back to CallbackURL-derived defaults so existing local-dev behavior and
// older callers keep working.
type ManifestInput struct {
	Name             string // app name, must be globally unique on GitHub
	CallbackURL      string // base URL of the local callback server, e.g. http://localhost:8765
	HomepageURL      string // production homepage URL (optional; defaults to CallbackURL)
	WebhookURL       string // production webhook URL (optional; defaults to CallbackURL+"/webhook")
	OAuthCallbackURL string // production OAuth callback URL (optional; defaults to CallbackURL+"/oauth/callback")
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
// events match what CronFoundry needs: Contents R+W (read/push skill files),
// Issues W (file reports), Pull Requests W (open skill-repo PRs from
// `+ Add job` / `+ Import job` flows), Metadata R (required by GitHub for
// any App), Push events.
func BuildManifest(in ManifestInput) Manifest {
	base := in.CallbackURL
	homepage := in.HomepageURL
	if homepage == "" {
		homepage = base
	}
	webhook := in.WebhookURL
	if webhook == "" {
		webhook = base + "/webhook"
	}
	oauthCB := in.OAuthCallbackURL
	if oauthCB == "" {
		oauthCB = base + "/oauth/callback"
	}
	return Manifest{
		Name:           in.Name,
		URL:            homepage,
		HookAttributes: HookAttributes{URL: webhook},
		RedirectURL:    base + "/callback",
		CallbackURLs:   []string{oauthCB},
		SetupURL:       base + "/installed",
		SetupOnUpdate:  true,
		Public:         false,
		DefaultEvents:  []string{"push"},
		DefaultPerms: map[string]string{
			"contents":      "write",
			"issues":        "write",
			"metadata":      "read",
			"pull_requests": "write",
		},
	}
}
