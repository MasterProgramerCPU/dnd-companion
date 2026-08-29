package server

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"dndcompanion/internal/hub"
	"dndcompanion/internal/state"
	"dndcompanion/internal/store"
)

// journeyStatuses are the visibility levels a place on the map can have.
var journeyStatuses = map[string]bool{
	"visited": true, "current": true, "rumored": true, "hidden": true,
}

// playerSafePartyKeys are the shared slices players may write directly. Loot is
// deliberately absent: it has its own ops so the rules about who can own what
// are enforced here rather than trusted to a client's bulk write.
var playerSafePartyKeys = map[string]bool{"gold": true, "notes": true}

// ---------------------------------------------------------------- init turns

func (s *Server) opInitUpdate(c *hub.Client, p payload) error {
	id := int64(p.intv("id", 0))
	rows, err := s.Store.InitiativeRows()
	if err != nil {
		return err
	}
	var row *store.InitRow
	for i := range rows {
		if rows[i].ID == id {
			row = &rows[i]
			break
		}
	}
	if row == nil {
		return nil
	}
	patch := p.sub("patch")

	// A PC's hit points live on the sheet, not on the initiative row, so edits
	// to a player's row are written through to the character.
	touchesSheet := patch.has("hp") || patch.has("hp_delta") || patch.has("conditions")
	if row.CharacterID != nil && touchesSheet {
		sh, _, err := s.loadSheet(*row.CharacterID)
		if err != nil {
			return err
		}
		hp, _ := sh["hp"].(map[string]any)
		if hp == nil {
			hp = map[string]any{}
			sh["hp"] = hp
		}
		if patch.has("hp_delta") {
			delta := patch.intv("hp_delta", 0)
			if delta < 0 {
				absorbed := min(numOf(hp["temp"], 0), -delta)
				hp["temp"] = numOf(hp["temp"], 0) - absorbed
				delta += absorbed
			}
			hp["current"] = numOf(hp["current"], 0) + delta
		}
		if patch.has("hp") {
			hp["current"] = patch.intv("hp", 0)
		}
		if patch.has("conditions") {
			sh["conditions"] = patch["conditions"]
		}
		clampHP(sh)
		if err := s.saveSheet(*row.CharacterID, sh); err != nil {
			return err
		}
		if err := s.pushCharacters(); err != nil {
			return err
		}
	}

	var sets []string
	var args []any
	for _, key := range []string{"name", "init", "hp", "hp_max", "ac", "note", "hidden", "defeated"} {
		if !patch.has(key) {
			continue
		}
		if row.CharacterID != nil && key == "hp" {
			continue // the sheet owns it
		}
		sets = append(sets, key+"=?")
		switch key {
		case "hidden", "defeated":
			args = append(args, boolToInt(patch.boolv(key)))
		case "init":
			args = append(args, patch.num(key, 0))
		default:
			args = append(args, patch[key])
		}
	}
	if patch.has("hp_delta") && row.CharacterID == nil && row.HP != nil {
		sets = append(sets, "hp=?")
		args = append(args, max(0, *row.HP+patch.intv("hp_delta", 0)))
	}
	if patch.has("conditions") && row.CharacterID == nil {
		raw, err := json.Marshal(patch["conditions"])
		if err != nil {
			return err
		}
		sets = append(sets, "conditions=?")
		args = append(args, string(raw))
	}
	if len(sets) > 0 {
		args = append(args, row.ID)
		if _, err := s.Store.Exec(
			"UPDATE initiative SET "+strings.Join(sets, ", ")+" WHERE id=?", args...); err != nil {
			return err
		}
	}
	return s.pushInitiative()
}

