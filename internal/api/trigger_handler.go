package api

import (
	"context"
	"net/http"

	"github.com/rs/zerolog/log"
)

func (s *Server) HandlerTrigger(mux *http.ServeMux) {
	// Trigger pipeline
	mux.HandleFunc("POST /api/trigger", cors(s.handleTrigger))
}

// ─── Trigger ─────────────────────────────────────────────────────────────────

// POST /api/trigger
// Body: {"workspace_id":"<board_id>","ticket_id":"<card_id_or_shortlink>"}
// Returns 202 Accepted immediately; the agent pipeline runs in the background.
func (s *Server) handleTrigger(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errJSON(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		WorkspaceID string `json:"workspace_id"`
		TicketID    string `json:"ticket_id"`
	}
	if err := readJSON(r, &req); err != nil {
		errJSON(w, http.StatusInternalServerError, "invalid JSON")
		return
	}
	if req.WorkspaceID == "" || req.TicketID == "" {
		errJSON(w, http.StatusInternalServerError, "workspace_id and ticket_id are required")
		return
	}

	if s.triggerSvc == nil || !s.triggerSvc.IsConfigured() {
		errJSON(w, http.StatusServiceUnavailable, "Trello integration not configured (TRELLO_KEY/TRELLO_TOKEN missing)")
		return
	}

	// Return 202 immediately; run pipeline in background.
	writeJSON(w, http.StatusAccepted, map[string]string{
		"status":       "accepted",
		"workspace_id": req.WorkspaceID,
		"ticket_id":    req.TicketID,
	})

	go func() {
		if err := s.triggerSvc.Run(context.Background(), req.WorkspaceID, req.TicketID); err != nil {
			log.Error().Err(err).
				Str("workspace_id", req.WorkspaceID).
				Str("ticket_id", req.TicketID).
				Msg("trigger pipeline failed")
		}
	}()
}
