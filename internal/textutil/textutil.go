// Package textutil provides shared text manipulation helpers used across
// multiple packages (phase, orchestrator, escalation). Extracted to avoid
// duplicate implementations with inconsistent behavior (go-validator: idiomatic).
package textutil

import "strings"

// Truncate returns the first n runes of s, appending "..." if truncated.
// Uses rune-based truncation to avoid splitting multi-byte UTF-8 characters.
func Truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}

// StripMarkdownFences removes ```json ... ``` wrappers from LLM output.
func StripMarkdownFences(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	// Remove opening fence line.
	if idx := strings.Index(s, "\n"); idx != -1 {
		s = s[idx+1:]
	}
	// Remove closing fence.
	if idx := strings.LastIndex(s, "```"); idx != -1 {
		s = s[:idx]
	}
	return strings.TrimSpace(s)
}
