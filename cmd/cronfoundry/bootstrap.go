// cmd/cronfoundry/bootstrap.go
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/gambtho/cronfoundry/internal/bootstrap/azure"
	"github.com/gambtho/cronfoundry/internal/secrets/server"
)

func newBootstrapCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bootstrap",
		Short: "Bootstrap a fresh CronFoundry deployment",
	}
	cmd.AddCommand(newBootstrapAzureCmd())
	return cmd
}

func newBootstrapAzureCmd() *cobra.Command {
	var (
		paramsOut    string
		templateFile string
		dryRun       bool
	)
	cmd := &cobra.Command{
		Use:   "azure",
		Short: "Interactive deploy to Azure",
		RunE: func(cmd *cobra.Command, args []string) error {
			runner := &azure.ExecRunner{Stdout: os.Stdout, Stderr: os.Stderr}
			in, err := azure.Prompt(cmd.Context(), os.Stdin, os.Stdout)
			if err != nil {
				return err
			}
			if err := in.Validate(); err != nil {
				return err
			}
			masterKey, err := server.GenerateMasterKey()
			if err != nil {
				return fmt.Errorf("generate master key: %w", err)
			}
			fmt.Fprintf(os.Stdout, "  generated master key: %s\n", masterKey) //nolint:errcheck

			if paramsOut == "" {
				paramsOut = filepath.Join("deploy", fmt.Sprintf("params.%s.json", in.Env))
			}
			binary, err := os.Executable()
			if err != nil {
				return err
			}
			bs := &azure.Bootstrap{
				Runner:       runner,
				Inputs:       in,
				MasterKey:    masterKey,
				ParamsPath:   paramsOut,
				TemplateFile: templateFile,
				Binary:       binary,
				DryRun:       dryRun,
				Stdout:       os.Stdout,
			}
			return bs.Run(cmd.Context())
		},
	}
	cmd.Flags().StringVar(&paramsOut, "params-out", "",
		"where to write the Bicep params file (default deploy/params.<env>.json)")
	cmd.Flags().StringVar(&templateFile, "template-file", "deploy/main.bicep",
		"path to the Bicep template")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"stop after writing params, before az deployment")
	return cmd
}
