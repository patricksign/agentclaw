package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// EnsureCollections creates the required Qdrant collections if they don't exist.
// HTTP 409 (conflict) is treated as success (collection already exists).
func EnsureCollections(ctx context.Context, baseURL, apiKey string) error {
	collections := []string{CollectionCode, CollectionDocs, CollectionPatterns}

	client := &http.Client{Timeout: 10 * time.Second}
	for _, name := range collections {
		if err := createCollection(ctx, client, baseURL, apiKey, name); err != nil {
			return fmt.Errorf("ensure collection %q: %w", name, err)
		}
	}
	return nil
}

func createCollection(ctx context.Context, client *http.Client, baseURL, apiKey, name string) error {
	body, _ := json.Marshal(map[string]any{
		"vectors": map[string]any{
			"size":     VectorDim,
			"distance": "Cosine",
		},
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		fmt.Sprintf("%s/collections/%s", baseURL, name),
		bytes.NewReader(body),
	)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("api-key", apiKey)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("create collection %q: %w", name, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusConflict:
		slog.Debug("qdrant collection already exists", "collection", name)
		return nil
	case resp.StatusCode >= 300:
		return fmt.Errorf("qdrant create collection %q: status %d", name, resp.StatusCode)
	default:
		slog.Info("qdrant collection created", "collection", name)
		return nil
	}
}
