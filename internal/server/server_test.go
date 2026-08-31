package server_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dndcompanion/internal/server"
	"dndcompanion/internal/sheet"
	"dndcompanion/internal/store"

	"github.com/gorilla/websocket"
)

// harness spins up a real server over a throwaway campaign.
func harness(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := st.InitRegistry(); err != nil {
		t.Fatalf("init registry: %v", err)
	}
	srv, err := server.New(st)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { ts.Close(); st.Close() })
	return ts, st
}

func post(t *testing.T, ts *httptest.Server, path string, body any) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(body)
	resp, err := http.Post(ts.URL+path, "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode != 200 {
		t.Fatalf("POST %s: %d %v", path, resp.StatusCode, out)
	}
	return out
}

func dial(t *testing.T, ts *httptest.Server, token string) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws?token=" + token
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// await reads until the named event arrives, so unrelated broadcasts in flight
// (presence, roster) don't make the test flaky.
func await(t *testing.T, conn *websocket.Conn, ev string) map[string]any {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for i := 0; i < 40; i++ {
		var msg struct {
			Ev   string          `json:"ev"`
			Data json.RawMessage `json:"data"`
		}
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("waiting for %q: %v", ev, err)
		}
		if msg.Ev != ev {
			continue
		}
		var out map[string]any
		json.Unmarshal(msg.Data, &out)
		return out
	}
	t.Fatalf("never saw %q", ev)
	return nil
}

func send(t *testing.T, conn *websocket.Conn, op string, data map[string]any) {
	t.Helper()
	if err := conn.WriteJSON(map[string]any{"op": op, "data": data}); err != nil {
		t.Fatalf("send %s: %v", op, err)
	}
}

func TestJoinCreateAndRoll(t *testing.T) {
	ts, _ := harness(t)

	dm := post(t, ts, "/api/join", map[string]any{"role": "dm", "display_name": "The DM"})
	token, _ := dm["token"].(string)
	if token == "" || dm["role"] != "dm" {
		t.Fatalf("bad join response: %v", dm)
	}

	conn := dial(t, ts, token)
	snap := await(t, conn, "snapshot")
	for _, key := range []string{"campaign", "characters", "initiative", "rolls", "party", "campaigns"} {
		if _, ok := snap[key]; !ok {
			t.Errorf("snapshot missing %q", key)
		}
	}

	send(t, conn, "char.create", map[string]any{
		"name": "Vex", "player": "Sam", "klass": "Rogue", "level": 5, "color": "#aa0000",
	})

	// Derived stats must arrive computed, not left to the client to work out.
	var chars []struct {
		Name    string `json:"name"`
		Derived struct {
			ProfBonus int `json:"prof_bonus"`
		} `json:"derived"`
	}
	if err := decodeEvent(t, conn, "characters", &chars); err == nil {
		found := false
		for _, c := range chars {
			if c.Name == "Vex" {
				found = true
				if c.Derived.ProfBonus != 3 { // level 5 => +3
					t.Errorf("Vex prof bonus = %d, want 3", c.Derived.ProfBonus)
				}
			}
		}
		if !found {
			t.Error("Vex missing from the characters broadcast")
		}
	}

	send(t, conn, "roll", map[string]any{"formula": "1d20+5", "label": "Stealth"})
	roll := await(t, conn, "roll")
	if roll["formula"] != "1d20+5" || roll["label"] != "Stealth" {
		t.Errorf("unexpected roll: %v", roll)
	}
	total, _ := roll["total"].(float64)
	if total < 6 || total > 25 {
		t.Errorf("1d20+5 produced %v, outside 6..25", total)
	}
}

// decodeEvent waits for one event and unmarshals its payload into out.
func decodeEvent(t *testing.T, conn *websocket.Conn, ev string, out any) error {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for i := 0; i < 40; i++ {
		var msg struct {
			Ev   string          `json:"ev"`
			Data json.RawMessage `json:"data"`
		}
		if err := conn.ReadJSON(&msg); err != nil {
			return err
		}
		if msg.Ev == ev {
			return json.Unmarshal(msg.Data, out)
		}
	}
	t.Fatalf("never saw %q", ev)
	return nil
}

