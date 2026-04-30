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
		"CF_GITHUB_APP_ID":   "12345",
		"CF_GITHUB_APP_SLUG": "cronfoundry-tng",
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
	if !strings.Contains(got, "CF_PEM=$'-----BEGIN-----\\nLINE\\n-----END-----\\n'") {
		t.Errorf("pem not bash-quoted properly:\n%s", got)
	}
	if !strings.Contains(got, "CF_PLAIN=simple") {
		t.Errorf("plain value should not be quoted:\n%s", got)
	}
}
