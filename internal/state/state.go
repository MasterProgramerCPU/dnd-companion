// Package state builds the JSON slices pushed to clients.
//
// The DM's view and the players' view of the same slice are rendered from the
// same data with different redactions, so the two can never drift apart.
package state

import (
	"dndcompanion/internal/sheet"
	"dndcompanion/internal/store"
)

// CharacterView is a character as the clients see it, derived stats included.
type CharacterView struct {
	ID        int64          `json:"id"`
	Name      string         `json:"name"`
	SortIdx   int            `json:"sort_idx"`
	UpdatedAt string         `json:"updated_at"`
	Sheet     map[string]any `json:"sheet"`
	Derived   sheet.Derived  `json:"derived"`
}

// Character renders one character.
func Character(c store.Character) CharacterView {
	return CharacterView{
		ID: c.ID, Name: c.Name, SortIdx: c.SortIdx, UpdatedAt: c.UpdatedAt,
		Sheet: c.Sheet, Derived: sheet.Derive(c.Sheet),
	}
}

// Characters renders the whole party.
func Characters(s *store.Store) ([]CharacterView, error) {
	rows, err := s.Characters()
	if err != nil {
		return nil, err
	}
	out := make([]CharacterView, 0, len(rows))
	for _, c := range rows {
		out = append(out, Character(c))
	}
	return out, nil
}

// RosterEntry is the lightweight form the join screen lists, with no sheet.
type RosterEntry struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Player  string `json:"player"`
	Klass   string `json:"klass"`
	Level   int    `json:"level"`
	Color   string `json:"color"`
	Claimed bool   `json:"claimed"`
}

// Roster lists who can be claimed on the join screen.
func Roster(s *store.Store) ([]RosterEntry, error) {
	rows, err := s.Characters()
	if err != nil {
		return nil, err
	}
	claimed, err := s.ClaimedCharacterIDs()
	if err != nil {
		return nil, err
	}
	out := make([]RosterEntry, 0, len(rows))
	for _, c := range rows {
		out = append(out, RosterEntry{
			ID: c.ID, Name: c.Name,
			Player:  str(c.Sheet["player"], ""),
			Klass:   str(c.Sheet["klass"], ""),
			Level:   num(c.Sheet["level"], 1),
			Color:   str(c.Sheet["color"], "#c9a227"),
			Claimed: claimed[c.ID],
		})
	}
	return out, nil
}

// InitEntry is one row of the initiative order as clients see it.
type InitEntry struct {
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	CharacterID *int64  `json:"character_id"`
	Init        float64 `json:"init"`
	HP          *int    `json:"hp"`
	HPMax       *int    `json:"hp_max"`
	AC          *int    `json:"ac"`
	Conditions  []any   `json:"conditions"`
	Note        string  `json:"note"`
	Hidden      bool    `json:"hidden"`
	Defeated    bool    `json:"defeated"`
	Wounds      string  `json:"wounds,omitempty"`
}

// Encounter is the round/turn state that rides along with the order.
type Encounter struct {
	Entries []InitEntry `json:"entries"`
	Round   int         `json:"round"`
	TurnID  *int64      `json:"turn_id"`
	Running bool        `json:"running"`
}

// Initiative is the DM's full view of the order.
func Initiative(s *store.Store) (Encounter, error) {
	rows, err := s.InitiativeRows()
	if err != nil {
		return Encounter{}, err
	}
	chars, err := s.Characters()
	if err != nil {
		return Encounter{}, err
	}
	sheets := map[int64]map[string]any{}
	for _, c := range chars {
		sheets[c.ID] = c.Sheet
	}

	entries := make([]InitEntry, 0, len(rows))
	for _, row := range rows {
		e := InitEntry{
			ID: row.ID, Name: row.Name, CharacterID: row.CharacterID, Init: row.Init,
			HP: row.HP, HPMax: row.HPMax, AC: row.AC, Conditions: row.Conditions,
			Note: row.Note, Hidden: row.Hidden, Defeated: row.Defeated,
		}
		// A PC's row mirrors the live sheet, so HP has exactly one home.
		if row.CharacterID != nil {
			if sh, ok := sheets[*row.CharacterID]; ok {
				hp := sub(sh, "hp")
				e.HP = ptr(num(hp["current"], 0))
				e.HPMax = ptr(num(hp["max"], 0))
				e.AC = ptr(num(sh["ac"], 10))
				e.Name = str(sh["name"], row.Name)
				e.Conditions = list(sh["conditions"])
			}
		}
		if e.Conditions == nil {
			e.Conditions = []any{}
		}
		entries = append(entries, e)
	}

	enc := Encounter{Entries: entries}
	var meta struct {
		Round   int    `json:"round"`
		TurnID  *int64 `json:"turn_id"`
		Running bool   `json:"running"`
	}
	if ok, _ := s.Meta("encounter", &meta); ok {
		enc.Round, enc.TurnID, enc.Running = meta.Round, meta.TurnID, meta.Running
	}
	return enc, nil
}

