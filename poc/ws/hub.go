// Package ws provides a WebSocket hub for real-time POC updates.
package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Message represents a WebSocket message.
type Message struct {
	Type      string    `json:"type"`
	Data      any       `json:"data"`
	Timestamp time.Time `json:"timestamp"`
}

// Client represents a connected WebSocket client.
type Client struct {
	hub    *Hub
	conn   *websocket.Conn
	send   chan []byte
	id     string
	mu     sync.Mutex
	closed bool
}

// Hub manages WebSocket connections and broadcasts messages.
type Hub struct {
	port       int
	heartbeat  time.Duration
	clients    map[*Client]bool
	broadcast  chan []byte
	register   chan *Client
	unregister chan *Client
	server     *http.Server
	mu         sync.RWMutex
	upgrader   websocket.Upgrader
}

// NewHub creates a new WebSocket hub.
func NewHub(port int, heartbeat time.Duration) *Hub {
	return &Hub{
		port:       port,
		heartbeat:  heartbeat,
		clients:    make(map[*Client]bool),
		broadcast:  make(chan []byte, 256),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(_ *http.Request) bool {
				return true
			},
		},
	}
}

// Attach starts the hub's broadcast loop and registers its WebSocket route on
// an existing mux, so the hub can share a single HTTP server with the rest of
// the application. The context must be first; it is the loop's lifetime.
func (h *Hub) Attach(ctx context.Context, mux *http.ServeMux) {
	mux.HandleFunc("/ws", h.handleWS)
	go h.run(ctx)
}

// Start starts the WebSocket server on its own listener.
func (h *Hub) Start(ctx context.Context) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", h.handleWS)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	h.server = &http.Server{
		Addr:              fmt.Sprintf(":%d", h.port),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go h.run(ctx)
	go func() {
		log.Printf("WebSocket server starting on :%d", h.port)
		if err := h.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("WebSocket server error: %v", err)
		}
	}()
}

// Stop stops the WebSocket server and closes all connections.
func (h *Hub) Stop() {
	h.mu.Lock()
	defer h.mu.Unlock()

	for client := range h.clients {
		client.close()
	}
	if h.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = h.server.Shutdown(ctx)
	}
}

func (h *Hub) run(ctx context.Context) {
	ticker := time.NewTicker(h.heartbeat)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()
			log.Printf("Client connected: %s (total: %d)", client.id, len(h.clients))
		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}
			h.mu.Unlock()
			log.Printf("Client disconnected: %s (total: %d)", client.id, len(h.clients))
		case message := <-h.broadcast:
			h.mu.RLock()
			for client := range h.clients {
				select {
				case client.send <- message:
				default:
					close(client.send)
					delete(h.clients, client)
				}
			}
			h.mu.RUnlock()
		case <-ticker.C:
			h.mu.RLock()
			for client := range h.clients {
				client.mu.Lock()
				if !client.closed {
					_ = client.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
					if err := client.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
						client.closed = true
						client.mu.Unlock()
						h.unregister <- client
						continue
					}
				}
				client.mu.Unlock()
			}
			h.mu.RUnlock()
		}
	}
}

func (h *Hub) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WS upgrade error: %v", err)
		return
	}

	client := &Client{
		hub:  h,
		conn: conn,
		send: make(chan []byte, 256),
		id:   generateClientID(),
	}

	h.register <- client

	go client.writePump()
	go client.readPump()
}

func (c *Client) readPump() {
	defer func() {
		c.hub.unregister <- c
		_ = c.conn.Close()
	}()

	c.conn.SetReadLimit(512)
	_ = c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		_ = c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, _, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WS read error: %v", err)
			}
			break
		}
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(c.hub.heartbeat)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			c.mu.Lock()
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				c.mu.Unlock()
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				c.mu.Unlock()
				return
			}
			c.mu.Unlock()
		case <-ticker.C:
			c.mu.Lock()
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				c.mu.Unlock()
				return
			}
			c.mu.Unlock()
		}
	}
}

func (c *Client) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed = true
		_ = c.conn.Close()
	}
}

// Broadcast sends a message to all connected clients.
func (h *Hub) Broadcast(msg Message) {
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("Broadcast marshal error: %v", err)
		return
	}
	select {
	case h.broadcast <- data:
	default:
		log.Printf("Broadcast channel full, dropping message")
	}
}

// ClientCount returns the number of connected clients.
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

func generateClientID() string {
	return time.Now().Format("20060102150405.000000")
}
