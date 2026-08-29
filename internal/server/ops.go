package server

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"dndcompanion/internal/dice"
	"dndcompanion/internal/hub"
	"dndcompanion/internal/sheet"
	"dndcompanion/internal/state"
	"dndcompanion/internal/store"
)

// payload is the free-form data half of a client message. Clients are not
// trusted, so every read coerces rather than asserts.
type payload map[string]any

func (p payload) has(key string) bool { _, ok := p[key]; return ok }

func (p payload) str(key, def string) string {
	if s, ok := p[key].(string); ok {
		return s
	}
	return def
}

func (p payload) clamped(key, def string, n int) string {
	s := strings.TrimSpace(p.str(key, def))
	if len(s) > n {
		s = s[:n]
	}
	return s
}

func (p payload) num(key string, def float64) float64 {
	switch v := p[key].(type) {
	case float64:
		return v
	case bool:
		if v {
			return 1
		}
		return 0
	}
	return def
}

func (p payload) intv(key string, def int) int { return int(p.num(key, float64(def))) }

func (p payload) boolv(key string) bool {
	switch v := p[key].(type) {
	case bool:
		return v
	case float64:
		return v != 0
	case string:
		return v != ""
	}
	return false
}

func (p payload) sub(key string) payload {
	if m, ok := p[key].(map[string]any); ok {
		return payload(m)
	}
	return payload{}
}

func (p payload) list(key string) []any {
	if l, ok := p[key].([]any); ok {
		return l
	}
	return nil
}

// ---------------------------------------------------------------- pushes

func (s *Server) pushCharacters() error {
	chars, err := state.Characters(s.Store)
	if err != nil {
		return err
	}
	s.Hub.Broadcast("characters", chars)
	return nil
}

func (s *Server) pushRoster() error {
	roster, err := state.Roster(s.Store)
	if err != nil {
		return err
	}
	s.Hub.Broadcast("roster", roster)
	return nil
}

func (s *Server) pushInitiative() error {
	dm, err := state.Initiative(s.Store)
	if err != nil {
		return err
	}
	players, err := state.InitiativeForPlayers(s.Store)
	if err != nil {
		return err
	}
	s.Hub.BroadcastSplit("initiative", dm, players)
	return nil
}

func (s *Server) pushParty() error {
	s.Hub.BroadcastSplit("party", state.Party(s.Store, true), state.Party(s.Store, false))
	return nil
}

func (s *Server) pushCampaigns() error {
	campaigns, err := s.Store.Campaigns()
	if err != nil {
		return err
	}
	s.Hub.ToDMs("campaigns", campaigns)
	return nil
}

// ---------------------------------------------------------------- sheets

func (s *Server) loadSheet(id int64) (map[string]any, string, error) {
	ch, err := s.Store.Character(id)
	if err != nil {
		return nil, "", err
	}
	if ch == nil {
		return nil, "", errors.New("no such character")
	}
	return ch.Sheet, ch.Name, nil
}

func (s *Server) saveSheet(id int64, sh map[string]any) error {
	name, _ := sh["name"].(string)
	if name == "" {
		name = "Unnamed"
	}
	return s.Store.SaveCharacter(id, name, sh)
}

// deepMerge applies a patch in place, descending into nested objects rather
// than replacing them wholesale — a client patching hp.current must not wipe
// hp.max.
func deepMerge(base, patch map[string]any) {
	for key, value := range patch {
		sub, isMap := value.(map[string]any)
		existing, wasMap := base[key].(map[string]any)
		if isMap && wasMap {
			deepMerge(existing, sub)
			continue
		}
		base[key] = value
	}
}

func numOf(v any, def int) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return def
}

