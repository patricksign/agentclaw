package api

import (
	"net/http"

	"github.com/patricksign/agentclaw/internal/agent"
	"github.com/patricksign/agentclaw/internal/integrations/pipeline"
	"github.com/patricksign/agentclaw/internal/integrations/trello"
	"github.com/patricksign/agentclaw/internal/memory"
	"github.com/patricksign/agentclaw/internal/queue"
	"github.com/patricksign/agentclaw/internal/state"
	"github.com/patricksign/agentclaw/internal/summarizer"
)

// ─── Server ──────────────────────────────────────────────────────────────────

type Server struct {
	pool        *agent.Pool
	queue       *queue.Queue
	mem         *memory.Store
	bus         *agent.EventBus
	hub         *wsHub
	triggerSvc  *pipeline.Service
	resolved    *state.ResolvedStore   // may be nil
	scratchpad  *state.Scratchpad      // may be nil
	summarizer  *summarizer.Summarizer // may be nil — set via SetSummarizer
	rateLimiter *rateLimiter
}

func NewServer(
	pool *agent.Pool,
	q *queue.Queue,
	mem *memory.Store,
	bus *agent.EventBus,
	trelloClient *trello.Client,
	telegramToken string,
	telegramChatID string,
) *Server {
	s := &Server{
		pool:        pool,
		queue:       q,
		mem:         mem,
		bus:         bus,
		hub:         newWsHub(),
		triggerSvc:  pipeline.NewService(trelloClient),
		resolved:    mem.Resolved(),   // may be nil — endpoints handle nil gracefully
		scratchpad:  mem.Scratchpad(), // may be nil
		rateLimiter: newRateLimiter(),
	}
	// Forward EventBus → WebSocket clients
	go s.forwardEvents()
	go s.hub.run()
	return s
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	s.HandlerWebsocket(mux)
	s.HandlerAgent(mux)
	s.HandlerTask(mux)
	s.HandlerMemory(mux)
	s.HandlerResolved(mux)
	s.HandlerScratchpad(mux)
	s.HandlerTrigger(mux)
	s.HandlerState(mux)

	// Static — serve dashboard frontend
	mux.Handle("/", http.FileServer(http.Dir("./static")))

	// Apply rate limiting to all routes except /ws.
	return withRateLimit(s.rateLimiter, []string{"/ws"}, mux)
}
