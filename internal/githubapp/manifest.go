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
