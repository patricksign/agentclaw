package api

import (
	"context"
	"net/http"

	"github.com/patricksign/AgentClaw/internal/integrations/pipeline"
	"github.com/patricksign/AgentClaw/internal/integrations/trello"
	"github.com/patricksign/AgentClaw/internal/memory"
	"github.com/patricksign/AgentClaw/internal/port"
	"github.com/patricksign/AgentClaw/internal/state"
)

// ─── Server ──────────────────────────────────────────────────────────────────

type Server struct {
	pool        port.AgentPool         // clean-arch: was *agent.Pool
	queue       port.TaskQueue         // clean-arch: was *queue.Queue
	executor    port.TaskExecutor      // clean-arch: was *agent.Executor (stored for handlers)
	events      port.EventBus           // clean-arch: was *agent.EventBus
	mem         *memory.Store          // legacy — separate migration
	hub         *wsHub
	triggerSvc  *pipeline.Service      // legacy — separate migration
	resolved    *state.ResolvedStore   // may be nil
	scratchpad  *state.Scratchpad      // may be nil
	summarizer  port.HistorySummarizer // clean-arch: was *summarizer.Summarizer
	rateLimiter *rateLimiter
	ctx         context.Context    // server-scoped context — cancelled on Shutdown
	cancel      context.CancelFunc // cancels ctx
}

func NewServer(
	pool port.AgentPool,
	q port.TaskQueue,
	exec port.TaskExecutor,
	mem *memory.Store,
	events port.EventBus,
	trelloClient *trello.Client,
	telegramToken string,
	telegramChatID string,
) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		pool:        pool,
		queue:       q,
		executor:    exec,
		mem:         mem,
		events:      events,
		hub:         newWsHub(),
		resolved:    mem.Resolved(),   // may be nil — endpoints handle nil gracefully
		scratchpad:  mem.Scratchpad(), // may be nil
		rateLimiter: newRateLimiter(),
		ctx:         ctx,
		cancel:      cancel,
	}
	// Forward EventBus → WebSocket clients
	go s.forwardEvents()
	go s.hub.run()
	return s
}

// SetSummarizer sets the history summarizer (optional — may be nil).
func (s *Server) SetSummarizer(sum port.HistorySummarizer) {
	s.summarizer = sum
}

// SetTriggerService sets the pipeline trigger service (optional).
func (s *Server) SetTriggerService(svc *pipeline.Service) {
	s.triggerSvc = svc
}

// Shutdown stops background pipelines and the WebSocket hub.
func (s *Server) Shutdown() {
	s.cancel()
	s.hub.shutdown()
	s.rateLimiter.Stop()
}

// Context returns the server-scoped context that is cancelled on Shutdown.
func (s *Server) Context() context.Context {
	return s.ctx
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
	s.HandlerMetric(mux)
	s.HandlerPricing(mux)
	s.HandlerHealth(mux)

	// Static — serve dashboard frontend
	mux.Handle("/", http.FileServer(http.Dir("./static")))

	// Apply rate limiting to all routes except /ws.
	return withRateLimit(s.rateLimiter, []string{"/ws"}, mux)
}
