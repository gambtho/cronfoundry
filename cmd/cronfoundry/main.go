// Package main is the CronFoundry service CLI. Starting in P2a it hosts the
// `admin` subcommand; P2c will absorb the P1 runner binary here as well.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:           "cronfoundry",
		Short:         "CronFoundry service CLI",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newAdminCmd())
	root.AddCommand(newRunnerCmd())
	root.AddCommand(newServeCmd())
	root.AddCommand(newSetupCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