func (s *Server) opInitTurn(c *hub.Client, p payload) error {
	enc, err := state.Initiative(s.Store)
	if err != nil {
		return err
	}
	var order []int64
	for _, e := range enc.Entries {
		if !e.Defeated {
			order = append(order, e.ID)
		}
	}

	round, turnID, running := enc.Round, enc.TurnID, enc.Running
	switch action := p.str("action", "next"); action {
	case "start":
		round, running = 1, true
		turnID = nil
		if len(order) > 0 {
			turnID = &order[0]
		}
	case "stop":
		round, turnID, running = 0, nil, false
	default:
		if len(order) == 0 {
			break
		}
		idx := -1
		if turnID != nil {
			for i, id := range order {
				if id == *turnID {
					idx = i
					break
				}
			}
		}
		step := 1
		if action != "next" {
			step = -1
			if idx < 0 {
				idx = 0
			}
		}
		next := idx + step
		switch {
		case next >= len(order):
			next, round = 0, round+1
		case next < 0:
			next, round = len(order)-1, max(1, round-1)
		}
		turnID = &order[next]
		running = true
	}

	if err := s.Store.SetMeta("encounter", map[string]any{
		"round": round, "turn_id": turnID, "running": running,
	}); err != nil {
		return err
	}
	return s.pushInitiative()
}

// ---------------------------------------------------------------- party

func (s *Server) opPartySet(c *hub.Client, p payload) error {
	key := p.str("key", "")
	if _, ok := store.PartyDefaults()[key]; !ok {
		return nil
	}
	if err := s.Store.SetParty(key, p["value"]); err != nil {
		return err
	}
	return s.pushParty()
}

// opPartySetPlayer lets players share the treasury and session notes; quests
// and NPCs stay DM-side.
func (s *Server) opPartySetPlayer(c *hub.Client, p payload) error {
	if c.IsDM() {
		return s.opPartySet(c, p)
	}
	if !playerSafePartyKeys[p.str("key", "")] {
		return errors.New("the DM keeps that one")
	}
	return s.opPartySet(c, p)
}

func (s *Server) opAnnounce(c *hub.Client, p payload) error {
	s.Hub.Broadcast("toast", map[string]string{
		"kind": "announce", "text": p.clamped("text", "", 400),
	})
	return nil
}

// ---------------------------------------------------------------- journey

func (s *Server) journey() (map[string]any, []any) {
	j, _ := s.Store.Party("journey").(map[string]any)
	if j == nil {
		j = map[string]any{"locations": []any{}}
	}
	locs, _ := j["locations"].([]any)
	if locs == nil {
		locs = []any{}
	}
	return j, locs
}

func locID(v any) string {
	if m, ok := v.(map[string]any); ok {
		if id, ok := m["id"].(string); ok {
			return id
		}
	}
	return ""
}

// validParent enforces that a place is reached from another place — never from
// itself, and never in a loop, or the graph could not be drawn.
func validParent(locs []any, parent string, selfID string) any {
	if parent == "" {
		return nil
	}
	byID := map[string]map[string]any{}
	for _, raw := range locs {
		if m, ok := raw.(map[string]any); ok {
			byID[locID(raw)] = m
		}
	}
	if _, ok := byID[parent]; !ok || parent == selfID {
		return nil
	}
	seen := map[string]bool{}
	walk := parent
	for walk != "" && !seen[walk] {
		if walk == selfID {
			return nil // would close a cycle
		}
		seen[walk] = true
		next, _ := byID[walk]["from"].(string)
		walk = next
	}
	return parent
}

func (s *Server) opJourneyAdd(c *hub.Client, p payload) error {
	j, locs := s.journey()

	// By default a new place follows on from the last one, making a straight road.
	parent := ""
	if p.has("from") {
		parent = p.str("from", "")
	} else if len(locs) > 0 {
		parent = locID(locs[len(locs)-1])
	}
	status := p.str("status", "visited")
	if !journeyStatuses[status] {
		status = "visited"
	}
	locs = append(locs, map[string]any{
		"id":     store.Token(8),
		"name":   p.clamped("name", "New place", 80),
		"body":   p.clamped("body", "", 2000),
		"status": status,
		"from":   validParent(locs, parent, ""),
	})
	j["locations"] = locs
	if err := s.Store.SetParty("journey", j); err != nil {
		return err
	}
	return s.pushParty()
}

