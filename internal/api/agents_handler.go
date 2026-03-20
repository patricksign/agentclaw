package api

import (
	"net/http"
	"strings"

	"github.com/patricksign/agentclaw/internal/agent"
)

func (s *Server) HandlerAgent(mux *http.ServeMux) {
	// Agents
	mux.HandleFunc("GET /api/agents", cors(s.handleAgents))
	mux.HandleFunc("POST /api/agents/:id/restart", cors(s.handleRestartAgent))
	mux.HandleFunc("POST /api/agents/:id/kill", cors(s.handleKillAgent))
}

// ─── Agents ──────────────────────────────────────────────────────────────────

// GET /api/agents — list all agents + status
func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		errJSON(w, 405, "method not allowed")
		return
	}
	statuses := s.pool.StatusAll()
	type agentInfo struct {
		ID     string       `json:"id"`
		Status agent.Status `json:"status"`
	}
	out := make([]agentInfo, 0, len(statuses))
	for id, st := range statuses {
		out = append(out, agentInfo{ID: id, Status: st})
	}
	writeJSON(w, 200, out)
}

// POST /api/agents/:id/restart  — restart agent
func (s *Server) handleRestartAgent(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/agents/"), "/")
	id := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	switch {
	case action == "restart" && r.Method == http.MethodPost:
		if err := s.pool.Restart(id); err != nil {
			errJSON(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, map[string]string{"status": "restarted"})

	case action == "kill" && r.Method == http.MethodPost:
		if err := s.pool.Kill(id); err != nil {
			errJSON(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, map[string]string{"status": "killed"})

	default:
		errJSON(w, 405, "method not allowed")
	}
}

// POST /api/agents/:id/kill     — kill agent
func (s *Server) handleKillAgent(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/agents/"), "/")
	id := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	switch {
	case action == "kill" && r.Method == http.MethodPost:
		if err := s.pool.Kill(id); err != nil {
			errJSON(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, map[string]string{"status": "killed"})

	default:
		errJSON(w, 405, "method not allowed")
	}
}
