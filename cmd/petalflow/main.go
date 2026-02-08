package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/petal-labs/petalflow/cli"
)

// Set via ldflags at build time.
var version = "dev"

func main() {
	if err := rootCmd.Execute(); err != nil {
		var exitErr *cli.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.Code)
		}
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "petalflow",
	Short: "PetalFlow workflow engine CLI",
	Long:  "PetalFlow — a CLI for defining, validating, compiling, and running AI agent workflows.",
	// SilenceUsage prevents printing usage on every error
	SilenceUsage: true,
}

func init() {
	rootCmd.PersistentFlags().BoolP("verbose", "", false, "Enable verbose/debug logging")
	rootCmd.PersistentFlags().BoolP("quiet", "", false, "Suppress all output except errors")
	rootCmd.PersistentFlags().Bool("no-color", false, "Disable colored output")

	rootCmd.Version = version
	rootCmd.SetVersionTemplate(fmt.Sprintf("petalflow version %s\n", version))

	rootCmd.AddCommand(cli.NewRunCmd())
	rootCmd.AddCommand(cli.NewCompileCmd())
	rootCmd.AddCommand(cli.NewValidateCmd())
	rootCmd.AddCommand(cli.NewServeCmd())
}
