package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestSetupCmd_HelpListsGithubApp(t *testing.T) {
	cmd := newSetupCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "github-app") {
		t.Errorf("help missing github-app subcommand:\n%s", out.String())
	}
}

func TestSetupGithubAppCmd_FlagsPresent(t *testing.T) {
	cmd := newSetupGithubAppCmd()
	for _, want := range []string{"state-file", "default-name", "port", "pem-dir", "manual", "timeout"} {
		if cmd.Flags().Lookup(want) == nil {
			t.Errorf("missing flag --%s", want)
		}
	}
}
