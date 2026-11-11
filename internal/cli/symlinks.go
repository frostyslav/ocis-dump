package cli

import (
	"github.com/spf13/cobra"

	"github.com/frostyslav/ocis-dump/internal/symlinkverify"
)

func newSymlinksCmd(g *globals) *cobra.Command {
	opt := symlinkverify.Options{}

	cmd := &cobra.Command{
		Use:   "symlinks <path>",
		Short: "Verify (and optionally repair) the OCIS node symlink tree",
		Long: `Check that every node has the symlink OCIS expects in its parent directory.
Exits with code 2 if symlinks are still incorrect after verification.

path is the OCIS data root. One of --data or --metadata is required.`,
		Example: `  ocis-dump symlinks /srv/ocis --data
  ocis-dump symlinks /srv/ocis --metadata --fix`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opt.Path = args[0]
			opt.Verbose = g.verbose
			return symlinkverify.Run(opt, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}

	f := cmd.Flags()
	f.BoolVarP(&opt.Fix, "fix", "f", false, "Repair missing/incorrect symlinks")
	f.BoolVarP(&opt.Metadata, "metadata", "m", false, "Process the metadata tree")
	f.BoolVarP(&opt.Data, "data", "d", false, "Process the user data tree")
	f.StringVar(&opt.MetadataDir, "metadata-subdir", symlinkverify.DefaultMetadataSubdir, "Metadata storage subdirectory")
	f.StringVar(&opt.DataDir, "data-subdir", symlinkverify.DefaultDataSubdir, "Data storage subdirectory")
	cmd.MarkFlagsMutuallyExclusive("metadata", "data")
	cmd.MarkFlagsOneRequired("metadata", "data")
	return cmd
}
