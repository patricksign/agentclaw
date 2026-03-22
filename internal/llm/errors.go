package llm

import (
	"errors"
	"fmt"
	"strings"
)

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("%s %d: %s", e.Provider, e.StatusCode, sanitizeErrorBody(e.Body))
}

// sanitizeErrorBody redacts potential sensitive data from API error responses.
// Provider APIs may echo back request content including API keys or tokens.
func sanitizeErrorBody(body string) string {
	lower := strings.ToLower(body)
	for _, keyword := range []string{"key", "token", "secret", "authorization", "password", "credential"} {
		if strings.Contains(lower, keyword) {
			return fmt.Sprintf("[redacted — error body contained '%s']", keyword)
		}
	}
	return body
}

// isPermanentError returns true for 4xx HTTP errors (auth, bad request, etc.)
// which should not trigger a fallback. Uses typed httpStatusError when available,
// falls back to string matching for wrapped errors.
func isPermanentError(err error) bool {
	var httpErr *httpStatusError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode >= 400 && httpErr.StatusCode < 500
	}
	return false
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// truncateErrorBody caps the API error response body at 200 bytes.
func truncateErrorBody(raw []byte) string {
	const maxErrBytes = 200
	if len(raw) <= maxErrBytes {
		return string(raw)
	}
	return string(raw[:maxErrBytes]) + "...(truncated)"
}