func TestPlayerCannotUseDMOps(t *testing.T) {
	ts, st := harness(t)

	dmToken, _ := post(t, ts, "/api/join", map[string]any{"role": "dm"})["token"].(string)
	dmConn := dial(t, ts, dmToken)
	await(t, dmConn, "snapshot")
	send(t, dmConn, "char.create", map[string]any{"name": "Pip"})
	await(t, dmConn, "characters")

	chars, err := st.Characters()
	if err != nil || len(chars) == 0 {
		t.Fatalf("character not created: %v", err)
	}

	player := post(t, ts, "/api/join", map[string]any{
		"role": "player", "character_id": chars[0].ID,
	})
	pConn := dial(t, ts, player["token"].(string))
	await(t, pConn, "snapshot")

	// A DM-only op must be refused rather than silently applied.
	send(t, pConn, "campaign.delete", map[string]any{"id": "whatever"})
	toast := await(t, pConn, "toast")
	if toast["kind"] != "error" || !strings.Contains(toast["text"].(string), "not allowed") {
		t.Errorf("expected refusal, got %v", toast)
	}

	// And quests stay DM-side even though gold does not.
	send(t, pConn, "party.set", map[string]any{"key": "quests", "value": []any{}})
	toast = await(t, pConn, "toast")
	if toast["kind"] != "error" {
		t.Errorf("expected refusal for quests, got %v", toast)
	}
}

func TestPlayerJoinRequiresRealCharacter(t *testing.T) {
	ts, _ := harness(t)
	raw, _ := json.Marshal(map[string]any{"role": "player"})
	resp, err := http.Post(ts.URL+"/api/join", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("joining with no character: got %d, want 400", resp.StatusCode)
	}
}

// A stale token must be closed with 4401 specifically: the frontend keys off
// that exact code to clear the token and send the player back to the join
// screen. Any other code leaves them in a reconnect loop instead.
func TestUnknownTokenIsClosedWith4401(t *testing.T) {
	ts, _ := harness(t)
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws?token=nonsense"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, _, err = conn.ReadMessage()
	if err == nil {
		t.Fatal("expected the connection to be closed for an unknown token")
	}
	var ce *websocket.CloseError
	if !errors.As(err, &ce) {
		t.Fatalf("closed with %v, want a websocket close error", err)
	}
	if ce.Code != 4401 {
		t.Errorf("close code %d, want 4401 (the frontend keys off it to re-join)", ce.Code)
	}
}

// Spending a class resource is applied as a delta on the server and clamped to
// the resource's own bounds, so it can neither go negative nor exceed its max.
func TestCharResourceSpendsAndClamps(t *testing.T) {
	ts, st := harness(t)

	dm := post(t, ts, "/api/join", map[string]any{"role": "dm", "display_name": "The DM"})
	conn := dial(t, ts, dm["token"].(string))
	await(t, conn, "snapshot")

	send(t, conn, "char.create", map[string]any{"name": "The Detective", "level": 10})
	await(t, conn, "characters")

	chars, err := st.Characters()
	if err != nil || len(chars) != 1 {
		t.Fatalf("characters: %v %d", err, len(chars))
	}
	id := chars[0].ID

	send(t, conn, "char.patch", map[string]any{"id": id, "patch": map[string]any{
		"resources": []any{map[string]any{
			"id": "case", "name": "Case Dice", "die": "d8", "max": 6, "used": 0,
		}},
	}})
	await(t, conn, "characters")

	usedNow := func() int {
		t.Helper()
		cs, err := st.Characters()
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		list, _ := cs[0].Sheet["resources"].([]any)
		if len(list) != 1 {
			t.Fatalf("resources: %v", list)
		}
		r, _ := list[0].(map[string]any)
		used, _ := r["used"].(float64)
		return int(used)
	}

	for _, tc := range []struct {
		name  string
		data  map[string]any
		times int
		want  int
	}{
		{"spend one", map[string]any{"delta": 1}, 1, 1},
		{"spend three more", map[string]any{"delta": 1}, 3, 4},
		{"cannot exceed max", map[string]any{"delta": 1}, 5, 6},
		{"restore one", map[string]any{"delta": -1}, 1, 5},
		{"cannot go negative", map[string]any{"delta": -1}, 9, 0},
		{"set outright", map[string]any{"set": 3}, 1, 3},
		{"a set is clamped too", map[string]any{"set": 99}, 1, 6},
	} {
		for i := 0; i < tc.times; i++ {
			data := map[string]any{"id": id, "res": "case"}
			for k, v := range tc.data {
				data[k] = v
			}
			send(t, conn, "char.resource", data)
			await(t, conn, "characters")
		}
		if got := usedNow(); got != tc.want {
			t.Errorf("%s: used = %d, want %d", tc.name, got, tc.want)
		}
	}

	// An unknown resource id must be a no-op, not an error or a stray write.
	send(t, conn, "char.resource", map[string]any{"id": id, "res": "nope", "delta": 1})
	await(t, conn, "characters")
	if got := usedNow(); got != 6 {
		t.Errorf("unknown resource changed used to %d", got)
	}
}

