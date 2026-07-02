package ws

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type Config struct {
	Enabled           bool     `json:"enabled" yaml:"enabled"`
	AllowedOrigins    []string `json:"allowed_origins" yaml:"allowed_origins"`
	HeartbeatInterval int      `json:"heartbeat_interval" yaml:"heartbeat_interval"`
	WriteQueueSize    int      `json:"write_queue_size" yaml:"write_queue_size"`
	WriteTimeout      int      `json:"write_timeout" yaml:"write_timeout"`
}

type Message struct {
	Type      string `json:"type"`
	Service   string `json:"service"`
	Timestamp string `json:"timestamp"`
	Payload   any    `json:"payload"`
}

type Hub struct {
	service  string
	cfg      Config
	mu       sync.RWMutex
	clients  map[*Client]struct{}
	closed   bool
	upgrader websocket.Upgrader
}

type Client struct {
	hub       *Hub
	conn      *websocket.Conn
	send      chan []byte
	closeOnce sync.Once
}

func Defaults(c Config) Config {
	if c.HeartbeatInterval <= 0 {
		c.HeartbeatInterval = 30
	}
	if c.WriteQueueSize <= 0 {
		c.WriteQueueSize = 64
	}
	if c.WriteTimeout <= 0 {
		c.WriteTimeout = 5
	}
	return c
}
func NewHub(service string, c Config) *Hub {
	c = Defaults(c)
	h := &Hub{service: service, cfg: c, clients: map[*Client]struct{}{}}
	h.upgrader.CheckOrigin = h.checkOrigin
	return h
}
func (h *Hub) checkOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	allowed := h.cfg.AllowedOrigins
	if len(allowed) == 0 {
		return false
	}
	for _, a := range allowed {
		a = strings.TrimSpace(a)
		if a == "*" || strings.EqualFold(a, origin) {
			return true
		}
	}
	return false
}
func (h *Hub) Handler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.cfg.Enabled {
		http.Error(w, "websocket disabled", http.StatusNotFound)
		return
	}
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	c := &Client{hub: h, conn: conn, send: make(chan []byte, h.cfg.WriteQueueSize)}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		_ = conn.Close()
		return
	}
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	go c.writePump()
	c.readPump()
}
func (h *Hub) Broadcast(typ string, payload any) {
	if h == nil {
		return
	}
	msg := Message{Type: typ, Service: h.service, Timestamp: time.Now().UTC().Format(time.RFC3339Nano), Payload: payload}
	b, err := json.Marshal(msg)
	if err != nil {
		return
	}
	h.mu.RLock()
	clients := make([]*Client, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.RUnlock()
	for _, c := range clients {
		select {
		case c.send <- b:
		default:
			c.Close()
		}
	}
}
func (h *Hub) ActiveConnections() int { h.mu.RLock(); defer h.mu.RUnlock(); return len(h.clients) }
func (h *Hub) Shutdown(ctx context.Context) error {
	h.mu.Lock()
	h.closed = true
	clients := make([]*Client, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.Unlock()
	for _, c := range clients {
		c.Close()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
func (c *Client) readPump() {
	defer c.Close()
	c.conn.SetReadLimit(4096)
	_ = c.conn.SetReadDeadline(time.Now().Add(time.Duration(c.hub.cfg.HeartbeatInterval*2) * time.Second))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(time.Duration(c.hub.cfg.HeartbeatInterval*2) * time.Second))
	})
	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			return
		}
	}
}
func (c *Client) writePump() {
	ticker := time.NewTicker(time.Duration(c.hub.cfg.HeartbeatInterval) * time.Second)
	defer func() { ticker.Stop(); c.Close() }()
	for {
		select {
		case msg, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(time.Duration(c.hub.cfg.WriteTimeout) * time.Second))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(time.Duration(c.hub.cfg.WriteTimeout) * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
func (c *Client) Close() {
	c.closeOnce.Do(func() {
		c.hub.mu.Lock()
		if _, ok := c.hub.clients[c]; ok {
			delete(c.hub.clients, c)
			close(c.send)
		}
		c.hub.mu.Unlock()
		if c.conn != nil {
			_ = c.conn.Close()
		}
	})
}
