// SPDX-License-Identifier: AGPL-3.0-only

// Command vaultreader serves Obsidian-compatible vaults over HTTP.
package main

import (
	"flag"

	"github.com/joaompfp/vaultreader/internal/server"
)

// version is stamped at build time: -ldflags "-X main.version=<git sha>".
var version = "dev"

func main() {
	opts := server.Options{Version: version}
	flag.StringVar(&opts.VaultsDir, "vaults", "/vaults", "path to vaults directory")
	flag.StringVar(&opts.Port, "port", "8080", "port to listen on")
	flag.StringVar(&opts.AppdataDir, "appdata", "/appdata", "path to appdata directory (vault icons, customisations)")
	flag.Parse()

	server.Run(opts)
}
