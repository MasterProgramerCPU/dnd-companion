// Command dnd-companion is the LAN table companion for in-person 5e games.
//
// Run it, and everyone at the table joins from the QR code it prints. Close it,
// and the session is over. It behaves the same on Linux and on Windows.
package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"

	"dndcompanion/internal/app"
)

// version is stamped at build time by build.sh.
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print the version and exit")
	port := flag.Int("port", envInt("DND_PORT", 8787), "port to listen on")
	dataDir := flag.String("data", os.Getenv("DND_DATA_DIR"),
		"where campaigns are stored (default: beside the app, else the per-user data folder)")
	url := flag.String("url", os.Getenv("DND_URL"),
		"the address to put in the QR code, when the guess is wrong")
	noOpen := flag.Bool("no-open", false, "don't open the DM console in a browser at startup")
	flag.Parse()

	if *showVersion {
		fmt.Println("dnd-companion", version)
		return
	}

	if err := app.Run(app.Options{
		Port:        *port,
		DataDir:     *dataDir,
		URL:         *url,
		OpenBrowser: !*noOpen,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "\n  %v\n\n", err)
		os.Exit(1)
	}
}

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil && v > 0 {
		return v
	}
	return def
}
