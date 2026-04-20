package main

import (
	"github.com/spf13/cobra"
)

// newAdminCmd returns the `admin` subcommand group. Leaf subcommands are
// registered in sibling files (admin_init.go, admin_setsecret.go).
func newAdminCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Operator utilities — init, secrets, triggers",
	}
	cmd.AddCommand(newAdminInitCmd())
	cmd.AddCommand(newAdminSetSecretCmd())
	return cmd
}
