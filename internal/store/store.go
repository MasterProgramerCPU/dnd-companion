// Package store is the SQLite layer: the campaign registry, the campaign files
// themselves, and the accessors the rest of the app reads and writes through.
//
// Each campaign is its own database file, and a small registry says which one
// is being played. That means a campaign is one file you can copy, archive or
// delete, and no query anywhere has to filter by campaign.
package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite" // pure Go: keeps CGO off so the app cross-compiles
)

const campaignSchema = `
CREATE TABLE IF NOT EXISTS meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS devices (
    token        TEXT PRIMARY KEY,
    display_name TEXT NOT NULL,
    role         TEXT NOT NULL,
    character_id INTEGER,
    created_at   TEXT NOT NULL,
    last_seen    TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS characters (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT NOT NULL,
    sheet      TEXT NOT NULL,
    sort_idx   INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS rolls (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    ts           TEXT NOT NULL,
    actor        TEXT NOT NULL,
    character_id INTEGER,
    label        TEXT NOT NULL DEFAULT '',
    formula      TEXT NOT NULL,
    total        INTEGER NOT NULL,
    detail       TEXT NOT NULL,
    crit         TEXT,
    secret       INTEGER NOT NULL DEFAULT 0,
    terms        TEXT NOT NULL DEFAULT '[]',
    by_device    TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS initiative (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT NOT NULL,
    character_id INTEGER,
    init         REAL NOT NULL DEFAULT 0,
    hp           INTEGER,
    hp_max       INTEGER,
    ac           INTEGER,
    conditions   TEXT NOT NULL DEFAULT '[]',
    note         TEXT NOT NULL DEFAULT '',
    hidden       INTEGER NOT NULL DEFAULT 0,
    defeated     INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS party (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
`

const registrySchema = `
CREATE TABLE IF NOT EXISTS campaigns (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    file        TEXT NOT NULL,
    created     TEXT NOT NULL,
    last_played TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS registry_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
`

// PartyDefaults are the shared slices a fresh campaign starts with.
func PartyDefaults() map[string]any {
	return map[string]any{
		"gold":   map[string]any{"pp": 0, "gp": 0, "ep": 0, "sp": 0, "cp": 0},
		"loot":   []any{},
		"quests": []any{},
		"npcs":   []any{},
		"notes":  map[string]any{"text": ""},
		// Handouts are written in advance and kept; pushing one reveals it.
		"handouts": []any{},
		// The journey: places the party has reached, linked by where they
		// travelled from, so the whole campaign draws as a graph.
		"journey": map[string]any{"locations": []any{}},
		// The DM's own creatures. Never sent to players — see state.Party.
		"bestiary": []any{},
	}
}

// Now matches the timestamp format the Python version wrote, so campaign files
// made by either implementation keep sorting correctly against each other.
func Now() string { return time.Now().UTC().Format("2006-01-02T15:04:05-07:00") }

// Token returns a URL-safe random identifier, as secrets.token_urlsafe does.
func Token(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // the OS entropy source failing is not something to paper over
	}
	return strings.TrimRight(base64.URLEncoding.EncodeToString(b), "=")
}

// Store owns the registry and the currently open campaign.
type Store struct {
	DataDir      string
	CampaignsDir string
	UploadsDir   string

	mu       sync.RWMutex
	registry *sql.DB
	campaign *sql.DB
	dbPath   string
}

// Open prepares the data directory and opens the registry.
func Open(dataDir string) (*Store, error) {
	s := &Store{
		DataDir:      dataDir,
		CampaignsDir: filepath.Join(dataDir, "campaigns"),
		UploadsDir:   filepath.Join(dataDir, "uploads"),
	}
	for _, dir := range []string{s.DataDir, s.CampaignsDir, s.UploadsDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create %s: %w", dir, err)
		}
	}
	db, err := openDB(filepath.Join(dataDir, "registry.db"), registrySchema)
	if err != nil {
		return nil, fmt.Errorf("open registry: %w", err)
	}
	s.registry = db
	return s, nil
}

func openDB(path, schema string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// One writer, a tiny dataset: serialising every statement is simpler than
	// reasoning about SQLITE_BUSY, and costs nothing at this size.
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{"PRAGMA journal_mode=WAL", "PRAGMA foreign_keys=ON"} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, err
		}
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// Close releases both databases.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.campaign != nil {
		s.campaign.Close()
		s.campaign = nil
	}
	if s.registry != nil {
		return s.registry.Close()
	}
	return nil
}