// A rest is applied by the server and announced to the table, and a player
// cannot rest somebody else's character.
func TestRestOps(t *testing.T) {
	ts, st := harness(t)

	dm := post(t, ts, "/api/join", map[string]any{"role": "dm", "display_name": "The DM"})
	conn := dial(t, ts, dm["token"].(string))
	await(t, conn, "snapshot")

	send(t, conn, "char.create", map[string]any{"name": "The Detective", "level": 10})
	await(t, conn, "characters")
	chars, _ := st.Characters()
	id := chars[0].ID

	// A spent character: hurt, and four of six case dice gone.
	send(t, conn, "char.patch", map[string]any{"id": id, "patch": map[string]any{
		"hp":       map[string]any{"current": 4, "max": 40, "temp": 6},
		"hit_dice": map[string]any{"die": "d8", "total": 10, "used": 6},
		"resources": []any{
			map[string]any{"id": "case", "name": "Case Dice", "max": 6, "used": 4,
				"recharge": "long", "short_regain": 1},
			map[string]any{"id": "expr", "name": "The Expression", "max": 1, "used": 1,
				"recharge": "short"},
		},
	}})
	await(t, conn, "characters")

	reload := func() map[string]any {
		t.Helper()
		cs, err := st.Characters()
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		return cs[0].Sheet
	}
	used := func(sh map[string]any, id string) int {
		t.Helper()
		for _, raw := range sh["resources"].([]any) {
			r := raw.(map[string]any)
			if r["id"] == id {
				n, _ := r["used"].(float64)
				return int(n)
			}
		}
		t.Fatalf("no resource %q", id)
		return -1
	}

	send(t, conn, "char.rest", map[string]any{"id": id, "kind": "short"})
	if got := await(t, conn, "toast")["text"].(string); !strings.Contains(got, "Short rest") {
		t.Errorf("short rest was not announced: %q", got)
	}
	await(t, conn, "characters")

	sh := reload()
	if got := used(sh, "expr"); got != 0 {
		t.Errorf("short-rest resource still %d used", got)
	}
	if got := used(sh, "case"); got != 3 {
		t.Errorf("case dice used = %d, want 3 after a short rest", got)
	}
	if got := int(sh["hp"].(map[string]any)["current"].(float64)); got != 4 {
		t.Errorf("a short rest healed to %d", got)
	}

	send(t, conn, "char.rest", map[string]any{"id": id, "kind": "long"})
	await(t, conn, "toast")
	await(t, conn, "characters")

	sh = reload()
	hp := sh["hp"].(map[string]any)
	if int(hp["current"].(float64)) != 40 || int(hp["temp"].(float64)) != 0 {
		t.Errorf("long rest left hp %v", hp)
	}
	if got := used(sh, "case"); got != 0 {
		t.Errorf("case dice used = %d after a long rest", got)
	}
	if got := int(sh["hit_dice"].(map[string]any)["used"].(float64)); got != 1 {
		t.Errorf("hit dice used = %d, want 1 (six spent, five back)", got)
	}

	// An unknown kind must be treated as the cheap rest, never the long one.
	send(t, conn, "char.patch", map[string]any{"id": id,
		"patch": map[string]any{"hp": map[string]any{"current": 1}}})
	await(t, conn, "characters")
	send(t, conn, "char.rest", map[string]any{"id": id, "kind": "nonsense"})
	await(t, conn, "toast")
	await(t, conn, "characters")
	if got := int(reload()["hp"].(map[string]any)["current"].(float64)); got != 1 {
		t.Errorf("an unrecognised rest kind healed the character to %d", got)
	}
}

