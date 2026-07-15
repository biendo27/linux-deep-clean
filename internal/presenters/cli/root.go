// Package cli presents the bootstrap command-line contract.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/biendo27/linux-deep-clean/internal/application"
	"github.com/spf13/cobra"
)

// NewRootCommand creates the deliberately small, offline bootstrap command.
// It has no discovery, state, authorization, or mutation authority.
func NewRootCommand(bootstrap application.Bootstrap) *cobra.Command {
	info := bootstrap.BuildInfo()
	var showVersion bool

	command := &cobra.Command{
		Use:           "ldclean",
		Short:         "Linux Deep Clean",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("unknown command %q", args[0])
			}
			return nil
		},
	}
	command.Flags().BoolVar(&showVersion, "version", false, "print version and exit")
	command.RunE = func(command *cobra.Command, _ []string) error {
		if showVersion {
			_, err := fmt.Fprintf(command.OutOrStdout(), "ldclean version %s\n", info.Version)
			return err
		}
		return command.Help()
	}

	return command
}

// Execute is the process-boundary adapter for the bootstrap CLI. The root
// guard is evaluated before a command is built or arguments are dispatched.
func Execute(ctx context.Context, bootstrap application.Bootstrap, stdout, stderr io.Writer) int {
	if err := bootstrap.RequireUnprivileged(); err != nil {
		_, _ = fmt.Fprint(stderr, "ldclean: refusing to run as root\n")
		return 1
	}

	info := bootstrap.BuildInfo()
	if err := info.Validate(); err != nil {
		_, _ = fmt.Fprint(stderr, "ldclean: invalid build metadata\n")
		return 1
	}

	command := NewRootCommand(bootstrap)
	command.SetArgs(os.Args[1:])
	command.SetOut(stdout)
	command.SetErr(stderr)
	if err := command.ExecuteContext(ctx); err != nil {
		_, _ = fmt.Fprintf(stderr, "ldclean: %v\n", err)
		return 2
	}

	return 0
}