// clampHP keeps hit points inside their own bounds: max is never negative,
// temp is never negative, and current sits between -max (dead) and max.
func clampHP(sh map[string]any) {
	hp, ok := sh["hp"].(map[string]any)
	if !ok {
		hp = map[string]any{}
		sh["hp"] = hp
	}
	maxHP := max(0, numOf(hp["max"], 0))
	temp := max(0, numOf(hp["temp"], 0))
	cur := numOf(hp["current"], 0)
	cur = min(cur, maxHP)
	cur = max(cur, -maxHP)
	hp["max"], hp["temp"], hp["current"] = maxHP, temp, cur
}

// ---------------------------------------------------------------- rolls

type rollMeta struct {
	label         string
	secret        bool
	actor         string
	setInitiative bool
	advantage     int
	formula       string
}

func (s *Server) rollMetaFrom(c *hub.Client, p payload) rollMeta {
	actor := p.clamped("actor", "", 60)
	if actor == "" {
		actor = c.Name
	}
	return rollMeta{
		label:         p.clamped("label", "", 60),
		secret:        p.boolv("secret") && c.IsDM(),
		actor:         actor,
		setInitiative: p.boolv("set_initiative"),
		advantage:     p.intv("advantage", 0),
		formula:       strings.TrimSpace(p.str("formula", "")),
	}
}

func (s *Server) recordRoll(c *hub.Client, meta rollMeta, result *dice.Result) error {
	var crit *string
	if v := result.Crit(); v != "" {
		crit = &v
	}
	row, err := s.Store.AddRoll(&store.Roll{
		Actor: meta.actor, CharacterID: c.CharacterID, Label: meta.label,
		Formula: result.Formula, Total: result.Total, Detail: result.Detail,
		Crit: crit, Secret: meta.secret, Terms: result.Breakdown(),
		ByDevice: DeviceID(c.Token),
	})
	if err != nil {
		return err
	}
	if row.Secret {
		s.Hub.ToDMs("roll", row)
	} else {
		s.Hub.Broadcast("roll", row)
	}
	if meta.setInitiative && c.CharacterID != nil {
		return s.opInitSelf(c, payload{"init": float64(result.Total)})
	}
	return nil
}

// opRoll rolls entirely on the server.
func (s *Server) opRoll(c *hub.Client, p payload) error {
	meta := s.rollMetaFrom(c, p)
	result, err := dice.Roll(meta.formula, meta.advantage)
	if err != nil {
		return err
	}
	return s.recordRoll(c, meta, result)
}

func (s *Server) prunePending() {
	cutoff := time.Now().Add(-pendingTTL)
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	for id, pr := range s.pending {
		if pr.ts.Before(cutoff) {
			delete(s.pending, id)
		}
	}
}

// opRollPlan tells the client which dice to physically throw for this formula.
func (s *Server) opRollPlan(c *hub.Client, p payload) error {
	s.prunePending()
	meta := s.rollMetaFrom(c, p)
	specs, err := dice.Parse(meta.formula, meta.advantage)
	if err != nil {
		return err
	}
	id := store.Token(9)
	s.pendingMu.Lock()
	s.pending[id] = pendingRoll{token: c.Token, specs: specs, meta: meta, ts: time.Now()}
	s.pendingMu.Unlock()
	s.Hub.Send(c, "roll.plan", map[string]any{"id": id, "dice": dice.Plan(specs)})
	return nil
}

// opRollCommit takes the faces the client's dice landed on and does the
// arithmetic here. A roll can only be cashed in once, and only by the device
// that asked for it.
func (s *Server) opRollCommit(c *hub.Client, p payload) error {
	s.prunePending()
	id := p.str("id", "")

	s.pendingMu.Lock()
	pending, ok := s.pending[id]
	if ok {
		delete(s.pending, id)
	}
	s.pendingMu.Unlock()

	if !ok || pending.token != c.Token {
		return errors.New("that roll expired — try again")
	}

	raw := p.list("values")
	if len(raw) > 32 {
		raw = raw[:32]
	}
	values := make([][]int, 0, len(raw))
	for _, term := range raw {
		items, _ := term.([]any)
		if len(items) > dice.MaxDice {
			items = items[:dice.MaxDice]
		}
		bucket := make([]int, 0, len(items))
		for _, v := range items {
			bucket = append(bucket, numOf(v, 0))
		}
		values = append(values, bucket)
	}

	result, err := dice.Evaluate(pending.specs, pending.meta.formula, dice.NewSupplied(values))
	if err != nil {
		return err
	}
	return s.recordRoll(c, pending.meta, result)
}

