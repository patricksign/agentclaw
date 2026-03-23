package memory

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/patricksign/AgentClaw/internal/domain"
	memcore "github.com/patricksign/AgentClaw/internal/memory"
	"github.com/patricksign/AgentClaw/internal/port"
)

// Compile-time check: Store implements port.MemoryStore.
var _ port.MemoryStore = (*Store)(nil)

// Store wraps the core memory.Store and optionally uses the new
// memory backends (Redis, Qdrant, Neo4j) for enriched context.
type Store struct {
	core     *memcore.Store
	shortMem port.ShortTermMemory
	semantic port.SemanticMemory
	graph    port.KnowledgeGraph
}

// NewStore creates an infra memory Store wrapping the core memory store.
func NewStore(core *memcore.Store) *Store {
	return &Store{core: core}
}

// NewStoreWithMemoryLayer creates a Store with the new memory backends injected.
func NewStoreWithMemoryLayer(core *memcore.Store, shortMem port.ShortTermMemory, semantic port.SemanticMemory, graph port.KnowledgeGraph) *Store {
	return &Store{
		core:     core,
		shortMem: shortMem,
		semantic: semantic,
		graph:    graph,
	}
}

// BuildContext assembles a tiered MemoryContext.
// If the new memory backends are available, it enriches context with
// Redis (context window), Qdrant (similar code + resolved patterns),
// and Neo4j (blocking tasks + skill summary) via parallel fetch.
// Falls back to legacy file-based behavior when backends are nil.
func (s *Store) BuildContext(ctx context.Context, agentID, role, taskTitle, complexity string) port.MemoryContext {
	// Legacy context from file/SQLite.
	coreMem := s.core.BuildContext(agentID, role, taskTitle, complexity)

	scratchpad := ""
	if coreMem.Scratchpad != nil {
		sp, err := coreMem.Scratchpad.ReadForContext()
		if err == nil {
			scratchpad = sp
		}
	}

	knownErrors := ""
	if coreMem.Resolved != nil {
		if matches, err := coreMem.Resolved.Search(taskTitle, role); err == nil {
			for _, m := range matches {
				knownErrors += m.ErrorPattern + ": " + m.ResolutionSummary + "\n"
			}
		}
	}

	result := port.MemoryContext{
		ProjectDoc:  coreMem.ProjectDoc,
		AgentDoc:    coreMem.AgentDoc,
		ScopeDoc:    scopeToString(coreMem.Scope),
		RecentTasks: recentTasksToString(coreMem.RecentTasks),
		ScratchPad:  scratchpad,
		KnownErrors: knownErrors,
	}

	// If no new backends are available, return legacy context.
	if s.shortMem == nil && s.semantic == nil && s.graph == nil {
		return result
	}

	// Parallel fetch from new backends with 3s timeout.
	fetchCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	var mu sync.Mutex

	var recentMsgsStr string
	var relevantCode string
	var patternsStr string
	var blockersStr string
	var skillStr string

	// 1. Redis: cached context window
	if s.shortMem != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			msgs, err := s.shortMem.GetContextWindow(fetchCtx, agentID)
			if err != nil {
				slog.Warn("BuildContext: GetContextWindow failed", "err", err, "agent", agentID)
				return
			}
			if len(msgs) == 0 {
				return
			}
			mu.Lock()
			recentMsgsStr = formatRecentMessages(msgs)
			mu.Unlock()
		}()
	}

	// 2. Qdrant: similar code chunks
	if s.semantic != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			chunks, err := s.semantic.SearchSimilarCode(fetchCtx, taskTitle, role, 5)
			if err != nil {
				slog.Warn("BuildContext: SearchSimilarCode failed", "err", err)
				return
			}
			if len(chunks) == 0 {
				return
			}
			mu.Lock()
			relevantCode = formatCodeChunks(chunks)
			mu.Unlock()
		}()

		// 3. Qdrant: resolved patterns
		wg.Add(1)
		go func() {
			defer wg.Done()
			patterns, err := s.semantic.SearchResolvedPatterns(fetchCtx, taskTitle, role, 3)
			if err != nil {
				slog.Warn("BuildContext: SearchResolvedPatterns failed", "err", err)
				return
			}
			if len(patterns) == 0 {
				return
			}
			mu.Lock()
			patternsStr = formatPatterns(patterns)
			mu.Unlock()
		}()
	}

	// 4. Neo4j: blocking tasks
	if s.graph != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			blockers, err := s.graph.GetBlockingTasks(fetchCtx, agentID)
			if err != nil {
				slog.Warn("BuildContext: GetBlockingTasks failed", "err", err)
				return
			}
			if len(blockers) == 0 {
				return
			}
			mu.Lock()
			blockersStr = formatBlockers(blockers)
			mu.Unlock()
		}()

		// 5. Neo4j: agent skill summary
		wg.Add(1)
		go func() {
			defer wg.Done()
			skill, err := s.graph.GetAgentSkillSummary(fetchCtx, agentID)
			if err != nil {
				slog.Warn("BuildContext: GetAgentSkillSummary failed", "err", err)
				return
			}
			if skill == nil {
				return
			}
			mu.Lock()
			skillStr = formatSkill(skill)
			mu.Unlock()
		}()
	}

	wg.Wait()

	if fetchCtx.Err() != nil {
		slog.Warn("BuildContext: parallel fetch timed out — using partial results", "agent", agentID)
	}

	// Enrich result with new data (append, don't overwrite legacy).
	if recentMsgsStr != "" {
		result.RecentTasks = recentMsgsStr
	}
	if relevantCode != "" {
		if result.ScratchPad != "" {
			result.ScratchPad += "\n\n--- Relevant Code ---\n" + relevantCode
		} else {
			result.ScratchPad = "--- Relevant Code ---\n" + relevantCode
		}
	}
	if patternsStr != "" {
		if result.KnownErrors != "" {
			result.KnownErrors += "\n" + patternsStr
		} else {
			result.KnownErrors = patternsStr
		}
	}
	if blockersStr != "" {
		result.ScopeDoc += "\n\n--- Blockers ---\n" + blockersStr
	}
	if skillStr != "" {
		result.AgentDoc += "\n\n--- Skill Summary ---\n" + skillStr
	}

	return result
}

