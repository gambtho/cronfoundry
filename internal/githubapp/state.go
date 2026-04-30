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
