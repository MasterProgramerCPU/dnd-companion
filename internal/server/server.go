// Package server is the HTTP and websocket surface.
//
// State changes go one way: a client sends an op over the websocket, the server
// validates it against that device's role, writes SQLite, and broadcasts the
// affected slice to everyone. Nothing is trusted from the client.
package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	dndcompanion "dndcompanion"
	"dndcompanion/internal/dice"
	"dndcompanion/internal/hub"
	"dndcompanion/internal/sheet"
	"dndcompanion/internal/state"
	"dndcompanion/internal/store"

	qrcode "github.com/skip2/go-qrcode"
)

// Conditions are the status effects a character can be marked with.
var Conditions = []string{
	"blinded", "charmed", "deafened", "frightened", "grappled", "incapacitated",
	"invisible", "paralyzed", "petrified", "poisoned", "prone", "restrained",
	"stunned", "unconscious", "concentrating", "exhaustion",
}

var imageTypes = map[string]string{
	"image/png": ".png", "image/jpeg": ".jpg", "image/webp": ".webp", "image/gif": ".gif",
}

const maxImageBytes = 10 << 20

// pendingRoll is a roll the client is part-way through: it has asked what dice
// to throw and we are waiting for it to report what they landed on.
type pendingRoll struct {
	token string
	specs []dice.Spec
	meta  rollMeta
	ts    time.Time
}

const pendingTTL = 120 * time.Second

// Server holds everything the handlers need.
type Server struct {
	Store *store.Store
	Hub   *hub.Hub

	mu     sync.Mutex
	appURL string

	pendingMu sync.Mutex
	pending   map[string]pendingRoll

	web fs.FS
}

// New wires up a server over an open store.
func New(s *store.Store) (*Server, error) {
	web, err := fs.Sub(dndcompanion.Web, "web")
	if err != nil {
		return nil, err
	}
	return &Server{Store: s, Hub: hub.New(), pending: map[string]pendingRoll{}, web: web}, nil
}

// SetURL records the address the QR code should point at.
func (s *Server) SetURL(url string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appURL = url
}

// URL is the address players join on.
func (s *Server) URL() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.appURL
}

// Handler builds the routed, header-decorated HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/", s.page("index.html"))
	mux.HandleFunc("/play", s.page("player.html"))
	mux.HandleFunc("/dm", s.page("dm.html"))
	mux.HandleFunc("/qr.svg", s.handleQR)

	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(s.web))))
	mux.Handle("/uploads/", http.StripPrefix("/uploads/",
		http.FileServer(http.Dir(s.Store.UploadsDir))))

	mux.HandleFunc("/api/lobby", s.handleLobby)
	mux.HandleFunc("/api/join", s.handleJoin)
	mux.HandleFunc("/api/me", s.handleMe)
	mux.HandleFunc("/api/upload", s.handleUpload)
	mux.HandleFunc("/ws", s.handleWS)

	return cacheHeaders(mux)
}

// cacheHeaders keeps phones from running yesterday's JavaScript against today's
// server, which fails in confusing, silent ways — a pushed handout that simply
// never appears. Without an explicit Cache-Control browsers guess a freshness
// lifetime and skip revalidating entirely, so say it outright.
func cacheHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.HasPrefix(path, "/uploads/"), strings.HasPrefix(path, "/static/vendor/"):
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		case strings.HasPrefix(path, "/static/"), path == "/", path == "/play", path == "/dm":
			w.Header().Set("Cache-Control", "no-cache")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) page(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// The mux routes "/" as a catch-all; anything unrecognised is a 404
		// rather than a surprise copy of the join screen.
		if r.URL.Path != "/" && r.URL.Path != "/play" && r.URL.Path != "/dm" {
			http.NotFound(w, r)
			return
		}
		body, err := fs.ReadFile(s.web, name)
		if err != nil {
			http.Error(w, "missing page", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(body)
	}
}

// handleQR serves the join QR. It falls back to the address the client actually
// reached us on, so it works however the server was started.
func (s *Server) handleQR(w http.ResponseWriter, r *http.Request) {
	target := s.URL()
	if target == "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		target = scheme + "://" + r.Host
	}
	q, err := qrcode.New(target, qrcode.Medium)
	if err != nil {
		http.Error(w, "qr failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Write(qrSVG(q.Bitmap()))
}

// qrSVG renders a QR bitmap as one SVG path, which keeps it sharp at any size
// and small enough to inline.
func qrSVG(bitmap [][]bool) []byte {
	n := len(bitmap)
	var path strings.Builder
	for y, row := range bitmap {
		for x, dark := range row {
			if dark {
				fmt.Fprintf(&path, "M%d %dh1v1h-1z", x, y)
			}
		}
	}
	return []byte(fmt.Sprintf(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges">`+
			`<rect width="%d" height="%d" fill="#fff"/><path d="%s" fill="#000"/></svg>`,
		n, n, n, n, path.String()))
}

// ---------------------------------------------------------------- REST

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"detail": msg})
}