// AppendProjectDoc delegates to the core memory store.
func (s *Store) AppendProjectDoc(section string) error {
	return s.core.AppendProjectDoc(section)
}

// ─── Formatters ─────────────────────────────────────────────────────────────

func formatRecentMessages(msgs []domain.LLMMessage) string {
	if len(msgs) == 0 {
		return ""
	}
	var sb strings.Builder
	// Show last 10 messages as compact context.
	start := 0
	if len(msgs) > 10 {
		start = len(msgs) - 10
	}
	for _, m := range msgs[start:] {
		sb.WriteString(fmt.Sprintf("[%s] %s\n", m.Role, truncate(m.Content, 200)))
	}
	return sb.String()
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func formatCodeChunks(chunks []port.CodeChunk) string {
	if len(chunks) == 0 {
		return ""
	}
	top := chunks
	if len(top) > 3 {
		top = top[:3]
	}
	var sb strings.Builder
	for _, c := range top {
		sb.WriteString(fmt.Sprintf("// %s\n%s\n\n", c.FilePath, c.Content))
	}
	return sb.String()
}

func formatPatterns(patterns []port.ResolvedPattern) string {
	if len(patterns) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, p := range patterns {
		sb.WriteString(fmt.Sprintf("Q: %s\nA: %s\n\n", p.Question, p.Answer))
	}
	return sb.String()
}

func formatBlockers(blockers []port.TaskNode) string {
	if len(blockers) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, b := range blockers {
		sb.WriteString(fmt.Sprintf("- [%s] %s (phase: %s, agent: %s)\n", b.Status, b.Title, b.Phase, b.AgentID))
	}
	return sb.String()
}

func formatSkill(skill *port.AgentSkill) string {
	if skill == nil {
		return ""
	}
	return fmt.Sprintf("Role: %s | Tasks: %d | Success: %.0f%% | Avg cost: $%.4f",
		skill.Role, skill.TotalTasks, skill.SuccessRate*100, skill.AvgCostUSD)
}

// scopeToString formats a ScopeManifest into a readable string.
func scopeToString(scope interface{}) string {
	if scope == nil {
		return ""
	}
	return fmt.Sprintf("%v", scope)
}

// recentTasksToString formats recent tasks into a compact string.
func recentTasksToString(tasks interface{}) string {
	if tasks == nil {
		return ""
	}
	return fmt.Sprintf("%v", tasks)
}
