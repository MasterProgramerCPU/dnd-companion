//go:build windows

package app

import "syscall"

// enableUTF8Console switches the console to UTF-8.
//
// Without this the QR's block characters land in the console's legacy code page
// (cp1252 on a typical Western install) and come out as mojibake. This is the
// same failure the Python build hit, solved at the console instead of by
// dropping the picture. Pure syscall, so CGO stays off.
func enableUTF8Console() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	const cpUTF8 = 65001
	kernel32.NewProc("SetConsoleOutputCP").Call(uintptr(cpUTF8))
	kernel32.NewProc("SetConsoleCP").Call(uintptr(cpUTF8))
}
