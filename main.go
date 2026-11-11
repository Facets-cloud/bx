// Command bx drives the user's running Chromium browser over the DevTools
// Protocol, one named profile at a time.
package main

import (
	"os"

	"github.com/Facets-cloud/bx/internal/app"
)

// version is set at release time with -ldflags "-X main.version=<tag>".
var version = "dev"

func main() {
	app.Version = version
	os.Exit(app.Run(os.Args[1:]))
}
