package main

import "github.com/spf13/cobra"

func newAdminInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "bootstrap (stub)",
		RunE:  func(cmd *cobra.Command, args []string) error { return nil },
	}
}
