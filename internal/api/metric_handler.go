package api

import (
	"net/http"
	"time"
)

func (s *Server) HandlerMetric(mux *http.ServeMux) {
	// Metrics
	mux.HandleFunc("GET /api/metrics/today", cors(s.handleMetricsToday))
	mux.HandleFunc("GET /api/metrics/period", cors(s.handleMetricsPeriod))
}

// ─── Metrics ─────────────────────────────────────────────────────────────────

// GET /api/metrics/today
func (s *Server) handleMetricsToday(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		errJSON(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	today := time.Now().Format("2006-01-02")
	stats, err := s.mem.StatsForPeriod(today)
	if err != nil {
		errJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// GET /api/metrics/period?from=2026-01-01&to=2026-03-31
func (s *Server) handleMetricsPeriod(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		errJSON(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")
	if from == "" {
		from = time.Now().AddDate(0, -1, 0).Format("2006-01-02")
	}
	if to == "" {
		to = time.Now().Format("2006-01-02")
	}
	stats, err := s.mem.StatsForRange(from, to)
	if err != nil {
		errJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stats)
}
