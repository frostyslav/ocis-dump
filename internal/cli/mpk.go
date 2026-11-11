package cli

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/frostyslav/ocis-dump/internal/mpkview"
)

func newMpkCmd() *cobra.Command {
	var (
		search bool
		output string
	)

	cmd := &cobra.Command{
		Use:   "mpk <file-or-dir>",
		Short: "Show the decoded contents of .mpk (MessagePack) metadata files",
		Example: `  ocis-dump mpk path/to/node.mpk
  ocis-dump mpk /srv/ocis/storage -s -o output.txt`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			w := cmd.OutOrStdout()
			if output != "" {
				f, cerr := os.Create(output)
				if cerr != nil {
					return cerr
				}
				defer func() {
					if cerr := f.Close(); err == nil {
						err = cerr
					}
				}()
				w = f
			}
			return mpkview.Run(args[0], search, w)
		},
	}
	cmd.Flags().BoolVarP(&search, "search", "s", false, "Search the given directory for all mpk files")
	cmd.Flags().StringVarP(&output, "output", "o", "", "File to write output to (default: stdout)")
	return cmd
}
