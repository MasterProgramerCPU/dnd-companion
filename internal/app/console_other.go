//go:build !windows

package app

// enableUTF8Console is a no-op everywhere but Windows, where the console
// defaults to a legacy code page that cannot render the QR.
func enableUTF8Console() {}
