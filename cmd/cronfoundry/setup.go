package main

import "github.com/spf13/cobra"

func newSetupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Interactive setup helpers used by install.sh",
	}
	cmd.AddCommand(newSetupGithubAppCmd())
	cmd.AddCommand(newSetupMintJWTCmd())
	return cmd
}
