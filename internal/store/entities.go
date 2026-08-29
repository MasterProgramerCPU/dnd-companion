package store

import (
	"database/sql"
	"encoding/json"
)

// RollLogLimit is how many rolls the log keeps on screen.
const RollLogLimit = 60

// Character is one row of the characters table, with its sheet decoded.
type Character struct {
	ID        int64          `json:"id"`
	Name      string         `json:"name"`
	Sheet     map[string]any `json:"sheet"`
	SortIdx   int            `json:"sort_idx"`
	UpdatedAt string         `json:"updated_at"`
}

// Characters lists the party in display order.
func (s *Store) Characters() ([]Character, error) {
	rows, err := s.db().Query("SELECT id,name,sheet,sort_idx,updated_at FROM characters ORDER BY sort_idx, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Character{}
	for rows.Next() {
		var c Character
		var raw string
		if err := rows.Scan(&c.ID, &c.Name, &raw, &c.SortIdx, &c.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &c.Sheet); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Character fetches one character.
func (s *Store) Character(id int64) (*Character, error) {
	var c Character
	var raw string
	err := s.db().QueryRow(
		"SELECT id,name,sheet,sort_idx,updated_at FROM characters WHERE id=?", id).
		Scan(&c.ID, &c.Name, &raw, &c.SortIdx, &c.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, json.Unmarshal([]byte(raw), &c.Sheet)
}

// CreateCharacter adds a character and returns its id.
func (s *Store) CreateCharacter(name string, sheet map[string]any) (int64, error) {
	raw, err := json.Marshal(sheet)
	if err != nil {
		return 0, err
	}
	var next int
	// A new character sorts after everyone already at the table.
	s.db().QueryRow("SELECT COALESCE(MAX(sort_idx)+1, 0) FROM characters").Scan(&next)
	res, err := s.db().Exec(
		"INSERT INTO characters(name,sheet,sort_idx,updated_at) VALUES(?,?,?,?)",
		name, string(raw), next, Now())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// SaveCharacter writes a character's sheet back.
func (s *Store) SaveCharacter(id int64, name string, sheet map[string]any) error {
	raw, err := json.Marshal(sheet)
	if err != nil {
		return err
	}
	_, err = s.db().Exec("UPDATE characters SET name=?, sheet=?, updated_at=? WHERE id=?",
		name, string(raw), Now(), id)
	return err
}

// DeleteCharacter removes a character and detaches anything pointing at them.
func (s *Store) DeleteCharacter(id int64) error {
	for _, q := range []string{
		"DELETE FROM characters WHERE id=?",
		"DELETE FROM initiative WHERE character_id=?",
		"UPDATE devices SET character_id=NULL WHERE character_id=?",
	} {
		if _, err := s.db().Exec(q, id); err != nil {
			return err
		}
	}
	return nil
}

// ClaimedCharacterIDs are the characters some device has already taken.
func (s *Store) ClaimedCharacterIDs() (map[int64]bool, error) {
	rows, err := s.db().Query("SELECT DISTINCT character_id FROM devices WHERE character_id IS NOT NULL")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// Device is a phone or browser that has joined.
type Device struct {
	Token       string `json:"token"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	CharacterID *int64 `json:"character_id"`
	CreatedAt   string `json:"created_at"`
	LastSeen    string `json:"last_seen"`
}

// Device looks a device up by its token.
func (s *Store) Device(token string) (*Device, error) {
	if token == "" {
		return nil, nil
	}
	var d Device
	err := s.db().QueryRow(
		"SELECT token,display_name,role,character_id,created_at,last_seen FROM devices WHERE token=?",
		token).Scan(&d.Token, &d.DisplayName, &d.Role, &d.CharacterID, &d.CreatedAt, &d.LastSeen)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// SaveDevice inserts or updates a device.
func (s *Store) SaveDevice(d *Device) error {
	_, err := s.db().Exec(`
		INSERT INTO devices(token,display_name,role,character_id,created_at,last_seen)
		VALUES(?,?,?,?,?,?)
		ON CONFLICT(token) DO UPDATE SET
			display_name=excluded.display_name, role=excluded.role,
			character_id=excluded.character_id, last_seen=excluded.last_seen`,
		d.Token, d.DisplayName, d.Role, d.CharacterID, d.CreatedAt, d.LastSeen)
	return err
}

// Roll is one entry of the shared roll log.
type Roll struct {
	ID          int64   `json:"id"`
	TS          string  `json:"ts"`
	Actor       string  `json:"actor"`
	CharacterID *int64  `json:"character_id"`
	Label       string  `json:"label"`
	Formula     string  `json:"formula"`
	Total       int     `json:"total"`
	Detail      string  `json:"detail"`
	Crit        *string `json:"crit"`
	Secret      bool    `json:"secret"`
	Terms       any     `json:"terms"`
	ByDevice    string  `json:"by_device"`
}

// AddRoll appends to the log and returns the stored row.
func (s *Store) AddRoll(r *Roll) (*Roll, error) {
	terms, err := json.Marshal(r.Terms)
	if err != nil {
		return nil, err
	}
	r.TS = Now()
	res, err := s.db().Exec(`
		INSERT INTO rolls(ts,actor,character_id,label,formula,total,detail,crit,secret,terms,by_device)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		r.TS, r.Actor, r.CharacterID, r.Label, r.Formula, r.Total, r.Detail, r.Crit,
		boolToInt(r.Secret), string(terms), r.ByDevice)
	if err != nil {
		return nil, err
	}
	r.ID, err = res.LastInsertId()
	return r, err
}

// Rolls returns the tail of the log, oldest first.
func (s *Store) Rolls(includeSecret bool) ([]Roll, error) {
	where := "WHERE secret=0"
	if includeSecret {
		where = ""
	}
	rows, err := s.db().Query(
		"SELECT id,ts,actor,character_id,label,formula,total,detail,crit,secret,terms,by_device "+
			"FROM rolls "+where+" ORDER BY id DESC LIMIT ?", RollLogLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Roll{}
	for rows.Next() {
		var r Roll
		var secret int
		var terms string
		if err := rows.Scan(&r.ID, &r.TS, &r.Actor, &r.CharacterID, &r.Label, &r.Formula,
			&r.Total, &r.Detail, &r.Crit, &secret, &terms, &r.ByDevice); err != nil {
			return nil, err
		}
		r.Secret = secret != 0
		if terms == "" {
			terms = "[]"
		}
		if err := json.Unmarshal([]byte(terms), &r.Terms); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Queried newest-first to honour the LIMIT, shown oldest-first.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// InitRow is one raw row of the initiative table.
type InitRow struct {
	ID          int64
	Name        string
	CharacterID *int64
	Init        float64
	HP          *int
	HPMax       *int
	AC          *int
	Conditions  []any
	Note        string
	Hidden      bool
	Defeated    bool
}

// InitiativeRows lists the order as stored, highest initiative first.
func (s *Store) InitiativeRows() ([]InitRow, error) {
	rows, err := s.db().Query(
		"SELECT id,name,character_id,init,hp,hp_max,ac,conditions,note,hidden,defeated " +
			"FROM initiative ORDER BY init DESC, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []InitRow{}
	for rows.Next() {
		var e InitRow
		var conditions string
		var hidden, defeated int
		if err := rows.Scan(&e.ID, &e.Name, &e.CharacterID, &e.Init, &e.HP, &e.HPMax, &e.AC,
			&conditions, &e.Note, &hidden, &defeated); err != nil {
			return nil, err
		}
		e.Hidden, e.Defeated = hidden != 0, defeated != 0
		if conditions == "" {
			conditions = "[]"
		}
		if err := json.Unmarshal([]byte(conditions), &e.Conditions); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Exec runs a statement against the open campaign.
func (s *Store) Exec(query string, args ...any) (sql.Result, error) {
	return s.db().Exec(query, args...)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
