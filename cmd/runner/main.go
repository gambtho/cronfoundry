package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:   "cronfoundry-runner",
		Short: "Execute a CronFoundry skill once",
	}
	root.AddCommand(&cobra.Command{
		Use:   "run",
		Short: "Run a skill from a cronfoundry.yaml manifest",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("run: not yet implemented")
		},
	})
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