// InitiativeForPlayers hides what the party has not been shown: hidden monsters
// become anonymous rows, and even visible monsters give a description of how
// hurt they are rather than exact numbers.
func InitiativeForPlayers(s *store.Store) (Encounter, error) {
	enc, err := Initiative(s)
	if err != nil {
		return enc, err
	}
	visible := make([]InitEntry, 0, len(enc.Entries))
	for _, e := range enc.Entries {
		if e.Hidden {
			visible = append(visible, InitEntry{
				ID: e.ID, Name: "???", Init: e.Init, Conditions: []any{},
				Hidden: true, Defeated: e.Defeated,
			})
			continue
		}
		if e.CharacterID == nil {
			e.AC = nil
			if e.HP != nil && e.HPMax != nil && *e.HPMax != 0 {
				e.Wounds = wounds(float64(*e.HP) / float64(*e.HPMax))
				e.HP, e.HPMax = nil, nil
			}
		}
		visible = append(visible, e)
	}
	enc.Entries = visible
	return enc, nil
}

func wounds(pct float64) string {
	switch {
	case pct >= 1:
		return "unharmed"
	case pct > 0.5:
		return "hurt"
	case pct > 0.25:
		return "bloodied"
	case pct > 0:
		return "near death"
	}
	return "down"
}

// JourneyForPlayers is the trail minus anywhere the DM is still keeping back.
//
// Dropping a hidden place would orphan anything reached through it, so those
// links are re-pointed at the nearest ancestor the players can actually see —
// the graph stays connected without revealing that there is a gap.
func JourneyForPlayers(s *store.Store) map[string]any {
	journey, _ := s.Party("journey").(map[string]any)
	locs := list(journey["locations"])

	byID := map[string]map[string]any{}
	hidden := map[string]bool{}
	for _, raw := range locs {
		loc, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id := str(loc["id"], "")
		byID[id] = loc
		if str(loc["status"], "") == "hidden" {
			hidden[id] = true
		}
	}

	visibleAncestor := func(id string) any {
		seen := map[string]bool{}
		for hidden[id] && !seen[id] {
			seen[id] = true
			id = str(byID[id]["from"], "")
		}
		if _, ok := byID[id]; ok && !hidden[id] {
			return id
		}
		return nil
	}

	out := []any{}
	for _, raw := range locs {
		loc, ok := raw.(map[string]any)
		if !ok || hidden[str(loc["id"], "")] {
			continue
		}
		item := map[string]any{}
		for k, v := range loc {
			item[k] = v
		}
		item["from"] = visibleAncestor(str(loc["from"], ""))
		out = append(out, item)
	}
	return map[string]any{"locations": out}
}

// Party is the shared slices. Players get the journey redacted, only the
// handouts the DM has actually shown them, and no bestiary at all.
func Party(s *store.Store, isDM bool) map[string]any {
	out := map[string]any{}
	for key := range store.PartyDefaults() {
		out[key] = s.Party(key)
	}
	if !isDM {
		// The stat blocks are the DM's preparation. Redacting a monster once it
		// is on the table would be pointless if its whole entry shipped to every
		// phone the moment it was written, so it never leaves this branch.
		delete(out, "bestiary")
		out["journey"] = JourneyForPlayers(s)
		shown := []any{}
		for _, raw := range list(out["handouts"]) {
			if h, ok := raw.(map[string]any); ok && truthy(h["revealed"]) {
				shown = append(shown, h)
			}
		}
		out["handouts"] = shown
	}
	return out
}

// Snapshot is everything a client needs on connect.
func Snapshot(s *store.Store, isDM bool) (map[string]any, error) {
	chars, err := Characters(s)
	if err != nil {
		return nil, err
	}
	var enc Encounter
	if isDM {
		enc, err = Initiative(s)
	} else {
		enc, err = InitiativeForPlayers(s)
	}
	if err != nil {
		return nil, err
	}
	rolls, err := s.Rolls(isDM)
	if err != nil {
		return nil, err
	}
	campaigns := []store.Campaign{}
	if isDM {
		if campaigns, err = s.Campaigns(); err != nil {
			return nil, err
		}
	}
	active, _ := s.ActiveID()
	return map[string]any{
		"campaign":   map[string]any{"name": s.CampaignName(""), "id": active},
		"characters": chars,
		"initiative": enc,
		"rolls":      rolls,
		"party":      Party(s, isDM),
		"campaigns":  campaigns,
	}, nil
}

// ---------------------------------------------------------------- coercion
//
// Sheets are free-form JSON written by clients, so every read has to survive a
// missing key or the wrong type rather than panicking mid-broadcast.

func str(v any, def string) string {
	if s, ok := v.(string); ok {
		return s
	}
	return def
}

func num(v any, def int) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return def
}

func sub(m map[string]any, key string) map[string]any {
	if v, ok := m[key].(map[string]any); ok {
		return v
	}
	return map[string]any{}
}

func list(v any) []any {
	if l, ok := v.([]any); ok {
		return l
	}
	return []any{}
}

func truthy(v any) bool {
	switch b := v.(type) {
	case bool:
		return b
	case float64:
		return b != 0
	case string:
		return b != ""
	}
	return false
}

func ptr(n int) *int { return &n }
