package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/patricksign/AgentClaw/internal/port"
	"golang.org/x/sync/errgroup"
)

const (
	CollectionCode     = "source_code"
	CollectionDocs     = "ocp_docs"
	CollectionPatterns = "resolved_patterns"

	VectorDim         = 1536 // OpenAI text-embedding-3-small
	embedConcurrency  = 5
)

var _ port.SemanticMemory = (*QdrantMemory)(nil)

type QdrantMemory struct {
	baseURL string
	apiKey  string
	client  *http.Client
	embedFn func(ctx context.Context, text string) ([]float32, error)
}

func NewQdrantMemory(baseURL, apiKey string, embedFn func(ctx context.Context, text string) ([]float32, error)) *QdrantMemory {
	return &QdrantMemory{
		baseURL: baseURL,
		apiKey:  apiKey,
		client:  &http.Client{Timeout: 30 * time.Second},
		embedFn: embedFn,
	}
}

// ─── Upsert Code Chunks ────────────────────────────────────────────────────

func (q *QdrantMemory) UpsertCodeChunk(ctx context.Context, chunk port.CodeChunk) error {
	vec, err := q.embed(ctx, chunk.Content)
	if err != nil {
		return err
	}
	chunk.Vector = vec
	return q.upsertPoints(ctx, CollectionCode, []qdrantPoint{
		{
			ID:     chunk.ID,
			Vector: vec,
			Payload: map[string]any{
				"task_id":   chunk.TaskID,
				"file_path": chunk.FilePath,
				"content":   chunk.Content,
				"language":  chunk.Language,
				"role":      chunk.Role,
			},
		},
	})
}

func (q *QdrantMemory) UpsertCodeChunks(ctx context.Context, chunks []port.CodeChunk) error {
	type indexedPoint struct {
		idx   int
		point qdrantPoint
	}

	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(embedConcurrency)

	results := make(chan indexedPoint, len(chunks))

	for i, c := range chunks {
		g.Go(func() error {
			vec, err := q.embed(gCtx, c.Content)
			if err != nil {
				slog.Warn("embed chunk failed, skipping", "chunk_id", c.ID, "error", err)
				return nil // skip, don't fail batch
			}
			results <- indexedPoint{
				idx: i,
				point: qdrantPoint{
					ID:     c.ID,
					Vector: vec,
					Payload: map[string]any{
						"task_id":   c.TaskID,
						"file_path": c.FilePath,
						"content":   c.Content,
						"language":  c.Language,
						"role":      c.Role,
					},
				},
			}
			return nil
		})
	}

	go func() {
		_ = g.Wait()
		close(results)
	}()

	points := make([]qdrantPoint, 0, len(chunks))
	for r := range results {
		_ = r.idx
		points = append(points, r.point)
	}

	if len(points) == 0 {
		return nil
	}
	return q.upsertPoints(ctx, CollectionCode, points)
}

// ─── Upsert Document ────────────────────────────────────────────────────────

func (q *QdrantMemory) UpsertDocument(ctx context.Context, doc port.SemanticDoc) error {
	vec, err := q.embed(ctx, doc.Title+" "+doc.Content)
	if err != nil {
		return err
	}
	collection := doc.Collection
	if collection == "" {
		collection = CollectionDocs
	}
	return q.upsertPoints(ctx, collection, []qdrantPoint{{
		ID:     doc.ID,
		Vector: vec,
		Payload: map[string]any{
			"title":   doc.Title,
			"content": doc.Content,
			"role":    doc.Role,
			"tags":    doc.Tags,
		},
	}})
}

// ─── Search ─────────────────────────────────────────────────────────────────

func (q *QdrantMemory) SearchSimilarCode(ctx context.Context, query, role string, limit int) ([]port.CodeChunk, error) {
	vec, err := q.embed(ctx, query)
	if err != nil {
		return nil, err
	}

	results, err := q.search(ctx, CollectionCode, vec, limit, map[string]any{
		"must": []map[string]any{
			{"key": "role", "match": map[string]any{"value": role}},
		},
	})
	if err != nil {
		return nil, err
	}

	chunks := make([]port.CodeChunk, 0, len(results))
	for _, r := range results {
		chunks = append(chunks, port.CodeChunk{
			ID:       fmt.Sprintf("%v", r.ID),
			TaskID:   payloadStr(r.Payload, "task_id"),
			FilePath: payloadStr(r.Payload, "file_path"),
			Content:  payloadStr(r.Payload, "content"),
			Language: payloadStr(r.Payload, "language"),
			Role:     payloadStr(r.Payload, "role"),
		})
	}
	return chunks, nil
}

