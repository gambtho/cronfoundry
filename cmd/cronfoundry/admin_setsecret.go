package main

import "github.com/spf13/cobra"

func newAdminSetSecretCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set-secret",
		Short: "store a secret (stub)",
		RunE:  func(cmd *cobra.Command, args []string) error { return nil },
	}
}
