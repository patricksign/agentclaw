package api

import (
	"encoding/json"
	"net/http"
	"os"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
)

func (s *Server) HandlerWebsocket(mux *http.ServeMux) {
	// WebSocket
	mux.HandleFunc("/ws", s.handleWS)
}

// ─── WebSocket ───────────────────────────────────────────────────────────────

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		allowed := os.Getenv("CORS_ORIGIN")
		if allowed == "" {
			// No origin configured: allow same-host only.
			origin := r.Header.Get("Origin")
			return origin == "" || origin == "http://"+r.Host || origin == "https://"+r.Host
		}
		return r.Header.Get("Origin") == allowed
	},
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Error().Err(err).Msg("ws upgrade failed")
		return
	}
	s.hub.connect(conn)
}

// forwardEvents subscribe EventBus → broadcast to all WS clients
func (s *Server) forwardEvents() {
	ch, unsub := s.bus.Subscribe("ws-hub")
	defer unsub()
	for evt := range ch {
		data, err := json.Marshal(evt)
		if err != nil {
			continue
		}
		s.hub.broadcast <- data
	}
}

// ─── WebSocket Hub ────────────────────────────────────────────────────────────

type wsClient struct {
	conn *websocket.Conn
	send chan []byte
}

type wsHub struct {
	clients    map[*wsClient]bool
	broadcast  chan []byte
	register   chan *wsClient
	unregister chan *wsClient
}

func newWsHub() *wsHub {
	return &wsHub{
		clients:    make(map[*wsClient]bool),
		broadcast:  make(chan []byte, 256),
		register:   make(chan *wsClient),
		unregister: make(chan *wsClient),
	}
}

func (h *wsHub) run() {
	for {
		select {
		case c := <-h.register:
			h.clients[c] = true
			log.Debug().Int("total", len(h.clients)).Msg("ws client connected")

		case c := <-h.unregister:
			if _, ok := h.clients[c]; ok {
				delete(h.clients, c)
				close(c.send)
			}
			log.Debug().Int("total", len(h.clients)).Msg("ws client disconnected")

		case msg := <-h.broadcast:
			for c := range h.clients {
				select {
				case c.send <- msg:
				default:
					close(c.send)
					delete(h.clients, c)
				}
			}
		}
	}
}

func (h *wsHub) connect(conn *websocket.Conn) {
	c := &wsClient{conn: conn, send: make(chan []byte, 64)}
	h.register <- c

	// unregisterOnce ensures exactly one unregister + conn.Close regardless
	// of which pump detects the disconnect first, preventing double-close panics.
	var unregisterOnce sync.Once
	cleanup := func() {
		unregisterOnce.Do(func() {
			h.unregister <- c
			conn.Close()
		})
	}

	// write pump
	go func() {
		defer cleanup()
		for msg := range c.send {
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		}
	}()

	// read pump — on disconnect, trigger cleanup so unregister fires even if
	// the write pump is blocked on a send.
	go func() {
		defer cleanup()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
}
