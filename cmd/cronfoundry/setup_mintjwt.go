package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/gambtho/cronfoundry/internal/github"
)

func newSetupMintJWTCmd() *cobra.Command {
	var appID, pemPath string
	cmd := &cobra.Command{
		Use:   "mint-jwt",
		Short: "Mint a short-lived GitHub App JWT for ad-hoc API calls",
		RunE: func(cmd *cobra.Command, _ []string) error {
			pem, err := os.ReadFile(pemPath)
			if err != nil {
				return fmt.Errorf("read pem: %w", err)
			}
			jwt, err := github.AppJWT(appID, pem)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), jwt)
			return nil
		},
	}
	cmd.Flags().StringVar(&appID, "app-id", "", "GitHub App ID")
	cmd.Flags().StringVar(&pemPath, "pem", "", "Path to App private-key PEM")
	_ = cmd.MarkFlagRequired("app-id")
	_ = cmd.MarkFlagRequired("pem")
	return cmd
}
