package port

import (
	"context"

	"github.com/patricksign/AgentClaw/internal/domain"
)

// HistorySummarizer abstracts agent history compression.
// Implemented by internal/summarizer.Summarizer.
type HistorySummarizer interface {
	CompressAgentHistory(ctx context.Context, agentID, role string) (costUSD float64, summaryLen int, err error)
	CompressAll(ctx context.Context, configs []domain.AgentConfig) (totalCost float64, err error)
}