// ---------------------------------------------------------------- characters

func (s *Server) opCharPatch(c *hub.Client, p payload) error {
	id := int64(p.intv("id", 0))
	if !c.IsDM() && (c.CharacterID == nil || *c.CharacterID != id) {
		return errors.New("that isn't your character")
	}
	sh, _, err := s.loadSheet(id)
	if err != nil {
		return err
	}
	deepMerge(sh, p.sub("patch"))
	clampHP(sh)
	if err := s.saveSheet(id, sh); err != nil {
		return err
	}
	if err := s.pushCharacters(); err != nil {
		return err
	}
	if err := s.pushInitiative(); err != nil {
		return err
	}
	return s.pushRoster()
}

func (s *Server) opCharHP(c *hub.Client, p payload) error {
	id := int64(p.intv("id", 0))
	if !c.IsDM() && (c.CharacterID == nil || *c.CharacterID != id) {
		return nil
	}
	sh, _, err := s.loadSheet(id)
	if err != nil {
		return err
	}
	hp, _ := sh["hp"].(map[string]any)
	if hp == nil {
		hp = map[string]any{}
		sh["hp"] = hp
	}

	delta := p.intv("delta", 0)
	if delta < 0 { // damage eats temp HP first
		absorbed := min(numOf(hp["temp"], 0), -delta)
		hp["temp"] = numOf(hp["temp"], 0) - absorbed
		delta += absorbed
	}
	hp["current"] = numOf(hp["current"], 0) + delta
	if p.has("set") {
		hp["current"] = p.intv("set", 0)
	}
	if p.has("temp") {
		hp["temp"] = p.intv("temp", 0)
	}
	if numOf(hp["current"], 0) > 0 {
		sh["death_saves"] = map[string]any{"successes": 0, "failures": 0}
	}
	clampHP(sh)
	if err := s.saveSheet(id, sh); err != nil {
		return err
	}
	if err := s.pushCharacters(); err != nil {
		return err
	}
	return s.pushInitiative()
}

func (s *Server) opCharCreate(c *hub.Client, p payload) error {
	name := p.clamped("name", "New Adventurer", 60)
	sh := sheet.Default(name, p.clamped("player", "", 60))
	for _, key := range []string{"klass", "race"} {
		if v := p.clamped(key, "", 60); v != "" {
			sh[key] = v
		}
	}
	sh["level"] = max(1, min(p.intv("level", 1), 20))
	if v := p.clamped("color", "", 16); v != "" {
		sh["color"] = v
	}
	if _, err := s.Store.CreateCharacter(name, sh); err != nil {
		return err
	}
	if err := s.pushCharacters(); err != nil {
		return err
	}
	return s.pushRoster()
}

func (s *Server) opCharDelete(c *hub.Client, p payload) error {
	if err := s.Store.DeleteCharacter(int64(p.intv("id", 0))); err != nil {
		return err
	}
	if err := s.pushCharacters(); err != nil {
		return err
	}
	if err := s.pushInitiative(); err != nil {
		return err
	}
	return s.pushRoster()
}

// ---------------------------------------------------------------- initiative