func (s *Store) db() *sql.DB {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.campaign
}

// DBPath is the file the open campaign lives in.
func (s *Store) DBPath() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.dbPath
}

// ---------------------------------------------------------------- campaigns

// baseName strips a directory prefix written by either platform. A registry
// copied from Linux holds "/home/x/campaigns/abc.db" and one from Windows holds
// "C:\\Users\\x\\campaigns\\abc.db"; filepath.Base only understands the
// separator of the machine it is running on, and would return the whole string
// for the other.
func baseName(stored string) string {
	if i := strings.LastIndexAny(stored, `/\`); i >= 0 {
		return stored[i+1:]
	}
	return stored
}

// resolveFile turns a registry path into a file on this machine.
//
// Campaign files are addressed by name inside the campaigns directory, so a
// data folder stays usable after it is moved, copied to another machine, or
// carried between Linux and Windows. An absolute path from an older registry is
// honoured only when the file is genuinely still there — otherwise it would
// point at someone else's copy, or at nothing.
func (s *Store) resolveFile(stored string) string {
	candidate := filepath.Join(s.CampaignsDir, baseName(stored))
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	if filepath.IsAbs(stored) {
		if _, err := os.Stat(stored); err == nil {
			return stored
		}
	}
	return candidate
}

// Campaign is one row of the registry.
type Campaign struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Created    string `json:"created"`
	LastPlayed string `json:"last_played"`
	Active     bool   `json:"active"`
}

// Campaigns lists every campaign, most recently played first.
func (s *Store) Campaigns() ([]Campaign, error) {
	active, _ := s.ActiveID()
	rows, err := s.registry.Query(
		"SELECT id,name,created,last_played FROM campaigns ORDER BY last_played DESC, created DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Campaign{}
	for rows.Next() {
		var c Campaign
		if err := rows.Scan(&c.ID, &c.Name, &c.Created, &c.LastPlayed); err != nil {
			return nil, err
		}
		c.Active = c.ID == active
		out = append(out, c)
	}
	return out, rows.Err()
}

// ActiveID is the campaign currently on the table.
func (s *Store) ActiveID() (string, error) {
	var id string
	err := s.registry.QueryRow("SELECT value FROM registry_meta WHERE key='active'").Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return id, err
}

func clampName(name, fallback string) string {
	name = strings.TrimSpace(name)
	if len(name) > 80 {
		name = name[:80]
	}
	if name == "" {
		return fallback
	}
	return name
}

// CreateCampaign registers a new campaign and returns its id.
func (s *Store) CreateCampaign(name string) (string, error) {
	cid := Token(8)
	// Stored as a bare file name, not a path: see resolveFile.
	_, err := s.registry.Exec(
		"INSERT INTO campaigns(id,name,file,created,last_played) VALUES(?,?,?,?,?)",
		cid, clampName(name, "New Campaign"), cid+".db", Now(), Now())
	return cid, err
}

// RenameCampaign changes a campaign's display name.
func (s *Store) RenameCampaign(cid, name string) error {
	_, err := s.registry.Exec("UPDATE campaigns SET name=? WHERE id=?", clampName(name, "Untitled"), cid)
	return err
}

// CampaignName is the name of the given campaign, or of the active one.
func (s *Store) CampaignName(cid string) string {
	if cid == "" {
		cid, _ = s.ActiveID()
	}
	var name string
	if err := s.registry.QueryRow("SELECT name FROM campaigns WHERE id=?", cid).Scan(&name); err != nil {
		return "The Campaign"
	}
	return name
}

// DeleteCampaign forgets a campaign and removes its file. It refuses to delete
// the one being played, or the last one left.
func (s *Store) DeleteCampaign(cid string) (bool, error) {
	var file string
	err := s.registry.QueryRow("SELECT file FROM campaigns WHERE id=?", cid).Scan(&file)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var count int
	if err := s.registry.QueryRow("SELECT COUNT(*) FROM campaigns").Scan(&count); err != nil {
		return false, err
	}
	active, _ := s.ActiveID()
	if count <= 1 || cid == active {
		return false, nil
	}
	file = s.resolveFile(file)
	if _, err := s.registry.Exec("DELETE FROM campaigns WHERE id=?", cid); err != nil {
		return false, err
	}
	// The -wal and -shm siblings are part of the database; leaving them behind
	// would resurrect a deleted campaign's tail on the next open.
	for _, suffix := range []string{"", "-wal", "-shm"} {
		os.Remove(file + suffix)
	}
	return true, nil
}

// OpenCampaign points the app at another campaign's file.
func (s *Store) OpenCampaign(cid string) (bool, error) {
	var file string
	err := s.registry.QueryRow("SELECT file FROM campaigns WHERE id=?", cid).Scan(&file)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	file = s.resolveFile(file)

	db, err := openDB(file, campaignSchema)
	if err != nil {
		return false, err
	}

	s.mu.Lock()
	if s.campaign != nil {
		s.campaign.Close()
	}
	s.campaign, s.dbPath = db, file
	s.mu.Unlock()

	if _, err := s.registry.Exec(
		"INSERT INTO registry_meta(key,value) VALUES('active',?) "+
			"ON CONFLICT(key) DO UPDATE SET value=excluded.value", cid); err != nil {
		return false, err
	}
	if _, err := s.registry.Exec("UPDATE campaigns SET last_played=? WHERE id=?", Now(), cid); err != nil {
		return false, err
	}
	return true, s.initCampaign()
}

// InitRegistry makes sure there is a campaign to play and opens it.
func (s *Store) InitRegistry() error {
	var count int
	if err := s.registry.QueryRow("SELECT COUNT(*) FROM campaigns").Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		// A campaign.db sitting in the data directory predates campaign files
		// having their own folder; adopt it rather than stranding it.
		legacy := filepath.Join(s.DataDir, "campaign.db")
		cid := Token(8)
		file := cid + ".db"
		if _, err := os.Stat(legacy); err == nil {
			file = legacy // adopted in place, so it keeps its full path
		}
		if _, err := s.registry.Exec(
			"INSERT INTO campaigns(id,name,file,created,last_played) VALUES(?,?,?,?,?)",
			cid, "The Campaign", file, Now(), Now()); err != nil {
			return err
		}
	}
	active, err := s.ActiveID()
	if err != nil {
		return err
	}
	if active == "" {
		if err := s.registry.QueryRow(
			"SELECT id FROM campaigns ORDER BY last_played DESC LIMIT 1").Scan(&active); err != nil {
			return err
		}
	}
	ok, err := s.OpenCampaign(active)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("could not open campaign %s", active)
	}
	return nil
}

// initCampaign applies additive migrations to a campaign that may predate them.
func (s *Store) initCampaign() error {
	db := s.db()
	have := map[string]bool{}
	rows, err := db.Query("PRAGMA table_info(rolls)")
	if err != nil {
		return err
	}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		have[name] = true
	}
	rows.Close()
	for _, col := range []struct{ name, decl string }{
		{"terms", "TEXT NOT NULL DEFAULT '[]'"},
		{"by_device", "TEXT NOT NULL DEFAULT ''"},
	} {
		if !have[col.name] {
			if _, err := db.Exec("ALTER TABLE rolls ADD COLUMN " + col.name + " " + col.decl); err != nil {
				return err
			}
		}
	}
	return nil
}

// ---------------------------------------------------------------- key/value

// Meta reads a JSON value from the campaign's meta table.
func (s *Store) Meta(key string, out any) (bool, error) { return s.kvGet("meta", key, out) }

// SetMeta writes a JSON value to the campaign's meta table.
func (s *Store) SetMeta(key string, value any) error { return s.kvSet("meta", key, value) }

// Party reads one of the shared slices, falling back to its default.
func (s *Store) Party(key string) any {
	var out any
	ok, err := s.kvGet("party", key, &out)
	if err != nil || !ok {
		if def, exists := PartyDefaults()[key]; exists {
			return def
		}
		return nil
	}
	return out
}

// SetParty writes one of the shared slices.
func (s *Store) SetParty(key string, value any) error { return s.kvSet("party", key, value) }

func (s *Store) kvGet(table, key string, out any) (bool, error) {
	var raw string
	err := s.db().QueryRow("SELECT value FROM "+table+" WHERE key=?", key).Scan(&raw)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal([]byte(raw), out)
}

func (s *Store) kvSet(table, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.db().Exec(
		"INSERT INTO "+table+"(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value",
		key, string(raw))
	return err
}
