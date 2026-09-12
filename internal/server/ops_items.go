package server

import (
	"errors"
	"fmt"

	"dndcompanion/internal/hub"
	"dndcompanion/internal/store"
)

// The DM's item library: treasure written up ahead of a session and kept with
// the campaign, so the same potion does not have to be typed again for the next
// party that finds one.
//
// The library is preparation, and like the bestiary it never leaves the DM's
// copy of the party state. A player learns what is on the shelf by being handed
// something off it: giving copies the item into the loot list, which is the
// list players already read, so nothing here has to be redacted item by item.
//
// Every op in this file is DM-only.

const maxItems = 400

// An item carries two separate notes, and the split is the point: `notes` is
// handed over with the item and is what a player reads in their pack, while
// `desc` is the DM's own — what the thing really does, who wants it back —
// and stays in the library when a copy is given away.
func normalizeItem(raw any) map[string]any {
	it, _ := raw.(map[string]any)
	if it == nil {
		it = map[string]any{}
	}
	id, _ := it["id"].(string)
	if id == "" {
		id = store.Token(8)
	}
	clamp := func(key string, n int) string {
		s, _ := it[key].(string)
		if len(s) > n {
			s = s[:n]
		}
		return s
	}
	return map[string]any{
		"id": id, "name": clamp("name", 80), "kind": clamp("kind", 60),
		// notes is capped where loot's is: it is copied into a loot entry
		// verbatim, and a longer note would be silently cut there instead.
		"notes": clamp("notes", 200),
		"desc":  clamp("desc", 4000),
		"qty":   max(1, numOf(it["qty"], 1)),
	}
}

func (s *Server) items() []any {
	raw, _ := s.Store.Party("items").([]any)
	out := make([]any, 0, len(raw))
	for _, it := range raw {
		out = append(out, normalizeItem(it))
	}
	return out
}

// findItem returns the entry with this id, and where it sits.
func findItem(list []any, id string) (map[string]any, int) {
	if id == "" {
		return nil, -1
	}
	for i, raw := range list {
		if m, ok := raw.(map[string]any); ok {
			if got, _ := m["id"].(string); got == id {
				return m, i
			}
		}
	}
	return nil, -1
}

// opItemSave writes one item, creating it if its id is new.
//
// The whole entry arrives at once rather than as a patch, for the reason the
// bestiary does the same: it is edited in a dialog that is saved or cancelled
// as a unit, and a half-written item is worse than an unsaved one.
func (s *Server) opItemSave(c *hub.Client, p payload) error {
	incoming := p.sub("item")
	if incoming.clamped("name", "", 80) == "" {
		return errors.New("give the item a name")
	}

	entry := normalizeItem(map[string]any(incoming))
	entry["name"] = incoming.clamped("name", "", 80)

	list := s.items()
	if existing, at := findItem(list, incoming.str("id", "")); existing != nil {
		entry["id"] = existing["id"]
		list[at] = entry
	} else {
		if len(list) >= maxItems {
			return fmt.Errorf("the item library is full at %d items", maxItems)
		}
		list = append(list, entry)
	}

	if err := s.Store.SetParty("items", list); err != nil {
		return err
	}
	return s.pushParty()
}

func (s *Server) opItemRemove(c *hub.Client, p payload) error {
	id := p.str("id", "")
	list := s.items()
	kept := make([]any, 0, len(list))
	for _, raw := range list {
		if m, ok := raw.(map[string]any); ok {
			if got, _ := m["id"].(string); got == id {
				continue
			}
		}
		kept = append(kept, raw)
	}
	if err := s.Store.SetParty("items", kept); err != nil {
		return err
	}
	return s.pushParty()
}

// opItemGive hands a copy of a library item to a character, or into the shared
// pile. The library keeps its entry: the same item can be found twice.
//
// What the player receives is an ordinary loot entry, so it can be dropped,
// passed on and thrown away like anything else they are carrying, and nothing
// in it points back at the library it came from.
func (s *Server) opItemGive(c *hub.Client, p payload) error {
	item, _ := findItem(s.items(), p.str("id", ""))
	if item == nil {
		return errors.New("no such item")
	}
	owner, err := s.validOwner(p["owner"])
	if err != nil {
		return err
	}

	qty := max(1, numOf(item["qty"], 1))
	if p.has("qty") {
		qty = max(1, p.intv("qty", 1))
	}
	name, _ := item["name"].(string)
	notes, _ := item["notes"].(string)

	entry := normalizeLoot(map[string]any{
		"name": name, "qty": float64(qty), "notes": notes,
	})
	// normalizeLoot only understands a float owner; set it back explicitly.
	entry["owner"] = owner
	if err := s.Store.SetParty("loot", append(s.loot(), entry)); err != nil {
		return err
	}

	// Treasure is usually handed over in front of everyone, but not always —
	// a note slipped to one player is a scene too, so the DM says which.
	if p.boolv("announce") {
		s.Hub.Broadcast("toast", map[string]string{
			"kind": "announce", "text": giveLine(name, qty, s.ownerLabel(owner)),
		})
	}
	return s.pushParty()
}

// ownerLabel names who is receiving something, for an announcement.
func (s *Server) ownerLabel(owner any) string {
	id, ok := owner.(int64)
	if !ok {
		return "The party"
	}
	ch, err := s.Store.Character(id)
	if err != nil || ch == nil {
		return "The party"
	}
	if name, _ := ch.Sheet["name"].(string); name != "" {
		return name
	}
	return "The party"
}

func giveLine(name string, qty int, who string) string {
	if qty > 1 {
		return fmt.Sprintf("%s receives %s ×%d", who, name, qty)
	}
	return fmt.Sprintf("%s receives %s", who, name)
}
