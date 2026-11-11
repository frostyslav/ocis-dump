// Package cli wires the ocis-dump sub-commands to the implementation packages.
package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/frostyslav/ocis-dump/internal/symlinkverify"
)

// Exit codes returned by Execute.
const (
	ExitOK        = 0
	ExitError     = 1
	ExitIncorrect = 2 // symlinks still incorrect after verification
)

// globals holds the flags shared by all sub-commands.
type globals struct {
	verbose bool
	quiet   bool
}

// NewRootCmd builds the ocis-dump command tree.
func NewRootCmd(version string) *cobra.Command {
	var g globals

	root := &cobra.Command{
		Use:           "ocis-dump",
		Short:         "Dump and inspect OCIS storage",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().BoolVarP(&g.verbose, "verbose", "v", false, "Show detailed output")
	root.PersistentFlags().BoolVarP(&g.quiet, "quiet", "q", false, "Only show errors and summary")
	root.MarkFlagsMutuallyExclusive("verbose", "quiet")

	root.AddCommand(newDumpCmd(&g), newMpkCmd(), newSymlinksCmd(&g))
	return root
}

// Execute runs the CLI with args and returns the process exit code.
func Execute(version string, args []string, stdout, stderr io.Writer) int {
	root := NewRootCmd(version)
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)

	err := root.Execute()
	switch {
	case err == nil:
		return ExitOK
	case errors.Is(err, symlinkverify.ErrIncorrect):
		fmt.Fprintf(stderr, "WARNING: %v\n", err)
		return ExitIncorrect
	default:
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return ExitError
	}
}
