package api

import "net/http"

func (s *Server) HandlerMemory(mux *http.ServeMux) {
	// Memory
	mux.HandleFunc("GET /api/memory/project", cors(s.handleProjectMemoryGet))
	mux.HandleFunc("PATH /api/memory/project", cors(s.handleProjectMemoryUpdate))
}

// ─── Memory ──────────────────────────────────────────────────────────────────

// GET   /api/memory/project — đọc project.md
func (s *Server) handleProjectMemoryGet(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		doc := s.mem.ReadProjectDoc()
		writeJSON(w, http.StatusOK, map[string]string{"content": doc})
	default:
		errJSON(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// ─── Memory ──────────────────────────────────────────────────────────────────

// PATCH /api/memory/project — cập nhật project.md
func (s *Server) handleProjectMemoryUpdate(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPatch:
		var req struct {
			Section string `json:"section"`
		}
		if err := readJSON(r, &req); err != nil {
			errJSON(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if err := s.mem.AppendProjectDoc(req.Section); err != nil {
			errJSON(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})

	default:
		errJSON(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
