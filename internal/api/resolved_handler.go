package api

import (
	"net/http"
	"strings"

	"github.com/patricksign/agentclaw/internal/state"
)

func (s *Server) HandlerResolved(mux *http.ServeMux) {

	// Resolved error pattern store
	mux.HandleFunc("GET /api/state/resolved", cors(s.getHandleResolved))
	mux.HandleFunc("GET /api/state/resolved/:id", cors(s.handleResolvedItemById))
	mux.HandleFunc("PATH /api/state/resolved/:id/resolve", cors(s.updateResolvedItemById))
}

// ─── Resolved error patterns ─────────────────────────────────────────────────

// GET /api/state/resolved
// Returns all ErrorPattern entries sorted by occurrence_count desc.
func (s *Server) getHandleResolved(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		errJSON(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.resolved == nil {
		writeJSON(w, http.StatusOK, []state.ErrorPattern{})
		return
	}
	patterns, err := s.resolved.LoadAll()
	if err != nil {
		errJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	if patterns == nil {
		patterns = []state.ErrorPattern{}
	}
	writeJSON(w, http.StatusOK, patterns)
}

// handleResolvedItem handles:
//
//	GET   /api/state/resolved/:id          — return full detail file content
//	PATCH /api/state/resolved/:id/resolve  — mark as resolved
func (s *Server) handleResolvedItemById(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/state/resolved/")
	parts := strings.SplitN(path, "/", 2)
	id := parts[0]
	sub := ""
	if len(parts) == 2 {
		sub = parts[1]
	}

	if !isValidResolvedID(id) {
		errJSON(w, http.StatusBadRequest, "invalid pattern id")
		return
	}

	if s.resolved == nil {
		errJSON(w, http.StatusServiceUnavailable, "resolved store not configured")
		return
	}

	switch {
	case sub == "" && r.Method == http.MethodGet:
		detail, err := s.resolved.LoadDetail(id)
		if err != nil {
			errJSON(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"id": id, "detail": detail})

	default:
		errJSON(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleResolvedItem handles:
//
//	GET   /api/state/resolved/:id          — return full detail file content
//	PATCH /api/state/resolved/:id/resolve  — mark as resolved
func (s *Server) updateResolvedItemById(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/state/resolved/")
	parts := strings.SplitN(path, "/", 2)
	id := parts[0]
	sub := ""
	if len(parts) == 2 {
		sub = parts[1]
	}

	if !isValidResolvedID(id) {
		errJSON(w, http.StatusBadRequest, "invalid pattern id")
		return
	}

	if s.resolved == nil {
		errJSON(w, http.StatusServiceUnavailable, "resolved store not configured")
		return
	}

	switch {
	case sub == "resolve" && r.Method == http.MethodPatch:
		if err := s.resolved.MarkResolved(id); err != nil {
			errJSON(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "resolved"})

	default:
		errJSON(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// isValidResolvedID reports whether id is a valid 6-hex-character pattern ID.
func isValidResolvedID(id string) bool {
	if len(id) != 6 {
		return false
	}
	for _, c := range id {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