func (s *Server) opInitAdd(c *hub.Client, p payload) error {
	count := max(1, min(p.intv("count", 1), 20))
	base := p.clamped("name", "Monster", 60)
	for i := 0; i < count; i++ {
		name := base
		if count > 1 {
			name = fmt.Sprintf("%s %d", base, i+1)
		}
		var init float64
		if v, ok := p["init"].(float64); ok {
			init = v
		} else if str, ok := p["init"].(string); !ok || str == "" {
			rolled, err := dice.Roll(fmt.Sprintf("1d20+%d", p.intv("init_mod", 0)), 0)
			if err != nil {
				return err
			}
			init = float64(rolled.Total)
		}
		// A hair of separation keeps a batch in the order it was added when
		// initiative ties, instead of leaving it to the id ordering.
		init += 0.001 * float64(count-i)

		var hp, ac *int
		if v, ok := p["hp"].(float64); ok {
			n := int(v)
			hp = &n
		}
		if v, ok := p["ac"].(float64); ok {
			n := int(v)
			ac = &n
		}
		hidden := 0
		if p.boolv("hidden") {
			hidden = 1
		}
		if _, err := s.Store.Exec(
			"INSERT INTO initiative(name,character_id,init,hp,hp_max,ac,conditions,note,hidden)"+
				" VALUES(?,NULL,?,?,?,?,'[]',?,?)",
			name, init, hp, hp, ac, p.clamped("note", "", 200), hidden); err != nil {
			return err
		}
	}
	return s.pushInitiative()
}

func (s *Server) opInitAddParty(c *hub.Client, p payload) error {
	rows, err := s.Store.InitiativeRows()
	if err != nil {
		return err
	}
	existing := map[int64]bool{}
	for _, r := range rows {
		if r.CharacterID != nil {
			existing[*r.CharacterID] = true
		}
	}
	chars, err := state.Characters(s.Store)
	if err != nil {
		return err
	}
	for _, ch := range chars {
		if existing[ch.ID] {
			continue
		}
		init := 0.0
		if p.boolv("roll") {
			rolled, err := dice.Roll(fmt.Sprintf("1d20+%d", ch.Derived.Initiative), 0)
			if err != nil {
				return err
			}
			init = float64(rolled.Total)
		}
		ac := numOf(ch.Sheet["ac"], 10)
		if _, err := s.Store.Exec(
			"INSERT INTO initiative(name,character_id,init,ac,conditions) VALUES(?,?,?,?,'[]')",
			ch.Name, ch.ID, init, ac); err != nil {
			return err
		}
	}
	return s.pushInitiative()
}

func (s *Server) opInitRemove(c *hub.Client, p payload) error {
	if _, err := s.Store.Exec("DELETE FROM initiative WHERE id=?", p.intv("id", 0)); err != nil {
		return err
	}
	return s.pushInitiative()
}

func (s *Server) opInitClear(c *hub.Client, p payload) error {
	if _, err := s.Store.Exec("DELETE FROM initiative"); err != nil {
		return err
	}
	if err := s.Store.SetMeta("encounter",
		map[string]any{"round": 0, "turn_id": nil, "running": false}); err != nil {
		return err
	}
	return s.pushInitiative()
}

// opInitSelf lets a player set initiative on their own row only.
func (s *Server) opInitSelf(c *hub.Client, p payload) error {
	if c.CharacterID == nil {
		return nil
	}
	value := p.num("init", 0)
	rows, err := s.Store.InitiativeRows()
	if err != nil {
		return err
	}
	for _, r := range rows {
		if r.CharacterID != nil && *r.CharacterID == *c.CharacterID {
			if _, err := s.Store.Exec("UPDATE initiative SET init=? WHERE id=?", value, r.ID); err != nil {
				return err
			}
			return s.pushInitiative()
		}
	}
	sh, name, err := s.loadSheet(*c.CharacterID)
	if err != nil {
		return err
	}
	if _, err := s.Store.Exec(
		"INSERT INTO initiative(name,character_id,init,ac,conditions) VALUES(?,?,?,?,'[]')",
		name, *c.CharacterID, value, numOf(sh["ac"], 10)); err != nil {
		return err
	}
	return s.pushInitiative()
}