func (s *Server) opJourneyUpdate(c *hub.Client, p payload) error {
	j, locs := s.journey()
	patch := p.sub("patch")
	target := p.str("id", "")
	for _, raw := range locs {
		loc, ok := raw.(map[string]any)
		if !ok || locID(raw) != target {
			continue
		}
		for _, key := range []string{"name", "body"} {
			if patch.has(key) {
				loc[key] = patch.clamped(key, "", 2000)
			}
		}
		if st := patch.str("status", ""); journeyStatuses[st] {
			loc["status"] = st
		}
		if patch.has("from") {
			loc["from"] = validParent(locs, patch.str("from", ""), target)
		}
		break
	}
	j["locations"] = locs
	if err := s.Store.SetParty("journey", j); err != nil {
		return err
	}
	return s.pushParty()
}

// opJourneyMove reorders the trail — the array order is the order they travelled.
func (s *Server) opJourneyMove(c *hub.Client, p payload) error {
	j, locs := s.journey()
	target := p.str("id", "")
	idx := -1
	for i, raw := range locs {
		if locID(raw) == target {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil
	}
	dest := max(0, min(idx+p.intv("by", 0), len(locs)-1))
	item := locs[idx]
	locs = append(locs[:idx], locs[idx+1:]...)
	locs = append(locs[:dest], append([]any{item}, locs[dest:]...)...)
	j["locations"] = locs
	if err := s.Store.SetParty("journey", j); err != nil {
		return err
	}
	return s.pushParty()
}

func (s *Server) opJourneyRemove(c *hub.Client, p payload) error {
	j, locs := s.journey()
	gone := p.str("id", "")

	var parent any
	for _, raw := range locs {
		if locID(raw) == gone {
			parent = raw.(map[string]any)["from"]
			break
		}
	}
	kept := []any{}
	for _, raw := range locs {
		if locID(raw) == gone {
			continue
		}
		// Reattach its children further up, so the chain never breaks.
		if loc, ok := raw.(map[string]any); ok {
			if from, _ := loc["from"].(string); from == gone {
				loc["from"] = parent
			}
		}
		kept = append(kept, raw)
	}
	j["locations"] = kept
	if err := s.Store.SetParty("journey", j); err != nil {
		return err
	}
	return s.pushParty()
}

// opJourneyHere marks where the party is now; anything already passed becomes
// somewhere they have been.
func (s *Server) opJourneyHere(c *hub.Client, p payload) error {
	j, locs := s.journey()
	target := p.str("id", "")
	name := ""
	for _, raw := range locs {
		loc, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		switch {
		case locID(raw) == target:
			loc["status"] = "current"
			name, _ = loc["name"].(string)
		case loc["status"] == "current":
			loc["status"] = "visited"
		}
	}
	j["locations"] = locs
	if err := s.Store.SetParty("journey", j); err != nil {
		return err
	}
	if err := s.pushParty(); err != nil {
		return err
	}
	if name != "" {
		s.Hub.Broadcast("toast", map[string]string{
			"kind": "announce", "text": "The party arrives at " + name,
		})
	}
	return nil
}

// ---------------------------------------------------------------- loot

// normalizeLoot puts one item in its current shape, whatever shape it arrived in.
func normalizeLoot(raw any) map[string]any {
	it, _ := raw.(map[string]any)
	if it == nil {
		it = map[string]any{}
	}
	id, _ := it["id"].(string)
	if id == "" {
		id = store.Token(8)
	}
	name, _ := it["name"].(string)
	if len(name) > 80 {
		name = name[:80]
	}
	notes, _ := it["notes"].(string)
	if len(notes) > 200 {
		notes = notes[:200]
	}
	var owner any
	if v, ok := it["owner"].(float64); ok {
		owner = int64(v)
	}
	return map[string]any{
		"id": id, "name": name, "qty": max(1, numOf(it["qty"], 1)),
		"notes": notes, "owner": owner,
	}
}

func (s *Server) loot() []any {
	raw, _ := s.Store.Party("loot").([]any)
	out := make([]any, 0, len(raw))
	for _, it := range raw {
		out = append(out, normalizeLoot(it))
	}
	return out
}

// validOwner resolves who an item belongs to: a real character, or the shared
// pile. There is no free-typed owner, so loot cannot drift onto someone who
// isn't in the party.
func (s *Server) validOwner(v any) (any, error) {
	switch owner := v.(type) {
	case nil:
		return nil, nil
	case string:
		if owner == "" || owner == "shared" {
			return nil, nil
		}
		n, err := strconv.ParseInt(owner, 10, 64)
		if err != nil {
			return nil, errors.New("loot must go to a character or the shared pile")
		}
		return s.checkOwner(n)
	case float64:
		return s.checkOwner(int64(owner))
	}
	return nil, errors.New("loot must go to a character or the shared pile")
}

func (s *Server) checkOwner(id int64) (any, error) {
	ch, err := s.Store.Character(id)
	if err != nil {
		return nil, err
	}
	if ch == nil {
		return nil, errors.New("no such character to give that to")
	}
	return id, nil
}

// mayHold: a player can put things in their own pack or the shared pile;
// sending something to another player is the DM's call.
func mayHold(c *hub.Client, owner any) bool {
	if c.IsDM() || owner == nil {
		return true
	}
	id, ok := owner.(int64)
	return ok && c.CharacterID != nil && *c.CharacterID == id
}

func ownerOf(it map[string]any) any {
	switch v := it["owner"].(type) {
	case int64:
		return v
	case float64:
		return int64(v)
	}
	return nil
}

func (s *Server) opLootAdd(c *hub.Client, p payload) error {
	name := p.clamped("name", "", 80)
	if name == "" {
		return nil
	}
	owner, err := s.validOwner(p["owner"])
	if err != nil {
		return err
	}
	if !mayHold(c, owner) {
		return errors.New("ask the DM to give that to someone else")
	}
	items := append(s.loot(), normalizeLoot(map[string]any{
		"name": name, "qty": p.num("qty", 1), "notes": p.str("notes", ""), "owner": owner,
	}))
	// normalizeLoot only understands a float owner; set it back explicitly.
	items[len(items)-1].(map[string]any)["owner"] = owner
	if err := s.Store.SetParty("loot", items); err != nil {
		return err
	}
	return s.pushParty()
}

func (s *Server) opLootUpdate(c *hub.Client, p payload) error {
	items := s.loot()
	patch := p.sub("patch")
	target := p.str("id", "")
	for _, raw := range items {
		it := raw.(map[string]any)
		if it["id"] != target {
			continue
		}
		if !mayHold(c, ownerOf(it)) {
			return errors.New("that isn't yours to change")
		}
		if patch.has("owner") {
			owner, err := s.validOwner(patch["owner"])
			if err != nil {
				return err
			}
			if !mayHold(c, owner) {
				return errors.New("ask the DM to give that to someone else")
			}
			it["owner"] = owner
		}
		if patch.has("name") {
			if v := patch.clamped("name", "", 80); v != "" {
				it["name"] = v
			}
		}
		if patch.has("qty") {
			it["qty"] = max(1, patch.intv("qty", 1))
		}
		if patch.has("notes") {
			it["notes"] = patch.clamped("notes", "", 200)
		}
		break
	}
	if err := s.Store.SetParty("loot", items); err != nil {
		return err
	}
	return s.pushParty()
}

func (s *Server) opLootRemove(c *hub.Client, p payload) error {
	items := s.loot()
	target := p.str("id", "")
	kept := []any{}
	found := false
	for _, raw := range items {
		it := raw.(map[string]any)
		if it["id"] == target {
			found = true
			// Players can throw away their own things; clearing the shared pile
			// is the DM's job, so nobody bins the party's treasure by accident.
			if !c.IsDM() && !sameOwner(ownerOf(it), c.CharacterID) {
				return errors.New("only the DM can remove that")
			}
			continue
		}
		kept = append(kept, raw)
	}
	if !found {
		return nil
	}
	if err := s.Store.SetParty("loot", kept); err != nil {
		return err
	}
	return s.pushParty()
}

func sameOwner(owner any, charID *int64) bool {
	id, ok := owner.(int64)
	if !ok {
		return charID == nil && owner == nil
	}
	return charID != nil && *charID == id
}

// opLootMove picks something up or puts it down. A player can only move loot
// between the shared pile and their own pack.
func (s *Server) opLootMove(c *hub.Client, p payload) error {
	owner, err := s.validOwner(p["owner"])
	if err != nil {
		return err
	}
	if !mayHold(c, owner) {
		return errors.New("ask the DM to hand that to someone else")
	}
	items := s.loot()
	target := p.str("id", "")
	for _, raw := range items {
		it := raw.(map[string]any)
		if it["id"] == target {
			it["owner"] = owner
			break
		}
	}
	if err := s.Store.SetParty("loot", items); err != nil {
		return err
	}
	return s.pushParty()
}

// ---------------------------------------------------------------- handouts

func normalizeHandout(raw any) map[string]any {
	h, _ := raw.(map[string]any)
	if h == nil {
		h = map[string]any{}
	}
	id, _ := h["id"].(string)
	if id == "" {
		id = store.Token(8)
	}
	title, _ := h["title"].(string)
	if title == "" {
		title = "Untitled"
	}
	if len(title) > 120 {
		title = title[:120]
	}
	body, _ := h["body"].(string)
	if len(body) > 8000 {
		body = body[:8000]
	}
	var image any
	if s, ok := h["image"].(string); ok && s != "" {
		if len(s) > 80 {
			s = s[:80]
		}
		image = s
	}
	revealed, _ := h["revealed"].(bool)
	ts, _ := h["ts"].(string)
	if ts == "" {
		ts = store.Now()
	}
	return map[string]any{
		"id": id, "title": title, "body": body,
		"image": image, "revealed": revealed, "ts": ts,
	}
}

func (s *Server) handouts() []any {
	raw, _ := s.Store.Party("handouts").([]any)
	out := make([]any, 0, len(raw))
	for _, h := range raw {
		out = append(out, normalizeHandout(h))
	}
	return out
}

// opHandoutSave writes one, or rewrites it. Saving alone never shows it to
// anyone; passing `push` saves and reveals in one step, so improvising one
// mid-scene doesn't need the client to guess the id it is about to be given.
func (s *Server) opHandoutSave(c *hub.Client, p payload) error {
	items := s.handouts()
	hid := p.str("id", "")

	var target map[string]any
	for _, raw := range items {
		if h := raw.(map[string]any); h["id"] == hid {
			target = h
			break
		}
	}
	if target == nil {
		target = normalizeHandout(map[string]any{
			"title": p["title"], "body": p["body"], "image": p["image"],
		})
		items = append(items, target)
	} else {
		if p.has("title") {
			if v := p.clamped("title", "", 120); v != "" {
				target["title"] = v
			}
		}
		if p.has("body") {
			target["body"] = p.clamped("body", "", 8000)
		}
		if p.has("image") {
			if v := p.clamped("image", "", 80); v != "" {
				target["image"] = v
			} else {
				target["image"] = nil
			}
		}
	}
	if p.boolv("push") {
		target["revealed"] = true
		target["ts"] = store.Now()
	}
	if err := s.Store.SetParty("handouts", items); err != nil {
		return err
	}
	if err := s.pushParty(); err != nil {
		return err
	}
	if p.boolv("push") {
		s.Hub.Broadcast("handout", target)
	}
	return nil
}

func (s *Server) opHandoutRemove(c *hub.Client, p payload) error {
	target := p.str("id", "")
	kept := []any{}
	for _, raw := range s.handouts() {
		if raw.(map[string]any)["id"] != target {
			kept = append(kept, raw)
		}
	}
	if err := s.Store.SetParty("handouts", kept); err != nil {
		return err
	}
	return s.pushParty()
}

// opHandoutPush reveals one and pops it on every phone. It stays in the
// players' list afterwards so they can read it again later.
func (s *Server) opHandoutPush(c *hub.Client, p payload) error {
	items := s.handouts()
	target := p.str("id", "")
	var shown map[string]any
	for _, raw := range items {
		if h := raw.(map[string]any); h["id"] == target {
			h["revealed"] = true
			h["ts"] = store.Now()
			shown = h
			break
		}
	}
	if shown == nil {
		return nil
	}
	if err := s.Store.SetParty("handouts", items); err != nil {
		return err
	}
	if err := s.pushParty(); err != nil {
		return err
	}
	s.Hub.Broadcast("handout", shown)
	return nil
}

// opHandoutHide takes one back out of the players' hands, keeping it in the DM's.
func (s *Server) opHandoutHide(c *hub.Client, p payload) error {
	items := s.handouts()
	target := p.str("id", "")
	for _, raw := range items {
		if h := raw.(map[string]any); h["id"] == target {
			h["revealed"] = false
			break
		}
	}
	if err := s.Store.SetParty("handouts", items); err != nil {
		return err
	}
	return s.pushParty()
}

// ---------------------------------------------------------------- campaigns

func (s *Server) opCampaignRename(c *hub.Client, p payload) error {
	id := p.str("id", "")
	if id == "" {
		id, _ = s.Store.ActiveID()
	}
	if err := s.Store.RenameCampaign(id, p.str("name", "")); err != nil {
		return err
	}
	active, _ := s.Store.ActiveID()
	s.Hub.Broadcast("campaign", map[string]any{"name": s.Store.CampaignName(""), "id": active})
	return s.pushCampaigns()
}

func (s *Server) opCampaignCreate(c *hub.Client, p payload) error {
	cid, err := s.Store.CreateCampaign(p.str("name", "New Campaign"))
	if err != nil {
		return err
	}
	if p.boolv("switch") {
		return s.opCampaignSwitch(c, payload{"id": cid})
	}
	return s.pushCampaigns()
}

// opCampaignSwitch puts a different campaign on the table.
//
// Everything — characters, loot, the journey, and the device tokens players
// joined with — lives inside a campaign's own file, so switching invalidates
// every session. Players are sent back to the join screen to pick a character
// in the new campaign; the DM who threw the switch is carried across so they
// don't have to sign back in mid-sentence.
func (s *Server) opCampaignSwitch(c *hub.Client, p payload) error {
	cid := p.str("id", "")
	active, _ := s.Store.ActiveID()
	if cid == "" || cid == active {
		return nil
	}
	ok, err := s.Store.OpenCampaign(cid)
	if err != nil || !ok {
		return err
	}
	name := c.Name
	if name == "" {
		name = "DM"
	}
	if err := s.Store.SaveDevice(&store.Device{
		Token: c.Token, DisplayName: name, Role: "dm", CharacterID: nil,
		CreatedAt: store.Now(), LastSeen: store.Now(),
	}); err != nil {
		return err
	}
	s.Hub.Broadcast("campaign.switched", map[string]any{
		"id": cid, "name": s.Store.CampaignName(""),
	})
	return nil
}

func (s *Server) opCampaignDelete(c *hub.Client, p payload) error {
	ok, err := s.Store.DeleteCampaign(p.str("id", ""))
	if err != nil {
		return err
	}
	if !ok {
		s.toast(c, "error", "can't delete the campaign you're playing")
	}
	return s.pushCampaigns()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
