package server

import (
	"encoding/json"
	"log"
	"net/http"

	"dndcompanion/internal/hub"
	"dndcompanion/internal/state"
	"dndcompanion/internal/store"

	"github.com/gorilla/websocket"
)

// opFunc handles one client message. Returning an error sends the client a
// toast rather than dropping the connection: one bad message must not end a
// session mid-combat.
type opFunc func(c *hub.Client, p payload) error

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	// The app is deliberately open on the LAN — anyone on the Wi-Fi can join or
	// open the DM console, which is the documented trust model. Checking Origin
	// here would reject phones reaching the server by IP without adding any
	// real protection.
	CheckOrigin: func(*http.Request) bool { return true },
}

func (s *Server) playerOps() map[string]opFunc {
	return map[string]opFunc{
		"roll":        s.opRoll,
		"roll.plan":   s.opRollPlan,
		"roll.commit": s.opRollCommit,
		"char.patch":  s.opCharPatch,
		"char.hp":     s.opCharHP,
		"party.set":   s.opPartySetPlayer,
		"init.self":   s.opInitSelf,
		"loot.add":    s.opLootAdd,
		"loot.update": s.opLootUpdate,
		"loot.remove": s.opLootRemove,
		"loot.move":   s.opLootMove,
	}
}

func (s *Server) dmOps() map[string]opFunc {
	return map[string]opFunc{
		"char.create":     s.opCharCreate,
		"char.delete":     s.opCharDelete,
		"init.add":        s.opInitAdd,
		"init.add_party":  s.opInitAddParty,
		"init.update":     s.opInitUpdate,
		"init.remove":     s.opInitRemove,
		"init.clear":      s.opInitClear,
		"init.turn":       s.opInitTurn,
		"party.set":       s.opPartySet,
		"loot.add":        s.opLootAdd,
		"loot.update":     s.opLootUpdate,
		"loot.remove":     s.opLootRemove,
		"loot.move":       s.opLootMove,
		"handout.save":    s.opHandoutSave,
		"handout.remove":  s.opHandoutRemove,
		"handout.push":    s.opHandoutPush,
		"handout.hide":    s.opHandoutHide,
		"journey.add":     s.opJourneyAdd,
		"journey.update":  s.opJourneyUpdate,
		"journey.move":    s.opJourneyMove,
		"journey.remove":  s.opJourneyRemove,
		"journey.here":    s.opJourneyHere,
		"announce":        s.opAnnounce,
		"campaign.rename": s.opCampaignRename,
		"campaign.create": s.opCampaignCreate,
		"campaign.switch": s.opCampaignSwitch,
		"campaign.delete": s.opCampaignDelete,
	}
}

// resolve picks the handler for an op, honouring the permission split: a DM
// gets the DM table first and the player table as a fallback; a player only
// ever gets the player table.
func (s *Server) resolve(op string, isDM bool) opFunc {
	if isDM {
		if fn, ok := s.dmOps()[op]; ok {
			return fn
		}
	}
	return s.playerOps()[op]
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	dev, err := s.Store.Device(token)
	if err != nil || dev == nil {
		// 4401: the client knows to send the player back to the join screen.
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(4401, "unknown device"))
		conn.Close()
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	client := &hub.Client{
		Conn: conn, Token: token, Name: dev.DisplayName,
		Role: dev.Role, CharacterID: dev.CharacterID,
	}
	s.Hub.Add(client)
	client.ReadDeadlines()
	s.Store.Exec("UPDATE devices SET last_seen=? WHERE token=?", store.Now(), token)

	defer func() {
		s.Hub.Drop(client)
		s.Hub.Broadcast("presence", s.Hub.Presence())
	}()

	snapshot, err := state.Snapshot(s.Store, client.IsDM())
	if err != nil {
		log.Printf("snapshot: %v", err)
		return
	}
	s.Hub.Send(client, "snapshot", snapshot)
	s.Hub.Broadcast("presence", s.Hub.Presence())

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var msg struct {
			Op   string  `json:"op"`
			Data payload `json:"data"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			s.toast(client, "error", "could not read that message")
			continue
		}
		handler := s.resolve(msg.Op, client.IsDM())
		if handler == nil {
			s.toast(client, "error", "not allowed: "+msg.Op)
			continue
		}
		if msg.Data == nil {
			msg.Data = payload{}
		}
		if err := handler(client, msg.Data); err != nil {
			s.toast(client, "error", err.Error())
		}
	}
}

func (s *Server) toast(c *hub.Client, kind, text string) {
	s.Hub.Send(c, "toast", map[string]string{"kind": kind, "text": text})
}