// party.rest is the DM's alone; a player asking for one gets nothing.
func TestPartyRestIsDMOnly(t *testing.T) {
	ts, st := harness(t)

	dm := post(t, ts, "/api/join", map[string]any{"role": "dm", "display_name": "The DM"})
	dmConn := dial(t, ts, dm["token"].(string))
	await(t, dmConn, "snapshot")
	send(t, dmConn, "char.create", map[string]any{"name": "Vex", "level": 5})
	await(t, dmConn, "characters")

	chars, _ := st.Characters()
	id := chars[0].ID
	send(t, dmConn, "char.patch", map[string]any{"id": id,
		"patch": map[string]any{"hp": map[string]any{"current": 3, "max": 30}}})
	await(t, dmConn, "characters")

	player := post(t, ts, "/api/join", map[string]any{
		"role": "player", "display_name": "Sam", "character_id": id})
	pConn := dial(t, ts, player["token"].(string))
	await(t, pConn, "snapshot")

	send(t, pConn, "party.rest", map[string]any{"kind": "long"})
	send(t, pConn, "roll", map[string]any{"formula": "1d20", "label": "after"})
	await(t, pConn, "roll") // the later message lands, so the rest was dropped

	cs, _ := st.Characters()
	if got := int(cs[0].Sheet["hp"].(map[string]any)["current"].(float64)); got != 3 {
		t.Errorf("a player triggered a party rest: hp is %d", got)
	}

	send(t, dmConn, "party.rest", map[string]any{"kind": "long"})
	await(t, dmConn, "toast")
	await(t, dmConn, "characters")
	cs, _ = st.Characters()
	if got := int(cs[0].Sheet["hp"].(map[string]any)["current"].(float64)); got != 30 {
		t.Errorf("the DM's party rest left hp at %d", got)
	}
}

// The bestiary is the DM's preparation. It must never reach a player — not in
// the snapshot they get on connect, and not in a party broadcast afterwards.
func TestBestiaryNeverReachesPlayers(t *testing.T) {
	ts, st := harness(t)

	dm := post(t, ts, "/api/join", map[string]any{"role": "dm", "display_name": "The DM"})
	dmConn := dial(t, ts, dm["token"].(string))
	await(t, dmConn, "snapshot")
	send(t, dmConn, "char.create", map[string]any{"name": "Vex", "level": 5})
	await(t, dmConn, "characters")
	chars, _ := st.Characters()

	send(t, dmConn, "bestiary.save", map[string]any{"creature": map[string]any{
		"name": "Corpse Inspector", "kind": "Medium undead", "cr": 5.0,
		"ac": 15, "hp_max": 58, "hp_formula": "9d8+18",
		"notes": "SPOILER: it is the conductor",
	}})
	await(t, dmConn, "party")

	player := post(t, ts, "/api/join", map[string]any{
		"role": "player", "display_name": "Sam", "character_id": chars[0].ID})
	pConn := dial(t, ts, player["token"].(string))

	snap := await(t, pConn, "snapshot")
	party, _ := snap["party"].(map[string]any)
	if party == nil {
		t.Fatal("player snapshot had no party")
	}
	if _, leaked := party["bestiary"]; leaked {
		t.Error("the player's snapshot carried the bestiary")
	}
	raw, _ := json.Marshal(snap)
	if bytes.Contains(raw, []byte("Corpse Inspector")) || bytes.Contains(raw, []byte("SPOILER")) {
		t.Error("a monster's stat block appeared somewhere in the player's snapshot")
	}

	// And not on the next broadcast either.
	send(t, dmConn, "bestiary.save", map[string]any{"creature": map[string]any{
		"name": "Ticket Wraith", "cr": 2.0, "ac": 13, "hp_max": 22,
	}})
	pushed := await(t, pConn, "party")
	if _, leaked := pushed["bestiary"]; leaked {
		t.Error("a party broadcast carried the bestiary to a player")
	}
	raw, _ = json.Marshal(pushed)
	if bytes.Contains(raw, []byte("Ticket Wraith")) {
		t.Error("a monster leaked in a party broadcast")
	}

	// A player cannot write one either.
	send(t, pConn, "bestiary.save", map[string]any{"creature": map[string]any{"name": "Mine"}})
	send(t, pConn, "roll", map[string]any{"formula": "1d20", "label": "after"})
	await(t, pConn, "roll")
	list, _ := st.Party("bestiary").([]any)
	if len(list) != 2 {
		t.Errorf("bestiary has %d creatures, want 2 — a player wrote to it", len(list))
	}
}

