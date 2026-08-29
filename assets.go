// Package dndcompanion holds the web assets, compiled into the binary.
//
// Everything the browser needs — pages, scripts, styles, fonts and the 3D dice —
// lives inside the executable. There is no unpack directory, nothing to extract
// at startup, and no way for the app and its assets to get separated.
package dndcompanion

import "embed"

//go:embed all:web
var Web embed.FS
