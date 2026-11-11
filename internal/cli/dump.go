package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/frostyslav/ocis-dump/internal/dump"
	"github.com/frostyslav/ocis-dump/internal/ocis"
)

// Environment variables that provide defaults for the progress flags.
const (
	envFileProgress        = "OCIS_DUMP_FILE_PROGRESS"
	envFileProgressMinSize = "OCIS_DUMP_FILE_PROGRESS_MIN_SIZE"
)

// envBool returns the boolean in the environment variable name, or def when
// it is unset. A malformed value is reported through *errp.
func envBool(name string, def bool, errp *error) bool {
	v, ok := os.LookupEnv(name)
	if !ok || v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		*errp = fmt.Errorf("invalid %s=%q: want true or false", name, v)
		return def
	}
	return b
}

func envString(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func newDumpCmd(g *globals) *cobra.Command {
	opt := dump.Options{}
	var (
		minSize string
		envErr  error
	)

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
			if envErr != nil {
				return envErr
			}
			size, err := ocis.ParseSize(minSize)
			if err != nil {
				return fmt.Errorf("--file-progress-min-size: %w", err)
			}
			opt.FileProgressMinSize = size
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
	f.BoolVar(&opt.FileProgress, "file-progress", envBool(envFileProgress, true, &envErr),
		"Show a line per large file being copied in each progress report (env "+envFileProgress+")")
	f.StringVar(&minSize, "file-progress-min-size", envString(envFileProgressMinSize, "1MiB"),
		"Smallest file to show with --file-progress, e.g. 512KiB, 10MiB, 1GiB (env "+envFileProgressMinSize+")")
	cmd.MarkFlagsMutuallyExclusive("list", "dry-run")
	return cmd
}
