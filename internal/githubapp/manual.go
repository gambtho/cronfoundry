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

	fmt.Fprint(out, manualInstructions)

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
