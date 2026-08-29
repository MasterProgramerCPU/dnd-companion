package store_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"dndcompanion/internal/store"

	_ "modernc.org/sqlite"
)

func open(t *testing.T, dir string) *store.Store {
	t.Helper()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := s.InitRegistry(); err != nil {
		t.Fatalf("init: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// A data folder must survive being moved — to another directory, another
// machine, or another operating system. Campaign files are addressed by name
// inside the campaigns directory precisely so this keeps working.
func TestDataDirectoryCanBeMoved(t *testing.T) {
	first := t.TempDir()
	s := open(t, first)
	if _, err := s.CreateCharacter("Vex", map[string]any{"name": "Vex", "level": 5.0}); err != nil {
		t.Fatalf("create character: %v", err)
	}
	if err := s.SetParty("notes", map[string]any{"text": "the tomb is trapped"}); err != nil {
		t.Fatalf("set notes: %v", err)
	}
	s.Close()

	second := t.TempDir()
	if err := os.CopyFS(second, os.DirFS(first)); err != nil {
		t.Fatalf("copy data dir: %v", err)
	}

	moved := open(t, second)
	if got := moved.DBPath(); filepath.Dir(filepath.Dir(got)) != second {
		t.Errorf("after moving, campaign file is %q — should be inside %q", got, second)
	}
	chars, err := moved.Characters()
	if err != nil || len(chars) != 1 || chars[0].Name != "Vex" {
		t.Fatalf("character did not survive the move: %v %v", chars, err)
	}
	notes, _ := moved.Party("notes").(map[string]any)
	if notes["text"] != "the tomb is trapped" {
		t.Errorf("notes did not survive the move: %v", notes)
	}
}

// A registry written on the other operating system holds paths with the other
// separator, which filepath.Base on this one would not split. The file is still
// sitting in the campaigns folder, and must still be found.
func TestRegistryPathFromTheOtherPlatformResolves(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir)
	active, _ := s.ActiveID()
	name := filepath.Base(s.DBPath())
	if _, err := s.CreateCharacter("Pip", map[string]any{"name": "Pip"}); err != nil {
		t.Fatalf("create character: %v", err)
	}
	s.Close()

	// Rewrite the row exactly as a Windows-made registry would have stored it.
	foreign := `C:\Users\someone\AppData\Local\DnDCompanion\campaigns\` + name
	reg, err := sql.Open("sqlite", filepath.Join(dir, "registry.db"))
	if err != nil {
		t.Fatalf("open registry: %v", err)
	}
	if _, err := reg.Exec("UPDATE campaigns SET file=? WHERE id=?", foreign, active); err != nil {
		t.Fatalf("rewrite path: %v", err)
	}
	reg.Close()

	reopened := open(t, dir)
	if got := reopened.DBPath(); got != filepath.Join(dir, "campaigns", name) {
		t.Fatalf("resolved to %q, want the local campaigns folder", got)
	}
	chars, err := reopened.Characters()
	if err != nil || len(chars) != 1 || chars[0].Name != "Pip" {
		t.Errorf("campaign not found through a foreign path: %v %v", chars, err)
	}
}

func TestDeletingTheActiveCampaignIsRefused(t *testing.T) {
	s := open(t, t.TempDir())
	active, err := s.ActiveID()
	if err != nil || active == "" {
		t.Fatalf("no active campaign: %v", err)
	}
	ok, err := s.DeleteCampaign(active)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if ok {
		t.Error("deleted the campaign being played — should have refused")
	}
}
