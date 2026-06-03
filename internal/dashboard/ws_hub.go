package dashboard

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		// Allow same-origin and localhost connections.
		host := r.Host
		return strings.Contains(origin, host) ||
			strings.HasPrefix(origin, "http://localhost") ||
			strings.HasPrefix(origin, "http://127.0.0.1")
	},
}

const maxClients = 100

type client struct {
	conn *websocket.Conn
	send chan []byte
}

type Hub struct {
	bus     *Bus
	eventCh chan Event

	clients    map[*client]struct{}
	register   chan *client
	unregister chan *client
	mu         sync.RWMutex
	stopCh     chan struct{}
}

func NewHub(bus *Bus) *Hub {
	return &Hub{
		bus:        bus,
		eventCh:    bus.Subscribe(),
		clients:    make(map[*client]struct{}),
		register:   make(chan *client),
		unregister: make(chan *client, maxClients), // buffered: prevents writePump/readPump defer blocks after Run() exits
		stopCh:     make(chan struct{}),
	}
}

func (h *Hub) Run() {
	for {
		select {
		case c := <-h.register:
			h.mu.Lock()
			h.clients[c] = struct{}{}
			h.mu.Unlock()

		case c := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[c]; ok {
				delete(h.clients, c)
				close(c.send)
			}
			h.mu.Unlock()

		case ev := <-h.eventCh:
			data, err := json.Marshal(ev)
			if err != nil {
				slog.Warn("dashboard: failed to marshal event", "err", err)
				continue
			}
			h.mu.RLock()
			for c := range h.clients {
				select {
				case c.send <- data:
				default:
				}
			}
			h.mu.RUnlock()

		case <-h.stopCh:
			h.bus.Unsubscribe(h.eventCh)
			h.mu.Lock()
			for c := range h.clients {
				close(c.send)
				delete(h.clients, c)
			}
			h.mu.Unlock()
			return
		}
	}
}

func (h *Hub) Stop() {
	select {
	case <-h.stopCh:
	default:
		close(h.stopCh)
	}
}

func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

func (h *Hub) HandleWS(w http.ResponseWriter, r *http.Request) {
	if h.ClientCount() >= maxClients {
		http.Error(w, "too many connections", http.StatusServiceUnavailable)
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Warn("dashboard: ws upgrade failed", "err", err)
		return
	}

	c := &client{conn: conn, send: make(chan []byte, 64)}
	h.register <- c

	go h.writePump(c)
	go h.readPump(c)
}

func (h *Hub) writePump(c *client) {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		h.unregister <- c
		_ = c.conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (h *Hub) readPump(c *client) {
	defer func() {
		h.unregister <- c
		_ = c.conn.Close()
	}()
	c.conn.SetReadLimit(512)
	_ = c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		_ = c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})
	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			break
		}
	}
}
