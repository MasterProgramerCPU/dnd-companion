// Package dndcompanion holds the web assets, compiled into the binary.
//
// This is the whole reason the Go build has no equivalent of the Python
// version's paths.py: there is no unpack directory and no "where did my
// assets go" question, because the files are in the executable itself.
package dndcompanion

import "embed"

//go:embed all:web
var Web embed.FS