func (q *QdrantMemory) SearchResolvedPatterns(ctx context.Context, question, role string, limit int) ([]port.ResolvedPattern, error) {
	vec, err := q.embed(ctx, question)
	if err != nil {
		return nil, err
	}

	results, err := q.search(ctx, CollectionPatterns, vec, limit, map[string]any{
		"must": []map[string]any{
			{"key": "role", "match": map[string]any{"value": role}},
		},
	})
	if err != nil {
		return nil, err
	}

	patterns := make([]port.ResolvedPattern, 0, len(results))
	for _, r := range results {
		occ := 1
		if v, ok := r.Payload["occurrence_count"]; ok {
			if n, ok := v.(float64); ok {
				occ = int(n)
			}
		}
		patterns = append(patterns, port.ResolvedPattern{
			ID:              fmt.Sprintf("%v", r.ID),
			Question:        payloadStr(r.Payload, "question"),
			Answer:          payloadStr(r.Payload, "answer"),
			Role:            payloadStr(r.Payload, "role"),
			OccurrenceCount: occ,
			Score:           r.Score,
		})
	}
	return patterns, nil
}

func (q *QdrantMemory) SearchDocs(ctx context.Context, query, collection string, limit int) ([]port.SemanticDoc, error) {
	vec, err := q.embed(ctx, query)
	if err != nil {
		return nil, err
	}
	results, err := q.search(ctx, collection, vec, limit, nil)
	if err != nil {
		return nil, err
	}
	docs := make([]port.SemanticDoc, 0, len(results))
	for _, r := range results {
		docs = append(docs, port.SemanticDoc{
			ID:      fmt.Sprintf("%v", r.ID),
			Title:   payloadStr(r.Payload, "title"),
			Content: payloadStr(r.Payload, "content"),
			Role:    payloadStr(r.Payload, "role"),
		})
	}
	return docs, nil
}

// ─── Delete ─────────────────────────────────────────────────────────────────

func (q *QdrantMemory) DeleteByTaskID(ctx context.Context, taskID string) error {
	return q.deleteByFilter(ctx, CollectionCode, map[string]any{
		"must": []map[string]any{
			{"key": "task_id", "match": map[string]any{"value": taskID}},
		},
	})
}

// ─── Qdrant REST helpers ────────────────────────────────────────────────────

type qdrantPoint struct {
	ID      string         `json:"id"`
	Vector  []float32      `json:"vector"`
	Payload map[string]any `json:"payload"`
}

type qdrantSearchResult struct {
	ID      any            `json:"id"`
	Score   float32        `json:"score"`
	Payload map[string]any `json:"payload"`
}

func (q *QdrantMemory) embed(ctx context.Context, text string) ([]float32, error) {
	return q.embedFn(ctx, text)
}

func (q *QdrantMemory) upsertPoints(ctx context.Context, collection string, points []qdrantPoint) error {
	body, err := json.Marshal(map[string]any{"points": points})
	if err != nil {
		return fmt.Errorf("qdrant marshal upsert: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		fmt.Sprintf("%s/collections/%s/points", q.baseURL, collection),
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("qdrant upsert request: %w", err)
	}
	q.setHeaders(req)

	resp, err := q.client.Do(req)
	if err != nil {
		return fmt.Errorf("qdrant upsert: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("qdrant upsert: status %d", resp.StatusCode)
	}
	return nil
}

func (q *QdrantMemory) search(ctx context.Context, collection string, vec []float32, limit int, filter map[string]any) ([]qdrantSearchResult, error) {
	payload := map[string]any{
		"vector":       vec,
		"limit":        limit,
		"with_payload": true,
	}
	if filter != nil {
		payload["filter"] = filter
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("qdrant marshal search: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/collections/%s/points/search", q.baseURL, collection),
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("qdrant search request: %w", err)
	}
	q.setHeaders(req)

	resp, err := q.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("qdrant search: %w", err)
	}
	defer resp.Body.Close()

	var out struct {
		Result []qdrantSearchResult `json:"result"`
	}
	return out.Result, json.NewDecoder(resp.Body).Decode(&out)
}

func (q *QdrantMemory) deleteByFilter(ctx context.Context, collection string, filter map[string]any) error {
	body, err := json.Marshal(map[string]any{"filter": filter})
	if err != nil {
		return fmt.Errorf("qdrant marshal delete: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/collections/%s/points/delete", q.baseURL, collection),
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("qdrant delete request: %w", err)
	}
	q.setHeaders(req)

	resp, err := q.client.Do(req)
	if err != nil {
		return fmt.Errorf("qdrant delete: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("qdrant delete: status %d", resp.StatusCode)
	}
	return nil
}

func (q *QdrantMemory) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	if q.apiKey != "" {
		req.Header.Set("api-key", q.apiKey)
	}
}

func payloadStr(p map[string]any, key string) string {
	if v, ok := p[key]; ok {
		return fmt.Sprintf("%v", v)
	}
	return ""
}
