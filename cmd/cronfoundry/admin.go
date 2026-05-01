package main

import (
	"github.com/spf13/cobra"
)

// newAdminCmd returns the `admin` subcommand group. Leaf subcommands are
// registered in sibling files (admin_init.go, admin_setsecret.go,
// admin_connectrepo.go, admin_listconnections.go, admin_listschedules.go,
// admin_triggersync.go, admin_listruns.go, admin_showrun.go).
func newAdminCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Operator utilities — init, secrets, repos, triggers",
	}
	cmd.AddCommand(newAdminInitCmd())
	cmd.AddCommand(newAdminSetSecretCmd())
	cmd.AddCommand(newAdminConnectRepoCmd())
	cmd.AddCommand(newAdminConnectCopilotCmd(prodConnectCopilotDeps()))
	cmd.AddCommand(newAdminListConnectionsCmd())
	cmd.AddCommand(newAdminListSchedulesCmd())
	cmd.AddCommand(newAdminTriggerSyncCmd())
	cmd.AddCommand(newAdminListRunsCmd())
	cmd.AddCommand(newAdminShowRunCmd())
	return cmd
}