// require resolves a device token, optionally demanding the DM role.
func (s *Server) require(token string, dmOnly bool) (*store.Device, error) {
	dev, err := s.Store.Device(token)
	if err != nil || dev == nil {
		return nil, fmt.Errorf("unknown device — rejoin from the QR code")
	}
	if dmOnly && dev.Role != "dm" {
		return nil, fmt.Errorf("DM only")
	}
	s.Store.Exec("UPDATE devices SET last_seen=? WHERE token=?", store.Now(), token)
	return dev, nil
}

func (s *Server) handleLobby(w http.ResponseWriter, r *http.Request) {
	roster, err := state.Roster(s.Store)
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	url := s.URL()
	if url == "" {
		url = "http://" + r.Host
	}
	active, _ := s.Store.ActiveID()
	writeJSON(w, 200, map[string]any{
		"campaign":   map[string]any{"name": s.Store.CampaignName(""), "id": active},
		"roster":     roster,
		"url":        url,
		"conditions": Conditions,
	})
}

func (s *Server) handleJoin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, 405, "POST only")
		return
	}
	var req struct {
		Role        string `json:"role"`
		DisplayName string `json:"display_name"`
		CharacterID *int64 `json:"character_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, 400, "bad request")
		return
	}
	role := "player"
	if req.Role == "dm" {
		role = "dm"
	}

	charID := req.CharacterID
	display := strings.TrimSpace(req.DisplayName)
	if len(display) > 60 {
		display = display[:60]
	}

	if role == "player" {
		// The DM builds the party; players only claim a seat at it.
		if charID == nil {
			httpError(w, 400, "pick your character")
			return
		}
		ch, err := s.Store.Character(*charID)
		if err != nil || ch == nil {
			httpError(w, 404, "that character is no longer in the party")
			return
		}
		if display == "" {
			display = ch.Name
		}
	} else {
		charID = nil
	}
	if display == "" {
		display = "Player"
		if role == "dm" {
			display = "DM"
		}
	}

	token := store.Token(18)
	dev := &store.Device{
		Token: token, DisplayName: display, Role: role, CharacterID: charID,
		CreatedAt: store.Now(), LastSeen: store.Now(),
	}
	if err := s.Store.SaveDevice(dev); err != nil {
		httpError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"token": token, "role": role, "character_id": charID, "display_name": display,
	})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	dev, err := s.require(r.URL.Query().Get("token"), false)
	if err != nil {
		httpError(w, 401, err.Error())
		return
	}
	active, _ := s.Store.ActiveID()
	writeJSON(w, 200, map[string]any{
		"token":        dev.Token,
		"device_id":    DeviceID(dev.Token),
		"role":         dev.Role,
		"character_id": dev.CharacterID,
		"display_name": dev.DisplayName,
		"campaign":     map[string]any{"name": s.Store.CampaignName(""), "id": active},
		"conditions":   Conditions,
		"skills":       sheet.Skills,
		"abilities":    sheet.Abilities,
	})
}

// DeviceID is a stable public id for a device — it lets a client recognise its
// own rolls without any token material going out over the broadcast.
func DeviceID(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])[:12]
}

// handleUpload stores a handout picture by content hash, so re-uploading the
// same image costs nothing and two handouts can share one file.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, 405, "POST only")
		return
	}
	if _, err := s.require(r.URL.Query().Get("token"), true); err != nil {
		httpError(w, 403, err.Error())
		return
	}
	if err := r.ParseMultipartForm(maxImageBytes); err != nil {
		httpError(w, 413, "picture is too large")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		httpError(w, 400, "no file")
		return
	}
	defer file.Close()

	ext, ok := imageTypes[header.Header.Get("Content-Type")]
	if !ok {
		httpError(w, 400, "pictures must be PNG, JPEG, WebP or GIF")
		return
	}
	blob, err := io.ReadAll(io.LimitReader(file, maxImageBytes+1))
	if err != nil {
		httpError(w, 400, "could not read that file")
		return
	}
	if len(blob) == 0 {
		httpError(w, 400, "empty file")
		return
	}
	if len(blob) > maxImageBytes {
		httpError(w, 413, fmt.Sprintf("picture is larger than %dMB", maxImageBytes>>20))
		return
	}
	sum := sha256.Sum256(blob)
	name := hex.EncodeToString(sum[:])[:16] + ext
	if err := os.MkdirAll(s.Store.UploadsDir, 0o755); err != nil {
		httpError(w, 500, err.Error())
		return
	}
	if err := os.WriteFile(filepath.Join(s.Store.UploadsDir, name), blob, 0o644); err != nil {
		httpError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"image": name})
}
