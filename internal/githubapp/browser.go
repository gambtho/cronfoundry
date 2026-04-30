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
