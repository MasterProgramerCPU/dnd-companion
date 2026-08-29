package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dndcompanion/internal/server"
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

func TestUnknownTokenIsRejected(t *testing.T) {
	ts, _ := harness(t)
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws?token=nonsense"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return // refused outright is fine too
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Error("expected the connection to be closed for an unknown token")
	}
}