// Saving, editing and removing a stat block, and the id staying put across an edit.
func TestBestiarySaveEditRemove(t *testing.T) {
	ts, st := harness(t)
	dm := post(t, ts, "/api/join", map[string]any{"role": "dm", "display_name": "The DM"})
	conn := dial(t, ts, dm["token"].(string))
	await(t, conn, "snapshot")

	send(t, conn, "bestiary.save", map[string]any{"creature": map[string]any{
		"name": "Goblin", "cr": 0.25, "ac": 15, "hp_max": 7, "hp_formula": "2d6",
		"abilities": map[string]any{"dex": 14},
	}})
	await(t, conn, "party")

	list, _ := st.Party("bestiary").([]any)
	if len(list) != 1 {
		t.Fatalf("want 1 creature, got %d", len(list))
	}
	first := list[0].(map[string]any)
	id, _ := first["id"].(string)
	if id == "" {
		t.Fatal("the server did not assign an id")
	}
	// A stat block fills in the rest of the model, so it derives like a sheet.
	if _, ok := first["skill_prof"]; !ok {
		t.Error("stat block is missing the fields it derives from")
	}

	// Editing keeps the id and does not append a second creature.
	send(t, conn, "bestiary.save", map[string]any{"creature": map[string]any{
		"id": id, "name": "Goblin Boss", "cr": 1.0, "ac": 17, "hp_max": 21,
	}})
	await(t, conn, "party")
	list, _ = st.Party("bestiary").([]any)
	if len(list) != 1 {
		t.Fatalf("editing added a creature: %d present", len(list))
	}
	edited := list[0].(map[string]any)
	if edited["id"] != id {
		t.Errorf("id changed on edit: %v -> %v", id, edited["id"])
	}
	ac, _ := edited["ac"].(float64)
	if edited["name"] != "Goblin Boss" || int(ac) != 17 {
		t.Errorf("edit did not apply: %v", edited)
	}

	// A nameless creature is refused rather than stored blank.
	send(t, conn, "bestiary.save", map[string]any{"creature": map[string]any{"name": "  "}})
	msg := await(t, conn, "toast")
	if msg["kind"] != "error" {
		t.Errorf("a nameless creature should be refused, got %v", msg)
	}
	list, _ = st.Party("bestiary").([]any)
	if len(list) != 1 {
		t.Errorf("a nameless creature was stored anyway: %d present", len(list))
	}

	send(t, conn, "bestiary.remove", map[string]any{"id": id})
	await(t, conn, "party")
	list, _ = st.Party("bestiary").([]any)
	if len(list) != 0 {
		t.Errorf("remove left %d creatures", len(list))
	}
}

