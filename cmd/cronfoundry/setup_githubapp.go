package main

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/gambtho/cronfoundry/internal/githubapp"
)

func newSetupGithubAppCmd() *cobra.Command {
	var (
		stateFile   string
		defaultName string
		port        int
		pemDir      string
		manual      bool
		timeout     time.Duration
		baseAPI     string
	)

	cmd := &cobra.Command{
		Use:   "github-app",
		Short: "Create and install a GitHub App via manifest flow",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if stateFile == "" || pemDir == "" {
				home, err := os.UserHomeDir()
				if err != nil {
					return fmt.Errorf("setup: resolve home directory (pass --state-file and --pem-dir to override): %w", err)
				}
				if stateFile == "" {
					stateFile = filepath.Join(home, ".cronfoundry-quickstart-state")
				}
				if pemDir == "" {
					pemDir = filepath.Join(home, ".cronfoundry")
				}
			}
			if defaultName == "" {
				u, err := user.Current()
				if err == nil && u != nil && u.Username != "" {
					defaultName = "cronfoundry-" + u.Username
				} else {
					defaultName = "cronfoundry-app"
				}
			}

			if manual {
				return githubapp.RunManual(os.Stdin, cmd.OutOrStdout(), githubapp.ManualOptions{
					StateFile: stateFile,
				})
			}

			conv := githubapp.NewConverter(baseAPI, nil)
			srv, err := githubapp.NewServer(githubapp.Options{
				Port:          port,
				StateFile:     stateFile,
				PEMDir:        pemDir,
				Converter:     conv,
				ManifestInput: githubapp.ManifestInput{Name: defaultName},
				Timeout:       timeout,
			})
			if err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(),
				"Starting local GitHub App setup helper at %s\nIf your browser doesn't open, visit that URL manually.\n",
				srv.URL())

			ctx, cancel := context.WithCancel(cmd.Context())
			defer cancel()
			res, err := srv.Run(ctx)
			if err != nil {
				return fmt.Errorf("setup: %w (re-run with --manual for the legacy prompts)", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"\nGitHub App created: id=%d slug=%s installation=%d\n  PEM: %s\n  state: %s\n",
				res.AppID, res.Slug, res.InstallationID, res.PEMPath, stateFile)
			return nil
		},
	}
	cmd.Flags().StringVar(&stateFile, "state-file", "", "install state file (default ~/.cronfoundry-quickstart-state)")
	cmd.Flags().StringVar(&defaultName, "default-name", "", "default GitHub App name (default cronfoundry-<user>)")
	cmd.Flags().IntVar(&port, "port", 8765, "localhost port for callback server")
	cmd.Flags().StringVar(&pemDir, "pem-dir", "", "directory to write the .pem file (default ~/.cronfoundry)")
	cmd.Flags().BoolVar(&manual, "manual", false, "skip the browser flow; prompt for credentials manually")
	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Minute, "abort if the user doesn't complete the flow in this time")
	cmd.Flags().StringVar(&baseAPI, "github-api", "https://api.github.com", "GitHub API base URL (override for testing)")
	return cmd
}
