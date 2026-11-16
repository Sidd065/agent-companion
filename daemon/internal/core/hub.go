package core

import (
	"encoding/json"
	"log"
	"sync"
)

// Client is one connected app instance (one WebSocket).
type Client struct {
	ID      string
	Device  string // device name
	Peer    string // paired device id
	send    chan []byte
	mu      sync.Mutex
	subs    map[string]bool
	closed  bool
	OnClose func()
}

// NewClient creates a client with a bounded send queue.
func NewClient(id string) *Client {
	return &Client{ID: id, send: make(chan []byte, 2048), subs: map[string]bool{}}
}

// Outbox is the channel the transport drains.
func (c *Client) Outbox() <-chan []byte { return c.send }

// SendBytes enqueues a frame; a client that can't keep up is disconnected
// (it will reconnect and resync from sequence numbers).
func (c *Client) SendBytes(b []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	select {
	case c.send <- b:
	default:
		log.Printf("client %s (%s) too slow; dropping connection", c.ID, c.Device)
		c.closed = true
		close(c.send)
		if c.OnClose != nil {
			go c.OnClose()
		}
	}
}

// Event sends a server event.
func (c *Client) Event(name string, data any) {
	b, err := json.Marshal(map[string]any{"event": name, "data": data})
	if err != nil {
		return
	}
	c.SendBytes(b)
}

// Close stops delivery to this client.
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed = true
		close(c.send)
	}
}

// Subscribe marks interest in a session's transcript ops.
func (c *Client) Subscribe(id string, on bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if on {
		c.subs[id] = true
	} else {
		delete(c.subs, id)
	}
}

// Subscribed reports whether the client follows a session.
func (c *Client) Subscribed(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.subs[id]
}

// Hub tracks connected clients.
type Hub struct {
	mu      sync.RWMutex
	clients map[*Client]bool
}

// NewHub creates an empty hub.
func NewHub() *Hub { return &Hub{clients: map[*Client]bool{}} }

// Add registers a client.
func (h *Hub) Add(c *Client) {
	h.mu.Lock()
	h.clients[c] = true
	h.mu.Unlock()
}

// Remove unregisters a client.
func (h *Hub) Remove(c *Client) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
}

// Count returns the number of connected clients.
func (h *Hub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// Clients returns a snapshot.
func (h *Hub) Clients() []*Client {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]*Client, 0, len(h.clients))
	for c := range h.clients {
		out = append(out, c)
	}
	return out
}

// Broadcast sends an event to every client.
func (h *Hub) Broadcast(name string, data any) {
	b, err := json.Marshal(map[string]any{"event": name, "data": data})
	if err != nil {
		return
	}
	for _, c := range h.Clients() {
		c.SendBytes(b)
	}
}

// ToSubscribers sends an event to clients following a session.
func (h *Hub) ToSubscribers(sessionID, name string, data any) {
	var b []byte
	for _, c := range h.Clients() {
		if !c.Subscribed(sessionID) {
			continue
		}
		if b == nil {
			var err error
			b, err = json.Marshal(map[string]any{"event": name, "data": data})
			if err != nil {
				return
			}
		}
		c.SendBytes(b)
	}
}

// HasSubscribers reports whether anyone follows a session.
func (h *Hub) HasSubscribers(sessionID string) bool {
	for _, c := range h.Clients() {
		if c.Subscribed(sessionID) {
			return true
		}
	}
	return false
}
