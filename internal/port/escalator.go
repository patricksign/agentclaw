package port

import (
	"context"

	"github.com/patricksign/AgentClaw/internal/domain"
)

// EscalatorRequest contains the context needed to resolve a question
// that an agent could not answer on its own.
type EscalatorRequest struct {
	Question    string `json:"question"`
	TaskContext string `json:"task_context"`
	AgentModel  string `json:"agent_model"`
	AgentRole   string `json:"agent_role"`
	TaskID      string `json:"task_id"`
	QuestionID  string `json:"question_id"`
}

// Escalator resolves questions by trying progressively more expensive
// strategies: cache lookup, cheaper model, more capable model, human.
type Escalator interface {
	Resolve(ctx context.Context, req EscalatorRequest) (domain.EscalationResult, error)
}

// HumanAsker abstracts the human notification channel (Telegram, Slack, etc.).
// Moved here from usecase/escalation so infra adapters can implement it
// without importing usecase (clean-arch: dependency rule).
type HumanAsker interface {
	AskHuman(ctx context.Context, agentID, taskID, taskTitle, questionID, question string) (msgID int, err error)
	RegisterReply(msgID int, taskID, questionID string) <-chan string
	// UnregisterReply removes a pending reply registration. Called when the
	// wait timeout expires to prevent channel and map entry leaks.
	UnregisterReply(msgID int)
}