// Adding a batch from the bestiary: numbered names, each with its own rolled
// hit points, and the stat block's own hidden setting carried across.
func TestInitAddFromBestiary(t *testing.T) {
	ts, st := harness(t)
	dm := post(t, ts, "/api/join", map[string]any{"role": "dm", "display_name": "The DM"})
	conn := dial(t, ts, dm["token"].(string))
	await(t, conn, "snapshot")

	send(t, conn, "bestiary.save", map[string]any{"creature": map[string]any{
		"name": "Goblin", "kind": "Small humanoid", "cr": 0.25,
		"ac": 15, "hp_max": 7, "hp_formula": "20d20", // wide, so equal rolls are unlikely
		"abilities": map[string]any{"dex": 14}, "hidden": true,
	}})
	await(t, conn, "party")
	list, _ := st.Party("bestiary").([]any)
	id := list[0].(map[string]any)["id"].(string)

	send(t, conn, "init.add_bestiary", map[string]any{"id": id, "count": 6})
	await(t, conn, "initiative")

	rows, err := st.InitiativeRows()
	if err != nil {
		t.Fatalf("initiative: %v", err)
	}
	if len(rows) != 6 {
		t.Fatalf("want 6 goblins, got %d", len(rows))
	}

	// Rows come back in initiative order, and each goblin rolled its own, so
	// check that the batch is all there rather than where each one landed.
	names := map[string]bool{}
	seen := map[int]bool{}
	for _, r := range rows {
		names[r.Name] = true
		if r.AC == nil || *r.AC != 15 {
			t.Errorf("%s has AC %v, want 15", r.Name, r.AC)
		}
		if !r.Hidden {
			t.Errorf("%s is not hidden, but its stat block is", r.Name)
		}
		if r.Note != "Small humanoid" {
			t.Errorf("%s note = %q", r.Name, r.Note)
		}
		if r.HP == nil || r.HPMax == nil || *r.HP != *r.HPMax {
			t.Fatalf("%s has hp %v/%v", r.Name, r.HP, r.HPMax)
		}
		if *r.HP < 20 || *r.HP > 400 {
			t.Errorf("%s rolled %d hit points, outside 20d20", r.Name, *r.HP)
		}
		seen[*r.HP] = true
	}
	for i := 1; i <= 6; i++ {
		if want := fmt.Sprintf("Goblin %d", i); !names[want] {
			t.Errorf("%q is missing from the batch", want)
		}
	}
	// Each creature rolls its own hit points rather than sharing one number.
	if len(seen) == 1 {
		t.Error("all six goblins have identical HP; the formula was rolled once")
	}

	// Without a formula, the flat number is used exactly.
	send(t, conn, "bestiary.save", map[string]any{"creature": map[string]any{
		"name": "Statue", "cr": 1.0, "ac": 17, "hp_max": 40,
	}})
	await(t, conn, "party")
	list, _ = st.Party("bestiary").([]any)
	var statue string
	for _, raw := range list {
		m := raw.(map[string]any)
		if m["name"] == "Statue" {
			statue = m["id"].(string)
		}
	}
	send(t, conn, "init.clear", map[string]any{})
	await(t, conn, "initiative")
	send(t, conn, "init.add_bestiary", map[string]any{"id": statue, "count": 1, "hidden": false})
	await(t, conn, "initiative")

	rows, _ = st.InitiativeRows()
	if len(rows) != 1 || rows[0].Name != "Statue" {
		t.Fatalf("unexpected rows: %+v", rows)
	}
	if rows[0].HP == nil || *rows[0].HP != 40 {
		t.Errorf("flat hit points = %v, want 40", rows[0].HP)
	}
	if rows[0].Hidden {
		t.Error("an explicit hidden:false was ignored")
	}

	// An unknown creature is an error, not a blank row.
	send(t, conn, "init.add_bestiary", map[string]any{"id": "nope", "count": 1})
	if msg := await(t, conn, "toast"); msg["kind"] != "error" {
		t.Errorf("adding an unknown creature gave %v", msg)
	}
}

// A monster's proficiency bonus comes from its challenge rating, so its saves
// and skills derive correctly without inventing a level for it.
func TestBestiaryDerivesFromCR(t *testing.T) {
	ts, st := harness(t)
	dm := post(t, ts, "/api/join", map[string]any{"role": "dm", "display_name": "The DM"})
	conn := dial(t, ts, dm["token"].(string))
	await(t, conn, "snapshot")

	send(t, conn, "bestiary.save", map[string]any{"creature": map[string]any{
		"name": "Corpse Inspector", "cr": 9.0,
		"abilities": map[string]any{"wis": 16},
		"save_prof": map[string]any{"wis": true},
	}})
	await(t, conn, "party")

	list, _ := st.Party("bestiary").([]any)
	block := list[0].(map[string]any)
	d := sheet.Derive(block)
	if d.ProfBonus != 4 { // CR 9 => +4
		t.Errorf("CR 9 proficiency bonus = %d, want 4", d.ProfBonus)
	}
	if d.Saves["wis"] != 7 { // +3 from Wis 16, +4 proficiency
		t.Errorf("Wisdom save = %+d, want +7", d.Saves["wis"])
	}
}
