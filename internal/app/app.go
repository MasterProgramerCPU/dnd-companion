// Package app is the lifecycle: find the data, start serving, print the QR,
// stop cleanly when the window closes.
//
// This behaves identically on Linux and Windows. There is no service, no
// autostart and nothing left running: the app is up while you have it open and
// gone when you close it.
package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"dndcompanion/internal/server"
	"dndcompanion/internal/store"

	qrcode "github.com/skip2/go-qrcode"
)

// AppName names the per-user data directory.
const AppName = "DnDCompanion"

// Options are the knobs Run understands.
type Options struct {
	Port        int
	DataDir     string
	URL         string
	OpenBrowser bool
}

// DefaultDataDir decides where campaigns live.
//
// A data directory sitting next to the executable wins, which is what makes a
// portable copy — binary and data in one folder — behave the way people expect
// when they move it to another machine. Otherwise campaigns go to the per-user
// location the OS guarantees is writable, so the app works when it is run from
// a Downloads folder.
func DefaultDataDir() string {
	for _, base := range candidateDirs() {
		if base == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(base, "data", "registry.db")); err == nil {
			return filepath.Join(base, "data")
		}
	}
	return filepath.Join(userDataRoot(), AppName)
}

func candidateDirs() []string {
	var out []string
	if exe, err := os.Executable(); err == nil {
		out = append(out, filepath.Dir(exe))
	}
	if cwd, err := os.Getwd(); err == nil {
		out = append(out, cwd)
	}
	return out
}

func userDataRoot() string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "windows":
		if v := os.Getenv("LOCALAPPDATA"); v != "" {
			return v
		}
		return filepath.Join(home, "AppData", "Local")
	case "darwin":
		return filepath.Join(home, "Library", "Application Support")
	default:
		if v := os.Getenv("XDG_DATA_HOME"); v != "" {
			return v
		}
		return filepath.Join(home, ".local", "share")
	}
}

// LANIP is the address phones on the Wi-Fi can reach this machine at.
//
// This asks the OS which interface would carry traffic to the internet; no
// packet is actually sent. On a machine with Hyper-V, WSL, VirtualBox or a VPN
// the answer can be a virtual adapter the phones cannot see, which is what
// DND_URL is for.
func LANIP() string {
	conn, err := net.Dial("udp4", "8.8.8.8:80")
	if err != nil {
		return "127.0.0.1"
	}
	defer conn.Close()
	if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		return addr.IP.String()
	}
	return "127.0.0.1"
}

// Run starts the app and blocks until it is asked to stop.
func Run(opts Options) error {
	enableUTF8Console()

	if opts.DataDir == "" {
		opts.DataDir = DefaultDataDir()
	}
	st, err := store.Open(opts.DataDir)
	if err != nil {
		return fmt.Errorf("open data directory: %w", err)
	}
	defer st.Close()

	if err := st.InitRegistry(); err != nil {
		return fmt.Errorf("prepare campaign: %w", err)
	}

	srv, err := server.New(st)
	if err != nil {
		return err
	}
	url := opts.URL
	if url == "" {
		url = fmt.Sprintf("http://%s:%d", LANIP(), opts.Port)
	}
	srv.SetURL(url)

	httpSrv := &http.Server{
		Addr:              fmt.Sprintf("0.0.0.0:%d", opts.Port),
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Bind before printing anything, so a port clash is reported as a sentence
	// rather than as a QR code for a server that never started.
	listener, err := net.Listen("tcp", httpSrv.Addr)
	if err != nil {
		return fmt.Errorf("could not listen on port %d — is the app already running?\n  (%w)",
			opts.Port, err)
	}

	banner(url, st.CampaignName(""), st.DBPath(), opts.DataDir)

	if opts.OpenBrowser {
		go openBrowser(url + "/dm")
	}

	errs := make(chan error, 1)
	go func() {
		if err := httpSrv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- err
		}
	}()

	// Ctrl-C in a terminal, closing the window, or a shutdown: all the same
	// thing here — stop serving and let the process end.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errs:
		return err
	case <-stop:
	}

	fmt.Println("\n  Stopping…")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return httpSrv.Shutdown(ctx)
}

func banner(url, campaign, dbPath, dataDir string) {
	if q, err := qrcode.New(url, qrcode.Medium); err == nil {
		fmt.Println()
		fmt.Print(asciiQR(q.Bitmap()))
	}
	fmt.Printf("\n  Players join at:  %s\n", url)
	fmt.Printf("  DM console:       %s/dm\n", url)
	fmt.Printf("  Playing:          %s\n", campaign)
	fmt.Printf("  Campaign file:    %s\n", dbPath)
	fmt.Printf("  Data folder:      %s\n", dataDir)
	fmt.Print("\n  Close this window (or press Ctrl-C) to stop.\n\n")
}

// asciiQR draws the code two rows per line using half-blocks.
//
// The polarity is deliberate: light modules are drawn as filled blocks and dark
// modules as gaps, which is what makes it scan on a terminal with a dark
// background — the usual case for the window this prints into.
func asciiQR(bitmap [][]bool) string {
	var sb strings.Builder
	for y := 0; y < len(bitmap); y += 2 {
		sb.WriteString("  ")
		for x := range bitmap[y] {
			top := !bitmap[y][x]
			bottom := true
			if y+1 < len(bitmap) {
				bottom = !bitmap[y+1][x]
			}
			switch {
			case top && bottom:
				sb.WriteRune('█')
			case top:
				sb.WriteRune('▀')
			case bottom:
				sb.WriteRune('▄')
			default:
				sb.WriteRune(' ')
			}
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	cmd.Start()
}
