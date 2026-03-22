package api

import (
	"crypto/subtle"
	"net/http"
	"os"

	"github.com/patricksign/AgentClaw/internal/adapter"
)

func (s *Server) HandlerAgent(mux *http.ServeMux) {
	// Agents
	mux.HandleFunc("GET /api/agents", cors(s.handleAgents))
	mux.HandleFunc("POST /api/agents/{id}/restart", cors(s.handleRestartAgent))
	mux.HandleFunc("POST /api/agents/{id}/kill", cors(s.handleKillAgent))
}

// ─── Agents ──────────────────────────────────────────────────────────────────

// GET /api/agents — list all agents + status
func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	// Method already enforced by mux pattern "GET /api/agents".
	statuses := s.pool.StatusAll()
	type agentInfo struct {
		ID     string         `json:"id"`
		Status adapter.Status `json:"status"`
	}
	out := make([]agentInfo, 0, len(statuses))
	for id, st := range statuses {
		out = append(out, agentInfo{ID: id, Status: st})
	}
	writeJSON(w, 200, out)
}

// requireAdminTokenFromReq checks the X-Admin-Token header against the ADMIN_TOKEN env var.
// Returns true if authorised (or no token is configured). Writes 401 and returns false otherwise.
func requireAdminTokenFromReq(w http.ResponseWriter, r *http.Request) bool {
	adminToken := os.Getenv("ADMIN_TOKEN")
	if adminToken == "" {
		return true
	}
	got := r.Header.Get("X-Admin-Token")
	if subtle.ConstantTimeCompare([]byte(got), []byte(adminToken)) != 1 {
		errJSON(w, http.StatusUnauthorized, "unauthorized — X-Admin-Token required")
		return false
	}
	return true
}

// POST /api/agents/{id}/restart — restart agent (requires admin token)
func (s *Server) handleRestartAgent(w http.ResponseWriter, r *http.Request) {
	if !requireAdminTokenFromReq(w, r) {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		errJSON(w, http.StatusBadRequest, "missing agent id")
		return
	}
	if err := s.pool.Restart(id); err != nil {
		errJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "restarted"})
}

// POST /api/agents/{id}/kill — kill agent (requires admin token)
func (s *Server) handleKillAgent(w http.ResponseWriter, r *http.Request) {
	if !requireAdminTokenFromReq(w, r) {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		errJSON(w, http.StatusBadRequest, "missing agent id")
		return
	}
	if err := s.pool.Kill(id); err != nil {
		errJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "killed"})
}
