// Command zaks-drive is the management command of ZAKS Drive: it installs, configures, starts,
// stops, updates and troubleshoots a deployment that runs under Docker Compose. The server is
// the separate binary zaks-drive-server (cmd/adapter). See docs/cli.md.
package main

import (
	"os"

	"github.com/Lamiv/zaks-drive/internal/ctl"
)

// version is set at build time (-X main.version=...); it is the version of the whole release.
var version = "dev"

func main() {
	os.Exit(ctl.NewApp(version).Main(os.Args[1:]))
}
