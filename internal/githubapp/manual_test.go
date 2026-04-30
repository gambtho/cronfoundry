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

	pem := filepath.Join(dir, "app.pem")
	if err := os.WriteFile(pem, []byte("-----BEGIN-----\nx\n-----END-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	in := strings.NewReader(strings.Join([]string{
		"12345",
		"Iv23liabcdef",
		"client-secret-value",
		pem,
		"55",
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
