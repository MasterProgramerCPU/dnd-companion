package server

import (
	"errors"
	"fmt"

	"dndcompanion/internal/dice"
	"dndcompanion/internal/hub"
	"dndcompanion/internal/sheet"
	"dndcompanion/internal/store"
)

// The DM's own creatures: stat blocks kept with the campaign and dropped into
// the initiative order when they are needed.
//
// A stat block is stored as the same free-form map a character sheet is, and is
// derived by the same code, so a monster's saves and skills are worked out the
// way a player's are. Every op here is DM-only, and state.Party never sends the
// list to a player.

const (
	maxBestiary   = 300
	maxBatchAdded = 20
)

// isTrue reads a JSON boolean that a client may have sent as a number.
func isTrue(v any) bool {
	switch b := v.(type) {
	case bool:
		return b
	case float64:
		return b != 0
	}
	return false
}

func (s *Server) bestiary() []any {
	list, _ := s.Store.Party("bestiary").([]any)
	return list
}

// findCreature returns the stat block with this id, and where it sits.
func findCreature(list []any, id string) (map[string]any, int) {
	for i, raw := range list {
		if m, ok := raw.(map[string]any); ok {
			if got, _ := m["id"].(string); got == id && id != "" {
				return m, i
			}
		}
	}
	return nil, -1
}

// opBestiarySave writes one stat block, creating it if its id is new.
//
// The whole block arrives at once rather than as a patch: it is edited in a
// dialog that is saved or cancelled as a unit, and a partial write would leave
// a creature half-updated if the connection dropped mid-edit.
func (s *Server) opBestiarySave(c *hub.Client, p payload) error {
	incoming := p.sub("creature")
	name := incoming.clamped("name", "", 60)
	if name == "" {
		return errors.New("give the creature a name")
	}

	block := sheet.Monster(name)
	for key, v := range incoming {
		switch key {
		case "id": // assigned here, never taken from the client
		default:
			block[key] = v
		}
	}
	block["name"] = name

	list := s.bestiary()
	if existing, at := findCreature(list, incoming.str("id", "")); existing != nil {
		block["id"] = existing["id"]
		list[at] = block
	} else {
		if len(list) >= maxBestiary {
			return fmt.Errorf("the bestiary is full at %d creatures", maxBestiary)
		}
		block["id"] = store.Token(8)
		list = append(list, block)
	}

	if err := s.Store.SetParty("bestiary", list); err != nil {
		return err
	}
	return s.pushParty()
}

func (s *Server) opBestiaryRemove(c *hub.Client, p payload) error {
	id := p.str("id", "")
	list := s.bestiary()
	kept := make([]any, 0, len(list))
	for _, raw := range list {
		if m, ok := raw.(map[string]any); ok {
			if got, _ := m["id"].(string); got == id {
				continue
			}
		}
		kept = append(kept, raw)
	}
	if err := s.Store.SetParty("bestiary", kept); err != nil {
		return err
	}
	return s.pushParty()
}

// rollHP gives one creature its hit points: its own roll of the stat block's
// formula if it has one, so six goblins are six different goblins, otherwise
// the flat number the block states.
func rollHP(block map[string]any) int {
	if formula, _ := block["hp_formula"].(string); formula != "" {
		if rolled, err := dice.Roll(formula, 0); err == nil {
			return max(1, rolled.Total)
		}
	}
	return max(0, numOf(block["hp_max"], 0))
}

// opInitAddBestiary drops a batch of one creature into the initiative order.
func (s *Server) opInitAddBestiary(c *hub.Client, p payload) error {
	block, _ := findCreature(s.bestiary(), p.str("id", ""))
	if block == nil {
		return errors.New("no such creature")
	}

	count := max(1, min(p.intv("count", 1), maxBatchAdded))
	base := p.clamped("name", "", 60)
	if base == "" {
		base, _ = block["name"].(string)
	}
	derived := sheet.Derive(block)

	// The stat block's own answer, unless the DM overrode it for this encounter.
	hidden := 0
	if p.has("hidden") {
		if p.boolv("hidden") {
			hidden = 1
		}
	} else if isTrue(block["hidden"]) {
		hidden = 1
	}
	ac := numOf(block["ac"], 10)
	note, _ := block["kind"].(string)

	// One initiative roll for the batch keeps a mob acting together, which is
	// how a stat block's own group is usually run; rolling each separately is
	// what the plain "add monsters" button already does.
	shared := p.boolv("shared_init")
	var sharedRoll float64
	if shared {
		rolled, err := dice.Roll(fmt.Sprintf("1d20%+d", derived.Initiative), 0)
		if err != nil {
			return err
		}
		sharedRoll = float64(rolled.Total)
	}

	for i := 0; i < count; i++ {
		name := base
		if count > 1 {
			name = fmt.Sprintf("%s %d", base, i+1)
		}
		init := sharedRoll
		if !shared {
			rolled, err := dice.Roll(fmt.Sprintf("1d20%+d", derived.Initiative), 0)
			if err != nil {
				return err
			}
			init = float64(rolled.Total)
		}
		// The same hair of separation opInitAdd uses, so a batch keeps the
		// order it was added in when initiative ties.
		init += 0.001 * float64(count-i)

		hp := rollHP(block)
		if _, err := s.Store.Exec(
			"INSERT INTO initiative(name,character_id,init,hp,hp_max,ac,conditions,note,hidden)"+
				" VALUES(?,NULL,?,?,?,?,'[]',?,?)",
			name, init, hp, hp, ac, note, hidden); err != nil {
			return err
		}
	}
	return s.pushInitiative()
}
