package webapi

// CopilotClientID is the public OAuth client_id of the GitHub Copilot
// platform — used for device-flow against GitHub's /login/device/code and
// /login/oauth/access_token endpoints to mint a Copilot seat token. This
// value is published by GitHub for any consumer doing Copilot OAuth
// (including the official VS Code and JetBrains Copilot extensions).
//
// It is NOT the operator's CronFoundry GitHub App client ID; that App
// authenticates webhooks and per-installation API calls and has nothing
// to do with the user's Copilot seat.
const CopilotClientID = "Iv1.b507a08c87ecfe98"

// copilotClientID is the unexported alias preserved for legacy callers within
// the webapi package.
const copilotClientID = CopilotClientID

// githubDeviceCodeResponse is the JSON from POST /login/device/code.
type githubDeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// githubTokenResponse is the JSON from POST /login/oauth/access_token.
// GitHub returns HTTP 200 for both success and soft errors (e.g. "authorization_pending"),
// with the error described in the Error field.
type githubTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Error        string `json:"error"`
}
