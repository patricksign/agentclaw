package api

import (
	"encoding/json"
	"io"
	"net/http"
	"os"

	"github.com/rs/zerolog/log"
)

// ─── Helpers ─────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Error().Err(err).Msg("writeJSON encode failed")
	}
}

// maxBodyBytes is the maximum accepted request body size (1 MiB).
const maxBodyBytes = 1 << 20

func readJSON(r *http.Request, v any) error {
	limited := io.LimitReader(r.Body, maxBodyBytes)
	return json.NewDecoder(limited).Decode(v)
}

func errJSON(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func cors(next http.HandlerFunc) http.HandlerFunc {
	allowedOrigin := os.Getenv("CORS_ORIGIN")

	return func(w http.ResponseWriter, r *http.Request) {
		origin := allowedOrigin
		switch {
		case r.Method == http.MethodGet || r.Method == http.MethodOptions:
			// GET and preflight: allow wildcard if no specific origin configured.
			if origin == "" {
				origin = "*"
			}
		default:
			// Mutation methods (POST, PATCH, PUT, DELETE): require a specific origin.
			if origin == "" {
				errJSON(w, http.StatusForbidden, "CORS: mutation requests require a specific CORS_ORIGIN")
				return
			}
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PATCH,PUT,DELETE,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next(w, r)
	}
}
