package cli

import "github.com/spf13/cobra"

// NewServeCmd creates the "serve" subcommand.
func NewServeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the daemon HTTP server",
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO: implement in Phase 2
			return nil
		},
	}

	cmd.Flags().IntP("port", "p", 8080, "Listen port")
	cmd.Flags().String("host", "0.0.0.0", "Listen host")
	cmd.Flags().String("cors-origin", "*", "Allowed CORS origin")
	cmd.Flags().String("store", "memory", "Workflow store backend: memory | file")
	cmd.Flags().String("store-path", "", "File store directory (only for --store file)")
	cmd.Flags().StringArray("provider-key", nil, "Set provider API key (repeatable)")

	return cmd
}
