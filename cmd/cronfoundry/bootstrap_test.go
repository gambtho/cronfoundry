// cmd/cronfoundry/bootstrap_test.go
package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBootstrapCmd_HasAzureSubcommand(t *testing.T) {
	cmd := newBootstrapCmd()
	require.Equal(t, "bootstrap", cmd.Name())
	var azureSub *cobraTestProbe
	for _, sub := range cmd.Commands() {
		if sub.Name() == "azure" {
			azureSub = &cobraTestProbe{
				dryRun:    sub.Flags().Lookup("dry-run") != nil,
				paramsOut: sub.Flags().Lookup("params-out") != nil,
			}
		}
	}
	require.NotNil(t, azureSub, "azure subcommand missing")
	require.True(t, azureSub.dryRun, "missing --dry-run flag")
	require.True(t, azureSub.paramsOut, "missing --params-out flag")
}

type cobraTestProbe struct {
	dryRun    bool
	paramsOut bool
}
