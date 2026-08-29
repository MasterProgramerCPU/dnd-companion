// Package hub is the websocket fan-out.
//
// Every connection carries its device row, so DM-only data can be gated at the
// moment of sending rather than trusted to the client.
package hub

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = 50 * time.Second
	sendBuffer = 32
)

// Envelope is the shape of every message sent to a client.
type Envelope struct {
	Ev   string `json:"ev"`
	Data any    `json:"data"`
}

// Client is one connected phone or browser.
type Client struct {
	Conn        *websocket.Conn
	Token       string
	Name        string
	Role        string
	CharacterID *int64

	send   chan Envelope
	closed chan struct{}
	once   sync.Once
}

// IsDM reports whether this connection may see and do DM-only things.
func (c *Client) IsDM() bool { return c.Role == "dm" }

// Presence is one entry of the "who's here" list.
type Presence struct {
	Name        string `json:"name"`
	Role        string `json:"role"`
	CharacterID *int64 `json:"character_id"`
}

// Hub tracks every live connection.
type Hub struct {
	mu      sync.RWMutex
	clients map[*Client]bool
}

// New returns an empty hub.
func New() *Hub { return &Hub{clients: map[*Client]bool{}} }

// Add registers a connection and starts its writer.
func (h *Hub) Add(c *Client) {
	c.send = make(chan Envelope, sendBuffer)
	c.closed = make(chan struct{})
	h.mu.Lock()
	h.clients[c] = true
	h.mu.Unlock()
	go c.writeLoop()
}

// Drop removes a connection and stops its writer.
func (h *Hub) Drop(c *Client) {
	h.mu.Lock()
	_, existed := h.clients[c]
	delete(h.clients, c)
	h.mu.Unlock()
	if existed {
		c.stop()
	}
}

func (c *Client) stop() {
	c.once.Do(func() {
		close(c.closed)
		c.Conn.Close()
	})
}

// writeLoop owns the connection's write side. Gorilla allows only one writer at
// a time, so every send funnels through here rather than through the callers.
func (c *Client) writeLoop() {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-c.closed:
			return
		case env := <-c.send:
			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.Conn.WriteJSON(env); err != nil {
				c.stop()
				return
			}
		case <-ticker.C:
			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				c.stop()
				return
			}
		}
	}
}

// Send queues one message to one client. A client that has stopped reading is
// dropped rather than allowed to block the table.
func (h *Hub) Send(c *Client, ev string, data any) {
	select {
	case c.send <- Envelope{Ev: ev, Data: data}:
	case <-c.closed:
	default:
		h.Drop(c)
	}
}

func (h *Hub) snapshot() []*Client {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]*Client, 0, len(h.clients))
	for c := range h.clients {
		out = append(out, c)
	}
	return out
}

// Broadcast sends to everyone.
func (h *Hub) Broadcast(ev string, data any) {
	for _, c := range h.snapshot() {
		h.Send(c, ev, data)
	}
}

// ToDMs sends only to DM consoles.
func (h *Hub) ToDMs(ev string, data any) {
	for _, c := range h.snapshot() {
		if c.IsDM() {
			h.Send(c, ev, data)
		}
	}
}

// BroadcastSplit sends the same event with a different payload depending on who
// is listening — the redaction happens here, not on the client.
func (h *Hub) BroadcastSplit(ev string, dmData, playerData any) {
	for _, c := range h.snapshot() {
		if c.IsDM() {
			h.Send(c, ev, dmData)
		} else {
			h.Send(c, ev, playerData)
		}
	}
}

// Presence lists who is at the table, one entry per distinct seat.
func (h *Hub) Presence() []Presence {
	type key struct {
		role string
		char int64
		name string
	}
	seen := map[key]bool{}
	out := []Presence{}
	for _, c := range h.snapshot() {
		k := key{role: c.Role, name: c.Name}
		if c.CharacterID != nil {
			k.char = *c.CharacterID
		}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, Presence{Name: c.Name, Role: c.Role, CharacterID: c.CharacterID})
	}
	return out
}

// ReadDeadlines configures the connection's keepalive.
func (c *Client) ReadDeadlines() {
	c.Conn.SetReadDeadline(time.Now().Add(pongWait))
	c.Conn.SetPongHandler(func(string) error {
		return c.Conn.SetReadDeadline(time.Now().Add(pongWait))
	})
}
