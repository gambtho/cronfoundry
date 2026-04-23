package webapi

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
