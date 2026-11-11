// Command ocis-dump dumps and inspects OCIS storage.
package main

import (
	"os"

	"github.com/frostyslav/ocis-dump/internal/cli"
)

// version is set at build time: -ldflags "-X main.version=1.2.3".
var version = "dev"

func main() {
	os.Exit(cli.Execute(version, os.Args[1:], os.Stdout, os.Stderr))
}
