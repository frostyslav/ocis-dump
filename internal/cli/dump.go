package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/frostyslav/ocis-dump/internal/dump"
)

func newDumpCmd(g *globals) *cobra.Command {
	opt := dump.Options{}

	cmd := &cobra.Command{
		Use:   "dump [topdir] [outdir]",
		Short: "Dump OCIS storage into a POSIX file tree",
		Long: `Extract files from an OCIS decomposed-fs storage into a standard directory
tree: <outdir>/<space type>/<username>/<path>.

topdir is the OCIS data root (default: $HOME/.ocis); outdir defaults to ".".`,
		Example: `  ocis-dump dump /srv/ocis ./backup
  ocis-dump dump -l -u "John Doe"
  ocis-dump dump /srv/ocis ./backup --dry-run`,
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			opt.Verbose, opt.Quiet = g.verbose, g.quiet
			opt.OutDir = "."
			if len(args) > 0 {
				opt.TopDir = args[0]
			} else {
				home, err := os.UserHomeDir()
				if err != nil {
					return fmt.Errorf("cannot determine default topdir: %w", err)
				}
				opt.TopDir = filepath.Join(home, ".ocis")
			}
			if len(args) > 1 {
				opt.OutDir = args[1]
			}
			if opt.Jobs < 1 {
				return fmt.Errorf("--jobs must be at least 1, got %d", opt.Jobs)
			}
			return dump.Run(opt, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}

	f := cmd.Flags()
	f.BoolVarP(&opt.List, "list", "l", false, "List files without copying")
	f.BoolVarP(&opt.DryRun, "dry-run", "n", false, "Show what would be copied without writing anything")
	f.StringVarP(&opt.User, "user", "u", "", "Filter by user's display name (substring, case-insensitive)")
	f.StringVar(&opt.Username, "username", "", "Filter by actual username (substring, case-insensitive)")
	f.BoolVarP(&opt.Info, "info", "i", false, "Only show space info, skip file tree")
	f.BoolVarP(&opt.Force, "force", "f", false, "Force overwrite of all files, even if unchanged")
	f.IntVarP(&opt.Jobs, "jobs", "j", 4, "Number of parallel copy workers")
	f.StringVar(&opt.StoragePrefix, "storage-prefix", dump.DefaultStoragePrefix, "Spaces path within the OCIS data directory")
	cmd.MarkFlagsMutuallyExclusive("list", "dry-run")
	return cmd
}
